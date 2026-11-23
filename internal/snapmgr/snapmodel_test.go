package snapmgr

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omloader/internal/config"
)

func newModelStore(t *testing.T, mode string) *Store {
	root := t.TempDir()
	s, err := Open(root, "img", true)
	if err != nil {
		t.Fatal(err)
	}
	s.Mode, s.BaseSize, s.MaxDepth = mode, 1<<20, 4
	s.Ret = config.Retention{KeepLast: 1, MinFreePercent: 10, KeepTagged: true}
	s.Statfs = func(string) (uint64, uint64, error) { return 1000, 500, nil }
	s.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	BootIDPath = filepath.Join(root, "boot_id")
	os.WriteFile(BootIDPath, []byte("boot-1\n"), 0o644)
	return s
}

// boot simulates `omloader boot` continuing the live state: lazily creates the live node + layer, writes a marker.
func boot(t *testing.T, s *Store, marker string) string {
	t.Helper()
	id, created := s.Tree.EnsureLive()
	if created {
		if err := s.CreateLayer(id); err != nil {
			t.Fatal(err)
		}
	}
	if s.Mode == ModeOverlay && marker != "" {
		if err := os.WriteFile(filepath.Join(s.dir(), id, "upper", marker), []byte(marker), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCreateFreezesAndContinues(t *testing.T) {
	for _, mode := range []string{ModeDM, ModeOverlay} {
		s := newModelStore(t, mode)
		if err := s.Create("S", "", nil); err == nil || !strings.Contains(err.Error(), "nothing to snapshot") {
			t.Fatal(mode, "fresh image has nothing to snapshot:", err)
		}
		n1 := boot(t, s, "m1")
		if n1b := boot(t, s, "m2"); n1b != n1 || len(s.Tree.Nodes) != 1 {
			t.Fatal(mode, "booting twice must continue the same layer", n1, n1b, s.Tree.Nodes)
		}
		if err := s.Create("S", "first", []string{"t1"}); err != nil {
			t.Fatal(mode, err)
		}
		if s.Tree.Current != "" || s.Tree.Head() != n1 || s.Tree.Nodes[n1].Label != "S" || s.Tree.Nodes[n1].Note != "first" {
			t.Fatal(mode, "tree after create", s.Tree)
		}
		if len(s.Tree.Nodes) != 1 {
			t.Fatal("create itself adds no layer")
		}
		n2 := boot(t, s, "m3")
		if n2 == n1 || s.Tree.Nodes[n2].Parent != n1 {
			t.Fatal(mode, "next boot gets a new live layer on top")
		}
		if mode == ModeOverlay {
			if _, err := os.Stat(filepath.Join(s.dir(), n1, "upper", "m3")); err == nil {
				t.Fatal("frozen snapshot touched")
			}
		}
		out := s.List(ListOptions{Tree: true, Sizes: true})
		for _, w := range []string{"base (original image)", "S", "tags=t1", `note="first"`, "● current (you are here)", "frozen", "allocated"} {
			if !strings.Contains(out, w) {
				t.Fatalf("%s list lacks %q:\n%s", mode, w, out)
			}
		}
		if s2, err := Open(s.Root, "img", false); err != nil || len(s2.Tree.Nodes) != 2 {
			t.Fatal(err)
		}
		// duplicates / reserved / syntax
		boot(t, s, "m4")
		for _, bad := range []string{"S", "base", "current", "a b", ""} {
			if s.Create(bad, "", nil) == nil {
				t.Fatalf("label %q must be refused", bad)
			}
		}
	}
}

func TestCreateGuards(t *testing.T) {
	s := newModelStore(t, ModeOverlay)
	for _, l := range []string{"a", "b", "c"} {
		boot(t, s, "x")
		if err := s.Create(l, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	boot(t, s, "x") // live depth 4 = max_chain_depth
	err := s.Create("d", "", nil)
	if err == nil || !strings.Contains(err.Error(), "flatten") {
		t.Fatalf("depth guard: %v", err)
	}
	if _, ok := s.Tree.ByLabel("d"); ok {
		t.Fatal("failed create must leave nothing")
	}
	s.MaxDepth = 10
	s.Statfs = func(string) (uint64, uint64, error) { return 1000, 50, nil }
	if err := s.Create("d", "", nil); err == nil || !strings.Contains(err.Error(), "space guard") {
		t.Fatalf("space guard: %v", err)
	}
	s.Ret.MinFreePercent = 0
	if err := s.Create("d", "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestRevert(t *testing.T) {
	for _, mode := range []string{ModeDM, ModeOverlay} {
		s := newModelStore(t, mode)
		n1 := boot(t, s, "m1")
		s.Create("S", "", nil)
		n2 := boot(t, s, "m2")
		if err := s.Revert("S", ""); err != nil {
			t.Fatal(mode, err)
		}
		if s.Tree.Current != "" || s.Tree.Head() != n1 || s.LayerExists(n2) {
			t.Fatal(mode, "revert discards the live layer and rebases on S", s.Tree)
		}
		if _, ok := s.Tree.Nodes[n2]; ok {
			t.Fatal("old live node must be gone")
		}
		n3 := boot(t, s, "m3")
		if n3 == n2 {
			t.Fatal("ids are never reused")
		}
		// revert --keep: old live frozen as sibling branch
		if err := s.Revert("base", "K"); err != nil {
			t.Fatal(mode, err)
		}
		if id, ok := s.Tree.ByLabel("K"); !ok || id != n3 || !s.LayerExists(n3) || s.Tree.Head() != "" {
			t.Fatal(mode, "keep", s.Tree)
		}
		boot(t, s, "m4")
		if err := s.Revert("nope", ""); err == nil {
			t.Fatal("unknown target")
		}
		if err := s.Revert("current", ""); err == nil {
			t.Fatal("current is not a revert target")
		}
		if err := s.Revert("base", "S"); err == nil {
			t.Fatal("keep label must be unique")
		}
		// revert with no live node + discard is a no-op rebase
		s.Revert("base", "")
		if err := s.Revert("S", ""); err != nil || s.Tree.Head() != n1 {
			t.Fatal(err)
		}
	}
}

func TestDeleteLeafAndRefusals(t *testing.T) {
	s := newModelStore(t, ModeDM)
	n1 := boot(t, s, "")
	s.Create("A", "", nil)
	boot(t, s, "")
	s.Revert("base", "B") // B = sibling tip (keeps old live), head = base
	if _, err := s.Delete("base", false, false); err == nil || !strings.Contains(err.Error(), "base") {
		t.Fatal("base cannot be deleted", err)
	}
	if _, err := s.Delete("A", false, false); err == nil || !strings.Contains(err.Error(), "flatten") {
		t.Fatalf("dm delete-with-child must be refused with the flatten hint: %v", err)
	}
	bid, _ := s.Tree.ByLabel("B")
	if what, err := s.Delete("B", false, false); err != nil || !strings.Contains(what, "remove B") || s.LayerExists(bid) {
		t.Fatal(what, err)
	}
	if _, ok := s.Tree.Nodes[n1]; !ok {
		t.Fatal("A survives")
	}
	if _, err := s.Delete("A", false, false); err != nil {
		t.Fatal("A is a leaf tip now:", err)
	}
}

func TestDeleteLeafTipAllowed(t *testing.T) {
	s := newModelStore(t, ModeDM)
	boot(t, s, "")
	s.Create("A", "", nil)
	s.Revert("base", "")
	// A is a leaf; head is base
	if what, err := s.Delete("A", false, false); err != nil || !strings.Contains(what, "remove A") {
		t.Fatal(what, err)
	}
	if len(s.Tree.Nodes) != 0 {
		t.Fatal(s.Tree.Nodes)
	}
}

func TestDeleteHeadNeedsForce(t *testing.T) {
	s := newModelStore(t, ModeOverlay)
	boot(t, s, "a")
	s.Create("A", "", nil) // live not created yet, based on A
	if _, err := s.Delete("A", false, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatal(err)
	}
	if _, err := s.Delete("A", false, true); err != nil || s.Tree.Head() != "" || len(s.Tree.Nodes) != 0 {
		t.Fatal(err, s.Tree)
	}
}

func TestDeleteRecursiveAndBranch(t *testing.T) {
	s := newModelStore(t, ModeDM)
	boot(t, s, "")
	s.Create("A", "", nil)
	boot(t, s, "")
	s.Create("B", "", nil)
	s.Revert("A", "")
	boot(t, s, "")
	s.Revert("base", "C") // C: sibling of B under A, head = base
	if _, err := s.Delete("A", false, false); err == nil || !strings.Contains(err.Error(), "several children") {
		t.Fatal(err)
	}
	if _, err := s.Delete("A", true, false); err != nil || len(s.Tree.Nodes) != 0 || s.Tree.Head() != "" {
		t.Fatal(err, s.Tree)
	}
}

func TestOverlayDeleteMergesIntoChild(t *testing.T) {
	s := newModelStore(t, ModeOverlay)
	n1 := boot(t, s, "from-s")
	up1 := filepath.Join(s.dir(), n1, "upper")
	os.WriteFile(filepath.Join(up1, "shared"), []byte("old"), 0o644)
	os.MkdirAll(filepath.Join(up1, "d"), 0o755)
	os.WriteFile(filepath.Join(up1, "d", "only-s"), []byte("s"), 0o644)
	s.Create("S", "", nil)
	n2 := boot(t, s, "from-live")
	up2 := filepath.Join(s.dir(), n2, "upper")
	os.WriteFile(filepath.Join(up2, "shared"), []byte("new"), 0o644)
	os.MkdirAll(filepath.Join(up2, "d"), 0o755)
	os.WriteFile(filepath.Join(up2, "d", "only-live"), []byte("l"), 0o644)
	if what, err := s.Delete("S", false, false); err != nil || !strings.Contains(what, "merge S into") {
		t.Fatal(what, err)
	}
	rd := func(p string) string { b, _ := os.ReadFile(filepath.Join(up2, p)); return string(b) }
	if rd("from-s") != "from-s" || rd("shared") != "new" || rd("d/only-s") != "s" || rd("d/only-live") != "l" || rd("from-live") != "from-live" {
		t.Fatal("merged content wrong")
	}
	if _, err := os.Stat(filepath.Join(s.dir(), n1)); err == nil {
		t.Fatal("S layer must be gone")
	}
	if s.Tree.Nodes[n2].Parent != "" || len(s.Tree.Nodes) != 1 || s.Tree.Current != n2 {
		t.Fatal(s.Tree)
	}
	if ps := s.Verify(VerifyOptions{}); CountProblems(ps) != 0 {
		t.Fatal(ps)
	}
}

func TestMergeWhiteoutsAndOpaque(t *testing.T) {
	// fakes: a regular file with content "WH" is a whiteout; a ".opq" file inside a directory marks it opaque
	isWh := func(p string, fi os.FileInfo) bool {
		b, _ := os.ReadFile(p)
		return fi.Mode().IsRegular() && bytes.Equal(b, []byte("WH"))
	}
	m := Merger{IsWhiteout: isWh,
		IsOpaque:  func(d string) bool { _, err := os.Lstat(filepath.Join(d, ".opq")); return err == nil },
		SetOpaque: func(d string) error { return os.WriteFile(filepath.Join(d, ".opq"), nil, 0o644) }}
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	w := func(base, rel, c string) {
		p := filepath.Join(base, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	// src (older): deleted x (whiteout), d1 opaque with a file, y file, z dir with a, w deleted dir
	w(src, "x", "WH")
	w(src, "d1/.opq", "")
	w(src, "d1/s", "s")
	w(src, "y", "src-y")
	w(src, "z/a", "src-a")
	w(src, "w", "WH")
	w(src, "gone", "WH")
	// dst (newer): recreated x as dir, d1 plain dir with file, y is whiteout, z dir with b, w recreated dir (plain), gone is regular file
	w(dst, "x/new", "n")
	w(dst, "d1/l", "l")
	w(dst, "y", "WH")
	w(dst, "z/b", "dst-b")
	w(dst, "w/q", "q")
	w(dst, "gone", "file")
	if err := m.Merge(src, dst); err != nil {
		t.Fatal(err)
	}
	rd := func(p string) string {
		b, err := os.ReadFile(filepath.Join(dst, p))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	if !m.IsOpaque(filepath.Join(dst, "x")) || rd("x/new") != "n" {
		t.Fatal("dir recreated over a src whiteout must become opaque")
	}
	if !m.IsOpaque(filepath.Join(dst, "d1")) || rd("d1/s") != "s" || rd("d1/l") != "l" {
		t.Fatal("src opaque dir keeps dst dir opaque and merges entries")
	}
	if rd("y") != "WH" {
		t.Fatal("dst whiteout wins over src file")
	}
	if rd("z/a") != "src-a" || rd("z/b") != "dst-b" || m.IsOpaque(filepath.Join(dst, "z")) {
		t.Fatal("plain dirs merge")
	}
	if !m.IsOpaque(filepath.Join(dst, "w")) {
		t.Fatal("w recreated")
	}
	if rd("gone") != "file" {
		t.Fatal("dst regular file wins over src whiteout")
	}
	// missing src (never booted) is fine
	if err := m.Merge(filepath.Join(root, "nosrc"), dst); err != nil {
		t.Fatal(err)
	}
}

func TestEphemeralGC(t *testing.T) {
	for _, mode := range []string{ModeDM, ModeOverlay} {
		s := newModelStore(t, mode)
		n1 := boot(t, s, "")
		e := s.Tree.AddNode(n1, true)
		s.CreateLayer(e)
		s.Save()
		if ps := s.Verify(VerifyOptions{}); CountProblems(ps) != 0 || len(ps) != 1 || !ps[0].Warn {
			t.Fatalf("ephemeral leftover is a note, not a failure: %v", ps)
		}
		plan, err := s.GCEphemeral(true)
		if err != nil || len(plan) != 1 || !s.LayerExists(e) {
			t.Fatal("dry run", plan, err)
		}
		// auto-gc at the start of create
		if err := s.Create("S", "", nil); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.Tree.Nodes[e]; ok || s.LayerExists(e) {
			t.Fatal(mode, "ephemeral must be gone after create")
		}
		if s.Tree.Nodes[n1].Label != "S" {
			t.Fatal("live node frozen only without ephemeral children")
		}
	}
}

func TestPruneKeepsLiveChain(t *testing.T) {
	s := newModelStore(t, ModeOverlay)
	s.MaxDepth = 10
	boot(t, s, "")
	s.Create("old", "", nil)
	boot(t, s, "")
	s.Revert("base", "") // discard
	boot(t, s, "")
	s.Create("newer", "", nil)
	s.Revert("base", "")
	boot(t, s, "x")
	s.Tree.Nodes[s.Tree.Current] = s.Tree.Nodes[s.Tree.Current]
	// two tip snapshots (old, newer); keep_last=1 -> old is deleted (it is older), nothing else
	for id, n := range s.Tree.Nodes {
		if n.Label == "old" {
			n.Created = "2020-01-01T00:00:00Z"
			s.Tree.Nodes[id] = n
		}
	}
	plan, err := s.Prune(true)
	if err != nil || len(plan) != 1 || plan[0] != "old" {
		t.Fatal(plan, err)
	}
	if _, err := s.Prune(false); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Tree.ByLabel("old"); ok {
		t.Fatal("old pruned")
	}
	if _, ok := s.Tree.ByLabel("newer"); !ok || s.Tree.Current == "" {
		t.Fatal("kept")
	}
}

func TestInUseLock(t *testing.T) {
	s := newModelStore(t, ModeOverlay)
	boot(t, s, "")
	if err := s.MarkInUse("n1"); err != nil {
		t.Fatal(err)
	}
	if busy, _ := s.InUse(); !busy {
		t.Fatal("in use after MarkInUse")
	}
	for name, f := range map[string]func() error{
		"create": func() error { return s.Create("S", "", nil) },
		"revert": func() error { return s.Revert("base", "") },
		"delete": func() error { _, err := s.Delete("S", false, false); return err },
	} {
		if err := f(); err == nil || !strings.Contains(err.Error(), "in use") {
			t.Fatalf("%s must refuse while in use: %v", name, err)
		}
	}
	// a different kernel boot id = stale
	os.WriteFile(BootIDPath, []byte("boot-2\n"), 0o644)
	if busy, _ := s.InUse(); busy {
		t.Fatal("lock of an earlier boot is stale")
	}
	if err := s.Create("S", "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyV2(t *testing.T) {
	s := newModelStore(t, ModeDM)
	boot(t, s, "")
	s.Create("a", "", nil)
	boot(t, s, "")
	if ps := s.Verify(VerifyOptions{}); len(ps) != 0 {
		t.Fatal(ps)
	}
	live := s.Tree.Current
	os.Remove(s.cowPath(live))
	os.WriteFile(filepath.Join(s.dir(), "zombie.cow"), nil, 0o644)
	var all []string
	for _, p := range s.Verify(VerifyOptions{}) {
		all = append(all, p.String())
	}
	j := strings.Join(all, "\n")
	if !strings.Contains(j, "missing COW file") || !strings.Contains(j, "orphan layer") {
		t.Fatal(j)
	}
}
