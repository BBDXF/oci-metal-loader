package tui

import (
	"fmt"
	"strings"
)

func wrap(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			for len([]rune(word)) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				r := []rune(word)
				out = append(out, string(r[:w]))
				word = string(r[w:])
			}
			switch {
			case line == "":
				line = word
			case len([]rune(line))+1+len([]rune(word)) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}

// View renders the current state into a canvas of the model's size.
func (m *Model) View() *Canvas {
	c := NewCanvas(m.W, m.H)
	if m.W < 80 || m.H < 24 {
		if m.W < 40 || m.H < 6 {
			c.Put(0, 0, trunc(L.TooSmall, m.W), StErr)
			return c
		}
		defer func() { c.Put(0, 1, trunc(L.TooSmall, m.W), StErr) }()
	}
	m.drawTabs(c)
	switch {
	case m.op != nil:
		m.drawOp(c)
	case m.help:
		m.drawHelp(c)
	default:
		switch m.Screen {
		case ScrImages:
			m.drawImages(c)
		case ScrDetail:
			m.drawDetail(c)
		case ScrPull:
			m.drawPull(c)
		case ScrStorage:
			m.drawStorage(c)
		case ScrNet:
			m.drawLines(c, m.netLines, m.nsc, L.NetTitle)
		case ScrSettings:
			m.drawLines(c, m.settings, m.ssc, L.SettingsTitle)
		}
	}
	// status message + hint bar
	if m.status != "" {
		st := StOK
		if m.stErr {
			st = StErr
		}
		c.Put(1, m.H-2, trunc(m.status, m.W-2), st)
	}
	c.Fill(0, m.H-1, m.W, 1, StBar)
	c.Put(0, m.H-1, " "+trunc(m.hint(), m.W-2), StBar)
	if m.dlg != nil {
		m.drawDialog(c)
	}
	return c
}

func (m *Model) hint() string {
	switch {
	case m.dlg != nil:
		switch m.dlg.Kind {
		case dkForm:
			return L.HintForm
		case dkMenu:
			return "Up/Down:choose  Enter:ok  1-9:quick pick  Esc:cancel"
		}
		return L.HintDialog
	case m.op != nil:
		return L.HintOp
	case m.help:
		return L.HintHelp
	}
	switch m.Screen {
	case ScrDetail:
		return L.HintDetail
	case ScrPull:
		return L.HintPull
	case ScrStorage:
		return L.HintStorage
	case ScrNet:
		return L.HintNet
	case ScrSettings:
		return L.HintSettings
	}
	return L.HintImages
}

func (m *Model) drawTabs(c *Canvas) {
	c.Fill(0, 0, m.W, 1, StBar)
	x := 1
	c.Put(x, 0, L.AppName, StBar)
	x += len(L.AppName) + 2
	cur := int(m.Screen)
	if m.Screen == ScrDetail {
		cur = 0
	}
	for i, t := range L.Tabs {
		lab := fmt.Sprintf("%d:%s", i+1, t)
		st := StBar
		if i == cur {
			st, lab = StBold, "["+lab+"]"
		} else {
			lab = " " + lab + " "
		}
		c.Put(x, 0, lab, st)
		x += len(lab)
	}
	if m.Th.ASCII {
		c.Put(m.W-len(L.ASCIINote)-1, 0, L.ASCIINote, StBar)
	}
}

