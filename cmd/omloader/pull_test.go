package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func hx(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestPullContainerOnlineThenOffline(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("no tar")
	}
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("no xz")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "metadata.yaml"), []byte("architecture: x86_64\nproperties:\n  os: Test\n"), 0o644)
	if out, err := exec.Command("tar", "-C", dir, "-cJf", filepath.Join(dir, "lxd.tar.xz"), "metadata.yaml").CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	meta, _ := os.ReadFile(filepath.Join(dir, "lxd.tar.xz"))
	sq := []byte("hsqs-fake-squashfs-content")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/streams/v1/index.json":
			fmt.Fprint(w, `{"index":{"images":{"datatype":"image-downloads","path":"streams/v1/images.json"}}}`)
		case "/streams/v1/images.json":
			fmt.Fprintf(w, `{"products":{"testos:rel:amd64:default":{"arch":"amd64","os":"testos","release":"rel","variant":"default","versions":{"20240101":{"items":{
"lxd.tar.xz":{"ftype":"lxd.tar.xz","sha256":"%s","size":%d,"path":"i/lxd.tar.xz"},
"root.squashfs":{"ftype":"squashfs","sha256":"%s","size":%d,"path":"i/root.squashfs"}}}}}}}`, hx(meta), len(meta), hx(sq), len(sq))
		case "/i/lxd.tar.xz":
			w.Write(meta)
		case "/i/root.squashfs":
			w.Write(sq)
		default:
			http.NotFound(w, r)
		}
	}))
	cfg := filepath.Join(dir, "c.yaml")
	conf := func(url string, extra string) {
		os.WriteFile(cfg, []byte("network:\n  retries: 0\n  timeout_sec: 3\nsources:\n  lxc-images:\n    type: simplestreams\n    url: "+url+"\n    mirrors: []\ncache:\n  path: "+filepath.Join(dir, "cache")+"\n"+extra), 0o644)
	}
	conf(srv.URL, "")
	root := filepath.Join(dir, "images")
	var o, e bytes.Buffer
	if rc := run([]string{"images", "list", "-c", cfg, "--type", "container"}, &o, &e); rc != 0 || !strings.Contains(o.String(), "lxc:testos/rel/default") {
		t.Fatal(rc, o.String(), e.String())
	}
	o.Reset()
	e.Reset()
	if rc := run([]string{"pull", "lxc:testos/rel", "-c", cfg, "--out", root}, &o, &e); rc != 0 {
		t.Fatal(rc, o.String(), e.String())
	}
	yml, _ := os.ReadFile(filepath.Join(root, "testos-rel", "image.yaml"))
	if !strings.Contains(string(yml), "type: lxd-container") || !strings.Contains(string(yml), hx(sq)) {
		t.Fatalf("%s", yml)
	}
	// server gone: --offline works from the cache (index, catalog and blobs)
	srv.Close()
	hits = 0
	o.Reset()
	e.Reset()
	if rc := run([]string{"pull", "lxc:testos/rel", "-c", cfg, "--out", root, "--name", "again", "--offline"}, &o, &e); rc != 0 || hits != 0 {
		t.Fatal(rc, o.String(), e.String())
	}
	if _, err := os.Stat(filepath.Join(root, "again", "base.squashfs")); err != nil {
		t.Fatal(err)
	}
	// unknown image offline-> error; policy blocks
	if rc := run([]string{"pull", "lxc:nope/x", "-c", cfg, "--out", root, "--offline"}, &o, &e); rc == 0 {
		t.Fatal("expected failure")
	}
	os.WriteFile(cfg, append([]byte("policy:\n  allowed_sources: [other]\n"), []byte("sources:\n  other:\n    type: simplestreams\n    url: http://x\n")...), 0o644)
	e.Reset()
	if rc := run([]string{"pull", "lxc:testos/rel", "-c", cfg, "--out", root}, &o, &e); rc == 0 || !strings.Contains(e.String(), "allowed_sources") {
		t.Fatal("allowed_sources not enforced:", e.String())
	}
	e.Reset()
	if rc := run([]string{"cache", "ls", "-c", cfg}, &o, &e); rc != 0 {
		t.Fatal(e.String())
	}
}

func TestSpec(t *testing.T) {
	s, o, r, v, err := parseSpec("lxc:ubuntu/noble/cloud")
	if err != nil || s != "" || o != "ubuntu" || r != "noble" || v != "cloud" {
		t.Fatal(s, o, r, v, err)
	}
	if s, o, r, _, err := parseSpec("ubuntu-releases:ubuntu/noble"); err != nil || s != "ubuntu-releases" || o != "ubuntu" || r != "noble" {
		t.Fatal(s, o, r, err)
	}
	if _, _, _, _, err := parseSpec("lxc:ubuntu"); err == nil {
		t.Fatal("bad spec")
	}
}
