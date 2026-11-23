package boot

import (
	"time"

	"omloader/internal/snapshot"
)

// mkTree builds base -> n1 -> n2 ... : every label becomes a frozen snapshot node (n1, n2, ...); with live a final
// unlabelled live node is appended.
func mkTree(image string, live bool, labels ...string) *snapshot.Tree {
	t := snapshot.New(image, "")
	parent := ""
	for _, l := range labels {
		id := t.AddNode(parent, false)
		n := t.Nodes[id]
		n.Label = l
		n.Created = time.Now().UTC().Format(time.RFC3339)
		t.Nodes[id] = n
		parent = id
	}
	if live {
		t.Current = t.AddNode(parent, false)
	} else {
		t.LiveParent = parent
	}
	return t
}

// chain3: base -> a(n1) -> b(n2) -> live(n3)
func chain3() *snapshot.Tree { return mkTree("u", true, "a", "b") }
