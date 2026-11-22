package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name, s string) string {
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const goodImage = `name: u
type: vm
format: raw
base: /images/u.raw
snapshots:
  mode: dm-snapshot
`

func TestDefaultsOnly(t *testing.T) {
	r, err := Load("", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Config.Snapshot.MaxChainDepth != 5 || r.Sources["snapshot.max_chain_depth"] != LayerDefaults {
		t.Fatalf("bad defaults: %+v", r.Config.Snapshot)
	}
}

func TestMergeAndSources(t *testing.T) {
	g := write(t, "b.yaml", "snapshot:\n  max_chain_depth: 8\nboot:\n  timeout: 9\n")
	i := write(t, "image.yaml", goodImage)
	r, err := Load(g, i)
	if err != nil {
		t.Fatal(err)
	}
	if r.Config.Snapshot.MaxChainDepth != 8 || r.Config.Snapshot.Retention.KeepLast != 10 {
		t.Fatal("deep merge failed")
	}
	if r.Sources["snapshot.max_chain_depth"] != LayerGlobal || r.Sources["snapshot.persistent"] != LayerDefaults {
		t.Fatal("sources wrong")
	}
	if r.Image.Snapshots.MaxChainDepth != 8 || r.Sources["image.snapshots.max_chain_depth"] != LayerGlobal {
		t.Fatal("image inherits global depth")
	}
	if r.Sources["image.name"] != LayerImage {
		t.Fatal("image source")
	}
}

func TestValidationErrors(t *testing.T) {
	g := write(t, "b.yaml", "storage:\n  images:\n    path: /a/../b\nboot:\n  timeout: -1\n")
	_, err := Load(g, "")
	if err == nil || !strings.Contains(err.Error(), ":3: storage.images.path") || !strings.Contains(err.Error(), "'..'") {
		t.Fatalf("got %v", err)
	}
}

func TestTypeConsistency(t *testing.T) {
	i := write(t, "image.yaml", strings.Replace(goodImage, "type: vm", "type: oci", 1))
	_, err := Load("", i)
	if err == nil || !strings.Contains(err.Error(), "requires format squashfs") {
		t.Fatalf("got %v", err)
	}
}

func TestOfflinePolicy(t *testing.T) {
	g := write(t, "b.yaml", "policy:\n  allow_online: false\n")
	i := write(t, "image.yaml", strings.Replace(goodImage, "/images/u.raw", "https://x/u.raw", 1))
	r, err := Load(g, i)
	if err == nil || !strings.Contains(err.Error(), "allow_online") {
		t.Fatalf("got %v", err)
	}
	if r.Config.SourceUsable("dockerhub") {
		t.Fatal("remote source should be unusable")
	}
}

func TestOfflineEnum(t *testing.T) {
	for _, v := range []string{"auto", "on", "off"} {
		g := write(t, "o.yaml", "cache:\n  offline: "+v+"\n")
		if _, err := Load(g, ""); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	g := write(t, "o.yaml", "cache:\n  offline: always\n")
	if _, err := Load(g, ""); err == nil {
		t.Fatal("always must be rejected")
	}
}

func TestUnknownKey(t *testing.T) {
	g := write(t, "b.yaml", "bogus: 1\n")
	if _, err := Load(g, ""); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("got %v", err)
	}
}
