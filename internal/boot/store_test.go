package boot

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Store-file mode: COW files live on an ext4 mounted from /dev/loop7, whose backing file sits on an NTFS host partition
// that also holds the base image. The plan must list the host first (rw), then the loop storage.
func TestTrackerStoreFile(t *testing.T) {
	host := "/mnt/images"
	mi := filepath.Join(t.TempDir(), "mountinfo")
	os.WriteFile(mi, []byte("30 1 8:1 / / rw - ext4 /dev/vda1 rw\n"+
		"40 30 8:17 / /mnt/images rw - ntfs3 /dev/vdb1 rw\n"+
		"41 30 7:7 / /mnt/snap rw - ext4 /dev/loop7 rw\n"), 0o644)
	r := &DryRunner{Respond: func(l string) (string, bool) {
		switch {
		case l == "losetup --noheadings -O BACK-FILE /dev/loop7":
			return host + "/omloader/store.img\n", true
		case strings.HasPrefix(l, "blkid -o export /dev/vdb1"):
			return "UUID=1A2B3C4D\nLABEL=VTOYDATA\nTYPE=ntfs\n", true
		case strings.HasPrefix(l, "blkid -o export /dev/loop7"):
			return "UUID=aaaa\nLABEL=OMLOADER\nTYPE=ext4\n", true
		}
		return "", false
	}}
	tr, err := newTracker(r, mi, false)
	if err != nil {
		t.Fatal(err)
	}
	base, err := tr.ref(host+"/images/u/base.raw", false)
	if err != nil {
		t.Fatal(err)
	}
	cow, err := tr.ref("/mnt/snap/snapshots/u/a.cow", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.list) != 2 || base.Storage != "s0" || cow.Storage != "s1" || cow.Rel != "snapshots/u/a.cow" {
		t.Fatalf("%+v %+v %+v", tr.list, base, cow)
	}
	h, s := tr.list[0], tr.list[1]
	if h.FSType != "ntfs3" || !h.RW || h.Loop != nil || h.UUID != "1A2B3C4D" {
		t.Fatalf("host %+v", h)
	}
	if s.FSType != "ext4" || !s.RW || s.Loop == nil || s.Loop.Storage != "s0" || s.Loop.Rel != "omloader/store.img" {
		t.Fatalf("loop storage %+v", s)
	}
	// a second COW on the same store reuses the storages
	if c2, err := tr.ref("/mnt/snap/snapshots/u/b.cow", true); err != nil || c2.Storage != "s1" || len(tr.list) != 2 {
		t.Fatal(c2, err, tr.list)
	}
}

func TestGuestAssembleStoreFile(t *testing.T) {
	gp := &GuestPlan{Version: 1, Image: "u", Sectors: 2048, BaseDM: "oml-u-base", RootIndex: 1, RootDev: "/dev/mapper/oml-u-a1",
		Storages: []StorageRef{
			{ID: "s0", UUID: "1A2B3C4D", FSType: "ntfs3", RW: true},
			{ID: "s1", FSType: "ext4", RW: true, Loop: &FileRef{Storage: "s0", Rel: "omloader/store.img"}}},
		Base:   FileRef{"s0", "images/u/base.raw"},
		Layers: []GuestLayer{{"a", "oml-u-a", FileRef{"s1", "snapshots/u/a.cow"}}}}
	r := &DryRunner{}
	mr := t.TempDir()
	if _, err := GuestAssemble(gp, r, GuestOptions{MountPrefix: mr + "/m-", Tries: 1, Delay: time.Millisecond, DevExists: func(string) bool { return true }}); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(r.Cmds, "\n")
	order := []string{
		"blkid -U 1A2B3C4D", "mount -t ntfs3 -o rw /dev/dry-by-uuid " + mr + "/m-s0",
		"losetup -f --show " + mr + "/m-s0/omloader/store.img", "mount -t ext4 -o rw /dev/loop0 " + mr + "/m-s1",
		"losetup -r -f --show " + mr + "/m-s0/images/u/base.raw",
		"losetup -f --show " + mr + "/m-s1/snapshots/u/a.cow",
	}
	pos := -1
	for _, w := range order {
		i := strings.Index(all, w)
		if i < 0 || i < pos {
			t.Fatalf("missing/misordered %q in:\n%s", w, all)
		}
		pos = i
	}
	if strings.Contains(all, "blkid -L") && strings.Count(all, "blkid") > 1 {
		t.Errorf("loop storage must not be searched by blkid:\n%s", all)
	}
	// host missing in the plan order
	bad := *gp
	bad.Storages = []StorageRef{gp.Storages[1], gp.Storages[0]}
	if _, err := GuestAssemble(&bad, &DryRunner{}, GuestOptions{MountPrefix: mr + "/n-", Tries: 1, Delay: time.Millisecond}); err == nil {
		t.Fatal("want plan order error")
	}
}

