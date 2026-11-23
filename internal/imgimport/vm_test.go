package imgimport

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omloader/internal/boot"
)

func vmTree(t *testing.T) string {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "boot"), 0o755)
	for _, f := range []string{"vmlinuz-6.8.0-9-generic", "vmlinuz-6.8.0-10-generic", "initrd.img-6.8.0-9-generic", "initrd.img-6.8.0-10-generic", "vmlinuz-6.9.0.old"} {
		os.WriteFile(filepath.Join(root, "boot", f), []byte("x"), 0o644)
	}
	os.Symlink("/boot/vmlinuz-6.8.0-9-generic", filepath.Join(root, "vmlinuz")) // absolute link must not escape root
	return root
}

func TestFindKernel(t *testing.T) {
	k, i, ok := FindKernel(vmTree(t))
	if !ok || k != "/boot/vmlinuz-6.8.0-10-generic" || i != "/boot/initrd.img-6.8.0-10-generic" {
		t.Fatal(k, i, ok)
	}
	// only the root /vmlinuz symlink (absolute target) + initrd.img
	r := t.TempDir()
	os.MkdirAll(filepath.Join(r, "boot"), 0o755)
	os.WriteFile(filepath.Join(r, "boot", "vmlinuz-5.10"), nil, 0o644)
	os.WriteFile(filepath.Join(r, "boot", "initrd.img-5.10"), nil, 0o644)
	os.Remove(filepath.Join(r, "boot", "vmlinuz-5.10"))
	os.WriteFile(filepath.Join(r, "boot", "real"), []byte("k"), 0o644)
	os.Symlink("/boot/real", filepath.Join(r, "vmlinuz"))
	os.Symlink("boot/initrd.img-5.10", filepath.Join(r, "initrd.img"))
	if k, i, ok := FindKernel(r); !ok || k != "/vmlinuz" || i != "/initrd.img" {
		t.Fatal(k, i, ok)
	}
	if _, _, ok := FindKernel(t.TempDir()); ok {
		t.Fatal("empty tree")
	}
}

func vmRunner(t *testing.T, tree string) *boot.DryRunner {
	return &boot.DryRunner{MntDir: tree, Respond: func(l string) (string, bool) {
		switch {
		case strings.HasPrefix(l, "qemu-img convert"):
			f := strings.Fields(l)
			os.WriteFile(f[len(f)-1], []byte("RAWDISK"), 0o644)
			return "", true
		case strings.HasPrefix(l, "blkid") && strings.HasSuffix(l, "p1"):
			return "vfat\n", true
		}
		return "", false
	}}
}

func TestProbeAndImportVM(t *testing.T) {
	r := vmRunner(t, vmTree(t))
	ki, err := Probe(r, "/x/base.raw", io.Discard)
	if err != nil || ki.RootPartition != 2 || ki.Kernel != "/boot/vmlinuz-6.8.0-10-generic" {
		t.Fatal(ki, err)
	}
	want := []string{"losetup --find --show -r /x/base.raw", "kpartx -a -v -r /dev/loop0", "mount -o ro,noload /dev/mapper/loop0p2", "umount", "kpartx -d /dev/loop0", "losetup -d /dev/loop0"}
	all := strings.Join(r.Cmds, "\n")
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("missing command %q in:\n%s", w, all)
		}
	}
	if strings.Contains(all, "mount -o ro,noload /dev/mapper/loop0p1") {
		t.Error("vfat partition must not be mounted")
	}

	// full Import, with probe
	disk := filepath.Join(t.TempDir(), "disk.qcow2")
	os.WriteFile(disk, []byte("qcow"), 0o644)
	out := t.TempDir()
	r = vmRunner(t, vmTree(t))
	d, err := Import(Options{Kind: "lxd-vm", Path: disk, Name: "vm1", Out: out, Runner: r})
	if err != nil {
		t.Fatal(err)
	}
	y, _ := os.ReadFile(filepath.Join(d, "image.yaml"))
	for _, w := range []string{"type: lxd-vm", "format: raw", "base: base.raw", "root_partition: 2", "path: /boot/vmlinuz-6.8.0-10-generic", "initrd: /boot/initrd.img-6.8.0-10-generic", "mode: dm-snapshot", "sha256: "} {
		if !strings.Contains(string(y), w) {
			t.Errorf("image.yaml lacks %q:\n%s", w, y)
		}
	}
	if _, err := os.Stat(filepath.Join(d, "base.raw")); err != nil {
		t.Fatal(err)
	}
	// --no-probe: no losetup at all
	r = vmRunner(t, "")
	d, err = Import(Options{Kind: "lxd-vm", Path: disk, Name: "vm2", Out: out, Runner: r, NoProbe: true})
	if err != nil {
		t.Fatal(err)
	}
	y, _ = os.ReadFile(filepath.Join(d, "image.yaml"))
	if strings.Contains(strings.Join(r.Cmds, "\n"), "losetup") || strings.Contains(string(y), "root_partition") {
		t.Fatalf("no-probe still probed:\n%s", y)
	}
	if _, err := Import(Options{Kind: "lxd-vm", Path: disk, Name: "vm3", Out: out, Runner: r, Data: []string{"a:/a:dir"}}); err == nil {
		t.Fatal("data on vm")
	}
}
