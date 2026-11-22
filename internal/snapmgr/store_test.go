package snapmgr

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestCreateLayersBothModes(t *testing.T) {
	for _, mode := range []string{ModeDM, ModeOverlay} {
		s := newStore(t, mode)
		if err := s.Create("a", "", "first", []string{"t1"}); err != nil {
			t.Fatal(mode, err)
		}
		if !s.LayerExists("a") {
			t.Fatal(mode, "layer missing")
		}
		if mode == ModeDM {
			a, al := s.Usage("a")
			if a != 1<<20 || al >= a {
				t.Fatalf("COW must be sparse: %d/%d", a, al)
			}
		}
		// parent data untouched by child creation
		marker := filepath.Join(s.dir(), "a", "upper", "f")
		if mode == ModeOverlay {
			os.WriteFile(marker, []byte("x"), 0o644)
		} else {
			os.WriteFile(s.cowPath("a"), []byte("SNAPDATA"), 0o644)
		}
		if err := s.Create("b", "", "", nil); err != nil {
			t.Fatal(err)
		}
		if mode == ModeOverlay {
			if b, _ := os.ReadFile(marker); string(b) != "x" {
				t.Fatal("parent touched")
			}
		} else if b, _ := os.ReadFile(s.cowPath("a")); string(b[:8]) != "SNAPDATA" {
			t.Fatal("parent COW touched")
		}
		if s.Tree.Nodes["b"].Parent != "a" || s.Tree.Current != "b" {
			t.Fatal("tree")
		}
		out := s.List(ListOptions{Tree: true, Sizes: true})
		if !strings.Contains(out, "frozen") || !strings.Contains(out, "tags=t1") || !strings.Contains(out, `note="first"`) || !strings.Contains(out, "*") || !strings.Contains(out, "allocated") {
			t.Fatalf("list:\n%s", out)
		}
		// persisted
		s2, err := Open(s.Root, "img", false)
		if err != nil || len(s2.Tree.Nodes) != 2 {
			t.Fatal(err)
		}
	}
}

