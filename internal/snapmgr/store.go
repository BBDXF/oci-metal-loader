package snapmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
	// IgnoreLock skips the "image is in use" check of mutating operations (boot-time auto-gc).
	IgnoreLock bool
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

// Dir is <root>/<image>.
func (s *Store) Dir() string { return s.dir() }

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

func (s *Store) checkIdle() error {
	if s.IgnoreLock {
		return nil
	}
	if busy, why := s.InUse(); busy {
		return fmt.Errorf("image %s is in use (%s): snapshots are managed from the loader before the image is booted: shut the system down and return to the loader first", s.Image, why)
	}
	return nil
}

func (s *Store) removeNodes(ids []string) error { return s.removeNodesHead(ids, false, "") }

// removeNodesHead removes nodes; with setHead the live state's base is moved to head afterwards.
func (s *Store) removeNodesHead(ids []string, setHead bool, head string) error {
	s.Tree.Remove(ids...)
	if setHead {
		s.Tree.SetHead(head)
	}
	if err := s.Save(); err != nil {
		return err
	}
	for _, n := range ids {
		if err := s.removeLayerID(n); err != nil {
			return err
		}
	}
	return nil
}

// removeLayerID removes the data of a node that is no longer in the tree (file name derived from the id).
func (s *Store) removeLayerID(id string) error {
	switch s.Mode {
	case ModeDM:
		return removeIfExists(filepath.Join(s.dir(), id+".cow"))
	case ModeOverlay:
		return os.RemoveAll(filepath.Join(s.dir(), id))
	}
	return nil
}

// GCEphemeral removes all ephemeral nodes (leftovers of ephemeral boots). dry only plans. Returns their ids.
func (s *Store) GCEphemeral(dry bool) ([]string, error) {
	plan := s.Tree.Ephemerals()
	if dry || len(plan) == 0 {
		return plan, nil
	}
	return plan, s.removeNodes(plan)
}

func (s *Store) depthGuard(parent, label string) error {
	d, err := s.Tree.Depth(parent)
	if err != nil {
		return err
	}
	if s.MaxDepth > 0 && d+1 > s.MaxDepth {
		return fmt.Errorf("chain depth %d would exceed max_chain_depth %d: delete or prune snapshots, or flatten first (`omloader snapshot flatten %s %s` builds a new flat image)", d+1, s.MaxDepth, s.Image, orLabel(label))
	}
	return nil
}

func orLabel(l string) string {
	if l == "" {
		return "<label>"
	}
	return l
}

// Create freezes the live state as snapshot label; the next boot continues in a fresh empty live layer on top.
func (s *Store) Create(label, note string, tags []string) error {
	if _, err := s.GCEphemeral(false); err != nil {
		return err
	}
	if err := s.checkIdle(); err != nil {
		return err
	}
	if err := s.Guard(); err != nil {
		return err
	}
	if s.Tree.Current != "" {
		if err := s.depthGuard(s.Tree.Current, label); err != nil {
			return err
		}
	}
	prev := s.Tree.Clone()
	if _, err := s.Tree.Freeze(label, note, tags); err != nil {
		return err
	}
	if err := s.Save(); err != nil {
		s.Tree = prev
		return err
	}
	return nil
}

// Revert discards the live state and continues from target (a label or "base"). With keep, the old live state is
// first frozen as snapshot keep (a sibling branch); otherwise its data is deleted.
func (s *Store) Revert(target, keep string) error {
	if _, err := s.GCEphemeral(false); err != nil {
		return err
	}
	if err := s.checkIdle(); err != nil {
		return err
	}
	id, err := s.Tree.ResolveSnapshot(target, true)
	if err != nil {
		return err
	}
	prev := s.Tree.Clone()
	old := s.Tree.Current
	if keep != "" {
		if err := s.Guard(); err != nil {
			return err
		}
		if _, err := s.Tree.Freeze(keep, "", nil); err != nil {
			return err
		}
		old = ""
	} else if old != "" {
		if ch := s.Tree.Children(old); len(ch) > 0 {
			return fmt.Errorf("live state %s unexpectedly has children %v", old, ch)
		}
		s.Tree.Remove(old)
	}
	s.Tree.SetHead(id)
	if err := s.Save(); err != nil {
		s.Tree = prev
		return err
	}
	if old != "" {
		return s.removeLayerID(old)
	}
	return nil
}

