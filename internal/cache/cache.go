// Package cache is the content-addressed download cache: blobs/<aa>/<sha256>, meta/<hash(url)>, tmp/<sha256>.part (resume).
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

type Cache struct{ Dir string }

var shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (c *Cache) Blob(sha string) string { return filepath.Join(c.Dir, "blobs", sha[:2], sha) }
func (c *Cache) Part(sha string) string { return filepath.Join(c.Dir, "tmp", sha+".part") }

// Has reports whether a verified blob exists and marks it as recently used.
func (c *Cache) Has(sha string) bool {
	if !shaRe.MatchString(sha) {
		return false
	}
	if _, err := os.Stat(c.Blob(sha)); err != nil {
		return false
	}
	now := time.Now()
	os.Chtimes(c.Blob(sha), now, now)
	return true
}

// Commit atomically moves a fully verified part file into place.
func (c *Cache) Commit(part, sha string) error {
	if !shaRe.MatchString(sha) {
		return fmt.Errorf("bad sha256 %q", sha)
	}
	if err := os.MkdirAll(filepath.Dir(c.Blob(sha)), 0o755); err != nil {
		return err
	}
	return os.Rename(part, c.Blob(sha))
}

func metaKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:16])
}

// PutMeta stores small documents (simplestreams index/catalog) keyed by URL-ish string.
func (c *Cache) PutMeta(key string, data []byte) error {
	p := filepath.Join(c.Dir, "meta", metaKey(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// GetMeta returns the stored document and its age.
func (c *Cache) GetMeta(key string) ([]byte, time.Duration, error) {
	p := filepath.Join(c.Dir, "meta", metaKey(key))
	st, err := os.Stat(p)
	if err != nil {
		return nil, 0, err
	}
	b, err := os.ReadFile(p)
	return b, time.Since(st.ModTime()), err
}

type Entry struct {
	Kind    string // blob | meta | partial
	Name    string
	Path    string
	Size    int64
	ModTime time.Time
}

// List returns all cache entries, oldest first.
func (c *Cache) List() ([]Entry, error) {
	var out []Entry
	for _, k := range []struct{ kind, glob string }{{"blob", "blobs/*/*"}, {"meta", "meta/*"}, {"partial", "tmp/*"}} {
		ms, _ := filepath.Glob(filepath.Join(c.Dir, k.glob))
		for _, m := range ms {
			if st, err := os.Stat(m); err == nil && st.Mode().IsRegular() {
				out = append(out, Entry{Kind: k.kind, Name: filepath.Base(m), Path: m, Size: st.Size(), ModTime: st.ModTime()})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.Before(out[j].ModTime) })
	return out, nil
}

// GC drops partial downloads older than a day, then the least recently used blobs until the total is <= maxBytes
// (maxBytes <= 0: only the partials). Returns removed entries.
func (c *Cache) GC(maxBytes int64) ([]Entry, error) {
	es, err := c.List()
	if err != nil {
		return nil, err
	}
	var removed []Entry
	var total int64
	var keep []Entry
	for _, e := range es {
		if e.Kind == "partial" && time.Since(e.ModTime) > 24*time.Hour {
			if os.Remove(e.Path) == nil {
				removed = append(removed, e)
			}
			continue
		}
		total += e.Size
		keep = append(keep, e)
	}
	if maxBytes > 0 {
		for _, e := range keep { // oldest first; blobs first, then metadata
			if total <= maxBytes {
				break
			}
			if e.Kind != "blob" {
				continue
			}
			if os.Remove(e.Path) == nil {
				removed = append(removed, e)
				total -= e.Size
			}
		}
	}
	return removed, nil
}
