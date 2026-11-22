package snapshot

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CreateWith is Create plus note and tags.
func (t *Tree) CreateWith(name, from, note string, tags []string) error {
	if err := t.Create(name, from); err != nil {
		return err
	}
	n := t.Nodes[name]
	n.Note = note
	for _, tg := range tags {
		if err := validTag(tg); err != nil {
			delete(t.Nodes, name)
			return err
		}
		n.Tags = addTag(n.Tags, tg)
	}
	t.Nodes[name] = n
	return nil
}

func validTag(s string) error {
	if s == "" || strings.ContainsAny(s, " \t,\n") {
		return fmt.Errorf("invalid tag %q (no spaces or commas)", s)
	}
	return nil
}

func addTag(tags []string, tg string) []string {
	for _, x := range tags {
		if x == tg {
			return tags
		}
	}
	tags = append(tags, tg)
	sort.Strings(tags)
	return tags
}

// Tag adds tags to a node.
func (t *Tree) Tag(name string, tags ...string) error {
	n, ok := t.Nodes[name]
	if !ok {
		return fmt.Errorf("snapshot %q not found", name)
	}
	for _, tg := range tags {
		if err := validTag(tg); err != nil {
			return err
		}
		n.Tags = addTag(n.Tags, tg)
	}
	t.Nodes[name] = n
	return nil
}

// Untag removes tags from a node (missing tags are an error).
func (t *Tree) Untag(name string, tags ...string) error {
	n, ok := t.Nodes[name]
	if !ok {
		return fmt.Errorf("snapshot %q not found", name)
	}
	for _, tg := range tags {
		var out []string
		found := false
		for _, x := range n.Tags {
			if x == tg {
				found = true
			} else {
				out = append(out, x)
			}
		}
		if !found {
			return fmt.Errorf("snapshot %q has no tag %q", name, tg)
		}
		n.Tags = out
	}
	t.Nodes[name] = n
	return nil
}

// SetNote sets the free-text note of a node.
func (t *Tree) SetNote(name, note string) error {
	n, ok := t.Nodes[name]
	if !ok {
		return fmt.Errorf("snapshot %q not found", name)
	}
	n.Note = note
	t.Nodes[name] = n
	return nil
}

// Descendants returns all nodes below name, deepest first (safe deletion order), excluding name.
func (t *Tree) Descendants(name string) []string {
	var out []string
	var walk func(n string)
	walk = func(n string) {
		for _, c := range t.Children(n) {
			walk(c)
			out = append(out, c)
		}
	}
	walk(name)
	return out
}

// Ancestors returns the strict ancestors of name, nearest first.
func (t *Tree) Ancestors(name string) []string {
	var out []string
	for p := t.Nodes[name].Parent; p != ""; p = t.Nodes[p].Parent {
		out = append(out, p)
	}
	return out
}

// Remove deletes nodes from the tree (no checks) and fixes Current (moves to the nearest surviving ancestor).
func (t *Tree) Remove(names ...string) {
	gone := map[string]bool{}
	for _, n := range names {
		gone[n] = true
	}
	for t.Current != "" && gone[t.Current] {
		t.Current = t.Nodes[t.Current].Parent
	}
	for n := range gone {
		delete(t.Nodes, n)
	}
}

// ParseCreated parses a node's created timestamp (zero time when invalid).
func (n Node) ParseCreated() time.Time {
	tm, _ := time.Parse(time.RFC3339, n.Created)
	return tm
}

// PlanDelete returns the nodes to delete (deepest first) for deleting name.
func (t *Tree) PlanDelete(name string, recursive, force bool) ([]string, error) {
	if _, ok := t.Nodes[name]; !ok {
		return nil, fmt.Errorf("snapshot %q not found", name)
	}
	desc := t.Descendants(name)
	if len(desc) > 0 && !recursive {
		return nil, fmt.Errorf("snapshot %q has children %v; use --recursive to delete the subtree", name, t.Children(name))
	}
	all := append(desc, name)
	for _, n := range all {
		if n == t.Current && !force {
			return nil, fmt.Errorf("snapshot %q is the current snapshot (or contains it); use --force", name)
		}
	}
	return all, nil
}

// PlanGC selects ephemeral tmp-* leaves older than olderThan (cascading: a tmp node whose tmp children are
// all selected is selected too). The current snapshot is never selected.
func (t *Tree) PlanGC(now time.Time, olderThan time.Duration) []string {
	sel := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, n := range t.Names() {
			nd := t.Nodes[n]
			if sel[n] || !strings.HasPrefix(n, "tmp-") || n == t.Current {
				continue
			}
			if c := nd.ParseCreated(); c.IsZero() || now.Sub(c) < olderThan {
				continue
			}
			ok := true
			for _, ch := range t.Children(n) {
				if !sel[ch] {
					ok = false
				}
			}
			if ok {
				sel[n] = true
				changed = true
			}
		}
	}
	return t.deepestFirst(sel)
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

// PlanPrune implements retention: keep the keepLast newest leaves, tagged nodes (if keepTagged) and
// the current snapshot, plus all their ancestors; everything else is deleted (deepest first).
func (t *Tree) PlanPrune(keepLast int, keepTagged bool) []string {
	keep := map[string]bool{}
	var leaves []string
	for _, n := range t.Names() {
		if len(t.Children(n)) == 0 {
			leaves = append(leaves, n)
		}
	}
	sort.SliceStable(leaves, func(i, j int) bool {
		ci, cj := t.Nodes[leaves[i]].ParseCreated(), t.Nodes[leaves[j]].ParseCreated()
		if !ci.Equal(cj) {
			return ci.After(cj)
		}
		return leaves[i] < leaves[j]
	})
	for i, n := range leaves {
		if i < keepLast {
			keep[n] = true
		}
	}
	if keepTagged {
		for n, nd := range t.Nodes {
			if len(nd.Tags) > 0 {
				keep[n] = true
			}
		}
	}
	if t.Current != "" {
		keep[t.Current] = true
	}
	for n := range keep {
		for _, a := range t.Ancestors(n) {
			keep[a] = true
		}
	}
	del := map[string]bool{}
	for n := range t.Nodes {
		if !keep[n] {
			del[n] = true
		}
	}
	return t.deepestFirst(del)
}

// MergePlan describes merging a leaf into its parent.
type MergePlan struct{ Node, Into string }

// PlanMerge validates a leaf-into-parent merge (safety check only; the operation is unsupported in v1).
func (t *Tree) PlanMerge(name string) (*MergePlan, error) {
	nd, ok := t.Nodes[name]
	if !ok {
		return nil, fmt.Errorf("snapshot %q not found", name)
	}
	if ch := t.Children(name); len(ch) > 0 {
		return nil, fmt.Errorf("cannot merge %q: it has children %v (only leaves can be merged)", name, ch)
	}
	if nd.Parent == "" {
		return nil, fmt.Errorf("cannot merge %q: its parent is the read-only base", name)
	}
	if ch := t.Children(nd.Parent); len(ch) > 1 {
		return nil, fmt.Errorf("cannot merge %q into %q: parent has other children %v", name, nd.Parent, ch)
	}
	return &MergePlan{Node: name, Into: nd.Parent}, nil
}
