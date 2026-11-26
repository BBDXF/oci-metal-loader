package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAmdHintNeeded(t *testing.T) {
	sys, cl := t.TempDir(), filepath.Join(t.TempDir(), "cmdline")
	w := func(p, s string) {
		p = filepath.Join(sys, p)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	os.WriteFile(cl, []byte("quiet\n"), 0o644)
	if amdHintNeeded(sys, cl) {
		t.Fatal("no gpu")
	}
	w("bus/pci/devices/0000:00:01.0/vendor", "0x1002\n")
	w("bus/pci/devices/0000:00:01.0/class", "0x020000\n")
	if amdHintNeeded(sys, cl) {
		t.Fatal("non-display AMD device")
	}
	w("bus/pci/devices/0000:00:01.0/class", "0x030000\n")
	if !amdHintNeeded(sys, cl) {
		t.Fatal("pci display")
	}
	os.WriteFile(cl, []byte("quiet nomodeset\n"), 0o644)
	if amdHintNeeded(sys, cl) {
		t.Fatal("nomodeset")
	}
	os.WriteFile(cl, []byte("quiet\n"), 0o644)
	os.RemoveAll(filepath.Join(sys, "bus"))
	w("class/drm/card0/device/vendor", "0x1002\n")
	if !amdHintNeeded(sys, cl) {
		t.Fatal("drm")
	}
}
