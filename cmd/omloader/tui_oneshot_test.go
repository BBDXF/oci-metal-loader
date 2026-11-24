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

func TestAutoBootFile(t *testing.T) {
	old := autobootFile
	defer func() { autobootFile = old }()
	autobootFile = filepath.Join(t.TempDir(), "autoboot")
	s := &realSvc{}
	if s.AutoBoot() != nil {
		t.Fatal("no file -> nil")
	}
	os.WriteFile(autobootFile, []byte("IMAGE=\"ubuntu-noble\"\nSNAPSHOT=\"before-upgrade\"\nTIMEOUT=\"3\"\nFLAGS=\"poweroff_on_fail=1 experimental=oci\"\n"), 0o644)
	a := s.AutoBoot()
	if a == nil || a.Image != "ubuntu-noble" || a.Snapshot != "before-upgrade" || a.Timeout != 3 || len(a.Flags) != 2 || a.Flags[1] != "experimental=oci" {
		t.Fatalf("%+v", a)
	}
	claimAutoBoot()
	if s.AutoBoot() == nil {
		t.Fatal("claimed by another console must still count down there")
	}
	s.AutoBootCancel()
	if !s.AutoBootCancelled() || s.AutoBoot() != nil {
		t.Fatal("cancelled -> nil (once per boot)")
	}
}

func TestApplyBootFlags(t *testing.T) {
	t.Setenv("OML_EXPERIMENTAL", "")
	if !applyBootFlags([]string{"poweroff_on_fail=1", "experimental=oci", "font=x"}) || os.Getenv("OML_EXPERIMENTAL") != "oci" {
		t.Fatal("flags not applied")
	}
	if applyBootFlags([]string{"poweroff_on_fail=0"}) || applyBootFlags(nil) {
		t.Fatal("poweroff_on_fail must need =1")
	}
}