func TestCreateGuards(t *testing.T) {
	s := newStore(t, ModeOverlay)
	for _, n := range []string{"a", "b", "c", "d"} {
		if err := s.Create(n, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	err := s.Create("e", "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "flatten") {
		t.Fatalf("depth guard: %v", err)
	}
	if _, ok := s.Tree.Nodes["e"]; ok || s.LayerExists("e") {
		t.Fatal("failed create must leave nothing")
	}
	if err := s.Create("f", "base", "", nil); err != nil {
		t.Fatal(err) // different branch at depth 1 is fine
	}
	s.Statfs = func(string) (uint64, uint64, error) { return 1000, 50, nil }
	if err := s.Create("g", "base", "", nil); err == nil || !strings.Contains(err.Error(), "space guard") {
		t.Fatalf("space guard: %v", err)
	}
	s.Ret.MinFreePercent = 0
	if err := s.Create("g", "base", "", nil); err != nil {
		t.Fatal("guard disabled", err)
	}
	if s.Create("a", "", "", nil) == nil || s.Create("bad name", "", "", nil) == nil {
		t.Fatal("dup/invalid names")
	}
}

func TestRollbackDiscard(t *testing.T) {
	for _, mode := range []string{ModeDM, ModeOverlay} {
		s := newStore(t, mode)
		s.Create("a", "", "", nil)
		s.Create("b", "", "", nil)
		if err := s.Rollback("a", true); err == nil || !strings.Contains(err.Error(), "frozen") {
			t.Fatal("frozen discard must be refused", err)
		}
		if err := s.Rollback("a", false); err != nil || s.Tree.Current != "a" {
			t.Fatal(err)
		}
		// leaf discard wipes own data
		if mode == ModeOverlay {
			os.WriteFile(filepath.Join(s.dir(), "b", "upper", "junk"), []byte("1"), 0o644)
		} else {
			os.WriteFile(s.cowPath("b"), []byte("JUNKJUNKJUNKJUNK"), 0o644)
		}
		if err := s.Rollback("b", true); err != nil {
			t.Fatal(err)
		}
		if mode == ModeOverlay {
			if _, err := os.Stat(filepath.Join(s.dir(), "b", "upper", "junk")); err == nil {
				t.Fatal("upper not wiped")
			}
		} else {
			h, _ := readHeader(s.cowPath("b"))
			if st, err := ParseCowHeader(h); err != nil || st != CowFresh {
				t.Fatal("COW not fresh", err)
			}
		}
		if !s.LayerExists("b") {
			t.Fatal("layer recreated")
		}
		if err := s.Rollback("base", false); err != nil || s.Tree.Current != "" {
			t.Fatal(err)
		}
	}
}

func TestDeleteGCPrune(t *testing.T) {
	s := newStore(t, ModeOverlay)
	s.Create("a", "", "", nil)
	s.Create("b", "", "", nil)
	s.Create("c", "b", "", nil)
	s.Create("tmp-old", "base", "", nil)
	n := s.Tree.Nodes["tmp-old"]
	n.Created = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	s.Tree.Nodes["tmp-old"] = n
	s.Tree.Current = "c"
	if _, err := s.Delete("a", false, false); err == nil {
		t.Fatal("non-leaf delete must be refused")
	}
	if _, err := s.Delete("c", false, false); err == nil {
		t.Fatal("current must need --force")
	}
	// gc: old tmp only
	plan, err := s.GC(24*time.Hour, true)
	if err != nil || len(plan) != 1 || !s.LayerExists("tmp-old") {
		t.Fatal(plan, err)
	}
	if _, err := s.GC(24*time.Hour, false); err != nil || s.LayerExists("tmp-old") {
		t.Fatal("gc did not remove layer")
	}
	// prune keep_last=1: leaves: c (current). tag protects
	s.Create("x", "a", "", []string{"keep"})
	s.Tree.Current = "c"
	plan, _ = s.Prune(true)
	if len(plan) != 0 && contains(plan, "x") {
		t.Fatal("tagged kept", plan)
	}
	s.Tree.Untag("x", "keep")
	plan, _ = s.Prune(true)
	if len(plan) != 1 || plan[0] != "x" || !s.LayerExists("x") {
		t.Fatalf("dry-run plan %v", plan)
	}
	if _, err := s.Prune(false); err != nil || s.LayerExists("x") || s.Tree.Nodes["x"].Parent != "" {
		t.Fatal("prune did not delete")
	}
	del, err := s.Delete("a", true, true)
	if err != nil || len(del) != 3 || s.LayerExists("a") || s.LayerExists("c") {
		t.Fatal(del, err)
	}
	if s.Tree.Current != "" {
		t.Fatal("current should fall back to base", s.Tree.Current)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
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

func TestVerify(t *testing.T) {
	s := newStore(t, ModeDM)
	s.Create("a", "", "", nil)
	s.Create("b", "", "", nil)
	if ps := s.Verify(VerifyOptions{}); len(ps) != 0 {
		t.Fatal(ps)
	}
	// valid header on a, corrupt b, orphan, missing
	f, _ := os.OpenFile(s.cowPath("a"), os.O_WRONLY, 0)
	f.Write(hdr(CowMagic, 1, 1, 8))
	f.Close()
	f, _ = os.OpenFile(s.cowPath("b"), os.O_WRONLY, 0)
	f.Write(hdr(CowMagic, 0, 1, 8))
	f.Close()
	os.WriteFile(filepath.Join(s.dir(), "zombie.cow"), nil, 0o644)
	s.Tree.Nodes["ghost"] = s.Tree.Nodes["a"]
	g := s.Tree.Nodes["ghost"]
	g.File = "ghost.cow"
	s.Tree.Nodes["ghost"] = g
	s.BaseSHA, s.Tree.BaseSHA256 = "aa", "bb"
	var all []string
	for _, p := range s.Verify(VerifyOptions{}) {
		all = append(all, p.String())
	}
	j := strings.Join(all, "\n")
	for _, want := range []string{"b: invalid COW header", "orphan layer", "ghost: missing COW file", "base_sha256"} {
		if !strings.Contains(j, want) {
			t.Errorf("missing %q in:\n%s", want, j)
		}
	}
	if strings.Contains(j, "a: invalid") {
		t.Error("a is valid")
	}
	// lite
	if err := s.VerifyLite(nil); err == nil || !strings.Contains(err.Error(), "verify") {
		t.Fatal("sha mismatch must refuse", err)
	}
	s.BaseSHA = "bb"
	if err := s.VerifyLite([]string{"ghost"}); err == nil {
		t.Fatal("missing ancestor")
	}
	if err := s.VerifyLite([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	// overlay + cycle
	o := newStore(t, ModeOverlay)
	o.Create("a", "", "", nil)
	os.RemoveAll(filepath.Join(o.dir(), "a", "work"))
	os.MkdirAll(filepath.Join(o.dir(), "stray"), 0o755)
	j = ""
	for _, p := range o.Verify(VerifyOptions{}) {
		j += p.String() + "\n"
	}
	if !strings.Contains(j, "missing work") || !strings.Contains(j, "orphan") {
		t.Fatal(j)
	}
	n := o.Tree.Nodes["a"]
	n.Parent = "a"
	o.Tree.Nodes["a"] = n
	if j = ""; true {
		for _, p := range o.Verify(VerifyOptions{}) {
			j += p.String()
		}
	}
	if !strings.Contains(j, "cycle") {
		t.Fatal("cycle", j)
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
