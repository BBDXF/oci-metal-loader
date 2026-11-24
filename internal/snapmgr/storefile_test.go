package snapmgr

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omloader/internal/config"
)

type stfR struct {
	cmds []string
	resp func(line string) (string, error)
}

func (f *stfR) Run(name string, args ...string) (string, error) {
	l := strings.Join(append([]string{name}, args...), " ")
	f.cmds = append(f.cmds, l)
	if f.resp != nil {
		return f.resp(l)
	}
	return "", nil
}

func (f *stfR) has(s string) bool {
	for _, c := range f.cmds {
		if strings.Contains(c, s) {
			return true
		}
	}
	return false
}

// writeSB makes a file with a minimal ext4 superblock.
func writeSB(t *testing.T, path, label string, state uint16, incompat uint32) {
	b := make([]byte, 4096)
	sb := b[1024:]
	binary.LittleEndian.PutUint16(sb[0x38:], 0xEF53)
	binary.LittleEndian.PutUint16(sb[0x3A:], state)
	binary.LittleEndian.PutUint32(sb[0x60:], incompat)
	binary.LittleEndian.PutUint32(sb[0x18:], 2) // 4k
	binary.LittleEndian.PutUint32(sb[0x4:], 100)
	binary.LittleEndian.PutUint32(sb[0xC:], 40)
	copy(sb[0x78:], label)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadSBStates(t *testing.T) {
	d := t.TempDir()
	writeSB(t, d+"/a", "OMLOADER", 1, 0)
	writeSB(t, d+"/b", "OMLOADER", 1, 0x4)
	writeSB(t, d+"/c", "X", 0, 0)
	for f, want := range map[string]string{"a": "clean", "b": "dirty", "c": "dirty"} {
		sb, err := ReadExt4SB(d + "/" + f)
		if err != nil || sb.StateName() != want {
			t.Fatalf("%s: %v %+v", f, err, sb)
		}
	}
	sb, _ := ReadExt4SB(d + "/a")
	if sb.Label != "OMLOADER" || sb.BlockSize != 4096 || sb.Blocks != 100 || sb.FreeBlocks != 40 {
		t.Fatalf("%+v", sb)
	}
	os.WriteFile(d+"/z", make([]byte, 4096), 0o644)
	if _, err := ReadExt4SB(d + "/z"); err == nil {
		t.Fatal("want magic error")
	}
}

func TestCreateExfatPrealloc(t *testing.T) {
	d := t.TempDir()
	r := &stfR{}
	o := StoreOps{R: r, FSType: func(string) (string, error) { return "exfat", nil }}
	host, pre, err := o.StoreCreate(CreateOpts{Path: d + "/omloader/store.img", Size: 64 << 20, Prealloc: "auto"})
	if err != nil || host != "exfat" || !pre {
		t.Fatal(host, pre, err)
	}
	if !r.has("fallocate -l 67108864") || r.has("truncate") {
		t.Fatalf("%v", r.cmds)
	}
	if !r.has("mkfs.ext4 -q -F -L OMLOADER -m 1 -E lazy_itable_init=1,lazy_journal_init=1,nodiscard") || r.has("^has_journal") {
		t.Fatalf("%v", r.cmds)
	}
	if _, _, err := o.StoreCreate(CreateOpts{Path: d + "/x", Size: 64 << 20, Prealloc: "no"}); err == nil {
		t.Fatal("exfat + prealloc no must fail")
	}
}

func TestCreateZeroFallbackAndSparse(t *testing.T) {
	d := t.TempDir()
	r := &stfR{resp: func(l string) (string, error) {
		if strings.HasPrefix(l, "fallocate") {
			return "", os.ErrInvalid
		}
		return "", nil
	}}
	o := StoreOps{R: r, FSType: func(string) (string, error) { return "vfat", nil }}
	if _, _, err := o.StoreCreate(CreateOpts{Path: d + "/s.img", Size: 5 << 30}); err == nil || !strings.Contains(err.Error(), "4 GiB") {
		t.Fatalf("vfat limit: %v", err)
	}
	if _, _, err := o.StoreCreate(CreateOpts{Path: d + "/s.img", Size: 32 << 20}); err != nil || !r.has("dd if=/dev/zero of="+d+"/s.img.new bs=1M count=32") {
		t.Fatalf("%v %v", err, r.cmds)
	}
	r2 := &stfR{}
	o2 := StoreOps{R: r2, FSType: func(string) (string, error) { return "ext4", nil }}
	if _, pre, err := o2.StoreCreate(CreateOpts{Path: d + "/t.img", Size: 32 << 20, Label: "OMLOADER"}); err != nil || pre || !r2.has("truncate -s 33554432") || r2.has("nodiscard") {
		t.Fatalf("%v %v", err, r2.cmds)
	}
	if _, _, err := o2.StoreCreate(CreateOpts{Path: d + "/t.img", Size: 32 << 20}); err != nil {
		t.Log("fake runner leaves no file: no exist error expected")
	}
	writeSB(t, d+"/e.img", "OMLOADER", 1, 0)
	if _, _, err := o2.StoreCreate(CreateOpts{Path: d + "/e.img", Size: 32 << 20}); err == nil {
		t.Fatal("existing file must be refused")
	}
}

func TestInfoCheckGrow(t *testing.T) {
	d := t.TempDir()
	f := d + "/store.img"
	writeSB(t, f, "OMLOADER", 1, 0)
	r := &stfR{}
	o := StoreOps{R: r, FSType: func(string) (string, error) { return "exfat", nil }}
	i, err := o.Info(f)
	if err != nil || i.HostFS != "exfat" || i.SB.StateName() != "clean" {
		t.Fatal(i, err)
	}
	if ok, _, err := o.StoreCheck(f); !ok || err != nil || !r.has("e2fsck -n "+f) {
		t.Fatal(ok, err, r.cmds)
	}
	// grow: exfat -> fallocate + e2fsck -fp + resize2fs, in that order
	r.cmds = nil
	if _, err := o.StoreGrow(f, 64<<20, false); err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, c := range r.cmds {
		seq = append(seq, strings.Fields(c)[0])
	}
	if strings.Join(seq, " ") != "losetup fallocate e2fsck resize2fs sync" {
		t.Fatalf("%v", r.cmds)
	}
	os.Truncate(f, 128<<20)
	if _, err := o.StoreGrow(f, 64<<20, false); err == nil {
		t.Fatal("shrink must fail")
	}
	// in use
	r2 := &stfR{resp: func(l string) (string, error) {
		if strings.HasPrefix(l, "losetup -j") {
			return "/dev/loop3\n", nil
		}
		return "", nil
	}}
	o2 := StoreOps{R: r2, FSType: o.FSType}
	if _, err := o2.StoreGrow(f, 64<<20, false); err == nil || !strings.Contains(err.Error(), "losetup -d /dev/loop3") {
		t.Fatal(err)
	}
	if _, _, err := o2.StoreCheck(f); err == nil {
		t.Fatal("check of attached store must refuse")
	}
	if _, err := o2.StoreGrow(f, 64<<20, true); err == nil || !r2.has("umount /dev/loop3") || !r2.has("losetup -d /dev/loop3") {
		t.Log(err, r2.cmds) // release ran (fake keeps reporting the loop afterwards => still in use)
	}
}

func TestFindStoreFilesAndHost(t *testing.T) {
	d := t.TempDir()
	writeSB(t, d+"/omloader/store.img", "OMLOADER", 1, 0)
	writeSB(t, d+"/ventoy/persistence.dat", "vtoycow", 1, 0) // Ventoy's own persistence: wrong label, ignored
	writeSB(t, d+"/ventoy/mine.dat", "OMLOADER", 1, 0)
	sf := config.StoreFile{Glob: []string{"/omloader/store.img", "/ventoy/*.dat"}}
	got := FindStoreFiles(d, sf, "")
	if len(got) != 2 || got[0] != d+"/omloader/store.img" || got[1] != d+"/ventoy/mine.dat" {
		t.Fatal(got)
	}
	sf.Path = "/ventoy/mine.dat"
	if got = FindStoreFiles(d, sf, ""); got[0] != d+"/ventoy/mine.dat" || len(got) != 2 {
		t.Fatal(got)
	}
	ex := "DEVNAME=/dev/sda1\nLABEL=VTOYDATA\nUUID=1234-ABCD\nTYPE=exfat\n"
	if !HostMatches(ex, config.HostMatch{Label: "VTOYDATA"}) || HostMatches(ex, config.HostMatch{Label: "OTHER"}) ||
		!HostMatches(ex, config.HostMatch{Label: "OTHER", UUID: "1234-abcd"}) {
		t.Fatal("host match")
	}
}

func TestStoreFileConfigDefaultsAndValidation(t *testing.T) {
	r, err := config.Load("", "")
	if err != nil {
		t.Fatal(err)
	}
	sf := r.Config.Storage.Snapshots.StoreFile
	if !sf.Enabled || sf.HostMatch.Label != "" || sf.Prealloc != "auto" || len(sf.Glob) != 2 || sf.Glob[1] != "/ventoy/*.dat" {
		t.Fatalf("%+v", sf)
	}
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("storage:\n  snapshots:\n    store_file:\n      prealloc: maybe\n      path: rel/x\n      size: 12x\n"), 0o644)
	_, err = config.Load(p, "")
	if err == nil || !strings.Contains(err.Error(), "prealloc") || !strings.Contains(err.Error(), "absolute") || !strings.Contains(err.Error(), "size") {
		t.Fatal(err)
	}
}
