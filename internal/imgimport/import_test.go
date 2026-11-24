package imgimport

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omloader/internal/boot"
	"omloader/internal/config"
)

type te struct {
	name, link, body string
	typ              byte
	mode             int64
}

func mkTar(t *testing.T, ents []te) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, e := range ents {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Linkname: e.link, Uid: 1000, Gid: 1000}
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	return b.Bytes()
}

func gz(b []byte) []byte {
	var o bytes.Buffer
	w := gzip.NewWriter(&o)
	w.Write(b)
	w.Close()
	return o.Bytes()
}

func bytesOpener(t *testing.T, b []byte) LayerOpener {
	p := filepath.Join(t.TempDir(), "l")
	os.WriteFile(p, b, 0o644)
	return FileOpener(p)
}

func readTar(t *testing.T, b []byte) (map[string]*tar.Header, map[string]string, []string) {
	hs, bodies, var_ := map[string]*tar.Header{}, map[string]string{}, []string{}
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		n := strings.TrimSuffix(h.Name, "/")
		hs[n] = h
		d, _ := io.ReadAll(tr)
		bodies[n] = string(d)
		var_ = append(var_, n)
	}
	return hs, bodies, var_
}

func TestMergeWhiteouts(t *testing.T) {
	l1 := mkTar(t, []te{
		{name: "./", typ: tar.TypeDir, mode: 0o755},
		{name: "etc/", typ: tar.TypeDir, mode: 0o755},
		{name: "etc/a.conf", body: "A1"},
		{name: "etc/b.conf", body: "B1"},
		{name: "etc/hard", link: "etc/a.conf", typ: tar.TypeLink},
		{name: "var/cache/x", body: "x"},
		{name: "var/cache/y", body: "y"},
		{name: "opt/app/bin", body: "bin"},
		{name: "usr/lib/old", body: "old"},
		{name: "sym", link: "/etc/a.conf", typ: tar.TypeSymlink},
		{name: "dev/null", typ: tar.TypeChar},
		{name: "tmp/gone/f", body: "f"},
	})
	l2 := mkTar(t, []te{
		{name: "etc/a.conf", body: "A2"},
		{name: "etc/.wh.b.conf", body: ""},
		{name: "var/cache/.wh..wh..opq"},
		{name: "var/cache/z", body: "z"},
		{name: "opt/app", body: "now a file"},
		{name: "usr/lib/new", body: "new"},
		{name: ".wh.tmp"},
	})
	var out bytes.Buffer
	if err := MergeLayers([]LayerOpener{bytesOpener(t, gz(l1)), bytesOpener(t, l2)}, &out, nil); err != nil {
		t.Fatal(err)
	}
	hs, body, order := readTar(t, out.Bytes())
	want := func(n string, yes bool) {
		t.Helper()
		if _, ok := hs[n]; ok != yes {
			t.Errorf("%s present=%v want %v (%v)", n, ok, yes, order)
		}
	}
	want("etc/a.conf", true)
	want("etc/b.conf", false)  // whiteout
	want("var/cache/x", false) // opaque
	want("var/cache/y", false)
	want("var/cache/z", true)
	want("var/cache", true)    // synthesized parent
	want("opt/app/bin", false) // dir replaced by file
	want("tmp", false)
	want("tmp/gone/f", false)
	want("usr/lib/old", true)
	want("usr/lib/new", true)
	want("etc/.wh.b.conf", false)
	want("var/cache/.wh..wh..opq", false)
	if body["etc/a.conf"] != "A2" {
		t.Errorf("a.conf=%q", body["etc/a.conf"])
	}
	if hs["etc/hard"].Typeflag != tar.TypeLink || hs["etc/hard"].Linkname != "etc/a.conf" {
		t.Errorf("hardlink lost: %+v", hs["etc/hard"])
	}
	if hs["sym"].Typeflag != tar.TypeSymlink || hs["sym"].Linkname != "/etc/a.conf" {
		t.Errorf("symlink: %+v", hs["sym"])
	}
	if hs["dev/null"].Typeflag != tar.TypeChar {
		t.Errorf("char dev lost")
	}
	if hs["etc/a.conf"].Uid != 1000 {
		t.Errorf("uid not preserved")
	}
	// parents precede children
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if pos["var/cache"] > pos["var/cache/z"] || pos["usr"] > pos["usr/lib/new"] {
		t.Errorf("order: %v", order)
	}
}

func TestMergeDropsDanglingHardlink(t *testing.T) {
	l1 := mkTar(t, []te{{name: "a", body: "1"}, {name: "b", link: "a", typ: tar.TypeLink}})
	l2 := mkTar(t, []te{{name: ".wh.a"}})
	var out bytes.Buffer
	if err := MergeLayers([]LayerOpener{bytesOpener(t, l1), bytesOpener(t, l2)}, &out, nil); err != nil {
		t.Fatal(err)
	}
	hs, _, _ := readTar(t, out.Bytes())
	if len(hs) != 0 {
		t.Errorf("expected empty, got %v", hs)
	}
}

