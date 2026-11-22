package cpio

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRoundTripWithSystemCpio(t *testing.T) {
	w := New()
	w.File("/omloader/bin/x", 0o755, []byte("hello"))
	w.Symlink("/omloader/l", "bin/x")
	b := w.Bytes()
	if !bytes.HasPrefix(b, []byte("070701")) || len(b)%4 != 0 {
		t.Fatal("bad header/alignment")
	}
	cp, err := exec.LookPath("cpio")
	if err != nil {
		t.Skip("no cpio")
	}
	d := t.TempDir()
	cmd := exec.Command(cp, "-id", "--quiet")
	cmd.Dir = d
	cmd.Stdin = bytes.NewReader(b)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(d, "omloader/bin/x"))
	if err != nil || string(got) != "hello" {
		t.Fatal(got, err)
	}
	if l, _ := os.Readlink(filepath.Join(d, "omloader/l")); l != "bin/x" {
		t.Fatal(l)
	}
	z, err := New().Gzip()
	if err != nil || len(z) == 0 {
		t.Fatal(err)
	}
}
