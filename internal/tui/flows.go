package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// bootNow ends the TUI with a boot request; there is no confirmation (the key bar says what Enter does).
// snapshot is "current", a snapshot label or "base"; ephemeral boots discard their changes.
func (m *Model) bootNow(image, snapshot string, eph bool) {
	if lb, ok := m.Svc.(LastBooter); ok && m.prefs().RememberLast {
		lb.RememberBoot(image, snapshot, eph)
	}
	m.res, m.done = Result{Kind: ResBoot, Boot: BootRequest{Image: image, Snapshot: snapshot, Ephemeral: eph}}, true
}

// bootBB boots like bootNow but forces the busybox console shell for this one boot (omloader boot --shell busybox).
func (m *Model) bootBB(image, snapshot string) {
	m.bootNow(image, snapshot, false)
	m.res.Boot.Shell = "busybox"
}

// dryRun streams `omloader boot --dry-run` for the given target.
func (m *Model) dryRun(image, snapshot string, eph bool) {
	m.startOp("boot preview (dry-run): "+image, func(ctx context.Context, w io.Writer) error {
		return m.Svc.BootPreview(ctx, image, snapshot, eph, w)
	}, nil)
}

// liveChanged: the image has a live node (booted since the last snapshot/revert), i.e. a revert would discard something.
func (m *Model) liveChanged() bool { return m.tree != nil && m.tree.Current != "" }

// headLabel is the label of the snapshot the live state sits on ("base" when none).
func (m *Model) headLabel() string {
	if m.tree == nil || m.tree.Head == "" {
		return "base"
	}
	for _, n := range m.tree.Nodes {
		if n.Name == m.tree.Head && n.Label != "" {
			return n.Label
		}
	}
	return "base"
}

// liveWritten is the size written to the live layer since its parent.
func (m *Model) liveWritten() int64 {
	if m.tree == nil {
		return 0
	}
	for _, n := range m.tree.Nodes {
		if n.Live {
			return n.Alloc
		}
	}
	return 0
}

// askRevert runs do() (revert, optionally followed by a boot) after the ONE confirmation that reverting needs. Without a live
// state there is nothing to lose and do() runs at once. In the dialog: Enter/y reverts (the live state is lost), K first
// asks for a snapshot label (default snap-YYYYMMDD-HHMM) and saves the live state, Esc/n cancels.
func (m *Model) askRevert(target string, do func()) {
	if !m.liveChanged() {
		do()
		return
	}
	lines := []string{
		fmt.Sprintf(L.RevertLose, target, human(m.liveWritten()), m.headLabel()),
		"",
		L.RevertTip,
	}
	d := m.confirm(L.RevertTitle+" -> "+target, true, lines, do)
	d.Buttons = L.RevertButtons
	saveFirst := func() {
		m.dlg = nil
		defer func() { m.dlg.Replace = true }()
		m.form(L.CreateTitle, []string{L.RevertSaveHelp}, []string{L.FieldName}, []string{defaultSnapLabel(m.Now())}, func(v []string) {
			name := strings.TrimSpace(v[0])
			if name == "" {
				m.message(L.Error, true, "name required")
				return
			}
			if err := m.Svc.Create(m.image, name, "", nil); err != nil {
				m.reloadTree()
				m.fail(err)
				return
			}
			m.reloadTree()
			do()
		})
	}
	d.Extra = map[rune]func(){'k': saveFirst, 'K': saveFirst}
}

// revertThen reverts the live state to target and calls next on success.
func (m *Model) revertThen(target string, next func()) {
	if err := m.Svc.Revert(m.image, target, ""); err != nil {
		m.reloadTree()
		m.fail(err)
		return
	}
	next()
}

// enterRow is Enter in the snapshot tree: current -> boot it at once; the snapshot the live state sits on -> boot current;
// another frozen snapshot or base -> (one confirmation when the live state has changes) revert to it and boot.
func (m *Model) enterRow(row TreeRow) {
	if m.tree == nil {
		return
	}
	switch {
	case !row.IsBase && (row.Node.Live || row.Node.Ephemeral):
		m.bootNow(m.image, "current", false)
	case !row.IsBase && row.Node.Name == m.tree.Head, row.IsBase && m.tree.Head == "" && !m.liveChanged():
		m.bootNow(m.image, "current", false)
	default:
		target := "base"
		if !row.IsBase {
			if target = snapLabel(row); target == "" {
				m.bootNow(m.image, "current", false)
				return
			}
		}
		m.askRevert(target, func() { m.revertThen(target, func() { m.bootNow(m.image, "current", false) }) })
	}
}

