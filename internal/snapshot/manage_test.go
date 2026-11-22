package snapshot

import (
	"reflect"
	"testing"
	"time"
)

func mk(t *testing.T) *Tree {
	tr := New("i", "")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	add := func(n, p string, age time.Duration) {
		tr.Nodes[n] = Node{Parent: p, File: n + ".cow", Created: now.Add(-age).Format(time.RFC3339)}
	}
	add("s1", "", 10*time.Hour)
	add("s2", "s1", 9*time.Hour)
	add("s3", "s2", 8*time.Hour)
	add("b", "s1", 1*time.Hour)
	add("tmp-1", "", 30*time.Hour)
	add("tmp-2", "", 1*time.Hour)
	tr.Current = "b"
	return tr
}

func TestTagsNotes(t *testing.T) {
	tr := mk(t)
	if err := tr.Tag("s2", "keep", "a"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tr.Nodes["s2"].Tags, []string{"a", "keep"}) {
		t.Fatal(tr.Nodes["s2"].Tags)
	}
	if tr.Tag("s2", "bad tag") == nil || tr.Untag("s2", "zz") == nil || tr.Tag("nope", "x") == nil {
		t.Fatal("expected errors")
	}
	tr.Untag("s2", "a")
	tr.SetNote("s2", "hello")
	if tr.Nodes["s2"].Note != "hello" || len(tr.Nodes["s2"].Tags) != 1 {
		t.Fatal("note/untag")
	}
	if err := tr.CreateWith("n", "s3", "nn", []string{"t"}); err != nil || tr.Nodes["n"].Parent != "s3" || tr.Nodes["n"].Note != "nn" {
		t.Fatal(err)
	}
	if tr.CreateWith("m", "s3", "", []string{"bad tag"}) == nil {
		t.Fatal("bad tag must fail")
	}
	if _, ok := tr.Nodes["m"]; ok {
		t.Fatal("m must be rolled back")
	}
}

func TestPlanDelete(t *testing.T) {
	tr := mk(t)
	if _, err := tr.PlanDelete("s1", false, false); err == nil {
		t.Fatal("children must refuse")
	}
	if _, err := tr.PlanDelete("s1", true, false); err == nil {
		t.Fatal("subtree contains current: must refuse without force")
	}
	p, err := tr.PlanDelete("s1", true, true)
	if err != nil || p[len(p)-1] != "s1" || len(p) != 4 {
		t.Fatalf("%v %v", p, err)
	}
	p, err = tr.PlanDelete("s3", false, false)
	if err != nil || len(p) != 1 {
		t.Fatal(p, err)
	}
	if _, err := tr.PlanDelete("b", false, false); err == nil {
		t.Fatal("current needs force")
	}
	tr.Remove("b")
	if tr.Current != "s1" {
		t.Fatalf("current=%q", tr.Current)
	}
}

func TestPlanGC(t *testing.T) {
	tr := mk(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := tr.PlanGC(now, 24*time.Hour); !reflect.DeepEqual(got, []string{"tmp-1"}) {
		t.Fatal(got)
	}
	if got := tr.PlanGC(now, 0); !reflect.DeepEqual(got, []string{"tmp-1", "tmp-2"}) {
		t.Fatal(got)
	}
	tr.Current = "tmp-2"
	if got := tr.PlanGC(now, 0); !reflect.DeepEqual(got, []string{"tmp-1"}) {
		t.Fatalf("current protected: %v", got)
	}
}

func TestPlanPrune(t *testing.T) {
	tr := mk(t)
	// leaves by time: b(1h) tmp-2(1h) s3(8h) tmp-1(30h). keep_last=1 -> b (tie broken by name: b < tmp-2)
	got := tr.PlanPrune(1, true)
	want := []string{"s3", "s2", "tmp-1", "tmp-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v want %v", got, want)
	}
	tr.Tag("s3", "keep")
	got = tr.PlanPrune(1, true) // s3 tagged: keeps s3,s2 (ancestors), s1
	if !reflect.DeepEqual(got, []string{"tmp-1", "tmp-2"}) {
		t.Fatal(got)
	}
	if got = tr.PlanPrune(1, false); !reflect.DeepEqual(got, want) {
		t.Fatal("keep_tagged=false", got)
	}
	tr.Current = "s3"
	if got = tr.PlanPrune(1, false); contains(got, "s3") || contains(got, "s2") || contains(got, "s1") {
		t.Fatal("current and ancestors must survive", got)
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

func TestPlanMerge(t *testing.T) {
	tr := mk(t)
	if _, err := tr.PlanMerge("s2"); err == nil {
		t.Fatal("non-leaf")
	}
	if _, err := tr.PlanMerge("tmp-1"); err == nil {
		t.Fatal("parent is base")
	}
	if _, err := tr.PlanMerge("b"); err == nil {
		t.Fatal("parent has other children")
	}
	p, err := tr.PlanMerge("s3")
	if err != nil || p.Into != "s2" {
		t.Fatal(p, err)
	}
}
