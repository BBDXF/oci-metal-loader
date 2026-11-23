package tui

import "testing"

func TestMirrorCycleAndSourceRef(t *testing.T) {
	m := &Model{}
	s := SourceInfo{Name: "lxc-images", Mirror: "official", MirrorIDs: []string{"official", "auto", "nju", "tuna"}}
	if m.SourceRef(s) != "lxc-images" || m.mirrorOf(s) != "official" {
		t.Fatal(m.SourceRef(s))
	}
	m.catAll = []CatalogItem{{OS: "x"}}
	for _, want := range []string{"auto", "nju", "tuna", "official"} {
		m.cycleMirror(s)
		if m.mirrorOf(s) != want || m.catAll != nil {
			t.Fatalf("got %s want %s", m.mirrorOf(s), want)
		}
	}
	m.cycleMirror(s)
	m.cycleMirror(s)
	if m.SourceRef(s) != "lxc-images@nju" {
		t.Fatal(m.SourceRef(s))
	}
	one := SourceInfo{Name: "lxd-images", MirrorIDs: nil}
	m.cycleMirror(one) // nothing to cycle
	if m.SourceRef(one) != "lxd-images" {
		t.Fatal()
	}
}