func TestMissingStoreModules(t *testing.T) {
	st := []StorageRef{{ID: "s0", FSType: "ntfs3"}, {ID: "s1", FSType: "ext4", Loop: &FileRef{Storage: "s0", Rel: "x"}}}
	r := &DryRunner{Respond: func(l string) (string, bool) { return "", false }}
	if m := missingStoreModules(r, "/m", "6.1", st); len(m) != 0 {
		t.Fatal(m)
	}
	if m := missingStoreModules(r, "/m", "6.1", st[:1]); m != nil {
		t.Fatal("plans without a store file need nothing", m)
	}
	// ntfs3 absent
	f := &failRunner{DryRunner: DryRunner{}, fail: "ntfs3"}
	if m := missingStoreModules(f, "/m", "6.1", st); len(m) != 1 || m[0] != "ntfs3" {
		t.Fatal(m)
	}
}

type failRunner struct {
	DryRunner
	fail string
}

func (f *failRunner) Run(name string, args ...string) (string, error) {
	if name == "modprobe" && args[len(args)-1] == f.fail {
		return "", os.ErrNotExist
	}
	return f.DryRunner.Run(name, args...)
}

func TestKernelVersionArch(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"usr/lib/modules/6.12.1-arch1-1/kernel/fs/ntfs3/ntfs3.ko.zst", "usr/lib/modules/6.12.1-arch1-1/kernel/drivers/block/loop.ko.xz"} {
		os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o755)
		os.WriteFile(filepath.Join(root, f), nil, 0o644)
	}
	os.Symlink("usr/lib", filepath.Join(root, "lib"))
	if v, err := kernelVersion(root, "/boot/vmlinuz-linux"); err != nil || v != "6.12.1-arch1-1" {
		t.Fatal(v, err)
	}
	if v, err := kernelVersion(root, "/boot/vmlinuz-6.12.1-arch1-1"); err != nil || v != "6.12.1-arch1-1" {
		t.Fatal(v, err)
	}
}

// fake module trees through the real kmod: .ko, .ko.zst, .ko.xz, .ko.gz and built-in must all count as present, absent ones as missing.
func TestModuleDetectionFakeTrees(t *testing.T) {
	if _, err := exec.LookPath("modprobe"); err != nil {
		t.Skip("no modprobe")
	}
	root := t.TempDir()
	d := filepath.Join(root, "usr/lib/modules/6.12.1-arch1-1")
	os.MkdirAll(filepath.Join(d, "kernel"), 0o755)
	os.Symlink("usr/lib", filepath.Join(root, "lib"))
	dep := ""
	for _, f := range []string{"kernel/loop.ko", "kernel/ntfs3.ko.zst", "kernel/ext4.ko.xz", "kernel/fuse.ko.gz"} {
		os.WriteFile(filepath.Join(d, f), nil, 0o644)
		dep += f + ":\n"
	}
	os.WriteFile(filepath.Join(d, "modules.dep"), []byte(dep), 0o644)
	os.WriteFile(filepath.Join(d, "modules.builtin"), []byte("kernel/vfat.ko\n"), 0o644)
	exec.Command("depmod", "-b", root, "6.12.1-arch1-1").Run() // empty fake .ko files make depmod complain but it still writes the indexes
	st := []StorageRef{{ID: "s0", FSType: "ntfs3"}, {ID: "s1", FSType: "ext4", Loop: &FileRef{Storage: "s0", Rel: "x"}}}
	if m := missingStoreModules(&ExecRunner{}, root, "6.12.1-arch1-1", st); len(m) != 0 {
		t.Fatal("present modules reported missing:", m)
	}
	st[0].FSType = "vfat" // built-in
	if m := missingStoreModules(&ExecRunner{}, root, "6.12.1-arch1-1", st); len(m) != 0 {
		t.Fatal("builtin reported missing:", m)
	}
	st[0].FSType = "nosuchfs"
	if m := missingStoreModules(&ExecRunner{}, root, "6.12.1-arch1-1", st); len(m) != 1 || m[0] != "nosuchfs" {
		t.Fatal(m)
	}
	fs := moduleFiles(&ExecRunner{}, root, "6.12.1-arch1-1", []string{"loop", "ntfs3", "ext4"})
	if len(fs) != 3 || !strings.HasSuffix(fs[1], "ntfs3.ko.zst") {
		t.Fatal(fs)
	}
}
