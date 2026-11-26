package boot

import (
	"bytes"
	"strings"
	"testing"

	"omloader/internal/config"
)

func TestGrowSectors(t *testing.T) {
	g := int64(1 << 30)
	if GrowSectors(4*g, 20*g) != 20*g/512 || GrowSectors(20*g, 20*g) != 0 || GrowSectors(30*g, 20*g) != 0 || GrowSectors(4*g, 0) != 0 {
		t.Fatal("decision")
	}
}

func TestVMRootSizeConfig(t *testing.T) {
	g := int64(1 << 30)
	for _, c := range []struct {
		def, img string
		want     int64
	}{{"", "", 20 * g}, {"8G", "", 8 * g}, {"8G", "40G", 40 * g}, {"8G", "off", 0}, {"off", "", 0}, {"", "keep", 0}, {"0", "", 0}} {
		got, err := config.EffectiveVMRootSize(config.GuestDefaults{VMRootSize: c.def}, config.Image{VMRootSize: c.img})
		if err != nil || got != c.want {
			t.Errorf("%+v: %d %v", c, got, err)
		}
	}
	if _, err := config.ParseVMRootSize("bogus"); err == nil {
		t.Error("bogus accepted")
	}
}

func TestBuildStackGrow(t *testing.T) {
	r := &DryRunner{}
	final, _, dms, err := buildStackGrow(r, "/b", "oml-x-base", 100, 400, []stackLayer{{DMName: "oml-x-n1", Cow: "/c"}})
	if err != nil || final != "/dev/mapper/oml-x-n1" || len(dms) != 2 {
		t.Fatal(err, final, dms)
	}
	c := strings.Join(r.Cmds, "\n")
	tbl := string(r.Stdin["dmsetup create --readonly oml-x-base"])
	if !strings.Contains(tbl, "linear") || !strings.Contains(tbl, "100 300 zero") || !strings.Contains(c, "0 400 snapshot /dev/mapper/oml-x-base") {
		t.Fatal(c)
	}
}

func TestGrowRootSequence(t *testing.T) {
	r := &DryRunner{Respond: func(l string) (string, bool) {
		switch {
		case strings.HasPrefix(l, "sfdisk -d"):
			return "label: gpt\n", true
		case strings.HasPrefix(l, "kpartx -a"):
			return "add map d1 (1)\nadd map d2 (2)\n", true
		case strings.HasPrefix(l, "blkid"):
			return "ext4\n", true
		}
		return "", false
	}}
	var w bytes.Buffer
	parts := Options{Image: "u"}.growRoot(r, "/dev/mapper/d", 2, 2, &w)
	if len(parts) != 2 {
		t.Fatal(parts, w.String())
	}
	c := strings.Join(r.Cmds, "\n")
	for _, s := range []string{"kpartx -d", "sgdisk -e /dev/mapper/d", "sfdisk --no-reread", "e2fsck -fp /dev/mapper/d2", "resize2fs /dev/mapper/d2"} {
		if !strings.Contains(c, s) {
			t.Errorf("missing %q in\n%s", s, c)
		}
	}
	// not last partition: untouched
	r2 := &DryRunner{}
	if (Options{}).growRoot(r2, "/d", 1, 2, &w) != nil || len(r2.Cmds) != 0 || !strings.Contains(w.String(), "not the last") {
		t.Fatal("must warn and skip", w.String())
	}
}

func TestBootGrowsRoot(t *testing.T) {
	f, res := setup(t, "good", chain3(), "")
	res.Image.VMRootSize = "20G"
	r, _, err := run(t, f, res)
	if err != nil {
		t.Fatal(err)
	}
	c := strings.Join(r.Cmds, "\n")
	if !strings.Contains(string(r.Stdin["dmsetup create --readonly oml-u-base"]), "zero") || !strings.Contains(c, "0 41943040 snapshot") {
		t.Fatalf("origin not extended:\n%s", c)
	}
}
