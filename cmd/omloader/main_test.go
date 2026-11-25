package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omloader/internal/snapmgr"
)

// simBoot simulates what `omloader boot` does to the tree: continue (lazily create) the live node and its layer.
func simBoot(t *testing.T, snap, image, imgs, marker string) {
	t.Helper()
	st, err := snapmgr.Open(snap, image, true)
	if err != nil {
		t.Fatal(err)
	}
	st.Mode = snapmgr.ModeOverlay
	id, created := st.Tree.EnsureLive()
	if created {
		if err := st.CreateLayer(id); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(snap, image, id, "upper", marker), []byte(marker), 0o644)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotCLI(t *testing.T) {
	root := t.TempDir()
	do := func(want int, a ...string) string {
		var o, e bytes.Buffer
		if c := run(a, &o, &e); c != want {
			t.Fatalf("%v: code %d want %d: %s", a, c, want, e.String())
		}
		return o.String()
	}
	do(1, "snapshot", "create", "u", "a", "--root", root) // nothing to snapshot on a fresh image
	if out := do(0, "snapshot", "list", "u", "--root", root); !strings.Contains(out, "base (original image)") || !strings.Contains(out, "current (you are here)") {
		t.Fatal(out)
	}
	do(1, "snapshot", "list", "..", "--root", root)
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
	simBoot(t, snap, "c", imgs, "m1")
	do(0, "snapshot", "create", "c", "s1", "--note", "first", "--tag", "gold")
	do(1, "snapshot", "create", "c", "s1") // duplicate / nothing new
	simBoot(t, snap, "c", imgs, "m2")
	do(1, "snapshot", "create", "c", "s1")
	do(0, "snapshot", "create", "c", "s2")
	simBoot(t, snap, "c", imgs, "m3")
	out := do(0, "snapshot", "list", "c", "--tree", "--sizes")
	for _, w := range []string{"base (original image)", "s1", "s2", "gold", "●", "current (you are here)", "allocated", "└──"} {
		if !strings.Contains(out, w) {
			t.Fatalf("list lacks %q:\n%s", w, out)
		}
	}
	do(0, "snapshot", "tag", "c", "s2", "x", "y")
	do(0, "snapshot", "untag", "c", "s2", "x")
	do(0, "snapshot", "note", "c", "s2", "hello", "world")
	do(1, "snapshot", "tag", "c", "base", "x")
	// delete the middle snapshot s1: merged into s2 (overlay)
	if out := do(0, "snapshot", "delete", "c", "s1"); !strings.Contains(out, "merge s1 into s2") {
		t.Fatal(out)
	}
	if b, err := os.ReadFile(snap + "/c/n2/upper/m1"); err != nil || string(b) != "m1" {
		t.Fatalf("merged content missing: %v", err)
	}
	do(1, "snapshot", "delete", "c", "base")
	do(1, "snapshot", "revert", "c", "nope")
	do(0, "snapshot", "revert", "c", "s2", "--keep", "kept")
	if out := do(0, "snapshot", "list", "c", "--tree"); !strings.Contains(out, "kept") {
		t.Fatal(out)
	}
	do(0, "snapshot", "rollback", "c", "base") // alias of revert
	do(0, "snapshot", "prune", "c", "--dry-run")
	do(0, "snapshot", "df", "c")
	do(0, "snapshot", "verify", "c")
	os.MkdirAll(snap+"/c/orphan", 0o755)
	if out := do(1, "snapshot", "verify", "c"); !strings.Contains(out, "orphan") {
		t.Fatal(out)
	}
	do(1, "snapshot", "merge", "c", "s2")
	do(0, "snapshot", "gc", "c", "--ephemeral")
	do(1, "snapshot", "bogus", "c")
}

func TestOCIGatedCLI(t *testing.T) {
	t.Setenv("OML_EXPERIMENTAL", "off")
	d := t.TempDir()
	var o, e bytes.Buffer
	if c := run([]string{"import", "oci-archive", "x", "--name", "n", "--out", d}, &o, &e); c != 1 || !strings.Contains(e.String(), "have been disabled") {
		t.Fatalf("import oci-archive must be refused: %d %s", c, e.String())
	}
	os.MkdirAll(d+"/images/c", 0o755)
	os.WriteFile(d+"/images/c/image.yaml", []byte("name: c\ntype: oci\nformat: squashfs\nbase: b\nsnapshots:\n  mode: overlayfs\n"), 0o644)
	e.Reset()
	if c := run([]string{"boot", "--image", "c", "--images-root", d + "/images", "--snap-root", d + "/s", "--dry-run"}, &o, &e); c != 1 || !strings.Contains(e.String(), "have been disabled") {
		t.Fatalf("boot of type oci must be refused: %d %s", c, e.String())
	}
	e.Reset()
	// --experimental-oci lifts the gate (then it fails later on the missing base, not on the gate)
	run([]string{"boot", "--image", "c", "--images-root", d + "/images", "--snap-root", d + "/s", "--dry-run", "--experimental-oci"}, &o, &e)
	if strings.Contains(e.String(), "have been disabled") {
		t.Fatalf("flag must opt in: %s", e.String())
	}
	t.Setenv("OML_EXPERIMENTAL", "")
	e.Reset()
	if c := run([]string{"import", "lxd", "nonexistent", "--name", "n", "--out", d}, &o, &e); strings.Contains(e.String(), "experimental") {
		t.Fatalf("lxd import is not gated (code %d): %s", c, e.String())
	}
}

func TestBootRefusesUninitialisedImage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "images", "img")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "image.yaml"), []byte("name: img\ntype: lxd-container\nformat: squashfs\nbase: base.squashfs\nsnapshots: {mode: overlayfs}\n"), 0o644)
	var out, errb bytes.Buffer
	args := []string{"--image", "img", "--images-root", filepath.Join(root, "images"), "--snap-root", filepath.Join(root, "snaps"), "--dry-run"}
	if rc := runBoot(args, &out, &errb); rc == 0 || !strings.Contains(errb.String(), "not initialised") {
		t.Fatalf("rc=%d err=%q", rc, errb.String())
	}
	errb.Reset()
	runBoot(append(args, "--accept-defaults"), &out, &errb)
	if strings.Contains(errb.String(), "not initialised") {
		t.Errorf("--accept-defaults still refused: %q", errb.String())
	}
}
