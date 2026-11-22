package snapshot

import (
	"path/filepath"
	"testing"
)

func TestOps(t *testing.T) {
	tr := New("u", "abc")
	for _, n := range []string{"a", "b", "c"} {
		if err := tr.Create(n, ""); err != nil {
			t.Fatal(err)
		}
	}
	if tr.Current != "c" {
		t.Fatal("current")
	}
	if d, _ := tr.Depth("c"); d != 3 {
		t.Fatalf("depth %d", d)
	}
	if tr.Create("a", "") == nil {
		t.Fatal("dup allowed")
	}
	if tr.Delete("a") == nil || tr.Delete("b") == nil {
		t.Fatal("deleted non-leaf")
	}
	if err := tr.Rollback("a"); err != nil || tr.Current != "a" {
		t.Fatal("rollback")
	}
	if err := tr.Create("x", "a"); err != nil { // branch
		t.Fatal(err)
	}
	if err := tr.Delete("x"); err != nil || tr.Current != "a" {
		t.Fatalf("delete current: %v %q", err, tr.Current)
	}
	if tr.Rollback("nope") == nil {
		t.Fatal("rollback missing")
	}
	if err := tr.Rollback("base"); err != nil || tr.Current != "" {
		t.Fatal("base")
	}
}

func TestCycle(t *testing.T) {
	tr := New("u", "")
	tr.Nodes["a"] = Node{Parent: "b"}
	tr.Nodes["b"] = Node{Parent: "a"}
	if tr.FindCycle() == "" || tr.Validate() == nil {
		t.Fatal("cycle not detected")
	}
	if _, err := tr.Chain("a"); err == nil {
		t.Fatal("chain on cycle")
	}
}

func TestFlatten(t *testing.T) {
	tr := New("u", "")
	tr.Create("a", "")
	tr.Create("b", "")
	p, err := tr.PlanFlatten("b")
	if err != nil || len(p.Merge) != 2 || len(p.Remove) != 1 || p.Remove[0] != "a" {
		t.Fatalf("%+v %v", p, err)
	}
	tr.Create("c", "a") // branch off a
	if _, err := tr.PlanFlatten("b"); err == nil {
		t.Fatal("should refuse branching flatten")
	}
}

func TestSaveLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tree.json")
	tr := New("u", "abc")
	tr.Create("a", "")
	if err := tr.Save(p); err != nil {
		t.Fatal(err)
	}
	l, err := Load(p)
	if err != nil || l.Current != "a" || l.Nodes["a"].Parent != "" {
		t.Fatalf("%+v %v", l, err)
	}
}
