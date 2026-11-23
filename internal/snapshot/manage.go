package snapshot

import (
	"fmt"
	"sort"
	"strings"
)

// ValidLabel checks a user snapshot label.
func ValidLabel(l string) error {
	if l == "" || !LabelRe.MatchString(l) {
		return fmt.Errorf("invalid snapshot label %q (letters, digits, _ . + - only)", l)
	}
	if l == Base || l == Current {
		return fmt.Errorf("label %q is reserved", l)
	}
	return nil
}

// Freeze labels the live node and makes the next live node hang below it. The live node must exist.
func (t *Tree) Freeze(label, note string, tags []string) (string, error) {
	if err := ValidLabel(label); err != nil {
		return "", err
	}
	if _, dup := t.ByLabel(label); dup {
		return "", fmt.Errorf("snapshot %q already exists", label)
	}
	if t.Current == "" {
		return "", fmt.Errorf("nothing to snapshot: the live state has no changes since %s", t.Display(t.LiveParent))
	}
	n := t.Nodes[t.Current]
	for _, tg := range tags {
		if err := validTag(tg); err != nil {
			return "", err
		}
		n.Tags = addTag(n.Tags, tg)
	}
	n.Label, n.Note = label, note
	id := t.Current
	t.Nodes[id] = n
	t.SetHead(id)
	return id, nil
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

func (t *Tree) snapNode(label string) (string, Node, error) {
	id, err := t.ResolveSnapshot(label, false)
	if err != nil {
		return "", Node{}, err
	}
	return id, t.Nodes[id], nil
}

// Tag adds tags to a snapshot (by label).
func (t *Tree) Tag(label string, tags ...string) error {
	id, n, err := t.snapNode(label)
	if err != nil {
		return err
	}
	for _, tg := range tags {
		if err := validTag(tg); err != nil {
			return err
		}
		n.Tags = addTag(n.Tags, tg)
	}
	t.Nodes[id] = n
	return nil
}

// Untag removes tags from a snapshot (missing tags are an error).
func (t *Tree) Untag(label string, tags ...string) error {
	id, n, err := t.snapNode(label)
	if err != nil {
		return err
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
			return fmt.Errorf("snapshot %q has no tag %q", label, tg)
		}
		n.Tags = out
	}
	t.Nodes[id] = n
	return nil
}

// SetNote sets the free-text note of a snapshot.
func (t *Tree) SetNote(label, note string) error {
	id, n, err := t.snapNode(label)
	if err != nil {
		return err
	}
	n.Note = note
	t.Nodes[id] = n
	return nil
}

// Keep returns the ids retention keeps: the newest keepLast tip snapshots (snapshots without snapshot descendants),
// tagged snapshots (if keepTagged), the live node and the head, plus all ancestors of those. Ephemeral nodes are
// never "kept" (gc removes them).
func (t *Tree) Keep(keepLast int, keepTagged bool) map[string]bool {
	keep := map[string]bool{}
	hasSnapBelow := func(id string) bool {
		for _, d := range t.Descendants(id) {
			if t.Nodes[d].Label != "" {
				return true
			}
		}
		return false
	}
	var tips []string
	for _, n := range t.Names() {
		if t.Nodes[n].Label != "" && !hasSnapBelow(n) {
			tips = append(tips, n)
		}
	}
	sort.SliceStable(tips, func(i, j int) bool {
		ci, cj := t.Nodes[tips[i]].ParseCreated(), t.Nodes[tips[j]].ParseCreated()
		if !ci.Equal(cj) {
			return ci.After(cj)
		}
		return len(tips[i]) > len(tips[j]) || len(tips[i]) == len(tips[j]) && tips[i] > tips[j]
	})
	for i, n := range tips {
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
	if h := t.Head(); h != "" {
		keep[h] = true
	}
	for n := range keep {
		for _, a := range t.Ancestors(n) {
			keep[a] = true
		}
	}
	return keep
}

// PlanPrune returns the non-ephemeral nodes retention deletes (deepest first).
func (t *Tree) PlanPrune(keepLast int, keepTagged bool) []string {
	keep := t.Keep(keepLast, keepTagged)
	del := map[string]bool{}
	for n, nd := range t.Nodes {
		if !keep[n] && !nd.Ephemeral {
			del[n] = true
		}
	}
	return t.deepestFirst(del)
}
