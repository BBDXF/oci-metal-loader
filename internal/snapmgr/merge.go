package snapmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Merger decides how overlay special entries are recognised (injectable for tests that cannot create char devices).
type Merger struct {
	IsWhiteout func(path string, fi os.FileInfo) bool // char device 0/0
	IsOpaque   func(dir string) bool                  // directory carries the overlay opaque xattr
	SetOpaque  func(dir string) error
}

var opaqueXattrs = []string{"trusted.overlay.opaque", "user.overlay.opaque"}

// RealMerger works on real overlay upper directories.
var RealMerger = Merger{
	IsWhiteout: func(_ string, fi os.FileInfo) bool {
		if fi.Mode()&os.ModeCharDevice == 0 {
			return false
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		return ok && st.Rdev == 0
	},
	IsOpaque: func(dir string) bool {
		buf := make([]byte, 4)
		for _, x := range opaqueXattrs {
			if n, err := syscall.Getxattr(dir, x, buf); err == nil && n > 0 && buf[0] == 'y' {
				return true
			}
		}
		return false
	},
	SetOpaque: func(dir string) error {
		var err error
		for _, x := range opaqueXattrs {
			if err = syscall.Setxattr(dir, x, []byte("y"), 0); err == nil {
				return nil
			}
		}
		return fmt.Errorf("mark %s opaque: %w", dir, err)
	},
}

// MergeUpper merges the upper directory src (the lower snapshot's changes) into dst (the child's upper), so that the
// overlay view of dst on top of the snapshot's former parent is unchanged. dst wins over src for every path; entries
// only src has are moved (renamed: ownership, xattrs, whiteouts are preserved as-is) into dst. Directories merge
// recursively; a whiteout or opaque marker in src below a plain directory of dst makes that directory opaque.
// src itself is left in place for the caller to delete.
func MergeUpper(src, dst string) error { return RealMerger.Merge(src, dst) }

// Merge is MergeUpper with this Merger.
func (m Merger) Merge(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil // never booted: nothing to merge
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return m.mergeDir(src, dst)
}

func (m Merger) mergeDir(src, dst string) error {
	ents, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range ents {
		sp, dp := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		sfi, err := os.Lstat(sp)
		if err != nil {
			return err
		}
		dfi, err := os.Lstat(dp)
		if os.IsNotExist(err) {
			if err := os.Rename(sp, dp); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		dIsDir := dfi.IsDir()
		switch {
		case sfi.IsDir() && dIsDir:
			if m.IsOpaque(dp) {
				continue // dst hides everything below
			}
			if err := m.mergeDir(sp, dp); err != nil {
				return err
			}
			if m.IsOpaque(sp) {
				if err := m.SetOpaque(dp); err != nil {
					return err
				}
			}
		case dIsDir && m.IsWhiteout(sp, sfi):
			// src deleted the path, dst recreated it as a plain directory: it must not show the lower content again
			if !m.IsOpaque(dp) {
				if err := m.SetOpaque(dp); err != nil {
					return err
				}
			}
		}
		// every other combination: dst wins, src's entry is dropped with src
	}
	return nil
}
