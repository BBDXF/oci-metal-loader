package snapmgr

import (
	"omloader/internal/config"
	"os"
	"path/filepath"
	"strconv"
)

// Runner mirrors boot.Runner (kept local to avoid an import cycle).
type Runner interface {
	Run(name string, args ...string) (string, error)
}

// ParseSize parses "512M", "20G", "1.5G", "1048576" (bytes) into bytes.
func ParseSize(s string) (int64, error) { return config.ParseSize(s) }

// EnsureExt4File creates a sparse ext4 image (once). Existing files are left untouched.
func EnsureExt4File(r Runner, path string, size int64, label string) (created bool, err error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".new"
	os.Remove(tmp)
	if _, err := r.Run("truncate", "-s", strconv.FormatInt(size, 10), tmp); err != nil {
		return false, err
	}
	if len(label) > 16 {
		label = label[:16]
	}
	if _, err := r.Run("mkfs.ext4", "-q", "-F", "-L", label, tmp); err != nil {
		os.Remove(tmp)
		return false, err
	}
	// a fake runner may not create the file; rename only when present
	if _, err := os.Stat(tmp); err == nil {
		if err := os.Rename(tmp, path); err != nil {
			return false, err
		}
	}
	return true, nil
}