// DeletePlan is what Delete would do.
type DeletePlan struct {
	Kind  string   // leaf | merge | subtree
	IDs   []string // layers removed (deepest first)
	Node  string   // the snapshot's node id
	Child string   // merge: the child that receives the snapshot's changes
	Desc  []string // human-readable description
	Head  bool     // the live state is based on this snapshot with no changes yet
}

// PlanDelete validates deleting snapshot label without changing anything.
func (s *Store) PlanDelete(label string, recursive, force bool) (*DeletePlan, error) {
	if label == snapshot.Base {
		return nil, fmt.Errorf("base is the read-only original image and cannot be deleted")
	}
	t := s.Tree
	id, err := t.ResolveSnapshot(label, false)
	if err != nil {
		return nil, err
	}
	var kids []string
	for _, k := range t.PersistentChildren(id) {
		kids = append(kids, k)
	}
	pl := &DeletePlan{Node: id, Head: t.Current == "" && t.LiveParent == id}
	if pl.Head && !force {
		return nil, fmt.Errorf("snapshot %q is what the live state is based on (no changes since): use --force to move the live state to %s, or revert elsewhere first", label, t.Display(t.Nodes[id].Parent))
	}
	switch {
	case len(kids) == 1 && !recursive:
		if s.Mode != ModeOverlay {
			return nil, fmt.Errorf("snapshot %q has a child (%s): deleting it would merge its changes into that child, which is not supported in %s mode (stacked COW layers cannot be merged). "+
				"Use `omloader snapshot flatten %s %s` to build a standalone image, or delete the whole branch with --recursive", label, t.Display(kids[0]), orMode(s.Mode), s.Image, label)
		}
		pl.Kind, pl.IDs, pl.Child = "merge", []string{id}, kids[0]
		pl.Desc = []string{fmt.Sprintf("merge %s into %s, then remove %s", label, t.Display(kids[0]), label)}
	case len(kids) == 0:
		pl.Kind, pl.IDs = "leaf", []string{id}
		pl.Desc = []string{"remove " + label}
	default:
		if !recursive {
			var names []string
			for _, k := range kids {
				names = append(names, t.Display(k))
			}
			return nil, fmt.Errorf("snapshot %q has several children %v; use --recursive to delete the whole subtree", label, names)
		}
		all := append(t.Descendants(id), id)
		for _, n := range all {
			if n == t.Current && !force {
				return nil, fmt.Errorf("the live state is inside the subtree of %q; use --force to delete it too", label)
			}
		}
		pl.Kind, pl.IDs = "subtree", all
		var names []string
		for _, n := range all {
			names = append(names, t.Display(n))
		}
		pl.Desc = []string{"remove the subtree: " + strings.Join(names, ", ")}
	}
	return pl, nil
}

// Delete removes snapshot label. A leaf branch tip is removed; a snapshot with exactly one child is merged into that
// child (overlay mode only); with recursive the whole subtree goes. Returns what was removed/merged (description).
func (s *Store) Delete(label string, recursive, force bool) (string, error) {
	if _, err := s.GCEphemeral(false); err != nil {
		return "", err
	}
	if err := s.checkIdle(); err != nil {
		return "", err
	}
	pl, err := s.PlanDelete(label, recursive, force)
	if err != nil {
		return "", err
	}
	t := s.Tree
	if pl.Kind == "merge" {
		if err := MergeUpper(filepath.Join(s.dir(), pl.Node, "upper"), filepath.Join(s.dir(), pl.Child, "upper")); err != nil {
			return "", fmt.Errorf("merging %s into %s: %w", label, t.Display(pl.Child), err)
		}
		cn := t.Nodes[pl.Child]
		cn.Parent = t.Nodes[pl.Node].Parent
		t.Nodes[pl.Child] = cn
		if pl.Head {
			t.LiveParent = cn.Parent
		}
	}
	oldParent := t.Nodes[pl.Node].Parent
	liveGone := pl.Head
	for _, n := range pl.IDs {
		if t.Current != "" && n == t.Current {
			liveGone = true
		}
	}
	if pl.Kind == "merge" {
		liveGone = false
		if pl.Head {
			t.LiveParent = oldParent
		}
	}
	if err := s.removeNodesHead(pl.IDs, liveGone, oldParent); err != nil {
		return "", err
	}
	return strings.Join(pl.Desc, "; "), nil
}

