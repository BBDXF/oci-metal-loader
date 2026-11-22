package boot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omloader/internal/config"
	"omloader/internal/snapshot"
)

func dmFixture(t *testing.T) (snap, images string) {
	d := t.TempDir()
	snap, images = filepath.Join(d, "snaps"), filepath.Join(d, "images")
	os.MkdirAll(filepath.Join(images, "vm"), 0o755)
	os.MkdirAll(filepath.Join(snap, "vm"), 0o755)
	os.WriteFile(filepath.Join(images, "vm", "base.raw"), make([]byte, 1<<20), 0o644)
	os.WriteFile(filepath.Join(images, "vm", "image.yaml"), []byte("name: vm\ntype: vm\nformat: raw\nbase: base.raw\nkernel:\n  path: /boot/vmlinuz\nsnapshots:\n  mode: dm-snapshot\n"), 0o644)
	tr := snapshot.New("vm", "")
	tr.Create("v1", "base")
	tr.Create("v2", "v1")
	tr.Save(filepath.Join(snap, "vm", "tree.json"))
	for _, n := range []string{"v1", "v2"} {
		os.WriteFile(filepath.Join(snap, "vm", n+".cow"), make([]byte, 4096), 0o644)
	}
	return
}

func TestFlattenDM(t *testing.T) {
	snap, images := dmFixture(t)
	r := &DryRunner{}
	var copied string
	cp := func(src, dst string) error {
		copied = src + " -> " + dst
		return os.WriteFile(dst, []byte("flat-disk"), 0o644)
	}
	d, err := FlattenDM(r, DMOptions{SnapRoot: snap, ImagesRoot: images, Image: "vm", Node: "v2", Copy: cp})
	if err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(r.Cmds, "\n")
	for _, want := range []string{
		"dmsetup create oml-x-vm-v1 --table '0 2048 snapshot /dev/loop0 /dev/loop1 P 8'",
		"dmsetup create oml-x-vm-v2 --table '0 2048 snapshot /dev/mapper/oml-x-vm-v1 /dev/loop2 P 8'",
		"dmsetup remove oml-x-vm-v2", "losetup -d /dev/loop0",
	} {
		if !strings.Contains(cmds, want) {
			t.Errorf("missing %q in\n%s", want, cmds)
		}
	}
	if copied != "/dev/mapper/oml-x-vm-v2 -> "+filepath.Join(d, "base.raw") {
		t.Errorf("copy: %q", copied)
	}
	if strings.Index(cmds, "dmsetup remove oml-x-vm-v2") > strings.Index(cmds, "dmsetup remove oml-x-vm-v1") {
		t.Error("teardown must be top-down")
	}
	res, err := config.Load("", filepath.Join(d, "image.yaml"))
	if err != nil || res.Image.Name != "vm-flat-v2" || res.Image.Base != "base.raw" || len(res.Image.SHA256) != 64 {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err := os.Stat(filepath.Join(snap, "vm-flat-v2")); err == nil {
		t.Error("new image must start with an empty tree dir")
	}
	if _, err := FlattenDM(r, DMOptions{SnapRoot: snap, ImagesRoot: images, Image: "vm", Node: "v2", Copy: cp}); err == nil {
		t.Error("existing target must be refused")
	}
	// missing layer is refused before anything runs
	os.Remove(filepath.Join(snap, "vm", "v1.cow"))
	r2 := &DryRunner{}
	if _, err := FlattenDM(r2, DMOptions{SnapRoot: snap, ImagesRoot: images, Image: "vm", Node: "v2", NewName: "x"}); err == nil || len(r2.Cmds) != 0 {
		t.Fatalf("%v %v", err, r2.Cmds)
	}
}

func TestExportDM(t *testing.T) {
	snap, images := dmFixture(t)
	r := &DryRunner{}
	out := filepath.Join(t.TempDir(), "o.img")
	var copied string
	cp := func(src, dst string) error { copied = src + " -> " + dst; return nil }
	if err := ExportDM(r, DMOptions{SnapRoot: snap, ImagesRoot: images, Image: "vm", Node: "v1", Raw: out, Copy: cp}); err != nil {
		t.Fatal(err)
	}
	if copied != "/dev/mapper/oml-x-vm-v1 -> "+out {
		t.Error(copied, r.Cmds)
	}
	no := func(string) (string, error) { return "", os.ErrNotExist }
	err := ExportDM(&DryRunner{}, DMOptions{SnapRoot: snap, ImagesRoot: images, Image: "vm", Node: "v1", Qcow2: out, Which: no})
	if err == nil || !strings.Contains(err.Error(), "--raw") {
		t.Fatalf("qemu-img hint: %v", err)
	}
	yes := func(s string) (string, error) { return "/usr/bin/" + s, nil }
	r = &DryRunner{}
	if err := ExportDM(r, DMOptions{SnapRoot: snap, ImagesRoot: images, Image: "vm", Node: "v1", Qcow2: out, Which: yes}); err != nil || !strings.Contains(strings.Join(r.Cmds, "\n"), "qemu-img convert -f raw -O qcow2 /dev/mapper/oml-x-vm-v1") {
		t.Fatal(err, r.Cmds)
	}
	if ExportDM(r, DMOptions{Image: "vm", Node: "v1"}) == nil {
		t.Fatal("needs a target")
	}
}

func TestDataVolumes(t *testing.T) {
	f := osetup(t, nil, "data:\n  - {name: e, mount: /var/lib/e, backend: ext4-file, size: 64M, snapshot: false}\n  - {name: d, mount: /srv/d, backend: dir}\n  - {name: n, mount: /mnt/n, backend: partition}\n")
	systemdRoot(f.o.NewRoot)
	f.o.Snapshot = "new:x"
	p, err := BuildPlan(f.o, f.res, f.img)
	if err != nil {
		t.Fatal(err)
	}
	var w strings.Builder
	f.o.Warn = &w
	r := &DryRunner{}
	if _, err := ExecuteOverlay(f.o, f.res, p, r); err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(r.Cmds, "\n")
	dr := filepath.Join(f.root, "data")
	for _, want := range []string{
		"truncate -s 67108864 " + filepath.Join(dr, "c", "e.img.new"),
		"mkfs.ext4 -q -F -L e",
		"mount -t ext4 -o loop " + filepath.Join(dr, "c", "e.img") + " " + filepath.Join(f.o.NewRoot, "var/lib/e"),
		"mount --bind " + filepath.Join(dr, "c", "d") + " " + filepath.Join(f.o.NewRoot, "srv/d"),
	} {
		if !strings.Contains(cmds, want) {
			t.Errorf("missing %q in\n%s", want, cmds)
		}
	}
	if !strings.Contains(w.String(), "partition") {
		t.Error("unsupported backend must warn")
	}
}

func TestSparseCopy(t *testing.T) {
	d := t.TempDir()
	src, dst := filepath.Join(d, "s"), filepath.Join(d, "d")
	data := make([]byte, 5<<20+123)
	copy(data[1<<20:], "hello")
	data[len(data)-1] = 7
	os.WriteFile(src, data, 0o644)
	if err := SparseCopy(src, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if len(got) != len(data) || string(got) != string(data) {
		t.Fatal("content/size mismatch")
	}
	// trailing zeros must keep the size
	os.WriteFile(src, make([]byte, 3<<20), 0o644)
	SparseCopy(src, dst)
	if st, _ := os.Stat(dst); st.Size() != 3<<20 {
		t.Fatal("size lost", st.Size())
	}
}
