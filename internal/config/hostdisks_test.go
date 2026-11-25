package config

import "testing"

func TestHostDisksDefaults(t *testing.T) {
	var h HostDisks
	if !h.Enabled() || h.EffMode() != "rw" || !h.ForceRW() || h.Root() != "/mnt/disks" || h.AllDisks() || !h.AutomountOn() {
		t.Fatalf("defaults: %+v", h)
	}
	f := false
	if (HostDisks{Mode: "off"}).Enabled() || (HostDisks{Mode: "ro"}).EffMode() != "ro" || (HostDisks{ForceRWData: &f}).ForceRW() || !(HostDisks{Disks: "all"}).AllDisks() {
		t.Fatal("overrides")
	}
}
