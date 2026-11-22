package boot

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omloader/internal/config"
	"omloader/internal/snapshot"
)

type fx struct {
	dir, snap string
	o         Options
	sha       string
}

func setup(t *testing.T, sha string, tree *snapshot.Tree, extra string) (*fx, *config.Resolved) {
	t.Helper()
	d := t.TempDir()
	f := &fx{dir: filepath.Join(d, "images", "u"), snap: filepath.Join(d, "snaps")}
	os.MkdirAll(f.dir, 0o755)
	data := make([]byte, 1<<20)
	data[5] = 1
	base := filepath.Join(f.dir, "base.raw")
	os.WriteFile(base, data, 0o644)
	h := sha256.Sum256(data)
	f.sha = hex.EncodeToString(h[:])
	if sha == "good" {
		sha = f.sha
	}
	y := "name: u\ntype: vm\nformat: raw\nbase: " + base + "\n"
	if sha != "" {
		y += "sha256: " + sha + "\n"
	}
	y += "kernel:\n  path: /boot/vmlinuz\n  initrd: /boot/initrd.img\n  cmdline: root={ROOT} ro\n" + extra + "snapshots:\n  mode: dm-snapshot\n"
	ip := filepath.Join(f.dir, "image.yaml")
	os.WriteFile(ip, []byte(y), 0o644)
	if tree != nil {
		os.MkdirAll(filepath.Join(f.snap, "u"), 0o755)
		if err := tree.Save(filepath.Join(f.snap, "u", "tree.json")); err != nil {
			t.Fatal(err)
		}
	}
	res, err := config.Load("", ip)
	if err != nil {
		t.Fatal(err)
	}
	f.o = Options{Image: "u", SnapRoot: f.snap, DryRun: true}
	return f, res
}

func chain3() *snapshot.Tree {
	t := snapshot.New("u", "")
	t.Create("a", "")
	t.Create("b", "")
	t.Create("c", "")
	return t
}

func run(t *testing.T, f *fx, res *config.Resolved) (*DryRunner, string, error) {
	p, err := BuildPlan(f.o, res, f.dir)
	if err != nil {
		return nil, "", err
	}
	r := &DryRunner{}
	dev, err := Execute(f.o, res, p, r)
	return r, dev, err
}

func TestChainOfThree(t *testing.T) {
	f, res := setup(t, "good", chain3(), "")
	r, dev, err := run(t, f, res)
	if err != nil {
		t.Fatal(err)
	}
	if dev != "/dev/mapper/oml-u-c" {
		t.Fatal(dev)
	}
	all := strings.Join(r.Cmds, "\n")
	for _, w := range []string{
		"losetup -r -f --show " + filepath.Join(f.dir, "base.raw"),
		"dmsetup create oml-u-a --table '0 2048 snapshot /dev/loop0 /dev/loop1 P 8'",
		"dmsetup create oml-u-b --table '0 2048 snapshot /dev/mapper/oml-u-a /dev/loop2 P 8'",
		"dmsetup create oml-u-c --table '0 2048 snapshot /dev/mapper/oml-u-b /dev/loop3 P 8'",
		"truncate -s 1048576 " + filepath.Join(f.snap, "u", "c.cow"),
		"kpartx -a -v /dev/mapper/oml-u-c",
		"mount -o ro /dev/mapper/oml-u-c",
		"--initrd=",
		"kexec -e",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("missing %q in:\n%s", w, all)
		}
	}
	if !strings.Contains(all, "'--command-line=root=/dev/mapper/oml-u-c1 rw rdinit=/omloader/preinit omloader.plan=/omloader/plan.json'") {
		t.Errorf("cmdline:\n%s", all)
	}
}

func TestPartialChain(t *testing.T) {
	f, res := setup(t, "good", chain3(), "")
	f.o.Snapshot = "b"
	f.o.Force = true
	r, dev, err := run(t, f, res)
	if err != nil || dev != "/dev/mapper/oml-u-b" {
		t.Fatal(dev, err)
	}
	if strings.Contains(strings.Join(r.Cmds, "\n"), "oml-u-c") {
		t.Fatal("c must not be used")
	}
}

