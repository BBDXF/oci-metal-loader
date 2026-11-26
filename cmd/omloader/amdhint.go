package main

import (
	"os"
	"path/filepath"
	"strings"
)

const amdHint = "AMD GPU detected: if the VM screen stays black, reboot the loader with the safe display entry."

// amdHintNeeded reports whether an AMD GPU is present (PCI vendor 0x1002, display class 0x03xxxx) while the loader
// kernel command line lacks nomodeset. sysfs is the /sys root, cmdlinePath is /proc/cmdline.
func amdHintNeeded(sysfs, cmdlinePath string) bool {
	if b, err := os.ReadFile(cmdlinePath); err == nil {
		for _, f := range strings.Fields(string(b)) {
			if f == "nomodeset" {
				return false
			}
		}
	}
	rd := func(p string) string { b, _ := os.ReadFile(p); return strings.ToLower(strings.TrimSpace(string(b))) }
	cards, _ := filepath.Glob(filepath.Join(sysfs, "class/drm/card*/device/vendor"))
	for _, p := range cards {
		if rd(p) == "0x1002" {
			return true
		}
	}
	devs, _ := filepath.Glob(filepath.Join(sysfs, "bus/pci/devices/*/vendor"))
	for _, p := range devs {
		if rd(p) == "0x1002" && strings.HasPrefix(rd(filepath.Join(filepath.Dir(p), "class")), "0x03") {
			return true
		}
	}
	return false
}
