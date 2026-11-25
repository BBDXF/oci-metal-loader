package boot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omloader/internal/config"
)

func testCfg(t *testing.T) *config.Config {
	r, err := config.Load("", "")
	if err != nil {
		t.Fatal(err)
	}
	return &r.Config
}

func TestInitPrefillAndMerge(t *testing.T) {
	c := testCfg(t)
	c.Guest.Timezone = "Asia/Shanghai"
	f := InitPrefill(c, ImageHints{Templates: []string{"/etc/hostname"}}, "My.Image")
	got := map[string]InitField{}
	for _, x := range f {
		got[x.Key] = x
	}
	if got[FTimezone].Value != "Asia/Shanghai" || got[FTimezone].Source != SrcDefault {
		t.Errorf("tz %+v", got[FTimezone])
	}
	if got[FHostname].Value != "my-image" || got[FHostname].Source != SrcImage {
		t.Errorf("host %+v", got[FHostname])
	}
	if got[FUser].Value != "oci" || got[FPassword].Value != "Oci123" {
		t.Errorf("user %+v %+v", got[FUser], got[FPassword])
	}
	// unchanged image-sourced field is kept; an edited one overrides
	ed := append([]InitField{}, f...)
	st := MergeInit(f, ed, "utc")
	if !st.keep(FHostname) || st.keep(FTimezone) {
		t.Errorf("keep %v", st.Keep)
	}
	ed[1].Value = "box"
	if st = MergeInit(f, ed, "utc"); st.keep(FHostname) || st.Hostname != "box" {
		t.Errorf("edited %+v", st)
	}
}

func TestNoCloudSeed(t *testing.T) {
	st := InitState{Timezone: "Asia/Shanghai", Hostname: "h", Locale: "en_US.UTF-8", Keymap: "us", Keep: []string{FLocale}}
	ud, md := NoCloudSeed(st)
	for _, w := range []string{"#cloud-config", "hostname: h", "timezone: Asia/Shanghai", "layout: us"} {
		if !strings.Contains(ud, w) {
			t.Errorf("user-data lacks %q:\n%s", w, ud)
		}
	}
	if strings.Contains(ud, "locale:") || !strings.Contains(md, "instance-id: omloader-") {
		t.Errorf("seed %q %q", ud, md)
	}
}

func TestApplyInit(t *testing.T) {
	root := t.TempDir()
	st := InitState{Timezone: "Asia/Shanghai", Hostname: "h", Locale: "en_US.UTF-8", Keymap: "us", RTC: "localtime"}
	if err := ApplyInit(root, st, GuestCaps{CloudInit: true, Systemd: true}, nil); err != nil {
		t.Fatal(err)
	}
	if l, _ := os.Readlink(filepath.Join(root, "etc/localtime")); l != "/usr/share/zoneinfo/Asia/Shanghai" {
		t.Errorf("localtime -> %q", l)
	}
	for f, w := range map[string]string{"etc/timezone": "Asia/Shanghai", "etc/locale.conf": "LANG=en_US.UTF-8", "etc/vconsole.conf": "KEYMAP=us",
		"etc/adjtime": "LOCAL", "var/lib/cloud/seed/nocloud/user-data": "timezone:"} {
		b, _ := os.ReadFile(filepath.Join(root, f))
		if !strings.Contains(string(b), w) {
			t.Errorf("%s = %q", f, b)
		}
	}
	// idempotent: the marker stops a rewrite
	os.WriteFile(filepath.Join(root, "etc/timezone"), []byte("X\n"), 0o644)
	ApplyInit(root, st, GuestCaps{Systemd: true}, nil)
	if b, _ := os.ReadFile(filepath.Join(root, "etc/timezone")); string(b) != "X\n" {
		t.Error("applied twice")
	}
	// non-systemd, non-cloud-init: /etc/TZ, no locale.conf, no seed
	r2 := t.TempDir()
	ApplyInit(r2, st, GuestCaps{}, nil)
	if _, err := os.Stat(filepath.Join(r2, "etc/TZ")); err != nil {
		t.Error("no /etc/TZ")
	}
	if _, err := os.Stat(filepath.Join(r2, "etc/locale.conf")); err == nil {
		t.Error("locale.conf on non-systemd")
	}
}

func TestInitLifecycleAndEligibility(t *testing.T) {
	c := testCfg(t)
	dir := t.TempDir()
	if InitReady("confirm", dir) || !InitReady("accept-defaults", dir) || !InitReady("skip", dir) {
		t.Error("eligibility before init")
	}
	if _, _, err := ResolveInit(c, ImageHints{}, "img", dir, false, false); err == nil {
		t.Fatal("expected refusal")
	} else if _, ok := err.(ErrNotInitialised); !ok {
		t.Fatalf("err %v", err)
	}
	st, skip, err := ResolveInit(c, ImageHints{}, "img", dir, true, false)
	if err != nil || skip || st.Hostname != "img" {
		t.Fatalf("accept: %+v %v %v", st, skip, err)
	}
	if !InitReady("confirm", dir) {
		t.Error("not ready after accept")
	}
	if got, _, err := ResolveInit(c, ImageHints{}, "img", dir, false, false); err != nil || got.Hash() != st.Hash() {
		t.Error("stored state not reused")
	}
	ClearInit(dir) // reset to base
	if InitReady("confirm", dir) {
		t.Error("ready after reset")
	}
	c.Guest.Init = "skip"
	if _, skip, _ := ResolveInit(c, ImageHints{}, "img", dir, false, false); !skip {
		t.Error("skip")
	}
}

func TestApplyRuntimeOnlyDefaults(t *testing.T) {
	st := InitState{User: "bob", Password: "pw"}
	rt := st.ApplyRuntime(config.Runtime{FromDefaults: true, AutologinUser: "oci"})
	if rt.Users[0].Name != "bob" || rt.RootPassword != "pw" || rt.AutologinUser != "bob" {
		t.Errorf("%+v", rt)
	}
	own := config.Runtime{Users: []config.User{{Name: "x"}}}
	if got := st.ApplyRuntime(own); got.Users[0].Name != "x" {
		t.Error("image's own users overridden")
	}
}
