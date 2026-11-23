package tui

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Update handles one key.
func (m *Model) Update(k Key) {
	if m.done {
		return
	}
	if m.help {
		m.help = false
		return
	}
	if m.dlg != nil {
		m.dialogKey(k)
		return
	}
	if m.op != nil {
		m.opKey(k)
		return
	}
	act := m.Km.Action(k)
	if act == "cancel" {
		m.say("Ctrl+C cancels running operations; nothing is running. Use q/Esc to leave the TUI.")
		return
	}
	if act == "help" {
		m.help = true
		return
	}
	if act == "redraw" {
		return
	}
	// global shortcuts
	if k.Name == "" {
		switch k.Rune {
		case '1', '2', '3', '4', '5':
			m.gotoTab(Screen(k.Rune - '1'))
			return
		case 'P':
			m.powerMenu()
			return
		case 's':
			m.shellConfirm()
			return
		}
	}
	if act == "next" || act == "prev" {
		t := int(m.Screen)
		if m.Screen == ScrDetail {
			t = 0
		}
		d := 1
		if act == "prev" {
			d = 4
		}
		m.gotoTab(Screen((t + d) % 5))
		return
	}
	switch m.Screen {
	case ScrImages:
		m.imagesKey(k, act)
	case ScrDetail:
		m.detailKey(k, act)
	case ScrPull:
		m.pullKey(k, act)
	case ScrStorage:
		m.storageKey(k, act)
	case ScrNet:
		m.netKey(k, act)
	case ScrSettings:
		m.settingsKey(k, act)
	}
}

func (m *Model) gotoTab(s Screen) {
	m.Screen = s
	switch s {
	case ScrImages:
		m.RefreshImages()
	case ScrPull:
		m.loadSources()
	case ScrStorage:
		m.loadStore()
	case ScrNet:
		m.netLines = nil
		m.runNetStatus(false)
	case ScrSettings:
		lines, err := m.Svc.Settings()
		if err != nil {
			m.fail(err)
		}
		m.settings, m.ssc = lines, 0
	}
}

func (m *Model) shellConfirm() {
	m.confirm(L.QuitTitle, false, []string{L.QuitText}, func() { m.res, m.done = Result{Kind: ResShell}, true })
}

func (m *Model) powerMenu() {
	if m.Busy() {
		m.say(L.PowerBusy)
		return
	}
	m.menu(L.PowerTitle, []string{L.PowerReboot, L.PowerOff, L.Cancel}, func(i int) {
		kinds := []ResultKind{ResReboot, ResPowerOff}
		if i > 1 {
			return
		}
		name := []string{L.PowerReboot, L.PowerOff}[i]
		m.confirm(name, true, []string{name + "?"}, func() { m.res, m.done = Result{Kind: kinds[i]}, true })
	})
}

// moveSel applies a navigation action to a list selection.
func moveSel(act string, sel, n, page int) int {
	switch act {
	case "up":
		sel--
	case "down":
		sel++
	case "pgup":
		sel -= page
	case "pgdn":
		sel += page
	case "home":
		sel = 0
	case "end":
		sel = n - 1
	}
	if sel >= n {
		sel = n - 1
	}
	if sel < 0 {
		sel = 0
	}
	return sel
}

func keepVisible(sel, top, vis int) int {
	if sel < top {
		top = sel
	}
	if sel >= top+vis {
		top = sel - vis + 1
	}
	if top < 0 {
		top = 0
	}
	return top
}

func (m *Model) bodyRows() int { return m.H - 4 }

// ---- images ----

// RefreshImages reloads the images list.
func (m *Model) RefreshImages() {
	imgs, err := m.Svc.Images()
	if err != nil {
		m.fail(err)
		return
	}
	m.images = imgs
	if m.isel >= len(imgs) {
		m.isel = len(imgs) - 1
	}
	if m.isel < 0 {
		m.isel = 0
	}
}

