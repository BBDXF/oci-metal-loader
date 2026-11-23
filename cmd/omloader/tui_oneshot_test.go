package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRememberLastBoot(t *testing.T) {
	d := t.TempDir()
	s := &realSvc{imagesRoot: filepath.Join(d, "images", "images"), snapRoot: filepath.Join(d, "snap", "snapshots")}
	os.MkdirAll(s.imagesRoot, 0o755)
	if i, sn := s.LastBoot(); i != "" || sn != "" {
		t.Fatal("nothing recorded yet")
	}
	s.RememberBoot("alpine", "current", false)
	if i, sn := s.LastBoot(); i != "alpine" || sn != "current" {
		t.Fatalf("%q %q", i, sn)
	}
	b, _ := os.ReadFile(filepath.Join(d, "images", "omloader", "last-boot"))
	if string(b) != "alpine\ncurrent\n" {
		t.Fatalf("file %q", b)
	}
	s.RememberBoot("deb", "s1", true)
	if i, sn := s.LastBoot(); i != "deb" || sn != "s1" {
		t.Fatalf("%q %q", i, sn)
	}
	b, _ = os.ReadFile(filepath.Join(d, "images", "omloader", "last-boot"))
	if string(b) != "deb\ns1\nephemeral\n" {
		t.Fatalf("file %q", b)
	}
	// unwritable / missing partition: silently ignored
	s2 := &realSvc{imagesRoot: "/nonexistent-omloader/images", snapRoot: "/nonexistent-omloader2/snapshots"}
	s2.RememberBoot("x", "current", false)
	if i, _ := s2.LastBoot(); i != "" {
		t.Fatal("must not record")
	}
}