type fakeRunner struct {
	boot.DryRunner
	merged []byte
}

func (f *fakeRunner) RunStdin(in io.Reader, name string, args ...string) (string, error) {
	f.merged, _ = io.ReadAll(in)
	// emulate mksquashfs: "-" OUT
	os.WriteFile(args[1], []byte("fake-squashfs"), 0o644)
	f.Cmds = append(f.Cmds, name+" "+strings.Join(args, " "))
	return "", nil
}

func writeBlob(t *testing.T, dir string, b []byte) string {
	h := sha256.Sum256(b)
	d := hex.EncodeToString(h[:])
	os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755)
	os.WriteFile(filepath.Join(dir, "blobs", "sha256", d), b, 0o644)
	return "sha256:" + d
}

func mkOCI(t *testing.T) string {
	dir := t.TempDir()
	l1 := writeBlob(t, dir, gz(mkTar(t, []te{{name: "sbin/init", body: "i", mode: 0o755}, {name: "etc/x", body: "1"}})))
	l2 := writeBlob(t, dir, gz(mkTar(t, []te{{name: "etc/.wh.x"}, {name: "etc/y", body: "2"}})))
	cfg := `{"architecture":"amd64","os":"linux","config":{"Entrypoint":["/sbin/init"],"Cmd":["--x"],"Env":["A=1","PATH=/bin"],"WorkingDir":"/srv","Volumes":{"/var/lib/data":{}}}}`
	c := writeBlob(t, dir, []byte(cfg))
	m, _ := json.Marshal(map[string]any{"schemaVersion": 2, "config": map[string]any{"digest": c},
		"layers": []map[string]any{{"digest": l1}, {"digest": l2}}})
	md := writeBlob(t, dir, m)
	idx, _ := json.Marshal(map[string]any{"schemaVersion": 2, "manifests": []map[string]any{{"digest": md}}})
	os.WriteFile(filepath.Join(dir, "index.json"), idx, 0o644)
	return dir
}

func TestImportOCIDir(t *testing.T) {
	t.Setenv("OML_EXPERIMENTAL", "oci")
	src := mkOCI(t)
	out := t.TempDir()
	fr := &fakeRunner{}
	d, err := Import(Options{Kind: "oci-dir", Path: src, Name: "app", Out: out, Runner: fr, ExperimentalOCI: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.Cmds[0], "-tar") || !strings.HasPrefix(fr.Cmds[0], "mksquashfs - ") {
		t.Errorf("cmd %v", fr.Cmds)
	}
	hs, _, _ := readTar(t, fr.merged)
	if _, ok := hs["etc/x"]; ok || hs["etc/y"] == nil || hs["sbin/init"] == nil {
		t.Errorf("merged: %v", hs)
	}
	res, err := config.Load("", filepath.Join(d, "image.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	img := res.Image
	sum := sha256.Sum256([]byte("fake-squashfs"))
	if img.SHA256 != hex.EncodeToString(sum[:]) || img.Type != "oci" || img.Base != "base.squashfs" {
		t.Errorf("image %+v", img)
	}
	rt := img.Runtime
	if rt.Entrypoint[0] != "/sbin/init" || rt.Cmd[0] != "--x" || rt.WorkingDir != "/srv" || rt.Env["A"] != "1" || rt.Network != "dhcp" {
		t.Errorf("runtime %+v", rt)
	}
	if len(img.Data) != 1 || img.Data[0].Mount != "/var/lib/data" || img.Data[0].Backend != "dir" {
		t.Errorf("data %+v", img.Data)
	}
	if _, err := Import(Options{Kind: "oci-dir", Path: src, Name: "app", Out: out, Runner: fr, ExperimentalOCI: true}); err == nil {
		t.Error("expected exists error")
	}
}

func TestImportArchives(t *testing.T) {
	t.Setenv("OML_EXPERIMENTAL", "oci")
	src := mkOCI(t)
	// oci-archive: tar of the layout dir
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		rel, _ := filepath.Rel(src, p)
		if rel == "." || fi.IsDir() {
			return nil
		}
		d, _ := os.ReadFile(p)
		tw.WriteHeader(&tar.Header{Name: rel, Mode: 0o644, Size: int64(len(d)), Typeflag: tar.TypeReg})
		tw.Write(d)
		return nil
	})
	tw.Close()
	ar := filepath.Join(t.TempDir(), "a.tar")
	os.WriteFile(ar, b.Bytes(), 0o644)
	out := t.TempDir()
	if _, err := Import(Options{Kind: "oci-archive", Path: ar, Name: "o", Out: out, Runner: &fakeRunner{}, TmpDir: t.TempDir(), ExperimentalOCI: true}); err != nil {
		t.Fatal(err)
	}
	// docker-archive
	l := mkTar(t, []te{{name: "bin/sh", body: "sh", mode: 0o755}})
	cfg := `{"architecture":"amd64","config":{"Cmd":["/bin/sh"]}}`
	man := `[{"Config":"cfg.json","Layers":["l0/layer.tar"]}]`
	b.Reset()
	tw = tar.NewWriter(&b)
	for n, d := range map[string][]byte{"manifest.json": []byte(man), "cfg.json": []byte(cfg), "l0/layer.tar": l} {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(d)), Typeflag: tar.TypeReg})
		tw.Write(d)
	}
	tw.Close()
	dk := filepath.Join(t.TempDir(), "d.tar")
	os.WriteFile(dk, b.Bytes(), 0o644)
	fr := &fakeRunner{}
	d, err := Import(Options{Kind: "docker-archive", Path: dk, Name: "dk", Out: out, Runner: fr, ExperimentalOCI: true})
	if err != nil {
		t.Fatal(err)
	}
	hs, _, _ := readTar(t, fr.merged)
	if hs["bin/sh"] == nil {
		t.Errorf("merged %v", hs)
	}
	res, err := config.Load("", filepath.Join(d, "image.yaml"))
	if err != nil || res.Image.Runtime.Cmd[0] != "/bin/sh" {
		t.Errorf("%v %+v", err, res)
	}
}

