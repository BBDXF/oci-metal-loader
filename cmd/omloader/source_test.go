package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceListTestRefAndUserSourcesFile(t *testing.T) {
	d := t.TempDir()
	t.Setenv("OML_SOURCES", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/m/meta/streams/v1/index.json" {
			w.Write([]byte(`{"index":{"i":{"datatype":"image-downloads","path":"streams/v1/images.json"}}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	cfg := filepath.Join(d, "omloader.yaml")
	os.WriteFile(cfg, []byte("network:\n  retries: 0\n  timeout_sec: 2\ncache:\n  path: "+d+"/cache\n"), 0o644)
	// user sources file next to -c: a new source with a mirror under another path, and the built-in lxd-images switched off
	os.WriteFile(filepath.Join(d, "sources.yaml"), []byte(`default_source: mint
sources:
  lxd-images: {enabled: false}
  mint:
    title: Corp Mint catalog
    type: simplestreams
    url: http://127.0.0.1:1
    mirror: corp
    servers:
      corp: {url: "`+srv.URL+`", path: m, region: cn, rewrite: [{from: "streams/v1/", to: "meta/streams/v1/"}]}
`), 0o644)
	var o, e bytes.Buffer
	if rc := run([]string{"source", "list", "-c", cfg}, &o, &e); rc != 0 {
		t.Fatal(rc, e.String())
	}
	out := o.String()
	for _, w := range []string{"lxc-images", "ubuntu-releases", "dockerhub", "mint  type=simplestreams", "[default]", "* mirror corp", "mirror tuna", "mirror nju", "Corp Mint catalog"} {
		if !strings.Contains(out, w) {
			t.Errorf("source list lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "lxd-images") {
		t.Errorf("disabled source listed:\n%s", out)
	}
	o.Reset()
	e.Reset()
	if rc := run([]string{"source", "test", "mint", "-c", cfg}, &o, &e); rc != 1 || !strings.Contains(o.String(), "OK") || !strings.Contains(o.String(), "FAIL") ||
		!strings.Contains(o.String(), "corp") || !strings.Contains(o.String(), "official") {
		t.Fatalf("rc=%d (the dead official server must FAIL, corp OK)\n%s%s", rc, o.String(), e.String())
	}
	o.Reset()
	if rc := run([]string{"source", "test", "mint", "--mirror", "nope", "-c", cfg}, &o, &e); rc == 0 || !strings.Contains(o.String(), "no mirror") {
		t.Fatalf("unknown mirror must fail: %d %s", rc, o.String())
	}
	o.Reset()
	if rc := run([]string{"source", "ref", "alpine:3", "--mirror", "1ms", "-c", cfg}, &o, &e); rc != 0 || strings.TrimSpace(o.String()) != "docker.1ms.run/library/alpine:3" {
		t.Fatalf("%d %q %s", rc, o.String(), e.String())
	}
	o.Reset()
	if rc := run([]string{"source", "ref", "docker.io/library/debian:12", "-c", cfg}, &o, &e); rc != 0 || strings.TrimSpace(o.String()) != "registry-1.docker.io/library/debian:12" {
		t.Fatalf("%d %q", rc, o.String())
	}
}
