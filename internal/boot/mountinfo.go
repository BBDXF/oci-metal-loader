package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type mountEnt struct{ Root, Point, FSType, Source string }

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func parseMountInfo(s string) []mountEnt {
	var out []mountEnt
	for _, ln := range strings.Split(s, "\n") {
		f := strings.Fields(ln)
		sep := -1
		for i, x := range f {
			if x == "-" {
				sep = i
				break
			}
		}
		if sep < 6 || len(f) < sep+3 {
			continue
		}
		out = append(out, mountEnt{Root: unescape(f[3]), Point: unescape(f[4]), FSType: f[sep+1], Source: unescape(f[sep+2])})
	}
	return out
}

// findMount returns the mount containing path (longest mount point) and the path relative to its fs root.
func findMount(ents []mountEnt, path string) (mountEnt, string, bool) {
	var best mountEnt
	found := false
	for _, e := range ents {
		p := strings.TrimSuffix(e.Point, "/")
		if path == e.Point || e.Point == "/" || strings.HasPrefix(path, p+"/") {
			if !found || len(e.Point) >= len(best.Point) {
				best, found = e, true
			}
		}
	}
	if !found {
		return best, "", false
	}
	rel := strings.TrimPrefix(path, strings.TrimSuffix(best.Point, "/"))
	return best, strings.TrimPrefix(rel, "/"), true
}

// storageTracker maps host files to storages (identified by blkid UUID/label).
type storageTracker struct {
	r       Runner
	mounts  []mountEnt
	list    []*StorageRef
	bySrc   map[string]*StorageRef
	dryFake bool
}

func newTracker(r Runner, mountInfoPath string, dry bool) (*storageTracker, error) {
	if mountInfoPath == "" {
		mountInfoPath = "/proc/self/mountinfo"
	}
	b, err := os.ReadFile(mountInfoPath)
	if err != nil && !dry {
		return nil, err
	}
	return &storageTracker{r: r, mounts: parseMountInfo(string(b)), bySrc: map[string]*StorageRef{}, dryFake: dry}, nil
}

func (t *storageTracker) ref(file string, rw bool) (FileRef, error) {
	real, err := filepath.EvalSymlinks(file)
	if err != nil {
		real = file
	}
	real, _ = filepath.Abs(real)
	m, rel, ok := findMount(t.mounts, real)
	if !ok || !strings.HasPrefix(m.Source, "/dev/") {
		if t.dryFake {
			m = mountEnt{Root: "/", Point: "/", FSType: "ext4", Source: "/dev/DRY-UNKNOWN"}
			rel = strings.TrimPrefix(real, "/")
		} else {
			return FileRef{}, fmt.Errorf("%s is not on a block-device filesystem (mount source %q)", file, m.Source)
		}
	}
	s := t.bySrc[m.Source+"|"+m.Root]
	if s == nil {
		// store-file mode: the filesystem sits on a loop device backed by a file on another storage; that host
		// storage is recorded first (and must be writable: the loop device writes into the file).
		var loopFile *FileRef
		var exts []Extent
		if strings.HasPrefix(m.Source, "/dev/loop") {
			bo, err := t.r.Run("losetup", "--noheadings", "-O", "BACK-FILE", m.Source)
			if err != nil {
				return FileRef{}, err
			}
			bf := strings.TrimSuffix(firstLine(bo), " (deleted)")
			if bf == "" {
				return FileRef{}, fmt.Errorf("cannot determine the backing file of %s", m.Source)
			}
			hr, err := t.ref(bf, true)
			if err != nil {
				return FileRef{}, fmt.Errorf("store file %s: %w", bf, err)
			}
			loopFile = &hr
			if !t.dryFake {
				if ex, size, err := FileExtents(bf); err == nil {
					if v, err := ValidateExtents(ex, size); err == nil {
						exts = v
						for _, h := range t.list {
							if h.ID == hr.Storage {
								h.NoMount = true
							}
						}
					}
				}
			}
		}
		out, err := t.r.Run("blkid", "-o", "export", m.Source)
		if err != nil {
			return FileRef{}, err
		}
		s = &StorageRef{ID: fmt.Sprintf("s%d", len(t.list)), Root: m.Root, Loop: loopFile, Extents: exts}
		for _, ln := range strings.Split(out, "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(ln), "=")
			switch k {
			case "UUID":
				s.UUID = v
			case "LABEL":
				s.Label = v
			case "TYPE":
				s.FSType = v
			}
		}
		if s.FSType == "ntfs" {
			s.FSType = "ntfs3" // the in-kernel driver (blkid only says "ntfs"); mounted with -t ntfs3 in the guest
		}
		if s.UUID == "" && s.Label == "" && s.Loop == nil {
			return FileRef{}, fmt.Errorf("%s has neither UUID nor label", m.Source)
		}
		t.bySrc[m.Source+"|"+m.Root] = s
		t.list = append(t.list, s)
	}
	if rw {
		s.RW = true
	}
	return FileRef{Storage: s.ID, Rel: rel}, nil
}
