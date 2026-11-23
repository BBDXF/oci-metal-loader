package snapshot

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// build: base -> A(n1) -> B(n2) -> live(n3); base -> C(n4) (snapshot tip)
func mk(t *testing.T) *Tree {
	tr := New("i", "")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	add := func(id, parent, label string, age time.Duration) {
		tr.Nodes[id] = Node{Parent: parent, Label: label, File: id + ".cow", Created: now.Add(-age).Format(time.RFC3339)}
	}
	add("n1", "", "A", 10*time.Hour)
	add("n2", "n1", "B", 9*time.Hour)
	add("n3", "n2", "", 8*time.Hour)
	add("n4", "", "C", 1*time.Hour)
	tr.Current, tr.NextID = "n3", 5
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestSaveLoadVersion(t *testing.T) {
	tr := mk(t)
	p := filepath.Join(t.TempDir(), "tree.json")
	if err := tr.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || !reflect.DeepEqual(got.Nodes, tr.Nodes) || got.Version != 2 || got.NextID != 5 {
		t.Fatalf("%v %+v", err, got)
	}
	// v1 file (no version field) is rejected with a hint
	os.WriteFile(p, []byte(`{"image":"i","current":"s1","nodes":{"s1":{"parent":"","file":"s1.cow","created":"x"}}}`), 0o644)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "delete") {
		t.Fatalf("v1 must be rejected clearly: %v", err)
	}
}

func TestValidate(t *testing.T) {
	tr := mk(t)
	tr.Nodes["n9"] = Node{Parent: "n1", Label: "A"}
	if tr.Validate() == nil {
		t.Fatal("duplicate label")
	}
	delete(tr.Nodes, "n9")
	n := tr.Nodes["n3"]
	n.Label = "x"
	tr.Nodes["n3"] = n
	if tr.Validate() == nil {
		t.Fatal("live node must be unlabelled")
	}
	n.Label = ""
	tr.Nodes["n3"] = n
	tr.Nodes["n5"] = Node{Parent: "zz"}
	if tr.Validate() == nil {
		t.Fatal("missing parent")
	}
	delete(tr.Nodes, "n5")
	tr.Nodes["bad"] = Node{}
	if tr.Validate() == nil {
		t.Fatal("bad id")
	}
}

func TestResolveDisplayHead(t *testing.T) {
	tr := mk(t)
	if id, _ := tr.Resolve("A"); id != "n1" {
		t.Fatal(id)
	}
	if id, err := tr.Resolve("base"); id != "" || err != nil {
		t.Fatal(id, err)
	}
	if id, _ := tr.Resolve("current"); id != "n3" {
		t.Fatal(id)
	}
	if _, err := tr.Resolve("nope"); err == nil {
		t.Fatal("unknown label")
	}
	if _, err := tr.ResolveSnapshot("current", true); err == nil {
		t.Fatal("current is not a snapshot")
	}
	if tr.Head() != "n2" || tr.Display("n2") != "B" || tr.Display("") != "base" || tr.Display("n3") != "current" {
		t.Fatal(tr.Head(), tr.Display("n2"))
	}
}

func TestFreezeAndLazyLive(t *testing.T) {
	tr := mk(t)
	id, err := tr.Freeze("D", "note", []string{"keep"})
	if err != nil || id != "n3" || tr.Nodes["n3"].Label != "D" || tr.Current != "" || tr.LiveParent != "n3" || tr.Head() != "n3" {
		t.Fatalf("%v %+v", err, tr)
	}
	if _, err := tr.Freeze("E", "", nil); err == nil {
		t.Fatal("nothing to snapshot without live node")
	}
	live, created := tr.EnsureLive()
	if !created || live != "n5" || tr.Nodes["n5"].Parent != "n3" || tr.Current != "n5" || tr.LiveParent != "" {
		t.Fatalf("%v %v %+v", live, created, tr)
	}
	if l2, c2 := tr.EnsureLive(); l2 != "n5" || c2 {
		t.Fatal("EnsureLive must be idempotent")
	}
	for _, bad := range []string{"", "base", "current", "a b", "A"} {
		if _, err := tr.Freeze(bad, "", nil); err == nil {
			t.Fatalf("label %q must be refused", bad)
		}
	}
	if _, err := tr.Freeze("F", "", []string{"bad tag"}); err == nil {
		t.Fatal("bad tag")
	}
	if tr.Nodes["n5"].Label != "" {
		t.Fatal("failed freeze must not label")
	}
}

