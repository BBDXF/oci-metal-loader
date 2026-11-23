package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wr(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuiltinSources(t *testing.T) {
	r, err := LoadWith("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &r.Config
	if c.DefaultSource != "lxc-images" {
		t.Fatal(c.DefaultSource)
	}
	for _, n := range []string{"lxc-images", "ubuntu-releases", "ubuntu-daily", "lxd-images", "dockerhub"} {
		if !c.Sources[n].IsEnabled() {
			t.Fatalf("missing built-in source %s", n)
		}
	}
	if got := strings.Join(c.ServerNames("lxc-images"), ","); got != "cernet,nju,tuna" {
		t.Fatalf("lxc servers %s (USTC has no lxc-images and must not be listed)", got)
	}
	if got := strings.Join(c.ServerNames("ubuntu-daily"), ","); got != "cernet,nju,tuna,ustc" {
		t.Fatal(got)
	}
	b, err := c.SourceBases("lxc-images", "tuna")
	if err != nil || b[0].Name != "tuna" || b[1].Name != "official" || b[0].URL != "https://mirrors.tuna.tsinghua.edu.cn/lxc-images" ||
		b[0].Rel("streams/v1/index.json") != "meta/simplestreams/v1/index.json" || b[0].Rel("images/x") != "images/x" {
		t.Fatalf("%+v %v", b, err)
	}
	if b, _ := c.SourceBases("lxc-images", ""); len(b) != 1 || b[0].Name != "official" {
		t.Fatalf("default is official only: %+v", b)
	}
	if b, _ := c.SourceBases("lxc-images", "auto"); len(b) != 4 {
		t.Fatalf("%+v", b)
	}
}

func TestSourcesOverrideAndRemove(t *testing.T) {
	d := t.TempDir()
	lo := wr(t, d, "lo.yaml", `sources:
  lxc-images:
    mirror: nju
    servers:
      tuna: {enabled: false}
      nju: {url: "https://nju.example", path: /lxc/}
      corp: {title: Corp, url: "https://corp.example/lxc"}
  mint-corp:
    type: simplestreams
    url: https://mint.example/
default_source: mint-corp
`)
	hi := wr(t, d, "hi.yaml", `sources:
  ubuntu-daily: {enabled: false}
  lxc-images:
    servers:
      corp: {url: "https://corp2.example/lxc"}
`)
	r, err := LoadWith("", "", []string{lo, hi})
	if err != nil {
		t.Fatal(err)
	}
	c := &r.Config
	if got := strings.Join(c.ServerNames("lxc-images"), ","); got != "cernet,corp,nju" {
		t.Fatalf("tuna removed, corp added: %s", got)
	}
	b, _ := c.SourceBases("lxc-images", "")
	if b[0].Name != "nju" || b[0].URL != "https://nju.example/lxc" || b[0].Rewrite == nil {
		t.Fatalf("field-level merge keeps rewrite, takes url/path: %+v", b[0])
	}
	if c.Sources["lxc-images"].Servers["corp"].URL != "https://corp2.example/lxc" || c.Sources["lxc-images"].Servers["corp"].Title != "Corp" {
		t.Fatal("later file wins per field")
	}
	if c.DefaultSource != "mint-corp" || !c.Sources["mint-corp"].IsEnabled() {
		t.Fatal("new source")
	}
	if strings.Contains(strings.Join(c.SourceNames(), ","), "ubuntu-daily") {
		t.Fatal("disabled source still listed")
	}
	if r.Sources["sources.lxc-images.mirror"] != LayerSources {
		t.Fatal(r.Sources["sources.lxc-images.mirror"])
	}
	// omloader.yaml (global) still wins over every sources file
	g := wr(t, d, "g.yaml", "sources:\n  lxc-images:\n    url: http://10.0.2.2:1\n    mirrors: [http://10.0.2.2:2]\n")
	r, err = LoadWith(g, "", []string{lo})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := r.Config.SourceBases("lxc-images", "official"); b[0].URL != "http://10.0.2.2:1" || b[1].URL != "http://10.0.2.2:2" {
		t.Fatalf("%+v", b)
	}
}

func TestSourcesFilesLookup(t *testing.T) {
	d := t.TempDir()
	g := wr(t, d, "omloader.yaml", "snapshot:\n  max_chain_depth: 7\n")
	s := wr(t, d, "sources.yaml", "default_source: lxd-images\n")
	t.Setenv("OML_SOURCES", "")
	found := SourcesFiles(g)
	if len(found) != 1 || found[0] != s {
		t.Fatalf("%v", found)
	}
	r, err := Load(g, "")
	if err != nil || r.Config.DefaultSource != "lxd-images" {
		t.Fatalf("%v", err)
	}
	e := wr(t, d, "env/s.yaml", "default_source: ubuntu-daily\n")
	t.Setenv("OML_SOURCES", e)
	if f := SourcesFiles(g); len(f) != 2 || f[0] != e || f[1] != s {
		t.Fatalf("env before the -c sibling: %v", f)
	}
}

func TestSourcesValidation(t *testing.T) {
	d := t.TempDir()
	bad := wr(t, d, "bad.yaml", `default_source: nope
sources:
  lxc-images:
    mirror: ghost
    servers:
      a: {title: no url}
      b: {url: "ftp://x"}
      c: {url: "https://c.example", rewrite: [{to: x}]}
  Bad_Name: {type: http, url: "http://x"}
  hub: {type: oci-registry, url: registry-1.docker.io, servers: {p: {url: "http://p.example"}}}
`)
	_, err := LoadWith("", "", []string{bad})
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, w := range []string{"default_source", "sources.lxc-images.mirror", "sources.lxc-images.servers.a.url", "servers.b.url", "servers.c.rewrite",
		"Bad_Name", "sources.hub.servers.p.url"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("missing %q in\n%v", w, err)
		}
	}
	if !strings.Contains(err.Error(), "bad.yaml:") {
		t.Errorf("error lines must name the file:\n%v", err)
	}
	for name, body := range map[string]string{"k.yaml": "boot:\n  timeout: 1\n", "u.yaml": "sources:\n  x:\n    colour: red\n", "t.yaml": "- a\n"} {
		if _, err := LoadWith("", "", []string{wr(t, d, name, body)}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	_, err = LoadWith("", "", []string{wr(t, d, "k2.yaml", "boot:\n  timeout: 1\n")})
	if !strings.Contains(err.Error(), "not allowed in a sources file") {
		t.Fatal(err)
	}
}

func TestRewriteImageRef(t *testing.T) {
	for _, tc := range [][3]string{
		{"alpine", "docker.1ms.run", "docker.1ms.run/library/alpine"},
		{"alpine:3.20", "https://docker.1ms.run/", "docker.1ms.run/library/alpine:3.20"},
		{"library/nginx:1", "docker.m.daocloud.io", "docker.m.daocloud.io/library/nginx:1"},
		{"docker.io/library/debian:12", "docker.1ms.run", "docker.1ms.run/library/debian:12"},
		{"registry-1.docker.io/user/app@sha256:abcd", "h.example", "h.example/user/app@sha256:abcd"},
		{"user/app", "h.example", "h.example/user/app"},
		{"ghcr.io/org/img:1", "docker.1ms.run", "ghcr.io/org/img:1"},
		{"localhost:5000/x", "h.example", "localhost:5000/x"},
		{"alpine", "", "docker.io/library/alpine"},
	} {
		if got := RewriteImageRef(tc[0], tc[1]); got != tc[2] {
			t.Errorf("%s via %s = %s, want %s", tc[0], tc[1], got, tc[2])
		}
	}
}