func (m *Model) drawImages(c *Canvas) {
	w := m.W
	nw := w - (13 + 9 + 8 + 5 + 12 + 11 + 6)
	if nw < 8 {
		nw = 8
	}
	hdr := pad(L.ColName, nw) + " " + pad(L.ColType, 13) + " " + pad(L.ColSize, 9) + " " + pad(L.ColBase, 8) + " " + pad(L.ColSnaps, 5) + " " + pad(L.ColCurrent, 12) + " " + L.ColUsed
	c.Put(0, 1, hdr, StBold)
	if len(m.images) == 0 {
		for i, l := range wrap(L.NoImages, w-4) {
			c.Put(2, 3+i, l, StDim)
		}
		return
	}
	vis := m.bodyRows() - 1
	for i := 0; i < vis && m.itop+i < len(m.images); i++ {
		im := m.images[m.itop+i]
		cur := im.Current
		if cur == "" {
			cur = L.BaseNode
		}
		used := "-"
		if !im.LastUsed.IsZero() {
			used = im.LastUsed.Format("01-02 15:04")
		}
		base := im.BaseOK
		if im.Err != "" {
			base = "ERROR"
		}
		line := pad(trunc(im.Name, nw), nw) + " " + pad(im.Type, 13) + " " + pad(human(im.Size), 9) + " " + pad(base, 8) + " " + pad(fmt.Sprint(im.Snaps), 5) + " " + pad(trunc(cur, 12), 12) + " " + used
		st := StNormal
		if m.itop+i == m.isel {
			st = StSel
		}
		c.Fill(0, 2+i, w, 1, st)
		c.Put(0, 2+i, trunc(line, w), st)
	}
	if im := m.images[m.isel]; im.Err != "" {
		c.Put(1, m.H-3, trunc(im.Err, w-2), StErr)
	}
}

func (m *Model) drawDetail(c *Canvas) {
	t := m.tree
	if t == nil {
		return
	}
	g := m.Th.g()
	c.Put(1, 1, trunc(fmt.Sprintf("%s  type=%s  mode=%s  current=%s  snapshots=%d", m.image, t.Type, t.Mode, nodeLabel(t.Current), len(t.Nodes)), m.W-2), StBold)
	vis := m.bodyRows() - 5
	for i := 0; i < vis && m.dtop+i < len(m.rows); i++ {
		r := m.rows[m.dtop+i]
		mark := " "
		if (r.IsBase && t.Current == "") || (!r.IsBase && r.Name == t.Current) {
			mark = g.cur
		}
		var line string
		if r.IsBase {
			line = mark + " " + L.BaseNode
		} else {
			n := r.Node
			var fl []string
			if r.Kids > 0 {
				fl = append(fl, L.Frozen)
			} else {
				fl = append(fl, L.Leaf)
			}
			if strings.HasPrefix(r.Name, "tmp-") {
				fl = append(fl, L.Ephemeral)
			}
			if n.NoLayer {
				fl = append(fl, L.NoLayer)
			}
			line = r.Prefix + mark + " " + r.Name + "  [" + strings.Join(fl, ",") + "]"
			if len(n.Tags) > 0 {
				line += " #" + strings.Join(n.Tags, " #")
			}
			line += "  " + human(n.Alloc)
		}
		st := StNormal
		if m.dtop+i == m.dsel {
			st = StSel
		}
		c.Fill(0, 2+i, m.W, 1, st)
		c.Put(1, 2+i, trunc(line, m.W-2), st)
	}
	// info pane of the selected node
	y := m.H - 5
	for x := 0; x < m.W; x++ {
		c.Put(x, y-0, g.h, StDim)
	}
	r := m.selRow()
	if r.IsBase {
		c.Put(1, y+1, "base image (read-only). Boot it ephemeral, or create the first snapshot from it.", StDim)
		if len(m.tree.Nodes) == 0 {
			c.Put(1, y+2, L.NoSnaps, StDim)
		}
		return
	}
	n := r.Node
	parent := n.Parent
	if parent == "" {
		parent = L.BaseNode
	}
	c.Put(1, y+1, trunc(fmt.Sprintf("%s  parent=%s  created=%s  size=%s", r.Name, parent, n.Created, human(n.Alloc)), m.W-2), StBold)
	c.Put(1, y+2, trunc("tags: "+strings.Join(n.Tags, ", "), m.W-2), StNormal)
	c.Put(1, y+3, trunc("note: "+n.Note, m.W-2), StNormal)
}