func TestImportLXD(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "rootfs.squashfs"), []byte("sq"), 0o644)
	os.WriteFile(filepath.Join(d, "lxd.tar"), mkTar(t, []te{{name: "metadata.yaml", body: "architecture: x86_64\nproperties:\n  os: debian\n"}}), 0o644)
	out := t.TempDir()
	dir, err := Import(Options{Kind: "lxd", Path: filepath.Join(d, "lxd.tar"), Name: "lx", Out: out, Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := config.Load("", filepath.Join(dir, "image.yaml"))
	if err != nil || res.Image.Type != "lxd-container" {
		t.Fatalf("%v %+v", err, res)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "base.squashfs")); string(b) != "sq" {
		t.Error("squashfs not copied")
	}
	os.WriteFile(filepath.Join(d, "lxd.tar"), mkTar(t, []te{{name: "metadata.yaml", body: "architecture: aarch64\n"}}), 0o644)
	if _, err := Import(Options{Kind: "lxd", Path: d, Name: "lx2", Out: out, Runner: &fakeRunner{}}); err == nil {
		// dir mode looks for lxd.tar.xz; absent => meta is the dir itself => error expected
		t.Error("expected error")
	}
}

func TestParseDataAndMerge(t *testing.T) {
	d, err := ParseData([]string{"vol:/var/lib/v:ext4-file:64M:false", "x:/srv/x:dir"})
	if err != nil || len(d) != 2 || d[0]["size"] != "64M" || d[0]["snapshot"] != false || d[1]["backend"] != "dir" {
		t.Fatalf("%v %v", d, err)
	}
	for _, bad := range []string{"a:b", "a:/x:tmpfs", "a:/../x:dir", ":/x:dir"} {
		if _, err := ParseData([]string{bad}); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
	cur := []map[string]any{{"mount": "/srv/x", "backend": "dir"}, {"mount": "/keep"}}
	out := mergeData(cur, d)
	if len(out) != 3 || out[0]["backend"] != "dir" || out[0]["name"] != "x" {
		t.Fatalf("%v", out)
	}
}

func TestImportOCIGatedWhenOff(t *testing.T) {
	t.Setenv("OML_EXPERIMENTAL", "off")
	for _, k := range []string{"oci-dir", "oci-archive", "docker-archive"} {
		_, err := Import(Options{Kind: k, Path: "x", Name: "n", Out: t.TempDir(), Runner: &fakeRunner{}})
		if err == nil || !strings.Contains(err.Error(), "have been disabled") {
			t.Errorf("%s: %v", k, err)
		}
	}
	// config key false (OCIDisabled) gates too; the flag overrides it
	t.Setenv("OML_EXPERIMENTAL", "")
	if _, err := Import(Options{Kind: "oci-dir", Path: "x", Name: "n", Out: t.TempDir(), Runner: &fakeRunner{}, OCIDisabled: true}); err == nil || !strings.Contains(err.Error(), "have been disabled") {
		t.Errorf("config false must gate: %v", err)
	}
	// default (no env, no config veto): passes the gate
	if _, err := Import(Options{Kind: "oci-dir", Path: "x", Name: "n", Out: t.TempDir(), Runner: &fakeRunner{}}); err != nil && strings.Contains(err.Error(), "have been disabled") {
		t.Errorf("on by default: %v", err)
	}
	t.Setenv("OML_EXPERIMENTAL", "oci")
	if _, err := Import(Options{Kind: "oci-dir", Path: "/nonexistent", Name: "n", Out: t.TempDir(), Runner: &fakeRunner{}}); err == nil || strings.Contains(err.Error(), "experimental") {
		t.Errorf("env opt-in must pass the gate: %v", err)
	}
}
