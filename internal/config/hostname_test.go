package config

import "testing"

func TestSanitizeHostname(t *testing.T) {
	long := ""
	for i := 0; i < 80; i++ {
		long += "a"
	}
	for in, want := range map[string]string{
		"vm-ubuntu-noble": "vm-ubuntu-noble", "ct-alpine-3-21": "ct-alpine-3-21", "Ubuntu_Noble": "ubuntu-noble", "ubuntu.noble.24.04": "ubuntu-noble-24-04",
		"UPPER": "upper", "--lead-trail--": "lead-trail", "a..b__c": "a-b-c", "": "omloader", "___": "omloader", "中文": "omloader",
		long: long[:63], long[:62] + ".x": long[:62], long[:62] + "-x": long[:62],
	} {
		if got := SanitizeHostname(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveHostname(t *testing.T) {
	g := GuestDefaults{Hostname: "image"}
	cases := []struct {
		g    GuestDefaults
		rt   Runtime
		img  string
		want string
		keep bool
	}{
		{g, Runtime{}, "vm-ubuntu-noble", "vm-ubuntu-noble", false},
		{GuestDefaults{}, Runtime{}, "My.Image", "my-image", false},
		{GuestDefaults{Hostname: "box"}, Runtime{}, "x", "box", false},
		{GuestDefaults{Hostname: "keep"}, Runtime{}, "x", "", true},
		{g, Runtime{Hostname: "fromimage"}, "x", "fromimage", false},
		{GuestDefaults{Hostname: "box"}, Runtime{Hostname: "keep"}, "x", "", true},
		{GuestDefaults{Hostname: "keep"}, Runtime{Hostname: "forced"}, "x", "forced", false},
	}
	for i, c := range cases {
		if n, k := ResolveHostname(c.g, c.rt, c.img); n != c.want || k != c.keep {
			t.Errorf("case %d: got %q,%v want %q,%v", i, n, k, c.want, c.keep)
		}
	}
	for _, bad := range []string{"Bad_Name", "-x", "a.b"} {
		if hostLabelRe.MatchString(bad) {
			t.Errorf("%q must be invalid", bad)
		}
	}
}