func (m *Model) drawPull(c *Canvas) {
	c.Put(1, 1, L.SourcesTitle, StBold)
	y := 2
	if len(m.sources) == 0 {
		c.Put(2, y, L.NoSources, StDim)
		y++
	}
	for i, s := range m.sources {
		note := ""
		if s.Note != "" {
			note = "  (" + s.Note + ")"
		}
		line := fmt.Sprintf("%-14s %-13s %s%s", s.Name, s.Type, s.URL, note)
		st := StNormal
		if i == m.ssel && !m.pullCat {
			st = StSel
		} else if i == m.ssel {
			st = StBold
		}
		c.Fill(0, y, m.W, 1, st)
		c.Put(1, y, trunc(line, m.W-2), st)
		y++
	}
	y++
	f := m.filter
	c.Put(1, y, trunc(fmt.Sprintf("%s: %s  os=%q release=%q type=%q", L.CatalogTitle, m.csource, f.OS, f.Release, f.Kind), m.W-2), StBold)
	y++
	if m.catalog == nil {
		c.Put(2, y, L.NoCatalog, StDim)
		return
	}
	vis := m.H - 2 - y
	for i := 0; i < vis && m.ctop+i < len(m.catalog); i++ {
		it := m.catalog[m.ctop+i]
		line := fmt.Sprintf("%-34s %-9s %-10s %s", it.Spec(), it.Kind, it.Version, human(it.Size))
		st := StNormal
		if m.pullCat && m.ctop+i == m.csel {
			st = StSel
		}
		c.Fill(0, y+i, m.W, 1, st)
		c.Put(1, y+i, trunc(line, m.W-2), st)
	}
}

func (m *Model) drawStorage(c *Canvas) {
	s := m.store
	c.Put(1, 1, L.StoreTitle, StBold)
	if m.storeErr != "" {
		c.Put(2, 3, trunc(m.storeErr, m.W-4), StErr)
		return
	}
	rows := [][2]string{{"kind", s.Kind}, {"path", s.Path}, {"host fs", s.HostFS}}
	if s.Kind == "store-file" {
		rows = append(rows, [2]string{"file size", human(s.Size) + " (allocated " + human(s.Alloc) + ")"},
			[2]string{"ext4 state", s.State}, [2]string{"attached", fmt.Sprint(s.Attached)})
	}
	rows = append(rows, [2]string{"fs free", fmt.Sprintf("%s of %s (%.1f%%)", human(s.Free), human(s.Total), s.FreePct)})
	y := 3
	for _, r := range rows {
		c.Put(2, y, fmt.Sprintf("%-12s %s", r[0], trunc(r[1], m.W-18)), StNormal)
		y++
	}
	y++
	guard, st := L.StoreGuardOK, StOK
	if s.MinFreePct > 0 && s.FreePct < float64(s.MinFreePct) {
		guard, st = L.StoreGuardLow, StErr
	}
	c.Put(2, y, fmt.Sprintf("%s: min_free_percent=%d%%  %s", L.StoreGuard, s.MinFreePct, guard), st)
	y++
	if s.Kind == "store-file" {
		ss := StOK
		if s.State != "clean" {
			ss = StErr
		}
		c.Put(2, y, "ext4: "+map[bool]string{true: L.StoreClean, false: L.StoreDirty}[s.State == "clean"], ss)
		y++
	}
	for _, n := range s.Notes {
		y++
		c.Put(2, y, trunc(n, m.W-4), StDim)
	}
}

func (m *Model) drawLines(c *Canvas, lines []string, top int, title string) {
	c.Put(1, 1, trunc(title, m.W-2), StBold)
	vis := m.bodyRows() - 1
	if top > len(lines)-vis {
		top = len(lines) - vis
	}
	if top < 0 {
		top = 0
	}
	for i := 0; i < vis && top+i < len(lines); i++ {
		c.Put(1, 2+i, trunc(lines[top+i], m.W-2), StNormal)
	}
}