func TestBaseReadOnly(t *testing.T) {
	f, res := setup(t, "good", nil, "")
	f.o.Snapshot = "base"
	f.o.RO = true
	r, dev, err := run(t, f, res)
	if err != nil || dev != "/dev/mapper/oml-u-base" {
		t.Fatal(dev, err)
	}
	all := strings.Join(r.Cmds, "\n")
	if !strings.Contains(all, "dmsetup create --readonly oml-u-base --table '0 2048 linear /dev/loop0 0'") || strings.Contains(all, "snapshot ") {
		t.Fatal(all)
	}
}

func TestBaseEphemeralChild(t *testing.T) {
	f, res := setup(t, "good", chain3(), "")
	f.o.Snapshot = "base"
	p, err := BuildPlan(f.o, res, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Layers) != 1 || !strings.HasPrefix(p.Layers[0].Node, "tmp-") || p.Tree.Nodes[p.Layers[0].Node].Parent != "" {
		t.Fatalf("%+v", p.Layers)
	}
	if p.Tree.Current != "c" {
		t.Fatalf("current must be unchanged, got %q", p.Tree.Current)
	}
}

func TestRefuseNonLeaf(t *testing.T) {
	f, res := setup(t, "good", chain3(), "")
	f.o.Snapshot = "a"
	if _, err := BuildPlan(f.o, res, f.dir); err == nil || !strings.Contains(err.Error(), "has children") {
		t.Fatal(err)
	}
	f.o.Force = true
	if _, err := BuildPlan(f.o, res, f.dir); err != nil {
		t.Fatal(err)
	}
}

func TestMissingSHAOK(t *testing.T) {
	f, res := setup(t, "", nil, "")
	if _, _, err := run(t, f, res); err != nil {
		t.Fatal(err)
	}
}

func TestChecksumMismatch(t *testing.T) {
	f, res := setup(t, strings.Repeat("a", 64), nil, "")
	if _, _, err := run(t, f, res); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatal(err)
	}
}

func TestSnapshotNotFound(t *testing.T) {
	f, res := setup(t, "good", chain3(), "")
	f.o.Snapshot = "zzz"
	if _, _, err := run(t, f, res); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatal(err)
	}
}

func TestNewSnapshotAndRootPartition(t *testing.T) {
	f, res := setup(t, "good", chain3(), "  root_partition: 2\n")
	f.o.Snapshot = "new:d"
	p, err := BuildPlan(f.o, res, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Layers) != 4 || p.Layers[3].Node != "d" || !p.NewTree {
		t.Fatalf("%+v", p.Layers)
	}
	r := &DryRunner{}
	if _, err := Execute(f.o, res, p, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.Cmds, "\n"), "mount -o ro /dev/mapper/oml-u-d2") {
		t.Fatal(r.Cmds)
	}
}

