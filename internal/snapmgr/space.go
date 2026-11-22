// Package snapmgr is the mode-aware snapshot manager: tree.json metadata plus the real on-disk layers
// (dm-snapshot: <root>/<image>/<node>.cow sparse files; overlayfs: <root>/<image>/<node>/{upper,work}).
package snapmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// StatfsFunc returns total and available bytes of the filesystem holding path (injectable for tests).
type StatfsFunc func(path string) (total, free uint64, err error)

// RealStatfs uses statfs(2).
func RealStatfs(path string) (uint64, uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return st.Blocks * uint64(st.Bsize), st.Bavail * uint64(st.Bsize), nil
}

func nearestExisting(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		n := filepath.Dir(p)
		if n == p {
			return p
		}
		p = n
	}
}

// FreePercent returns the free percentage of the filesystem holding path.
func FreePercent(path string, fn StatfsFunc) (total, free uint64, pct float64, err error) {
	if fn == nil {
		fn = RealStatfs
	}
	total, free, err = fn(nearestExisting(path))
	if err != nil {
		return 0, 0, 0, err
	}
	if total == 0 {
		return 0, 0, 0, fmt.Errorf("statfs %s: zero size filesystem", path)
	}
	return total, free, float64(free) * 100 / float64(total), nil
}

// CheckSpace is the space guard: it refuses when less than minPct percent of the snapshot partition is free.
func CheckSpace(path string, minPct int, fn StatfsFunc) error {
	if minPct <= 0 {
		return nil
	}
	total, free, pct, err := FreePercent(path, fn)
	if err != nil {
		return err
	}
	if pct < float64(minPct) {
		return fmt.Errorf("space guard: snapshot partition of %s has %.1f%% free (%s of %s), below snapshot.retention.min_free_percent=%d%%; free space (omloader snapshot prune / gc / delete) or lower the limit",
			path, pct, Human(int64(free)), Human(int64(total)), minPct)
	}
	return nil
}

// Human formats bytes (IEC).
func Human(n int64) string {
	const u = "KMGTP"
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	f := float64(n)
	i := -1
	for f >= 1024 && i < len(u)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f%ciB", f, u[i])
}
