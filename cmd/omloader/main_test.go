package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSnapshotCLI(t *testing.T) {
	root := t.TempDir()
	do := func(want int, a ...string) string {
		var o, e bytes.Buffer
		if c := run(a, &o, &e); c != want {
			t.Fatalf("%v: code %d want %d: %s", a, c, want, e.String())
		}
		return o.String()
	}
	do(0, "snapshot", "create", "u", "a", "--root", root)
	do(0, "snapshot", "create", "u", "b", "--root", root)
	do(1, "snapshot", "delete", "u", "a", "--root", root)
	do(0, "snapshot", "rollback", "u", "a", "--root", root)
	if out := do(0, "snapshot", "list", "u", "--root", root); out == "" {
		t.Fatal("empty list")
	}
	do(1, "snapshot", "list", "nope", "--root", root)
	do(0, "config", "validate")
}

func TestSnapshotOpsCLI(t *testing.T) {
	t.Setenv("OML_EXPERIMENTAL", "oci")
	d := t.TempDir()
	snap, imgs := d+"/snaps", d+"/images"
	os.MkdirAll(imgs+"/c", 0o755)
	os.WriteFile(imgs+"/c/base.squashfs", []byte("sq"), 0o644)
	os.WriteFile(imgs+"/c/image.yaml", []byte("name: c\ntype: oci\nformat: squashfs\nbase: base.squashfs\nsnapshots:\n  mode: overlayfs\n"), 0o644)
	cfg := d + "/g.yaml"
	os.WriteFile(cfg, []byte("snapshot:\n  retention:\n    min_free_percent: 0\n    keep_last: 1\n"), 0o644)
	c := []string{"--root", snap, "--images-root", imgs, "-c", cfg}
	do := func(want int, a ...string) string {
		var o, e bytes.Buffer
		if code := run(append(a, c...), &o, &e); code != want {
			t.Fatalf("%v: code %d want %d\nout=%s\nerr=%s", a, code, want, o.String(), e.String())
		}
		return o.String() + e.String()
	}
	do(0, "snapshot", "create", "c", "s1", "--note", "first", "--tag", "gold")
	do(0, "snapshot", "create", "c", "s2", "--from", "s1")
	do(0, "snapshot", "create", "c", "s3", "--from", "s1")
	if _, err := os.Stat(snap + "/c/s2/upper"); err != nil {
		t.Fatal("layer not created", err)
	}
	if out := do(0, "snapshot", "list", "c", "--tree", "--sizes"); !strings.Contains(out, "frozen") || !strings.Contains(out, "gold") || !strings.Contains(out, "├──") {
		t.Fatal(out)
	}
	do(1, "snapshot", "delete", "c", "s1")
	do(1, "snapshot", "rollback", "c", "s1", "--discard")
	do(0, "snapshot", "tag", "c", "s2", "x", "y")
	do(0, "snapshot", "untag", "c", "s2", "x")
	do(0, "snapshot", "note", "c", "s2", "hello", "world")
	if out := do(0, "snapshot", "prune", "c", "--dry-run"); !strings.Contains(out, "would delete 0") {
		t.Fatal(out) // s3 is current; s2 tagged y
	}
	do(0, "snapshot", "rollback", "c", "s2")
	if out := do(0, "snapshot", "prune", "c", "--dry-run"); !strings.Contains(out, "would delete 1 snapshot(s): s3") {
		t.Fatal(out)
	}
	do(0, "snapshot", "df", "c")
	do(0, "snapshot", "verify", "c")
	os.MkdirAll(snap+"/c/orphan", 0o755)
	if out := do(1, "snapshot", "verify", "c"); !strings.Contains(out, "orphan") {
		t.Fatal(out)
	}
	do(1, "snapshot", "merge", "c", "s1")
	if out := do(1, "snapshot", "merge", "c", "s3"); !strings.Contains(out, "cannot merge") {
		t.Fatal(out)
	}
	do(0, "snapshot", "gc", "c", "--tmp", "--older-than", "0s")
	do(0, "snapshot", "delete", "c", "s1", "--recursive", "--force")
	do(1, "snapshot", "bogus", "c")
}

func TestOCIGatedCLI(t *testing.T) {
	t.Setenv("OML_EXPERIMENTAL", "")
	d := t.TempDir()
	var o, e bytes.Buffer
	if c := run([]string{"import", "oci-archive", "x", "--name", "n", "--out", d}, &o, &e); c != 1 || !strings.Contains(e.String(), "experimental and disabled by default") {
		t.Fatalf("import oci-archive must be refused: %d %s", c, e.String())
	}
	os.MkdirAll(d+"/images/c", 0o755)
	os.WriteFile(d+"/images/c/image.yaml", []byte("name: c\ntype: oci\nformat: squashfs\nbase: b\nsnapshots:\n  mode: overlayfs\n"), 0o644)
	e.Reset()
	if c := run([]string{"boot", "--image", "c", "--images-root", d + "/images", "--snap-root", d + "/s", "--dry-run"}, &o, &e); c != 1 || !strings.Contains(e.String(), "experimental and disabled by default") {
		t.Fatalf("boot of type oci must be refused: %d %s", c, e.String())
	}
	e.Reset()
	// --experimental-oci lifts the gate (then it fails later on the missing base, not on the gate)
	run([]string{"boot", "--image", "c", "--images-root", d + "/images", "--snap-root", d + "/s", "--dry-run", "--experimental-oci"}, &o, &e)
	if strings.Contains(e.String(), "experimental and disabled") {
		t.Fatalf("flag must opt in: %s", e.String())
	}
	t.Setenv("OML_EXPERIMENTAL", "")
	e.Reset()
	if c := run([]string{"import", "lxd", "nonexistent", "--name", "n", "--out", d}, &o, &e); strings.Contains(e.String(), "experimental") {
		t.Fatalf("lxd import is not gated (code %d): %s", c, e.String())
	}
}