func (m *Model) imagesKey(k Key, act string) {
	m.isel = moveSel(act, m.isel, len(m.images), m.bodyRows())
	m.itop = keepVisible(m.isel, m.itop, m.bodyRows()-1)
	switch {
	case act == "select" || k.Rune == 'o':
		if len(m.images) > 0 {
			m.OpenImage(m.images[m.isel].Name)
		}
	case k.Rune == 'r':
		m.RefreshImages()
		m.say(L.Refreshed)
	case act == "back" || k.Rune == 'q':
		m.shellConfirm()
	}
}

// OpenImage loads the detail screen.
func (m *Model) OpenImage(name string) {
	t, err := m.Svc.Tree(name)
	if err != nil {
		m.fail(err)
		return
	}
	m.image, m.Screen = name, ScrDetail
	m.setTree(t)
}

func (m *Model) setTree(t *TreeInfo) {
	m.tree = t
	m.rows = BuildRows(t, m.Th)
	if m.dsel >= len(m.rows) {
		m.dsel = len(m.rows) - 1
	}
	if m.dsel < 0 {
		m.dsel = 0
	}
}

func (m *Model) reloadTree() {
	t, err := m.Svc.Tree(m.image)
	if err != nil {
		m.fail(err)
		return
	}
	m.setTree(t)
	m.RefreshImages()
}

// BuildRows lays the snapshot tree out depth first ("base" is the root).
func BuildRows(t *TreeInfo, th Theme) []TreeRow {
	g := th.g()
	kids := map[string][]string{}
	nodes := map[string]*NodeInfo{}
	for i := range t.Nodes {
		n := &t.Nodes[i]
		nodes[n.Name] = n
		kids[n.Parent] = append(kids[n.Parent], n.Name)
	}
	for _, v := range kids {
		sort.Strings(v)
	}
	rows := []TreeRow{{Name: "base", IsBase: true, Kids: len(kids[""])}}
	var walk func(parent, prefix string)
	walk = func(parent, prefix string) {
		for i, n := range kids[parent] {
			br, next := g.branch, g.pipe
			if i == len(kids[parent])-1 {
				br, next = g.last, g.blank
			}
			rows = append(rows, TreeRow{Name: n, Prefix: prefix + br, Node: nodes[n], Kids: len(kids[n])})
			walk(n, prefix+next)
		}
	}
	walk("", "")
	return rows
}

// selNode returns the selected node name ("" = base).
func (m *Model) selNode() string {
	if m.dsel < 0 || m.dsel >= len(m.rows) || m.rows[m.dsel].IsBase {
		return ""
	}
	return m.rows[m.dsel].Name
}

func (m *Model) selRow() TreeRow {
	if m.dsel < 0 || m.dsel >= len(m.rows) {
		return TreeRow{IsBase: true, Name: "base"}
	}
	return m.rows[m.dsel]
}

func nodeLabel(n string) string {
	if n == "" {
		return L.BaseNode
	}
	return n
}

func (m *Model) detailKey(k Key, act string) {
	m.dsel = moveSel(act, m.dsel, len(m.rows), m.bodyRows())
	m.dtop = keepVisible(m.dsel, m.dtop, m.bodyRows()-5)
	row := m.selRow()
	switch {
	case act == "back" || k.Rune == 'q':
		m.Screen = ScrImages
		m.RefreshImages()
	case act == "select" || k.Rune == 'b':
		m.bootMenu()
	case k.Rune == 'r':
		m.rollbackMenu(row)
	case k.Rune == 'c':
		m.createForm(row)
	case k.Rune == 'd':
		m.deleteFlow(row)
	case k.Rune == 't':
		m.tagForm(row)
	case k.Rune == 'n':
		m.noteForm(row)
	case k.Rune == 'p':
		m.pruneMenu()
	case k.Rune == 'f':
		m.flattenForm(row)
	case k.Rune == 'e':
		m.exportForm(row)
	case k.Rune == 'v':
		m.verifyMenu()
	case k.Rune == 'R':
		m.reloadTree()
		m.say(L.Refreshed)
	}
}

// ---- boot ----