func TestIDsNeverReused(t *testing.T) {
	tr := New("i", "")
	a := tr.AddNode("", false)
	tr.Remove(a)
	b := tr.AddNode("", false)
	if a == b {
		t.Fatal("ids must not be reused")
	}
}

func TestRemoveLiveMovesHead(t *testing.T) {
	tr := mk(t)
	tr.Remove("n3")
	if tr.Current != "" || tr.LiveParent != "n2" {
		t.Fatalf("%+v", tr)
	}
}

func TestChainDepthEphemerals(t *testing.T) {
	tr := mk(t)
	c, _ := tr.Chain("n3")
	if !reflect.DeepEqual(c, []string{"n1", "n2", "n3"}) {
		t.Fatal(c)
	}
	if d, _ := tr.Depth("n3"); d != 3 {
		t.Fatal(d)
	}
	e1 := tr.AddNode("n3", true)
	e2 := tr.AddNode(e1, true)
	if got := tr.Ephemerals(); !reflect.DeepEqual(got, []string{e2, e1}) {
		t.Fatal(got)
	}
	if len(tr.PersistentChildren("n3")) != 0 || len(tr.Children("n3")) != 1 {
		t.Fatal("ephemeral children are not persistent")
	}
	if tr.Names()[len(tr.Names())-1] != e2 {
		t.Fatal(tr.Names())
	}
}

func TestTagsNotes(t *testing.T) {
	tr := mk(t)
	if err := tr.Tag("B", "keep", "a"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tr.Nodes["n2"].Tags, []string{"a", "keep"}) {
		t.Fatal(tr.Nodes["n2"].Tags)
	}
	if tr.Tag("B", "bad tag") == nil || tr.Untag("B", "zz") == nil || tr.Tag("nope", "x") == nil || tr.Tag("base", "x") == nil {
		t.Fatal("expected errors")
	}
	tr.Untag("B", "a")
	tr.SetNote("B", "hello")
	if tr.Nodes["n2"].Note != "hello" || len(tr.Nodes["n2"].Tags) != 1 {
		t.Fatal("note/untag")
	}
}

func TestPruneRules(t *testing.T) {
	tr := mk(t)
	// tips: B (live below it, newer) and C. keep_last=1 keeps newest tip C; live node n3, head B and ancestor A are kept.
	if got := tr.PlanPrune(1, true); len(got) != 0 {
		t.Fatalf("nothing deletable: %v", got)
	}
	// an old leaf branch: base -> D(n5, old) with a tag-less sibling
	tr.Nodes["n5"] = Node{Label: "D", File: "n5.cow", Created: "2025-01-01T00:00:00Z"}
	tr.Nodes["n6"] = Node{Parent: "n5", Label: "E", File: "n6.cow", Created: "2025-01-02T00:00:00Z"}
	got := tr.PlanPrune(1, true)
	if !reflect.DeepEqual(got, []string{"n6", "n5"}) {
		t.Fatalf("old branch deepest first: %v", got)
	}
	tr.Tag("E", "golden")
	if got := tr.PlanPrune(1, true); len(got) != 0 {
		t.Fatalf("tagged node and its ancestor kept: %v", got)
	}
	if got := tr.PlanPrune(1, false); len(got) != 2 {
		t.Fatalf("keep_tagged=false: %v", got)
	}
	// keep_last=0: still never the live node, head or ancestors
	got = tr.PlanPrune(0, false)
	for _, g := range got {
		if g == "n3" || g == "n2" || g == "n1" {
			t.Fatalf("live chain deleted: %v", got)
		}
	}
	// ephemeral nodes are gc's business
	tr.AddNode("n3", true)
	for _, g := range tr.PlanPrune(0, false) {
		if tr.Nodes[g].Ephemeral {
			t.Fatal("prune must not plan ephemerals")
		}
	}
}
