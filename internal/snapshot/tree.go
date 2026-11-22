// Package snapshot models the tree.json snapshot metadata (pure logic, no IO on images).
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
)

// Node is one snapshot. Parent "" means it sits directly on the base image.
type Node struct {
	Parent  string   `json:"parent"`
	File    string   `json:"file"`
	Created string   `json:"created"`
	Note    string   `json:"note,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// Tree is the tree.json document.
type Tree struct {
	Image      string          `json:"image"`
	BaseSHA256 string          `json:"base_sha256"`
	Current    string          `json:"current"` // "" = base
	Nodes      map[string]Node `json:"nodes"`
}

// New returns an empty tree.
func New(image, baseSHA string) *Tree {
	return &Tree{Image: image, BaseSHA256: baseSHA, Nodes: map[string]Node{}}
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
	if t.Nodes == nil {
		t.Nodes = map[string]Node{}
	}
	return t, t.Validate()
}

// Save writes tree.json atomically.
func (t *Tree) Save(path string) error {
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

// Validate checks parents exist, no cycles, current exists.
func (t *Tree) Validate() error {
	for n, nd := range t.Nodes {
		if nd.Parent != "" {
			if _, ok := t.Nodes[nd.Parent]; !ok {
				return fmt.Errorf("node %q: missing parent %q", n, nd.Parent)
			}
		}
	}
	if c := t.FindCycle(); c != "" {
		return fmt.Errorf("cycle detected at node %q", c)
	}
	if t.Current != "" {
		if _, ok := t.Nodes[t.Current]; !ok {
			return fmt.Errorf("current %q not found", t.Current)
		}
	}
	return nil
}

// FindCycle returns a node on a cycle, or "".
func (t *Tree) FindCycle() string {
	for _, n := range t.sortedNames() {
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

func (t *Tree) sortedNames() []string {
	var s []string
	for n := range t.Nodes {
		s = append(s, n)
	}
	sort.Strings(s)
	return s
}

// Names returns sorted snapshot names.
func (t *Tree) Names() []string { return t.sortedNames() }

// Create adds snapshot name on top of from ("" = current; "base" = base image).
func (t *Tree) Create(name, from string) error {
	if name == "" || name == "base" || name == "current" {
		return fmt.Errorf("invalid snapshot name %q", name)
	}
	if _, ok := t.Nodes[name]; ok {
		return fmt.Errorf("snapshot %q already exists", name)
	}
	switch from {
	case "":
		from = t.Current
	case "base":
		from = ""
	default:
		if _, ok := t.Nodes[from]; !ok {
			return fmt.Errorf("parent snapshot %q not found", from)
		}
	}
	t.Nodes[name] = Node{Parent: from, File: name + ".cow", Created: time.Now().UTC().Format(time.RFC3339)}
	t.Current = name
	return nil
}

// Children returns sorted children of name.
func (t *Tree) Children(name string) []string {
	var c []string
	for _, n := range t.sortedNames() {
		if t.Nodes[n].Parent == name {
			c = append(c, n)
		}
	}
	return c
}

// Delete removes a leaf snapshot. If it was current, current moves to its parent.
func (t *Tree) Delete(name string) error {
	nd, ok := t.Nodes[name]
	if !ok {
		return fmt.Errorf("snapshot %q not found", name)
	}
	if ch := t.Children(name); len(ch) > 0 {
		return fmt.Errorf("snapshot %q has children %v; delete them first", name, ch)
	}
	delete(t.Nodes, name)
	if t.Current == name {
		t.Current = nd.Parent
	}
	return nil
}

// Rollback sets current to name ("base" = base image).
func (t *Tree) Rollback(name string) error {
	if name == "base" {
		t.Current = ""
		return nil
	}
	if _, ok := t.Nodes[name]; !ok {
		return fmt.Errorf("snapshot %q not found", name)
	}
	t.Current = name
	return nil
}

// Chain returns the lineage from base-most ancestor to name.
func (t *Tree) Chain(name string) ([]string, error) {
	var rev []string
	seen := map[string]bool{}
	for cur := name; cur != ""; cur = t.Nodes[cur].Parent {
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

// Depth is the chain length of name (base = 0).
func (t *Tree) Depth(name string) (int, error) {
	c, err := t.Chain(name)
	return len(c), err
}

// FlattenPlan describes merging a chain into a single new layer (no IO).
type FlattenPlan struct {
	Target   string   // snapshot whose state is preserved
	Merge    []string // layers merged, base-most first
	Remove   []string // layer files to remove after merge (all but Target)
	NewDepth int
}

// PlanFlatten plans collapsing name's chain into one layer on the base.
// Fails if intermediate snapshots have other children (they would be orphaned).
func (t *Tree) PlanFlatten(name string) (*FlattenPlan, error) {
	chain, err := t.Chain(name)
	if err != nil {
		return nil, err
	}
	for _, n := range chain[:len(chain)-1] {
		for _, c := range t.Children(n) {
			if c != chain[indexOf(chain, n)+1] {
				return nil, fmt.Errorf("cannot flatten: %q has other child %q", n, c)
			}
		}
	}
	p := &FlattenPlan{Target: name, Merge: chain, NewDepth: 1}
	p.Remove = append(p.Remove, chain[:len(chain)-1]...)
	return p, nil
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