func (m *Model) bootMenu() {
	cur := nodeLabel(m.tree.Current)
	items := []string{L.BootCurrent + " (" + cur + ")"}
	targets := []string{"current"}
	if n := m.selNode(); n != "" && n != m.tree.Current {
		items = append(items, L.BootSelected+" ("+n+")")
		targets = append(targets, n)
	}
	items = append(items, L.BootNew, L.BootBase)
	targets = append(targets, "new:", "base")
	m.menu(L.BootTitle+" "+m.image, items, func(i int) {
		switch targets[i] {
		case "new:":
			m.form(L.BootTitle, []string{L.BootNewPrompt}, []string{L.FieldName}, []string{""}, func(v []string) {
				if strings.TrimSpace(v[0]) == "" {
					m.message(L.Error, true, "name required")
					return
				}
				m.bootConfirm("new:" + strings.TrimSpace(v[0]))
			})
		default:
			m.bootConfirm(targets[i])
		}
	})
}

func (m *Model) nodeHasKids(n string) bool {
	for _, r := range m.rows {
		if !r.IsBase && r.Name == n {
			return r.Kids > 0
		}
	}
	return false
}

func (m *Model) bootConfirm(target string) {
	if target != "current" && target != "base" && !strings.HasPrefix(target, "new:") && m.nodeHasKids(target) {
		m.confirm(L.BootForceTitle, true, []string{L.BootForceText}, func() { m.bootConfirm2(target, true) })
		return
	}
	if target == "current" && m.tree.Current != "" && m.nodeHasKids(m.tree.Current) {
		m.confirm(L.BootForceTitle, true, []string{L.BootForceText}, func() { m.bootConfirm2(target, true) })
		return
	}
	m.bootConfirm2(target, false)
}

func (m *Model) bootConfirm2(target string, force bool) {
	args := m.Svc.BootArgs(m.image, target, force)
	lines := []string{strings.Join(args, " ")}
	if m.tree.Type == "vm" {
		lines = append(lines, "VM image: the dm-snapshot stack is assembled and the kernel is started with kexec (no return).")
	} else {
		lines = append(lines, "Container image: root is mounted (squashfs + overlayfs), then the loader runs systemctl switch-root.")
	}
	lines = append(lines, "", L.BootPreview)
	image := m.image
	d := m.confirm(L.BootConfirm, force, lines, func() {
		m.res, m.done = Result{Kind: ResBoot, Boot: BootRequest{Image: image, Snapshot: target, Force: force}}, true
	})
	d.Extra = map[rune]func(){'d': func() {
		m.opResume = d
		m.dlg = nil
		m.startOp("boot preview (dry-run): "+image, func(ctx context.Context, w io.Writer) error {
			return m.Svc.BootPreview(ctx, image, target, force, w)
		}, nil)
	}}
}

// ---- snapshot actions ----

func (m *Model) after(what string) func(error) {
	return func(err error) {
		m.reloadTree()
		if err != nil {
			m.stErr, m.status = true, what+": "+err.Error()
		} else {
			m.sayf("%s: ok", what)
		}
	}
}

func (m *Model) createForm(row TreeRow) {
	from := ""
	if !row.IsBase {
		from = row.Name
	}
	m.form(L.CreateTitle, []string{L.FieldFrom + ": " + nodeLabel(from)}, []string{L.FieldName, L.FieldNote, L.FieldTag}, []string{"", "", ""}, func(v []string) {
		name := strings.TrimSpace(v[0])
		if name == "" {
			m.message(L.Error, true, "name required")
			return
		}
		var tags []string
		if t := strings.TrimSpace(v[2]); t != "" {
			tags = strings.Fields(t)
		}
		from2 := from
		if from == "" {
			from2 = "base"
		}
		if err := m.Svc.Create(m.image, name, from2, v[1], tags); err != nil {
			m.reloadTree()
			m.fail(err)
			return
		}
		m.reloadTree()
		m.sayf("created snapshot %s", name)
	})
}

func (m *Model) rollbackMenu(row TreeRow) {
	node := "base"
	if !row.IsBase {
		node = row.Name
	}
	m.menu(L.RollbackTitle+" -> "+node, []string{L.RollbackKeep, L.RollbackDiscard, L.Cancel}, func(i int) {
		do := func(discard bool) {
			if err := m.Svc.Rollback(m.image, node, discard); err != nil {
				m.fail(err)
				return
			}
			m.reloadTree()
			m.sayf("rollback to %s: ok", node)
		}
		switch i {
		case 0:
			m.confirm(L.RollbackTitle, false, []string{"Rollback to " + node + "?"}, func() { do(false) })
		case 1:
			m.confirm(L.RollbackTitle, true, []string{L.RollbackSure}, func() { do(true) })
		}
	})
}

