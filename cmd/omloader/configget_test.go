package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigGet(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, "boot.yaml")
	os.WriteFile(f, []byte("boot:\n  default_image: omloci\n  timeout: 7\n  extra_cmdline_flags: [poweroff_on_fail=1, experimental=oci]\n"), 0o644)
	get := func(args ...string) (int, string, string) {
		var o, e bytes.Buffer
		rc := run(append([]string{"config", "get"}, args...), &o, &e)
		return rc, o.String(), e.String()
	}
	for _, c := range []struct {
		args []string
		rc   int
		out  string
	}{
		{[]string{"-c", f, "boot.default_image"}, 0, "omloci\n"},
		{[]string{"boot.default_image", "-c", f}, 0, "omloci\n"},
		{[]string{"-c", f, "boot.timeout"}, 0, "7\n"},
		{[]string{"-c", f, "boot.extra_cmdline_flags"}, 0, "poweroff_on_fail=1\nexperimental=oci\n"},
		{[]string{"boot.default_image"}, 0, "\n"},
		{[]string{"boot.default_snapshot"}, 0, "current\n"},
		{[]string{"snapshot.persistent"}, 0, "true\n"},
		{[]string{"-c", f, "boot.nope"}, 1, ""},
		{[]string{"-c", f, "boot"}, 1, ""},
		{[]string{}, 1, ""},
	} {
		rc, out, _ := get(c.args...)
		if rc != c.rc || out != c.out {
			t.Errorf("%v: rc=%d out=%q, want rc=%d out=%q", c.args, rc, out, c.rc, c.out)
		}
	}
}
