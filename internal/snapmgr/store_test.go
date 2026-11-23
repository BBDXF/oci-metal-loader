package snapmgr

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omloader/internal/config"
)

func newStore(t *testing.T, mode string) *Store {
	root := t.TempDir()
	s, err := Open(root, "img", true)
	if err != nil {
		t.Fatal(err)
	}
	s.Mode, s.BaseSize, s.MaxDepth = mode, 1<<20, 4
	s.Ret = config.Retention{KeepLast: 1, MinFreePercent: 10, KeepTagged: true}
	s.Statfs = func(string) (uint64, uint64, error) { return 1000, 500, nil }
	return s
}

func hdr(magic, valid, version, chunk uint32) []byte {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint32(b[0:], magic)
	binary.LittleEndian.PutUint32(b[4:], valid)
	binary.LittleEndian.PutUint32(b[8:], version)
	binary.LittleEndian.PutUint32(b[12:], chunk)
	return b
}

func TestCowHeader(t *testing.T) {
	if st, err := ParseCowHeader(make([]byte, 16)); err != nil || st != CowFresh {
		t.Fatal("zero = fresh")
	}
	if st, err := ParseCowHeader(hdr(CowMagic, 1, 1, 8)); err != nil || st != CowValid {
		t.Fatal(err)
	}
	if string(hdr(CowMagic, 1, 1, 8)[:4]) != "SnAp" {
		t.Fatal("magic bytes must spell SnAp")
	}
	for name, b := range map[string][]byte{"magic": hdr(0x1234, 1, 1, 8), "invalid": hdr(CowMagic, 0, 1, 8), "version": hdr(CowMagic, 1, 2, 8), "chunk": hdr(CowMagic, 1, 1, 6), "short": {1, 2, 3}} {
		if _, err := ParseCowHeader(b); err == nil {
			t.Fatal(name, "must fail")
		}
	}
}

func TestSpaceAndSize(t *testing.T) {
	fn := func(string) (uint64, uint64, error) { return 1000, 99, nil }
	if err := CheckSpace("/nonexistent/x", 10, fn); err == nil || !strings.Contains(err.Error(), "9.9%") {
		t.Fatal(err)
	}
	if CheckSpace("/", 0, fn) != nil {
		t.Fatal("0 disables")
	}
	if _, _, _, err := FreePercent("/tmp", nil); err != nil {
		t.Fatal(err) // real statfs works
	}
	for in, want := range map[string]int64{"64M": 64 << 20, "1G": 1 << 30, "512": 512, "1.5GiB": 3 << 29, "2k": 2048} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("%s: %d %v", in, got, err)
		}
	}
	if _, err := ParseSize("x"); err == nil {
		t.Fatal("bad size")
	}
	if Human(1536) != "1.5KiB" {
		t.Fatal(Human(1536))
	}
}

type fakeR struct{ cmds []string }

func (f *fakeR) Run(n string, a ...string) (string, error) {
	f.cmds = append(f.cmds, n+" "+strings.Join(a, " "))
	if n == "truncate" {
		os.WriteFile(a[len(a)-1], nil, 0o644)
	}
	return "", nil
}

func TestEnsureExt4(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "vol.img")
	r := &fakeR{}
	c, err := EnsureExt4File(r, p, 64<<20, "a-very-long-volume-name")
	if err != nil || !c || len(r.cmds) != 2 || !strings.Contains(r.cmds[1], "mkfs.ext4 -q -F -L a-very-long-vol") {
		t.Fatal(c, err, r.cmds)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("not renamed into place")
	}
	if c, _ := EnsureExt4File(r, p, 64<<20, "x"); c || len(r.cmds) != 2 {
		t.Fatal("existing volume must be left alone")
	}
}