func (m *Model) deleteFlow(row TreeRow) {
	if row.IsBase {
		m.message(L.Info, false, "The base image cannot be deleted here.")
		return
	}
	node := row.Name
	rec := row.Kids > 0
	force := false
	run := func() {
		plan, err := m.Svc.DeletePlan(m.image, node, rec, force)
		if err != nil {
			m.fail(err)
			return
		}
		lines := []string{"Delete: " + strings.Join(plan, ", ")}
		if rec {
			lines = append([]string{L.DeleteRec}, lines...)
		}
		m.confirm(L.DeleteTitle, true, lines, func() {
			if err := m.Svc.Delete(m.image, node, rec, force); err != nil {
				m.fail(err)
				return
			}
			m.reloadTree()
			m.sayf("deleted %s", strings.Join(plan, " "))
		})
	}
	if node == m.tree.Current {
		m.confirm(L.DeleteTitle, true, []string{L.DeleteCurrent}, func() { force = true; run() })
		return
	}
	run()
}

func (m *Model) tagForm(row TreeRow) {
	if row.IsBase {
		m.message(L.Info, false, "Tags and notes apply to snapshots, not to the base image.")
		return
	}
	cur := ""
	if row.Node != nil {
		cur = strings.Join(row.Node.Tags, ",")
	}
	m.form(L.TagTitle+" "+row.Name, []string{"current tags: " + cur}, []string{L.TagHelp}, []string{""}, func(v []string) {
		t := strings.TrimSpace(v[0])
		if t == "" {
			return
		}
		rm := strings.HasPrefix(t, "-")
		if err := m.Svc.Tag(m.image, row.Name, strings.TrimPrefix(t, "-"), rm); err != nil {
			m.fail(err)
			return
		}
		m.reloadTree()
	})
}

func (m *Model) noteForm(row TreeRow) {
	if row.IsBase || row.Node == nil {
		m.message(L.Info, false, "Tags and notes apply to snapshots, not to the base image.")
		return
	}
	m.form(L.NoteTitle+" "+row.Name, nil, []string{L.FieldNote}, []string{row.Node.Note}, func(v []string) {
		if err := m.Svc.SetNote(m.image, row.Name, v[0]); err != nil {
			m.fail(err)
			return
		}
		m.reloadTree()
	})
}

func (m *Model) pruneMenu() {
	m.menu(L.PruneTitle, []string{L.PruneRetention, L.PruneGC, L.Cancel}, func(i int) {
		if i > 1 {
			return
		}
		gc := i == 1
		plan, err := m.Svc.Prune(m.image, gc, true)
		if err != nil {
			m.fail(err)
			return
		}
		if len(plan) == 0 {
			m.message(L.Info, false, L.PruneNothing)
			return
		}
		m.confirm(L.PruneAsk, true, []string{"dry-run result: would delete " + fmt.Sprint(len(plan)) + " snapshot(s):", strings.Join(plan, ", ")}, func() {
			if _, err := m.Svc.Prune(m.image, gc, false); err != nil {
				m.fail(err)
			}
			m.reloadTree()
			m.sayf("deleted %d snapshot(s)", len(plan))
		})
	})
}

func (m *Model) flattenForm(row TreeRow) {
	node := "base"
	if !row.IsBase {
		node = row.Name
	}
	if row.IsBase {
		m.message(L.Info, false, "Select a snapshot to flatten.")
		return
	}
	m.form(L.FlattenTitle+" "+node, []string{"Creates a new image with the snapshot merged into the base (needs root; may take a while)."},
		[]string{L.FieldName}, []string{m.image + "-" + node}, func(v []string) {
			name, img := strings.TrimSpace(v[0]), m.image
			m.startOp("flatten "+img+"@"+node+" -> "+name, func(ctx context.Context, w io.Writer) error {
				return m.Svc.Flatten(ctx, img, node, name, w)
			}, m.after("flatten"))
		})
}

