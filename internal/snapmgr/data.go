package snapmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Runner mirrors boot.Runner (kept local to avoid an import cycle).
type Runner interface {
	Run(name string, args ...string) (string, error)
}

// ParseSize parses "512M", "20G", "1.5G", "1048576" (bytes) into bytes.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	for suf, m := range map[string]int64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40} {
		for _, x := range []string{suf + "IB", suf + "B", suf} {
			if strings.HasSuffix(s, x) {
				s, mult = strings.TrimSuffix(s, x), m
				goto parsed
			}
		}
	}
parsed:
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return int64(f * float64(mult)), nil
}

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
