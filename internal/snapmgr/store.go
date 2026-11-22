package snapmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"omloader/internal/config"
	"omloader/internal/snapshot"
)

// Modes.
const (
	ModeNone    = ""            // metadata only (image.yaml not found)
	ModeDM      = "dm-snapshot" // <root>/<image>/<node>.cow
	ModeOverlay = "overlayfs"   // <root>/<image>/<node>/{upper,work}
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)

// Store is the snapshot tree of one image plus its on-disk layers.
type Store struct {
	Root     string // snapshots root
	Image    string
	Mode     string
	BaseFile string // absolute path of the base image ("" if unknown)
	BaseSize int64  // bytes; sizes the sparse COW files (dm)
	BaseSHA  string // image.yaml sha256
	MaxDepth int
	Ret      config.Retention
	Tree     *snapshot.Tree
	TreePath string
	Statfs   StatfsFunc
	Now      func() time.Time
}

// Open loads <root>/<image>/tree.json. With create, a missing tree becomes an empty one (not saved until Save).
func Open(root, image string, create bool) (*Store, error) {
	if !nameRe.MatchString(image) || image == ".." {
		return nil, fmt.Errorf("invalid image name %q", image)
	}
	s := &Store{Root: root, Image: image, MaxDepth: 5, Ret: config.Retention{KeepLast: 10, MinFreePercent: 10, KeepTagged: true},
		TreePath: filepath.Join(root, image, "tree.json"), Now: time.Now}
	if _, err := os.Stat(s.TreePath); os.IsNotExist(err) && create {
		s.Tree = snapshot.New(image, "")
		return s, nil
	}
	t, err := snapshot.Load(s.TreePath)
	if err != nil {
		return nil, err
	}
	s.Tree = t
	return s, nil
}

// WithImage fills mode/base/limits from a resolved image.yaml (imageDir = directory of image.yaml).
func (s *Store) WithImage(res *config.Resolved, imageDir string) error {
	img := res.Image
	s.Mode = img.Snapshots.Mode
	s.BaseSHA = img.SHA256
	s.MaxDepth = img.Snapshots.MaxChainDepth
	s.Ret = res.Config.Snapshot.Retention
	if s.Tree.BaseSHA256 == "" {
		s.Tree.BaseSHA256 = img.SHA256
	}
	if b := img.Base; b != "" && !strings.Contains(b, "://") {
		if !filepath.IsAbs(b) {
			b = filepath.Join(imageDir, b)
		}
		s.BaseFile = b
		if st, err := os.Stat(b); err == nil {
			s.BaseSize = st.Size()
		}
	}
	return nil
}

// Save writes tree.json (creating the image dir).
func (s *Store) Save() error {
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return err
	}
	return s.Tree.Save(s.TreePath)
}

func (s *Store) dir() string { return filepath.Join(s.Root, s.Image) }

func (s *Store) cowPath(n string) string {
	f := s.Tree.Nodes[n].File
	if f == "" {
		f = n + ".cow"
	}
	return filepath.Join(s.dir(), f)
}

func (s *Store) layerDesc(n string) string {
	if s.Mode == ModeOverlay {
		return filepath.Join(s.dir(), n, "upper")
	}
	return s.cowPath(n)
}

// LayerExists reports whether the node's on-disk layer exists (always true in metadata-only mode).
func (s *Store) LayerExists(n string) bool {
	switch s.Mode {
	case ModeDM:
		_, err := os.Stat(s.cowPath(n))
		return err == nil
	case ModeOverlay:
		st, err := os.Stat(filepath.Join(s.dir(), n, "upper"))
		return err == nil && st.IsDir()
	}
	return true
}