func (m *Model) exportForm(row TreeRow) {
	if row.IsBase {
		m.message(L.Info, false, "Select a snapshot to export.")
		return
	}
	kind, path := m.Svc.ExportDefault(m.image)
	m.form(L.ExportTitle+" "+row.Name, []string{"Format: " + kind}, []string{L.FieldOut}, []string{path}, func(v []string) {
		img, node, out := m.image, row.Name, strings.TrimSpace(v[0])
		m.startOp("export "+img+"@"+node, func(ctx context.Context, w io.Writer) error {
			return m.Svc.Export(ctx, img, node, out, w)
		}, m.after("export"))
	})
}

func (m *Model) verifyMenu() {
	m.menu(L.VerifyTitle, []string{"Verify (metadata and layers)", L.VerifyDeep, L.Cancel}, func(i int) {
		if i > 1 {
			return
		}
		img := m.image
		m.startOp("verify "+img, func(ctx context.Context, w io.Writer) error {
			return m.Svc.Verify(ctx, img, i == 1, w)
		}, nil)
	})
}

// ---- operation view ----

func (m *Model) closeOp() {
	m.op = nil
	if m.opResume != nil {
		m.dlg, m.opResume = m.opResume, nil
	}
}

func (m *Model) opKey(k Key) {
	act := m.Km.Action(k)
	fin, _ := m.op.Finished()
	page := m.H - 8
	switch act {
	case "cancel":
		if !fin {
			m.op.Cancel()
			m.say(L.Cancelled)
		}
		return
	case "up":
		m.opScroll++
	case "down":
		m.opScroll--
	case "pgup":
		m.opScroll += page
	case "pgdn":
		m.opScroll -= page
	case "home":
		m.opScroll = 1 << 20
	case "end":
		m.opScroll = 0
	}
	if m.opScroll < 0 {
		m.opScroll = 0
	}
	if fin && (act == "select" || act == "back" || k.Rune == 'q') {
		m.Tick()
		m.closeOp()
	}
}

// ---- dialogs ----

func (m *Model) dialogKey(k Key) {
	d := m.dlg
	act := m.Km.Action(k)
	switch d.Kind {
	case dkMsg:
		if act == "select" || act == "back" || k.Rune == 'q' || k.Rune == ' ' {
			m.dlg = nil
		}
	case dkConfirm:
		switch {
		case act == "select" || k.Rune == 'y':
			m.dlg = nil
			d.OnOK()
		case act == "back" || act == "cancel" || k.Rune == 'n' || k.Rune == 'q':
			m.dlg = nil
		default:
			if f, ok := d.Extra[k.Rune]; ok && k.Name == "" {
				f()
			}
		}
	case dkMenu:
		switch {
		case act == "up" || act == "down" || act == "home" || act == "end":
			d.Sel = moveSel(act, d.Sel, len(d.Items), 1)
		case act == "select":
			m.dlg = nil
			d.OnPick(d.Sel)
		case act == "back" || act == "cancel":
			m.dlg = nil
		case k.Name == "" && k.Rune >= '1' && int(k.Rune-'1') < len(d.Items):
			m.dlg = nil
			d.OnPick(int(k.Rune - '1'))
		}
	case dkForm:
		switch {
		case k.Name == "esc" || k.Name == "ctrl-c":
			m.dlg = nil
		case k.Name == "tab" || k.Name == "down":
			d.Fi = (d.Fi + 1) % len(d.Labels)
		case k.Name == "btab" || k.Name == "up":
			d.Fi = (d.Fi + len(d.Labels) - 1) % len(d.Labels)
		case k.Name == "enter":
			if d.Fi < len(d.Labels)-1 {
				d.Fi++
				return
			}
			m.dlg = nil
			d.OnSubmit(d.Values)
		case k.Name == "bs":
			if r := []rune(d.Values[d.Fi]); len(r) > 0 {
				d.Values[d.Fi] = string(r[:len(r)-1])
			}
		case k.Name == "" && k.Rune >= 0x20:
			d.Values[d.Fi] += string(k.Rune)
		}
	}
}
