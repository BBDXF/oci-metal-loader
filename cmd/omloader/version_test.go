package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionBase(t *testing.T) {
	old := baseFile
	defer func() { baseFile = old }()
	baseFile = filepath.Join(t.TempDir(), "none")
	var o, e bytes.Buffer
	if c := run([]string{"version"}, &o, &e); c != 0 || strings.TrimSpace(o.String()) != "omloader "+version {
		t.Fatalf("plain without file: %d %q", c, o.String())
	}
	o.Reset()
	if c := run([]string{"version", "--long"}, &o, &e); c != 0 || !strings.Contains(o.String(), "base: unknown") {
		t.Fatalf("long without file: %q", o.String())
	}
	baseFile = filepath.Join(t.TempDir(), "r")
	os.WriteFile(baseFile, []byte("DEBIAN_VERSION=12.8\nDEBIAN_CODENAME=bookworm\nKERNEL_VERSION=6.1.0-28-amd64\nBUILD_DATE=2026-11-23\nGIT_COMMIT=abc\n"), 0o644)
	o.Reset()
	run([]string{"version"}, &o, &e)
	if !strings.Contains(o.String(), "base: Debian 12.8 bookworm, kernel 6.1.0-28-amd64, built 2026-11-23") {
		t.Fatal(o.String())
	}
	o.Reset()
	run([]string{"version", "--long"}, &o, &e)
	if !strings.Contains(o.String(), "  GIT_COMMIT=abc") {
		t.Fatal(o.String())
	}
	o.Reset()
	run([]string{"version", "--json"}, &o, &e)
	if !strings.Contains(o.String(), `"KERNEL_VERSION": "6.1.0-28-amd64"`) || !strings.Contains(o.String(), `"omloader"`) {
		t.Fatal(o.String())
	}
}