// revertKey is `r`: revert without booting (the live row resets to the snapshot it sits on); same dialog as Enter.
func (m *Model) revertKey(row TreeRow) {
	target := "base"
	switch {
	case row.IsBase:
	case row.Node.Live || row.Node.Ephemeral:
		target = m.headLabel()
	default:
		if target = snapLabel(row); target == "" {
			m.message(L.Info, false, L.RevertSelect)
			return
		}
	}
	m.askRevert(target, func() {
		m.revertThen(target, func() {
			m.reloadTree()
			m.sayf(L.NoticeReverted, target)
		})
	})
}

// ephemeralKey is `e`: boot the selected state on a throwaway layer (nothing is kept).
func (m *Model) ephemeralKey(row TreeRow) {
	switch {
	case row.IsBase:
		m.bootNow(m.image, "base", true)
	case row.Node.Live || row.Node.Ephemeral || snapLabel(row) == "":
		m.bootNow(m.image, "current", true)
	default:
		m.bootNow(m.image, snapLabel(row), true)
	}
}

// snapshotKey is `s`: one prompt, the label (default snap-YYYYMMDD-HHMM; Enter accepts).
func (m *Model) snapshotKey() {
	defer func() { m.dlg.Replace = true }()
	m.form(L.CreateTitle, []string{L.CreateHelp}, []string{L.FieldName}, []string{defaultSnapLabel(m.Now())}, func(v []string) {
		name := strings.TrimSpace(v[0])
		if name == "" {
			m.message(L.Error, true, "name required")
			return
		}
		if err := m.Svc.Create(m.image, name, "", nil); err != nil {
			m.reloadTree()
			m.fail(err)
			return
		}
		m.reloadTree()
		m.sayf("created snapshot %s", name)
	})
}

// imageMenu is Space/m in the images list.
func (m *Model) imageMenu() {
	if len(m.images) == 0 {
		return
	}
	img := m.images[m.isel].Name
	items := []string{L.MenuBootCurrent, L.MenuBootThrow, L.MenuOpenTree, L.MenuPreview, L.MenuBootBB, L.Cancel}
	m.menu(img, items, func(i int) {
		switch i {
		case 0:
			m.bootNow(img, "current", false)
		case 1:
			m.bootNow(img, "current", true)
		case 2:
			m.OpenImage(img)
		case 3:
			m.dryRun(img, "current", false)
		case 4:
			m.bootBB(img, "current")
		}
	})
}

// rowMenu is Space/m on a tree row: everything that is not on the one-key path.
func (m *Model) rowMenu(row TreeRow) {
	label := snapLabel(row)
	type act struct {
		name string
		do   func()
	}
	var acts []act
	add := func(n string, f func()) { acts = append(acts, act{n, f}) }
	add(L.MenuBootCurrent, func() { m.bootNow(m.image, "current", false) })
	switch {
	case row.IsBase:
		add(L.MenuBootSelRO, func() { m.bootNow(m.image, "base", true) })
	case label != "":
		add(fmt.Sprintf(L.MenuBootSelROFmt, label), func() { m.bootNow(m.image, label, true) })
	}
	add(L.MenuBootThrow, func() { m.bootNow(m.image, "current", true) })
	add(L.MenuPreview, func() { m.dryRun(m.image, "current", false) })
	if row.IsBase || label != "" {
		add(L.MenuRevert, func() { m.revertKey(row) })
	}
	add(L.MenuSnapshot, m.snapshotKey)
	if label != "" {
		add(L.MenuTag, func() { m.tagForm(row) })
		add(L.MenuNote, func() { m.noteForm(row) })
		add(L.MenuDelete, func() { m.deleteFlow(row) })
		add(L.MenuFlatten, func() { m.flattenForm(row) })
		add(L.MenuExport, func() { m.exportForm(row) })
	}
	add(L.MenuPrune, m.pruneMenu)
	add(L.MenuVerify, m.verifyMenu)
	add(L.MenuBootBB, func() { m.bootBB(m.image, "current") })
	add(L.MenuDetails, func() { m.message(L.MenuDetails, false, m.detailsText(row)...) })
	var items []string
	for _, a := range acts {
		items = append(items, a.name)
	}
	m.menu(L.MenuTitle+" "+m.image, items, func(i int) { acts[i].do() })
}

func (m *Model) detailsText(row TreeRow) []string {
	var out []string
	for _, s := range m.nodeInfo() {
		out = append(out, s.s)
	}
	return append([]string{m.image + "  type=" + m.tree.Type + "  mode=" + m.tree.Mode}, out...)
}

// selectLast selects the image (and, in the tree, the snapshot) that was booted last.
func (m *Model) selectLast() {
	lb, ok := m.Svc.(LastBooter)
	if !ok {
		return
	}
	img, _ := lb.LastBoot()
	for i, im := range m.images {
		if im.Name == img {
			m.isel = i
			m.itop = keepVisible(i, 0, m.bodyRows()-1)
			return
		}
	}
}
