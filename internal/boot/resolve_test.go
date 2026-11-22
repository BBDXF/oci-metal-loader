package boot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInRoot(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "boot"), 0o755)
	os.MkdirAll(filepath.Join(root, "usr/lib"), 0o755)
	os.WriteFile(filepath.Join(root, "boot/vmlinuz-1.2"), []byte("k"), 0o644)
	os.Symlink("/boot/vmlinuz-1.2", filepath.Join(root, "boot/vmlinuz")) // absolute
	os.Symlink("vmlinuz-1.2", filepath.Join(root, "boot/rel"))           // relative
	os.Symlink("usr/lib", filepath.Join(root, "lib"))
	os.Symlink("../../../../../../etc/passwd", filepath.Join(root, "boot/escape")) // must stay inside
	os.Symlink("/loop", filepath.Join(root, "loop"))
	for in, want := range map[string]string{
		"/boot/vmlinuz": "boot/vmlinuz-1.2",
		"/boot/rel":     "boot/vmlinuz-1.2",
		"/lib":          "usr/lib",
	} {
		got, err := ResolveInRoot(root, in)
		if err != nil || got != filepath.Join(root, want) {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	if _, err := ResolveInRoot(root, "/boot/escape"); err == nil || !strings.Contains(err.Error(), "no such") {
		t.Errorf("escape must resolve inside root and fail as missing: %v", err)
	}
	if _, err := ResolveInRoot(root, "/loop"); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("loop: %v", err)
	}
	if _, err := ResolveInRoot(root, "/nope"); err == nil {
		t.Error("missing must fail")
	}
}
