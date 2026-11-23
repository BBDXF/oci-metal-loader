// Package snapshot models the tree.json (version 2) snapshot metadata (pure logic, no IO on images).
//
// Model ("VM snapshots"): every image has a tree rooted at the read-only base image. Nodes are keyed by a stable id
// (n1, n2, ...; never reused) that names their on-disk layer. A node with a Label is a snapshot (frozen); the single
// writable live state is Current (normally unlabelled), a leaf below some snapshot or below base. While no live
// node exists (fresh image, after create/revert) LiveParent says where it will be created.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"time"
)

// Version is the tree.json format version this code reads and writes.
const Version = 2

// Reserved labels.
const (
	Base    = "base"
	Current = "current"
)

var idRe = regexp.MustCompile(`^n[0-9]+$`)

// LabelRe is the allowed label syntax.
var LabelRe = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)

// Node is one layer. Parent "" means it sits directly on the base image.
type Node struct {
	Parent    string   `json:"parent"`
	Label     string   `json:"label,omitempty"`
	File      string   `json:"file"`
	Created   string   `json:"created"`
	Note      string   `json:"note,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Ephemeral bool     `json:"ephemeral,omitempty"`
}

// Tree is the tree.json document.
type Tree struct {
	Version    int             `json:"version"`
	Image      string          `json:"image"`
	BaseSHA256 string          `json:"base_sha256"`
	Current    string          `json:"current"`               // id of the live node, "" = none yet
	LiveParent string          `json:"live_parent,omitempty"` // parent of the live node to be created (only when Current == "")
	NextID     int             `json:"next_id"`
	Nodes      map[string]Node `json:"nodes"`
}

// New returns an empty tree.
func New(image, baseSHA string) *Tree {
	return &Tree{Version: Version, Image: image, BaseSHA256: baseSHA, NextID: 1, Nodes: map[string]Node{}}
}

// Load reads tree.json.
func Load(path string) (*Tree, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	t := &Tree{}
	if err := json.Unmarshal(b, t); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if t.Version != Version {
		return nil, fmt.Errorf("%s: snapshot tree format version %d is not supported (this omloader uses version %d): "+
			"delete %s and the layer files next to it, or recreate the image's snapshots", path, t.Version, Version, path)
	}
	if t.Nodes == nil {
		t.Nodes = map[string]Node{}
	}
	return t, t.Validate()
}

// Save writes tree.json atomically.
func (t *Tree) Save(path string) error {
	t.Version = Version
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Clone returns a deep copy.
func (t *Tree) Clone() *Tree {
	c := *t
	c.Nodes = make(map[string]Node, len(t.Nodes))
	for k, v := range t.Nodes {
		v.Tags = append([]string(nil), v.Tags...)
		c.Nodes[k] = v
	}
	return &c
}

// Validate checks ids, parents, cycles, labels and the live node.
func (t *Tree) Validate() error {
	labels := map[string]string{}
	for _, n := range t.Names() {
		nd := t.Nodes[n]
		if !idRe.MatchString(n) {
			return fmt.Errorf("node id %q is invalid (want n<number>)", n)
		}
		if nd.Parent != "" {
			if _, ok := t.Nodes[nd.Parent]; !ok {
				return fmt.Errorf("node %q: missing parent %q", n, nd.Parent)
			}
		}
		if nd.Label != "" {
			if nd.Label == Base || nd.Label == Current || !LabelRe.MatchString(nd.Label) {
				return fmt.Errorf("node %q: invalid label %q", n, nd.Label)
			}
			if o, dup := labels[nd.Label]; dup {
				return fmt.Errorf("label %q used by both %s and %s", nd.Label, o, n)
			}
			labels[nd.Label] = n
		}
	}
	if c := t.FindCycle(); c != "" {
		return fmt.Errorf("cycle detected at node %q", c)
	}
	if t.Current != "" {
		nd, ok := t.Nodes[t.Current]
		if !ok {
			return fmt.Errorf("current %q not found", t.Current)
		}
		if nd.Label != "" || nd.Ephemeral {
			return fmt.Errorf("current node %q must be an unlabelled, non-ephemeral live node", t.Current)
		}
		if t.LiveParent != "" {
			return fmt.Errorf("live_parent must be empty while a live node exists")
		}
	}
	if t.LiveParent != "" {
		if _, ok := t.Nodes[t.LiveParent]; !ok {
			return fmt.Errorf("live_parent %q not found", t.LiveParent)
		}
	}
	return nil
}

// FindCycle returns a node on a cycle, or "".
func (t *Tree) FindCycle() string {
	for _, n := range t.Names() {
		seen := map[string]bool{}
		for cur := n; cur != ""; cur = t.Nodes[cur].Parent {
			if seen[cur] {
				return cur
			}
			seen[cur] = true
			if _, ok := t.Nodes[cur]; !ok {
				break
			}
		}
	}
	return ""
}

// Names returns the node ids in numeric order (n2 before n10).
func (t *Tree) Names() []string {
	var s []string
	for n := range t.Nodes {
		s = append(s, n)
	}
	sort.Slice(s, func(i, j int) bool {
		if len(s[i]) != len(s[j]) {
			return len(s[i]) < len(s[j])
		}
		return s[i] < s[j]
	})
	return s
}

// Labels returns the snapshot labels, sorted.
func (t *Tree) Labels() []string {
	var s []string
	for _, nd := range t.Nodes {
		if nd.Label != "" {
			s = append(s, nd.Label)
		}
	}
	sort.Strings(s)
	return s
}

// ByLabel returns the id carrying label.
func (t *Tree) ByLabel(label string) (string, bool) {
	for id, nd := range t.Nodes {
		if nd.Label != "" && nd.Label == label {
			return id, true
		}
	}
	return "", false
}

// Resolve maps a user reference to a node id: "base" -> "" (base image), a label -> its id, "current" -> the live node.
func (t *Tree) Resolve(ref string) (id string, err error) {
	switch ref {
	case Base:
		return "", nil
	case Current:
		if t.Current == "" {
			return "", fmt.Errorf("there is no live state yet (nothing has changed since %s)", t.Display(t.LiveParent))
		}
		return t.Current, nil
	}
	if id, ok := t.ByLabel(ref); ok {
		return id, nil
	}
	return "", fmt.Errorf("snapshot %q not found", ref)
}

// ResolveSnapshot is Resolve restricted to labelled snapshots (and base when allowBase).
func (t *Tree) ResolveSnapshot(ref string, allowBase bool) (string, error) {
	if ref == Base {
		if !allowBase {
			return "", fmt.Errorf("the base image is read-only and cannot be used here")
		}
		return "", nil
	}
	if ref == Current {
		return "", fmt.Errorf("%q is the live state, not a snapshot", ref)
	}
	return t.Resolve(ref)
}

// Display is the user-facing name of a node id ("" = "base").
func (t *Tree) Display(id string) string {
	if id == "" {
		return Base
	}
	if nd, ok := t.Nodes[id]; ok {
		if nd.Label != "" {
			return nd.Label
		}
		if id == t.Current {
			return Current
		}
	}
	return id
}

// Head is the parent of the live state: the snapshot (or "" = base) it sits on.
func (t *Tree) Head() string {
	if t.Current != "" {
		return t.Nodes[t.Current].Parent
	}
	return t.LiveParent
}

// AddNode adds an empty node below parent and returns its id.
func (t *Tree) AddNode(parent string, ephemeral bool) string {
	for {
		id := fmt.Sprintf("n%d", t.NextID)
		t.NextID++
		if _, used := t.Nodes[id]; used {
			continue
		}
		t.Nodes[id] = Node{Parent: parent, File: id + ".cow", Created: time.Now().UTC().Format(time.RFC3339), Ephemeral: ephemeral}
		return id
	}
}

// EnsureLive returns the live node, creating it below the head when there is none.
func (t *Tree) EnsureLive() (id string, created bool) {
	if t.Current != "" {
		return t.Current, false
	}
	t.Current = t.AddNode(t.LiveParent, false)
	t.LiveParent = ""
	return t.Current, true
}

// SetHead discards the notion of a live node and lets the next live node be created below parent (an id, "" = base).
// The caller is responsible for the old live node's data.
func (t *Tree) SetHead(parent string) {
	t.Current = ""
	t.LiveParent = parent
}

// Children returns the children ids of id (all, ephemeral included), sorted.
func (t *Tree) Children(id string) []string {
	var c []string
	for _, n := range t.Names() {
		if t.Nodes[n].Parent == id {
			c = append(c, n)
		}
	}
	return c
}

// PersistentChildren are the children that are not ephemeral.
func (t *Tree) PersistentChildren(id string) []string {
	var c []string
	for _, n := range t.Children(id) {
		if !t.Nodes[n].Ephemeral {
			c = append(c, n)
		}
	}
	return c
}

// Ephemerals returns the ids of all ephemeral nodes, deepest first.
func (t *Tree) Ephemerals() []string {
	sel := map[string]bool{}
	for n, nd := range t.Nodes {
		if nd.Ephemeral {
			sel[n] = true
		}
	}
	return t.deepestFirst(sel)
}

// Remove deletes nodes from the tree (no checks). If the live node goes, the head becomes the live node's parent.
func (t *Tree) Remove(ids ...string) {
	for _, n := range ids {
		if n == t.Current {
			t.LiveParent = t.Nodes[n].Parent
			t.Current = ""
		}
	}
	for _, n := range ids {
		delete(t.Nodes, n)
	}
	if _, ok := t.Nodes[t.LiveParent]; t.LiveParent != "" && !ok {
		t.LiveParent = ""
	}
}

// Chain returns the lineage from the base-most ancestor to id.
func (t *Tree) Chain(id string) ([]string, error) {
	var rev []string
	seen := map[string]bool{}
	for cur := id; cur != ""; cur = t.Nodes[cur].Parent {
		if seen[cur] {
			return nil, errors.New("cycle in chain at " + cur)
		}
		if _, ok := t.Nodes[cur]; !ok {
			return nil, fmt.Errorf("snapshot %q not found", cur)
		}
		seen[cur] = true
		rev = append(rev, cur)
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, nil
}

// Depth is the chain length of id (base = 0).
func (t *Tree) Depth(id string) (int, error) {
	c, err := t.Chain(id)
	return len(c), err
}

// Ancestors returns the strict ancestors of id, nearest first.
func (t *Tree) Ancestors(id string) []string {
	var out []string
	for p := t.Nodes[id].Parent; p != ""; p = t.Nodes[p].Parent {
		out = append(out, p)
	}
	return out
}

// Descendants returns all nodes below id, deepest first (safe deletion order), excluding id.
func (t *Tree) Descendants(id string) []string {
	var out []string
	var walk func(n string)
	walk = func(n string) {
		for _, c := range t.Children(n) {
			walk(c)
			out = append(out, c)
		}
	}
	walk(id)
	return out
}

// ParseCreated parses a node's created timestamp (zero time when invalid).
func (n Node) ParseCreated() time.Time {
	tm, _ := time.Parse(time.RFC3339, n.Created)
	return tm
}

func (t *Tree) deepestFirst(sel map[string]bool) []string {
	var out []string
	for n := range sel {
		out = append(out, n)
	}
	depth := func(n string) int { return len(t.Ancestors(n)) }
	sort.Slice(out, func(i, j int) bool {
		if di, dj := depth(out[i]), depth(out[j]); di != dj {
			return di > dj
		}
		return out[i] < out[j]
	})
	return out
}