// CreateLayer makes an empty layer: sparse COW sized to the base (dm) or empty upper/work (overlay).
func (s *Store) CreateLayer(n string) error {
	switch s.Mode {
	case ModeDM:
		if s.BaseSize <= 0 {
			return fmt.Errorf("base image size unknown; cannot size the COW file")
		}
		if err := os.MkdirAll(s.dir(), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(s.cowPath(n), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Truncate(s.BaseSize)
	case ModeOverlay:
		for _, d := range []string{"upper", "work"} {
			if err := os.MkdirAll(filepath.Join(s.dir(), n, d), 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

// RemoveLayer deletes the node's on-disk data.
func (s *Store) RemoveLayer(n string) error {
	switch s.Mode {
	case ModeDM:
		return removeIfExists(s.cowPath(n))
	case ModeOverlay:
		return os.RemoveAll(filepath.Join(s.dir(), n))
	}
	return nil
}

func removeIfExists(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// WipeLayer resets the node to a fresh empty state (own data only).
func (s *Store) WipeLayer(n string) error {
	if err := s.RemoveLayer(n); err != nil {
		return err
	}
	return s.CreateLayer(n)
}

// Usage returns apparent and allocated bytes of the node's layer.
func (s *Store) Usage(n string) (apparent, alloc int64) {
	switch s.Mode {
	case ModeDM:
		return fileUsage(s.cowPath(n))
	case ModeOverlay:
		return dirUsage(filepath.Join(s.dir(), n))
	}
	return 0, 0
}

func fileUsage(p string) (int64, int64) {
	var st syscall.Stat_t
	if err := syscall.Lstat(p, &st); err != nil {
		return 0, 0
	}
	return st.Size, st.Blocks * 512
}

func dirUsage(p string) (a, b int64) {
	filepath.Walk(p, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			b += st.Blocks * 512
		}
		if fi.Mode().IsRegular() {
			a += fi.Size()
		}
		return nil
	})
	return
}

func fileSHA(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Guard runs the space guard against the snapshot partition.
func (s *Store) Guard() error { return CheckSpace(s.Root, s.Ret.MinFreePercent, s.Statfs) }

// Create adds a snapshot (metadata + empty layer). The parent's data is never touched.
func (s *Store) Create(name, from, note string, tags []string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid snapshot name %q", name)
	}
	if err := s.Guard(); err != nil {
		return err
	}
	if from != "" && from != "base" {
		if _, ok := s.Tree.Nodes[from]; !ok {
			return fmt.Errorf("parent snapshot %q not found", from)
		}
	}
	parent := from
	if parent == "" {
		parent = s.Tree.Current
	} else if parent == "base" {
		parent = ""
	}
	depth := 1
	if parent != "" {
		d, err := s.Tree.Depth(parent)
		if err != nil {
			return err
		}
		depth = d + 1
	}
	if s.MaxDepth > 0 && depth > s.MaxDepth {
		return fmt.Errorf("chain depth %d would exceed max_chain_depth %d: flatten first (`omloader snapshot flatten %s %s`)", depth, s.MaxDepth, s.Image, orBase(parent))
	}
	prev := *s.Tree
	prevNodes := map[string]snapshot.Node{}
	for k, v := range s.Tree.Nodes {
		prevNodes[k] = v
	}
	if err := s.Tree.CreateWith(name, from, note, tags); err != nil {
		return err
	}
	undo := func() { s.Tree.Nodes, s.Tree.Current = prevNodes, prev.Current }
	if err := s.CreateLayer(name); err != nil {
		s.RemoveLayer(name)
		undo()
		return err
	}
	if err := s.Save(); err != nil {
		s.RemoveLayer(name)
		undo()
		return err
	}
	return nil
}

func orBase(n string) string {
	if n == "" {
		return "<node>"
	}
	return n
}

// Rollback moves current to node ("base" = base image). With discard, the node's own data is wiped
// (only allowed for leaves: frozen layers are lower layers of their children and are never touched).
func (s *Store) Rollback(node string, discard bool) error {
	if node == "base" {
		if discard {
			return fmt.Errorf("--discard needs a snapshot, not base")
		}
		s.Tree.Current = ""
		return s.Save()
	}
	if _, ok := s.Tree.Nodes[node]; !ok {
		return fmt.Errorf("snapshot %q not found", node)
	}
	if discard {
		if ch := s.Tree.Children(node); len(ch) > 0 {
			return fmt.Errorf("snapshot %q is frozen (children %v): its data is part of their stack and is not discarded; create a new child instead", node, ch)
		}
		if err := s.WipeLayer(node); err != nil {
			return err
		}
	}
	s.Tree.Current = node
	return s.Save()
}

// Delete removes a snapshot (and with recursive its subtree) including on-disk layers.
func (s *Store) Delete(node string, recursive, force bool) ([]string, error) {
	plan, err := s.Tree.PlanDelete(node, recursive, force)
	if err != nil {
		return nil, err
	}
	return plan, s.apply(plan)
}

func (s *Store) apply(names []string) error {
	for _, n := range names {
		if err := s.RemoveLayer(n); err != nil {
			return err
		}
	}
	s.Tree.Remove(names...)
	return s.Save()
}

// GC removes tmp-* ephemerals older than olderThan. dry only plans.
func (s *Store) GC(olderThan time.Duration, dry bool) ([]string, error) {
	plan := s.Tree.PlanGC(s.Now(), olderThan)
	if dry || len(plan) == 0 {
		return plan, nil
	}
	return plan, s.apply(plan)
}

// Prune applies the retention policy. dry only plans.
func (s *Store) Prune(dry bool) ([]string, error) {
	plan := s.Tree.PlanPrune(s.Ret.KeepLast, s.Ret.KeepTagged)
	if dry || len(plan) == 0 {
		return plan, nil
	}
	return plan, s.apply(plan)
}

// ListOptions for List.
type ListOptions struct{ Tree, Sizes bool }

func (s *Store) flags(n string) string {
	var f []string
	if n == s.Tree.Current {
		f = append(f, "current")
	}
	if len(s.Tree.Children(n)) > 0 {
		f = append(f, "frozen")
	}
	if strings.HasPrefix(n, "tmp-") {
		f = append(f, "ephemeral")
	}
	if s.Mode != ModeNone && !s.LayerExists(n) {
		f = append(f, "NO-LAYER")
	}
	return strings.Join(f, ",")
}

func (s *Store) line(n string, o ListOptions) string {
	nd := s.Tree.Nodes[n]
	p := nd.Parent
	if p == "" {
		p = "base"
	}
	mark := " "
	if n == s.Tree.Current {
		mark = "*"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", mark, n)
	if !o.Tree {
		fmt.Fprintf(&b, "\tparent=%s", p)
	}
	fmt.Fprintf(&b, "\tcreated=%s", nd.Created)
	if fl := s.flags(n); fl != "" {
		fmt.Fprintf(&b, "\t[%s]", fl)
	}
	if len(nd.Tags) > 0 {
		fmt.Fprintf(&b, "\ttags=%s", strings.Join(nd.Tags, ","))
	}
	if nd.Note != "" {
		fmt.Fprintf(&b, "\tnote=%q", nd.Note)
	}
	if o.Sizes {
		a, al := s.Usage(n)
		fmt.Fprintf(&b, "\tsize=%s apparent/%s allocated", Human(a), Human(al))
	}
	return b.String()
}

// List renders the snapshots (flat or as a tree).
func (s *Store) List(o ListOptions) string {
	var b strings.Builder
	if !o.Tree {
		for _, n := range s.Tree.Names() {
			b.WriteString(s.line(n, o) + "\n")
		}
		return b.String()
	}
	fmt.Fprintf(&b, "base%s\n", map[bool]string{true: "  *", false: ""}[s.Tree.Current == ""])
	var walk func(parent, prefix string)
	walk = func(parent, prefix string) {
		ch := s.Tree.Children(parent)
		sort.Strings(ch)
		for i, n := range ch {
			br, next := "├── ", "│   "
			if i == len(ch)-1 {
				br, next = "└── ", "    "
			}
			b.WriteString(prefix + br + s.line(n, o) + "\n")
			walk(n, prefix+next)
		}
	}
	walk("", "")
	return b.String()
}

// DF reports the snapshot partition usage and per-snapshot sizes.
func (s *Store) DF() (string, error) {
	total, free, pct, err := FreePercent(s.Root, s.Statfs)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "snapshot partition (%s): total=%s free=%s (%.1f%%) min_free_percent=%d%%\n", s.Root, Human(int64(total)), Human(int64(free)), pct, s.Ret.MinFreePercent)
	var sa, sb int64
	for _, n := range s.Tree.Names() {
		a, al := s.Usage(n)
		sa, sb = sa+a, sb+al
	}
	fmt.Fprintf(&b, "image %s: %d snapshots, %s apparent, %s allocated\n", s.Image, len(s.Tree.Nodes), Human(sa), Human(sb))
	return b.String(), nil
}
