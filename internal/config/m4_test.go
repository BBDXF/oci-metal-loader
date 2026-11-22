package config

import (
	"strings"
	"testing"
)

func TestM4Keys(t *testing.T) {
	g := write(t, "m4.yaml", `mirror_policy:
  region_preset: cn
  strategy: fastest
network:
  proxy: socks5://127.0.0.1:1080
  no_proxy: "10.0.0.0/8,.lan"
sources:
  lxc-images:
    mirrors: [https://m.example/lxc]
    rewrite:
      - {from: "https://images.linuxcontainers.org", to: "https://m.example/lxc"}
policy:
  allowed_sources: [lxc-images]
`)
	r, err := Load(g, "")
	if err != nil {
		t.Fatal(err)
	}
	m := r.Config.SourceMirrors("lxc-images")
	if len(m) != 3 || m[0] != "https://m.example/lxc" || !strings.Contains(m[1], "tsinghua") {
		t.Fatalf("%v", m)
	}
	if !r.Config.SourceAllowed("lxc-images") || r.Config.SourceAllowed("dockerhub") || r.Config.SourceUsable("dockerhub") {
		t.Fatal("allowed_sources")
	}
	if r2, _ := Load("", ""); len(r2.Config.SourceMirrors("lxc-images")) != 0 {
		t.Fatal("default has no mirrors")
	}
}

func TestM4Validation(t *testing.T) {
	g := write(t, "bad.yaml", "network:\n  proxy: ftp://x\npolicy:\n  allowed_sources: [nope]\nsources:\n  a:\n    type: http\n    url: http://a\n    mirrors: [ftp://m]\nmirror_policy:\n  strategy: weird\n")
	_, err := Load(g, "")
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, w := range []string{"network.proxy", "allowed_sources", "sources.a.mirrors", "mirror_policy.strategy"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("missing %s in %v", w, err)
		}
	}
}
