package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerImageName(t *testing.T) {
	for in, want := range map[string]string{"nginx:alpine": "nginx-alpine", "docker.io/library/busybox": "busybox", "jrei/systemd-debian:12": "jrei-systemd-debian-12", "Alpine:3.20": "alpine-3.20"} {
		if got := dockerImageName(in); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
}

func TestPullDockerWithFakeCrane(t *testing.T) {
	d := t.TempDir()
	t.Setenv("OML_SOURCES", "")
	t.Setenv("OML_EXPERIMENTAL", "")
	cfg := filepath.Join(d, "omloader.yaml")
	os.WriteFile(cfg, []byte("network:\n  retries: 0\n  timeout_sec: 2\ncache:\n  path: "+d+"/cache\n"), 0o644)
	os.WriteFile(filepath.Join(d, "sources.yaml"), []byte(`sources:
  dockerhub:
    type: oci-registry
    url: registry.test
    mirror: bad
    servers:
      bad: {url: bad.example}
      good: {url: good.example}
`), 0o644)
	// fake crane: 403 for bad.example, otherwise writes an (invalid) archive; records its arguments
	bin := filepath.Join(d, "bin")
	os.MkdirAll(bin, 0o755)
	script := "#!/bin/sh\necho \"$@\" >> " + d + "/crane.args\ncase \"$*\" in *bad.example*) echo 'denied: 403' >&2; exit 1;; esac\necho 'Pulling blob 1/1' >&2\nmkdir -p \"$7\"; echo x > \"$7/index.json\"\n"
	os.WriteFile(filepath.Join(bin, "crane"), []byte(script), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	imgs := filepath.Join(d, "images")

	var o, e bytes.Buffer
	// gate: experimental off -> refused before anything runs
	if rc := run([]string{"pull", "docker:nginx:alpine", "-c", cfg, "--out", imgs}, &o, &e); rc == 0 || !strings.Contains(e.String(), "experimental") {
		t.Fatalf("gate: rc=%d %s", rc, e.String())
	}
	if _, err := os.Stat(filepath.Join(d, "crane.args")); err == nil {
		t.Fatal("crane ran although OCI is disabled")
	}
	o.Reset()
	e.Reset()
	// bad mirror first (403), then good; the archive is not a real OCI archive so the import after the pull fails: that proves the order
	rc := run([]string{"pull", "docker:nginx:alpine", "--experimental-oci", "-c", cfg, "--out", imgs}, &o, &e)
	out := o.String()
	if rc == 0 || !strings.Contains(out, "via bad") || !strings.Contains(out, "trying the next server") || !strings.Contains(out, "via official") || strings.Contains(e.String(), "crane") {
		t.Fatalf("fallback: rc=%d\n%s\n%s", rc, out, e.String())
	}
	args, _ := os.ReadFile(filepath.Join(d, "crane.args"))
	if !strings.Contains(string(args), "pull --format oci --platform linux/amd64 bad.example/library/nginx:alpine") || !strings.Contains(string(args), "registry.test/library/nginx:alpine") {
		t.Fatalf("crane args: %s", args)
	}
	if _, err := os.Stat(filepath.Join(d, "cache", "oci", "nginx-alpine-amd64.tar")); err != nil {
		t.Fatal("archive not kept in the cache:", err)
	}
	// offline: no crane call, uses the cache
	os.Remove(filepath.Join(d, "crane.args"))
	o.Reset()
	run([]string{"pull", "docker:nginx:alpine", "--experimental-oci", "--offline", "-c", cfg, "--out", imgs}, &o, &e)
	if !strings.Contains(o.String(), "offline, using the cached archive") {
		t.Fatal(o.String())
	}
	if _, err := os.Stat(filepath.Join(d, "crane.args")); err == nil {
		t.Fatal("offline must not call crane")
	}
	// missing crane: clear hint
	t.Setenv("PATH", "/nonexistent")
	e.Reset()
	if rc := run([]string{"pull", "docker:busybox", "--experimental-oci", "-c", cfg, "--out", imgs}, &o, &e); rc == 0 || !strings.Contains(e.String(), "crane") || !strings.Contains(e.String(), "install") {
		t.Fatalf("missing crane: %d %s", rc, e.String())
	}
}
