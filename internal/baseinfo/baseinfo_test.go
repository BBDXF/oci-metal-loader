package baseinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r")
	os.WriteFile(p, []byte("# c\nOMLOADER_VERSION=0.2.0\nDEBIAN_VERSION=12.8\nDEBIAN_CODENAME=bookworm\nKERNEL_VERSION=\"6.1.0-28-amd64\"\nBUILD_DATE=2026-11-23\nZ=1\n"), 0o644)
	in, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := in.Line(); got != "Debian 12.8 bookworm, kernel 6.1.0-28-amd64, built 2026-11-23" {
		t.Fatal(got)
	}
	l := in.Long()
	if l[0] != "OMLOADER_VERSION=0.2.0" || l[len(l)-1] != "Z=1" {
		t.Fatal(l)
	}
	if b, _ := in.JSON(); !strings.Contains(string(b), `"DEBIAN_VERSION": "12.8"`) {
		t.Fatal(string(b))
	}
	if _, err := Read(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Fatal("want error")
	}
	if (Info(nil)).Line() != "" {
		t.Fatal("empty")
	}
}
