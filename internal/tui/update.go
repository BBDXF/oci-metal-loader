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
		m.helpKey(k)
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
	typing := m.typing()
	if act == "cancel" {
		m.warn("Ctrl+C cancels running operations; nothing is running. Use q/Esc to leave the TUI.")
		return
	}
	if act == "help" && (!typing || k.Name != "") {
		m.help, m.helpTop = true, 0
		return
	}
	if act == "redraw" {
		return
	}
	// global shortcuts
	if k.Name == "" && !typing {
		switch k.Rune {
		case '1', '2', '3', '4', '5':
			m.gotoTab(Screen(k.Rune - '1'))
			return
		case 'P':
			m.powerMenu()
			return
		case 'S':
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
	m.refreshHeader(true)
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
		m.warn(L.PowerBusy)
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

func (m *Model) bodyRows() int { return m.H - 5 }

// typing is true while the Pull catalog search box has the keyboard (letters and digits are text).
func (m *Model) typing() bool { return m.Screen == ScrPull && m.pullCat && m.dlg == nil && m.op == nil }

func (m *Model) helpKey(k Key) {
	act := m.Km.Action(k)
	page := m.bodyRows() - 1
	switch {
	case k.Name == "esc" || k.Name == "enter" || k.Name == "f1" || k.Name == "" && (k.Rune == '?' || k.Rune == 'q'):
		m.help = false
	case act == "up":
		m.helpTop--
	case act == "down":
		m.helpTop++
	case act == "pgup":
		m.helpTop -= page
	case act == "pgdn":
		m.helpTop += page
	case act == "home":
		m.helpTop = 0
	case act == "end":
		m.helpTop = 1 << 20
	}
	if m.helpTop < 0 {
		m.helpTop = 0
	}
}

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
	case act == "select":
		if len(m.images) > 0 {
			m.bootNow(m.images[m.isel].Name, "current", false)
		}
	case k.Rune == 'o' && k.Name == "" || k.Name == "right":
		if len(m.images) > 0 {
			m.OpenImage(m.images[m.isel].Name)
		}
	case k.Rune == 'e' && k.Name == "":
		if len(m.images) > 0 {
			m.bootNow(m.images[m.isel].Name, "current", true)
		}
	case k.Rune == ' ' && k.Name == "" || k.Rune == 'm' && k.Name == "" || k.Name == "menu":
		m.imageMenu()
	case k.Rune == 'r' && k.Name == "":
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
	m.dsel = m.defaultRow(name)
	m.dtop = keepVisible(m.dsel, 0, m.detailVis(m.bodyRows()))
}

// defaultRow is the row Enter should land on: the last booted snapshot of this image, else the live state ("current"),
// else the snapshot it sits on, else base. (Enter on base would revert, so base is never the default when anything else exists.)
func (m *Model) defaultRow(image string) int {
	if lb, ok := m.Svc.(LastBooter); ok {
		if li, ls := lb.LastBoot(); li == image && ls != "" && ls != "current" {
			for i, r := range m.rows {
				if !r.IsBase && r.Node.Label == ls {
					return i
				}
			}
		}
	}
	for i, r := range m.rows {
		if !r.IsBase && r.Node.Live {
			return i
		}
	}
	for i, r := range m.rows {
		if !r.IsBase && r.Node.Name == m.tree.Head {
			return i
		}
	}
	return 0
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

// rowTitle is the displayed name of a tree node.
func rowTitle(n *NodeInfo) string {
	switch {
	case n.Live:
		return L.CurrentNode
	case n.Label != "":
		return n.Label
	case n.Ephemeral:
		return L.EphemeralNode
	}
	return n.Name
}

// BuildRows lays the snapshot tree out depth first ("base" is the root; the live state is a leaf under its snapshot).
func BuildRows(t *TreeInfo, th Theme) []TreeRow {
	g := th.g()
	kids := map[string][]string{}
	nodes := map[string]*NodeInfo{}
	infos := append([]NodeInfo{}, t.Nodes...)
	if t.Current == "" {
		infos = append(infos, NodeInfo{Name: "(live)", Parent: t.Head, Live: true})
	}
	for i := range infos {
		n := &infos[i]
		nodes[n.Name] = n
		kids[n.Parent] = append(kids[n.Parent], n.Name)
	}
	for _, v := range kids {
		sort.Slice(v, func(i, j int) bool {
			if len(v[i]) != len(v[j]) {
				return len(v[i]) < len(v[j])
			}
			return v[i] < v[j]
		})
	}
	persistent := func(id string) int {
		c := 0
		for _, k := range kids[id] {
			if !nodes[k].Ephemeral {
				c++
			}
		}
		return c
	}
	rows := []TreeRow{{Name: "base", IsBase: true, Kids: persistent("")}}
	var walk func(parent, prefix string)
	walk = func(parent, prefix string) {
		for i, n := range kids[parent] {
			br, next := g.branch, g.pipe
			if i == len(kids[parent])-1 {
				br, next = g.last, g.blank
			}
			rows = append(rows, TreeRow{Name: n, Prefix: prefix + br, Node: nodes[n], Kids: persistent(n)})
			walk(n, prefix+next)
		}
	}
	walk("", "")
	return rows
}

func (m *Model) selRow() TreeRow {
	if m.dsel < 0 || m.dsel >= len(m.rows) {
		return TreeRow{IsBase: true, Name: "base"}
	}
	return m.rows[m.dsel]
}

// snapLabel returns the snapshot label of a row ("" when it is not a snapshot).
func snapLabel(r TreeRow) string {
	if r.IsBase || r.Node == nil {
		return ""
	}
	return r.Node.Label
}

func (m *Model) detailKey(k Key, act string) {
	m.dsel = moveSel(act, m.dsel, len(m.rows), m.bodyRows())
	m.dtop = keepVisible(m.dsel, m.dtop, m.detailVis(m.bodyRows()))
	row := m.selRow()
	r := k.Rune
	if k.Name != "" {
		r = 0
	}
	switch {
	case act == "back" || r == 'q':
		m.Screen = ScrImages
		m.RefreshImages()
	case act == "select":
		m.enterRow(row)
	case r == ' ' || r == 'm' || k.Name == "menu":
		m.rowMenu(row)
	case r == 'e':
		m.ephemeralKey(row)
	case r == 's' || r == 'c':
		m.snapshotKey()
	case r == 'r':
		m.revertKey(row)
	case r == 'd':
		m.deleteFlow(row)
	case r == 't':
		m.tagForm(row)
	case r == 'n':
		m.noteForm(row)
	case r == 'p':
		m.pruneMenu()
	case r == 'f':
		m.flattenForm(row)
	case r == 'x':
		m.exportForm(row)
	case r == 'v':
		m.verifyMenu()
	case r == 'R':
		m.reloadTree()
		m.say(L.Refreshed)
	}
}

// ---- snapshot actions ----

func (m *Model) after(what string) func(error) {
	return func(err error) {
		m.reloadTree()
		if err != nil {
			m.stErr, m.stWarn, m.status = true, false, what+": "+err.Error()
		} else {
			m.sayf("%s: ok", what)
		}
	}
}

func (m *Model) subtreeHasLive(id string) bool {
	if m.tree.Current == "" {
		return false
	}
	for p := m.tree.Current; p != ""; {
		if p == id {
			return true
		}
		n := ""
		for _, nd := range m.tree.Nodes {
			if nd.Name == p {
				n = nd.Parent
			}
		}
		p = n
	}
	return false
}

func (m *Model) deleteFlow(row TreeRow) {
	if row.IsBase {
		m.message(L.Info, false, L.DeleteBase)
		return
	}
	label := snapLabel(row)
	if label == "" {
		m.message(L.Info, false, L.DeleteNotSnap)
		return
	}
	rec := row.Kids > 1
	force := false
	run := func() {
		plan, err := m.Svc.DeletePlan(m.image, label, rec, force)
		if err != nil {
			m.fail(err)
			return
		}
		lines := []string{fmt.Sprintf(L.DeleteWhat, label, human(row.Node.Alloc))}
		lines = append(lines, plan...)
		if row.Kids == 1 {
			lines = append(lines, "", L.DeleteMerge)
		}
		m.confirm(L.DeleteTitle, true, lines, func() {
			if err := m.Svc.Delete(m.image, label, rec, force); err != nil {
				m.reloadTree()
				m.fail(err)
				return
			}
			m.reloadTree()
			m.sayf("deleted %s", label)
		})
	}
	if rec && m.subtreeHasLive(row.Name) {
		m.confirm(L.DeleteTitle, true, []string{L.DeleteRec, L.DeleteCurrent}, func() { force = true; run() })
		return
	}
	run()
}

func (m *Model) tagForm(row TreeRow) {
	label := snapLabel(row)
	if label == "" {
		m.message(L.Info, false, L.TagNotSnap)
		return
	}
	cur := strings.Join(row.Node.Tags, ",")
	m.form(L.TagTitle+" "+label, []string{"current tags: " + cur}, []string{L.TagHelp}, []string{""}, func(v []string) {
		t := strings.TrimSpace(v[0])
		if t == "" {
			return
		}
		rm := strings.HasPrefix(t, "-")
		if err := m.Svc.Tag(m.image, label, strings.TrimPrefix(t, "-"), rm); err != nil {
			m.fail(err)
			return
		}
		m.reloadTree()
	})
}

func (m *Model) noteForm(row TreeRow) {
	label := snapLabel(row)
	if label == "" {
		m.message(L.Info, false, L.TagNotSnap)
		return
	}
	m.form(L.NoteTitle+" "+label, nil, []string{L.FieldNote}, []string{row.Node.Note}, func(v []string) {
		if err := m.Svc.SetNote(m.image, label, v[0]); err != nil {
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
		why := L.RetWhat
		if gc {
			why = L.GCWhat
		}
		m.confirm(L.PruneAsk, true, []string{fmt.Sprintf(L.PruneWhat, len(plan)), strings.Join(plan, ", "), why}, func() {
			if _, err := m.Svc.Prune(m.image, gc, false); err != nil {
				m.fail(err)
			}
			m.reloadTree()
			m.sayf("deleted %d layer(s)", len(plan))
		})
	})
}

func (m *Model) flattenForm(row TreeRow) {
	label := snapLabel(row)
	if label == "" {
		m.message(L.Info, false, L.SelectSnap)
		return
	}
	m.form(L.FlattenTitle+" "+label, []string{"Creates a new image with the snapshot merged into the base (needs root; may take a while)."},
		[]string{L.FieldName}, []string{m.image + "-flat-" + label}, func(v []string) {
			name, img := strings.TrimSpace(v[0]), m.image
			m.startOp("flatten "+img+"@"+label+" -> "+name, func(ctx context.Context, w io.Writer) error {
				return m.Svc.Flatten(ctx, img, label, name, w)
			}, m.after("flatten"))
		})
}

func (m *Model) exportForm(row TreeRow) {
	label := snapLabel(row)
	if label == "" {
		m.message(L.Info, false, L.SelectSnap)
		return
	}
	kind, path := m.Svc.ExportDefault(m.image)
	m.form(L.ExportTitle+" "+label, []string{"Format: " + kind}, []string{L.FieldOut}, []string{path}, func(v []string) {
		img, out := m.image, strings.TrimSpace(v[0])
		m.startOp("export "+img+"@"+label, func(ctx context.Context, w io.Writer) error {
			return m.Svc.Export(ctx, img, label, out, w)
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
	m.refreshHeader(true)
	if m.opResume != nil {
		m.dlg, m.opResume = m.opResume, nil
	}
}

func (m *Model) opKey(k Key) {
	act := m.Km.Action(k)
	fin, _ := m.op.Finished()
	page := m.H - 8
	if act == "help" {
		m.help, m.helpTop = true, 0
		return
	}
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
	if k.Name == "" && k.Rune == 'l' {
		m.opHideLog = !m.opHideLog
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
			if d.Replace && d.Fi == 0 {
				d.Values[0], d.Replace = "", false
			}
			d.Values[d.Fi] += string(k.Rune)
		}
	}
}