func orMode(m string) string {
	if m == "" {
		return "this"
	}
	return m
}

// Prune applies the retention policy to the snapshots (never the live node, tagged or ancestors of kept ones).
func (s *Store) Prune(dry bool) ([]string, error) {
	if !dry {
		if _, err := s.GCEphemeral(false); err != nil {
			return nil, err
		}
		if err := s.checkIdle(); err != nil {
			return nil, err
		}
	}
	plan := s.Tree.PlanPrune(s.Ret.KeepLast, s.Ret.KeepTagged)
	var names []string
	for _, n := range plan {
		names = append(names, s.Tree.Display(n))
	}
	if dry || len(plan) == 0 {
		return names, nil
	}
	return names, s.removeNodes(plan)
}

// ListOptions for List.
type ListOptions struct{ Tree, Sizes bool }

func (s *Store) flags(n string) string {
	var f []string
	nd := s.Tree.Nodes[n]
	if n == s.Tree.Current {
		f = append(f, "current")
	} else if nd.Label != "" {
		f = append(f, "snapshot")
	}
	if n != "" && n == s.Tree.Head() {
		f = append(f, "live-state-based-here")
	}
	if len(s.Tree.PersistentChildren(n)) > 0 {
		f = append(f, "frozen")
	}
	if nd.Ephemeral {
		f = append(f, "ephemeral")
	}
	if s.Mode != ModeNone && !s.LayerExists(n) {
		f = append(f, "NO-LAYER")
	}
	return strings.Join(f, ",")
}

func (s *Store) line(n string, o ListOptions) string {
	nd := s.Tree.Nodes[n]
	var b strings.Builder
	name := nd.Label
	mark := " "
	switch {
	case n == s.Tree.Current:
		mark, name = "●", "current (you are here)"
	case n == s.Tree.Head():
		mark = "●"
	}
	if name == "" {
		name = n
		if nd.Ephemeral {
			name = "(ephemeral boot)"
		}
	}
	fmt.Fprintf(&b, "%s %s", mark, name)
	if !o.Tree {
		fmt.Fprintf(&b, "\tparent=%s", s.Tree.Display(nd.Parent))
	}
	fmt.Fprintf(&b, "\tid=%s\tcreated=%s", n, nd.Created)
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
	t := s.Tree
	virtualLive := t.Current == ""
	liveLine := func() string {
		return "● current (you are here)\t[no changes yet: created at the next boot]"
	}
	if !o.Tree {
		fmt.Fprintf(&b, "%s base (original image)\t[read-only]\n", map[bool]string{true: "●", false: " "}[t.Head() == ""])
		for _, n := range t.Names() {
			b.WriteString(s.line(n, o) + "\n")
		}
		if virtualLive {
			b.WriteString(liveLine() + "\tparent=" + t.Display(t.LiveParent) + "\n")
		}
		return b.String()
	}
	fmt.Fprintf(&b, "%s base (original image)\n", map[bool]string{true: "●", false: " "}[t.Head() == ""])
	var walk func(parent, prefix string)
	walk = func(parent, prefix string) {
		ch := t.Children(parent)
		virt := virtualLive && t.LiveParent == parent
		for i, n := range ch {
			br, next := "├── ", "│   "
			if i == len(ch)-1 && !virt {
				br, next = "└── ", "    "
			}
			b.WriteString(prefix + br + s.line(n, o) + "\n")
			walk(n, prefix+next)
		}
		if virt {
			b.WriteString(prefix + "└── " + liveLine() + "\n")
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
	fmt.Fprintf(&b, "image %s: %d layers (%d snapshots), %s apparent, %s allocated\n", s.Image, len(s.Tree.Nodes), len(s.Tree.Labels()), Human(sa), Human(sb))
	return b.String(), nil
}
