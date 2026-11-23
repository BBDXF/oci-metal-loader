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
type: lxd-vm
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
	i := write(t, "image.yaml", strings.Replace(goodImage, "type: lxd-vm", "type: oci", 1))
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

func TestRuntimeUsers(t *testing.T) {
	ok := goodImage + "runtime:\n  autologin: true\n  autologin_user: alice\n  debug_shell: auto\n  shutdown_sync: false\n  root_password_hash: '$6$s$h'\n  users:\n    - name: alice\n      password: pw\n      groups: [sudo]\n      sudo: true\n      ssh_authorized_keys: [\"ssh-ed25519 AAAA a\"]\n"
	r, err := Load("", write(t, "i.yaml", ok))
	if err != nil {
		t.Fatal(err)
	}
	if errs := r.Validate(); len(errs) != 0 {
		t.Fatalf("valid config rejected: %v", errs)
	}
	if rt := r.Image.Runtime; len(rt.Users) != 1 || !rt.Users[0].Sudo || rt.SyncOnShutdown() || rt.AutologinUser != "alice" {
		t.Fatalf("runtime: %+v", rt)
	}
	bad := goodImage + "runtime:\n  debug_shell: maybe\n  users:\n    - name: Bad Name\n    - name: ok\n      password: a\n      password_hash: b\n    - name: ok\n      shell: sh\n"
	_, err = Load("", write(t, "b.yaml", bad))
	if err == nil {
		t.Fatal("invalid users accepted")
	}
	e := err.Error()
	for _, w := range []string{"debug_shell", "invalid user name", "not both", "duplicate user", "absolute path"} {
		if !strings.Contains(e, w) {
			t.Errorf("missing %q in:\n%s", w, e)
		}
	}
}

func TestOldVMTypeRejected(t *testing.T) {
	i := write(t, "image.yaml", strings.Replace(goodImage, "type: lxd-vm", "type: vm", 1))
	if _, err := Load("", i); err == nil || !strings.Contains(err.Error(), "use \"lxd-vm\"") {
		t.Fatalf("got %v", err)
	}
	i = write(t, "image2.yaml", strings.Replace(goodImage, "format: raw", "format: squashfs", 1))
	if _, err := Load("", i); err == nil || !strings.Contains(err.Error(), "type lxd-vm requires format raw") {
		t.Fatalf("got %v", err)
	}
}