func (m *Model) drawOp(c *Canvas) {
	o := m.op
	fin, err := o.Finished()
	state, st := L.Running, StBold
	if fin {
		state, st = L.Done, StOK
		if err != nil {
			state, st = L.Failed, StErr
		}
		if o.Cancelled {
			state = L.Cancelled
		}
	}
	c.Put(1, 1, trunc(o.Title+"  ["+state+"]", m.W-2), st)
	c.Put(1, 2, trunc(o.Progress(m.Now()), m.W-2), StDim)
	lines := o.Lines()
	if fin && err != nil {
		lines = append(lines, "", L.Error+": "+err.Error())
	}
	vis := m.H - 5
	bottom := len(lines) - m.opScroll
	if bottom < 0 {
		bottom = 0
	}
	if bottom > len(lines) {
		bottom = len(lines)
	}
	top := bottom - vis
	if top < 0 {
		top = 0
	}
	for i := top; i < bottom; i++ {
		l := lines[i]
		s := StNormal
		if strings.HasPrefix(l, L.Error) {
			s = StErr
		}
		c.Put(1, 3+i-top, trunc(l, m.W-2), s)
	}
}

func (m *Model) drawHelp(c *Canvas) {
	c.Put(1, 1, L.HelpTitle, StBold)
	for i, l := range L.HelpLines {
		c.Put(2, 3+i, trunc(l, m.W-4), StNormal)
	}
	c.Put(2, 4+len(L.HelpLines), trunc(m.hintFor(), m.W-4), StDim)
}

func (m *Model) hintFor() string {
	h := m.help
	m.help = false
	s := m.hint()
	m.help = h
	return s
}

func (m *Model) drawDialog(c *Canvas) {
	d := m.dlg
	w := m.W - 8
	if w > 70 {
		w = 70
	}
	var lines []string
	for _, l := range d.Lines {
		lines = append(lines, wrap(l, w-4)...)
	}
	switch d.Kind {
	case dkMenu:
		for i, it := range d.Items {
			lines = append(lines, fmt.Sprintf("%d) %s", i+1, it))
		}
	case dkForm:
		lines = append(lines, "")
		for range d.Labels {
			lines = append(lines, "")
		}
	case dkConfirm:
		lines = append(lines, "", "[Enter/y] "+L.Yes+"    [Esc/n] "+L.No)
	}
	h := len(lines) + 2
	if h > m.H-3 {
		h = m.H - 3
	}
	x, y := (m.W-w)/2, (m.H-h)/2
	st := StNormal
	if d.Warn {
		st = StWarn
	}
	c.Box(x, y, w, h, m.Th, st)
	title := " " + d.Title + " "
	if d.Warn && d.Kind != dkMsg || d.Warn {
		title = " " + L.Warning + ": " + d.Title + " "
	}
	c.Put(x+2, y, trunc(title, w-4), StBold)
	nl := len(lines)
	if d.Kind == dkForm {
		nl -= len(d.Labels)
	}
	for i, l := range lines {
		if i >= h-2 {
			break
		}
		s := StNormal
		if d.Kind == dkMenu && i >= len(lines)-len(d.Items) && i-(len(lines)-len(d.Items)) == d.Sel {
			s = StSel
		}
		if s == StSel {
			c.Fill(x+1, y+1+i, w-2, 1, StSel)
		}
		c.Put(x+2, y+1+i, trunc(l, w-4), s)
	}
	if d.Kind == dkForm {
		for i, lab := range d.Labels {
			row := y + 1 + nl + i
			if row >= y+h-1 {
				break
			}
			val := d.Values[i]
			cur := ""
			if i == d.Fi {
				cur = "_"
			}
			lw := 0
			for _, l := range d.Labels {
				if len(l) > lw {
					lw = len(l)
				}
			}
			s := StNormal
			if i == d.Fi {
				s = StBold
			}
			c.Put(x+2, row, trunc(pad(lab, lw)+": "+val+cur, w-4), s)
		}
	}
}