func TestGuestInitrdAndSymlinks(t *testing.T) {
	f, res := setup(t, "good", chain3(), "")
	mnt := t.TempDir()
	os.MkdirAll(filepath.Join(mnt, "boot"), 0o755)
	os.MkdirAll(filepath.Join(mnt, "usr/lib/modules/6.1.0-x/kernel"), 0o755)
	os.Symlink("usr/lib", filepath.Join(mnt, "lib"))
	os.WriteFile(filepath.Join(mnt, "boot/vmlinuz-6.1.0-x"), []byte("KERNEL"), 0o644)
	os.WriteFile(filepath.Join(mnt, "boot/initrd.img-6.1.0-x"), []byte("INITRD"), 0o644)
	os.Symlink("/boot/vmlinuz-6.1.0-x", filepath.Join(mnt, "boot/vmlinuz")) // absolute symlinks
	os.Symlink("/boot/initrd.img-6.1.0-x", filepath.Join(mnt, "boot/initrd.img"))
	os.WriteFile(filepath.Join(mnt, "usr/lib/modules/6.1.0-x/kernel/dm-snapshot.ko"), []byte("MOD"), 0o644)
	mi := filepath.Join(t.TempDir(), "mountinfo")
	os.WriteFile(mi, []byte("30 1 8:1 / / rw - ext4 /dev/vda1 rw\n40 30 8:17 / "+filepath.Dir(f.snap)+" rw - ext4 /dev/vdb1 rw\n"), 0o644)
	self := filepath.Join(t.TempDir(), "self")
	os.WriteFile(self, []byte("SELF"), 0o755)
	f.o.SelfBinary, f.o.MountInfoPath = self, mi
	f.o.Tools = map[string]string{}
	for _, n := range GuestTools {
		f.o.Tools[n] = "/bin/ls"
	}
	var cap Captured
	f.o.Capture = &cap
	p, err := BuildPlan(f.o, res, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	r := &DryRunner{MntDir: mnt}
	r.Respond = func(l string) (string, bool) {
		if strings.HasPrefix(l, "modprobe") && strings.HasSuffix(l, " dm-snapshot") {
			return "insmod " + mnt + "/lib/modules/6.1.0-x/kernel/dm-snapshot.ko\n", true
		}
		return "", false
	}
	if _, err := Execute(f.o, res, p, r); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(r.Cmds, "\n")
	if !strings.Contains(all, "kexec -l "+mnt+"/boot/vmlinuz-6.1.0-x ") {
		t.Errorf("kernel symlink not resolved inside root:\n%s", all)
	}
	gp := cap.Plan
	if gp == nil || len(gp.Layers) != 3 || len(gp.Storages) != 1 && len(gp.Storages) != 2 || len(gp.Modules) != 1 || gp.RootIndex != 1 || gp.RootDev != "/dev/mapper/oml-u-c1" {
		t.Fatalf("%+v", gp)
	}
	if gp.Layers[0].Cow.Rel == "" || gp.Storages[0].UUID == "" && gp.Storages[0].Label == "" {
		t.Fatalf("%+v", gp)
	}
	zr, err := gzip.NewReader(bytes.NewReader(cap.Extra))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	for _, w := range []string{"omloader/plan.json", "omloader/preinit", "omloader/omloader", "omloader/bin/dmsetup", "omloader/modules/000-dm-snapshot.ko", "TRAILER!!!"} {
		if !bytes.Contains(raw, []byte(w)) {
			t.Errorf("cpio lacks %s", w)
		}
	}
	if !strings.Contains(cap.Cmdline, "rdinit=/omloader/preinit") {
		t.Error(cap.Cmdline)
	}
}

func TestGuestAssemble(t *testing.T) {
	gp := &GuestPlan{Version: 1, Image: "u", Sectors: 2048, BaseDM: "oml-u-base", RootIndex: 1, RootDev: "/dev/mapper/oml-u-b1",
		Storages: []StorageRef{{ID: "s0", UUID: "U0", FSType: "exfat"}, {ID: "s1", UUID: "U1", FSType: "ext4", RW: true}},
		Base:     FileRef{"s0", "images/u/base.raw"},
		Layers:   []GuestLayer{{"a", "oml-u-a", FileRef{"s1", "snapshots/u/a.cow"}}, {"b", "oml-u-b", FileRef{"s1", "snapshots/u/b.cow"}}},
		Modules:  []string{"/omloader/modules/000-x.ko"}}
	r := &DryRunner{}
	mr := t.TempDir()
	root, err := GuestAssemble(gp, r, GuestOptions{MountPrefix: mr + "/m-", Tries: 1, Delay: time.Millisecond, DevExists: func(string) bool { return true }})
	if err != nil || root != "/dev/mapper/oml-u-b1" {
		t.Fatal(root, err)
	}
	all := strings.Join(r.Cmds, "\n")
	for _, w := range []string{
		"insmod /omloader/modules/000-x.ko",
		"blkid -U U0", "mount -t exfat -o ro /dev/dry-by-uuid " + mr + "/m-s0",
		"mount -t ext4 -o rw /dev/dry-by-uuid " + mr + "/m-s1",
		"losetup -r -f --show " + mr + "/m-s0/images/u/base.raw",
		"dmsetup create oml-u-a --table '0 2048 snapshot /dev/loop0 /dev/loop1 P 8'",
		"dmsetup create oml-u-b --table '0 2048 snapshot /dev/mapper/oml-u-a /dev/loop2 P 8'",
		"kpartx -a -v /dev/mapper/oml-u-b",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("missing %q in:\n%s", w, all)
		}
	}
}

func TestSkipChecksum(t *testing.T) {
	f, res := setup(t, strings.Repeat("a", 64), nil, "")
	res.Config.Verify.BaseChecksum = "skip"
	if _, _, err := run(t, f, res); err != nil {
		t.Fatal(err)
	}
}
