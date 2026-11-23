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
			for strWidth(word) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				cut, cw := 0, 0
				for i, r := range word {
					if cw+runeWidth(r) > w {
						break
					}
					cw += runeWidth(r)
					cut = i + len(string(r))
				}
				if cut == 0 {
					cut = len(string([]rune(word)[0]))
				}
				out = append(out, word[:cut])
				word = word[cut:]
			}
			switch {
			case line == "":
				line = word
			case strWidth(line)+1+strWidth(word) <= w:
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

// View renders the current state into a canvas of the model's size: row 0 tabs, row 1 header (version / store / network),
// the body, a status row and the key bar on the last row.
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
	m.drawHeader(c)
	// the body is drawn in its own coordinates (row 0 = tab row) one row lower and one row shorter
	real := m.H
	m.H, c.oy = real-1, 1
	switch {
	case m.help:
		m.drawHelp(c)
	case m.op != nil:
		m.drawOp(c)
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
	m.H, c.oy = real, 0
	if m.status != "" {
		st := StOK
		switch {
		case m.stErr:
			st = StErr
		case m.stWarn:
			st = StWarn
		}
		c.Put(1, m.H-2, trunc(m.status, m.W-2), st)
	}
	c.Fill(0, m.H-1, m.W, 1, StKeys)
	c.Put(0, m.H-1, " "+trunc(m.hint(), m.W-2), StKeys)
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
			return L.HintMenu
		case dkMsg:
			return L.HintMsg
		}
		return L.HintDialog
	case m.help:
		return L.HintHelp
	case m.op != nil:
		if f, _ := m.op.Finished(); f {
			return L.HintOpDone
		}
		return L.HintOpRun
	}
	switch m.Screen {
	case ScrDetail:
		return L.HintDetail
	case ScrPull:
		if m.pullCat {
			return L.HintPullCat
		}
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
	c.Fill(0, 0, m.W, 1, StTitle)
	x := 1
	c.Put(x, 0, L.AppName, StTitle)
	x += len(L.AppName) + 2
	cur := int(m.Screen)
	if m.Screen == ScrDetail {
		cur = 0
	}
	for i, t := range L.Tabs {
		lab := fmt.Sprintf("%d:%s", i+1, t)
		st := StTitle
		if i == cur {
			st, lab = StTabOn, "["+lab+"]"
		} else {
			lab = " " + lab + " "
		}
		c.Put(x, 0, lab, st)
		x += len(lab)
	}
	if m.Th.ASCII {
		c.Put(m.W-len(L.ASCIINote)-1, 0, L.ASCIINote, StTitle)
	}
}

func (m *Model) drawHeader(c *Canvas) {
	c.Put(1, 1, trunc(m.headerText(m.W-2), m.W-2), StFaint)
}

// rows is the number of body rows below the title row that a list may use (draw context: m.H is the body height).
func (m *Model) bh() int { return m.H - 4 }

// drawScroll draws a vertical scroll bar in column x over rows y..y+h-1 (only when the list is longer than the window).
func (m *Model) drawScroll(c *Canvas, x, y, h, total, top int) {
	if total <= h || h < 2 {
		return
	}
	g := m.Th.g()
	th := h * h / total
	if th < 1 {
		th = 1
	}
	pos := 0
	if total > h {
		pos = top * (h - th) / (total - h)
	}
	for i := 0; i < h; i++ {
		if i >= pos && i < pos+th {
			c.Put(x, y+i, g.thumb, StBold)
		} else {
			c.Put(x, y+i, g.track, StFaint)
		}
	}
}

// putPos writes the "3/296" position indicator right aligned on row y.
func (m *Model) putPos(c *Canvas, y, sel, total int) {
	if total == 0 {
		return
	}
	s := fmt.Sprintf(L.PosFmt, sel+1, total)
	c.Put(m.W-1-strWidth(s)-1, y, s, StBold)
}

type seg struct {
	s  string
	st Style
}

// putSegs draws coloured segments left to right inside w cells; a selected row is one reverse-video bar.
func (m *Model) putSegs(c *Canvas, x, y, w int, segs []seg, sel bool) {
	if sel {
		c.Fill(0, y, m.W, 1, StSel)
	}
	for _, sg := range segs {
		if w <= 0 {
			return
		}
		t := trunc(sg.s, w)
		st := sg.st
		if sel {
			st = StSel
		}
		c.Put(x, y, t, st)
		x += strWidth(t)
		w -= strWidth(t)
	}
}

func (m *Model) drawImages(c *Canvas) {
	w := m.W
	nw := w - (13 + 9 + 8 + 5 + 12 + 11 + 6 + 1)
	if nw < 8 {
		nw = 8
	}
	hdr := pad(L.ColName, nw) + " " + pad(L.ColType, 13) + " " + pad(L.ColSize, 9) + " " + pad(L.ColBase, 8) + " " + pad(L.ColSnaps, 5) + " " + pad(L.ColCurrent, 12) + " " + L.ColUsed
	c.Put(0, 1, trunc(hdr, w-1), StBold)
	if len(m.images) == 0 {
		for i, l := range wrap(L.NoImages, w-4) {
			c.Put(2, 3+i, l, StDim)
		}
		return
	}
	m.putPos(c, m.H-2, m.isel, len(m.images))
	vis := m.bh() - 1
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
		base, bst := im.BaseOK, StNormal
		if im.Err != "" || base == "MISMATCH" {
			base, bst = "ERROR", StErr
		}
		if im.Err != "" {
			base = "ERROR"
		}
		sel := m.itop+i == m.isel
		x := 0
		y := 2 + i
		cell := func(t string, wd int, st Style) seg { return seg{pad(t, wd) + " ", st} }
		_ = x
		m.putSegs(c, 0, y, w-1, []seg{
			cell(trunc(im.Name, nw), nw, StBase), cell(im.Type, 13, StNormal), cell(human(im.Size), 9, StFaint), cell(base, 8, bst),
			cell(fmt.Sprint(im.Snaps), 5, StNormal), cell(trunc(cur, 12), 12, StCur), {used, StFaint},
		}, sel)
	}
	m.drawScroll(c, w-1, 2, vis, len(m.images), m.itop)
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
	head := L.BaseNode
	snaps := 0
	for _, n := range t.Nodes {
		if n.Label != "" {
			snaps++
			if n.Name == t.Head {
				head = n.Label
			}
		}
	}
	hdr := fmt.Sprintf("%s  type=%s  mode=%s  live on=%s  snapshots=%d", m.image, t.Type, t.Mode, head, snaps)
	c.Put(1, 1, trunc(hdr, m.W-14), StBold)
	m.putPos(c, 1, m.dsel, len(m.rows))
	vis := m.detailVis(m.bh())
	tw := m.W - 3
	if m.sideDetail() {
		tw = m.W - sidePanelW - 4
	}
	for i := 0; i < vis && m.dtop+i < len(m.rows); i++ {
		r := m.rows[m.dtop+i]
		isCur := (r.IsBase && t.Head == "") || (!r.IsBase && (r.Node.Live || (r.Node.Name == t.Head && !r.Node.Ephemeral)))
		mark := " "
		if isCur {
			mark = g.cur
		}
		var segs []seg
		if r.IsBase {
			segs = []seg{{mark, StCur}, {" " + L.BaseNode, StBase}, {" (original image)", StFaint}}
		} else {
			n := r.Node
			title, tst := rowTitle(n), StNormal
			switch {
			case n.Live:
				title, tst = rowTitle(n), StCur
			case n.Ephemeral:
				tst = StEph
			case r.Kids > 0:
				tst = StFrozen
			}
			segs = []seg{{r.Prefix, StFaint}, {mark, StCur}, {" " + title, tst}}
			var fl []string
			switch {
			case n.Live:
			case n.Ephemeral:
				fl = append(fl, L.Ephemeral)
			case r.Kids > 0:
				fl = append(fl, L.Frozen)
			}
			if n.NoLayer {
				fl = append(fl, L.NoLayer)
			}
			if len(fl) > 0 {
				fst := StFrozen
				if n.Ephemeral {
					fst = StEph
				}
				if n.NoLayer {
					fst = StErr
				}
				segs = append(segs, seg{"  [" + strings.Join(fl, ",") + "]", fst})
			}
			if len(n.Tags) > 0 {
				segs = append(segs, seg{" #" + strings.Join(n.Tags, " #"), StTag})
			}
			if !(n.Live && n.Name == "(live)") {
				segs = append(segs, seg{"  " + human(n.Alloc), StFaint})
			}
		}
		m.putSegs(c, 1, 2+i, tw, segs, m.dtop+i == m.dsel)
	}
	sx := m.W - 1
	if m.sideDetail() {
		sx = m.W - sidePanelW - 2
	}
	m.drawScroll(c, sx, 2, vis, len(m.rows), m.dtop)
	// info pane of the selected node: below the tree, or (wide terminals) as a panel on the right
	info := m.nodeInfo()
	if m.sideDetail() {
		x := m.W - sidePanelW
		for y := 2; y <= m.H-3; y++ {
			c.Put(x-1, y, g.v, StFrame)
		}
		c.Put(x, 2, L.InfoTitle, StBold)
		y := 4
		for _, ln := range info {
			for _, w := range wrap(ln.s, sidePanelW-2) {
				if y > m.H-3 {
					break
				}
				c.Put(x, y, w, ln.st)
				y++
			}
		}
		return
	}
	y := m.H - 5
	for x := 0; x < m.W; x++ {
		c.Put(x, y, g.h, StFrame)
	}
	for i, ln := range info {
		c.Put(1, y+1+i, trunc(ln.s, m.W-2), ln.st)
	}
}

const sidePanelW = 42

// sideDetail: terminals wide enough show the node info as a side panel (the tree keeps all rows).
func (m *Model) sideDetail() bool { return m.W >= 110 }

// detailVis is the number of tree rows for a body height bh.
func (m *Model) detailVis(bh int) int {
	if m.sideDetail() {
		return bh - 1
	}
	return bh - 5
}

// nodeInfo describes the selected tree row (parent, created, size, tags, note).
func (m *Model) nodeInfo() []seg {
	t := m.tree
	r := m.selRow()
	if r.IsBase {
		out := []seg{{"base: the original image (read-only, never modified). Revert to it, or boot it ephemeral.", StDim}}
		if len(t.Nodes) == 0 {
			out = append(out, seg{L.NoSnaps, StDim})
		}
		return out
	}
	n := r.Node
	parent := L.BaseNode
	for _, o := range t.Nodes {
		if o.Name == n.Parent && n.Parent != "" {
			parent = rowTitle(&o)
		}
	}
	if m.sideDetail() {
		return []seg{{rowTitle(n), StBold}, {"parent:  " + parent, StNormal}, {"created: " + n.Created, StNormal}, {"size:    " + human(n.Alloc), StFaint},
			{"tags:    " + strings.Join(n.Tags, ", "), StTag}, {"note:    " + n.Note, StNormal}}
	}
	return []seg{{fmt.Sprintf("%s  parent=%s  created=%s  size=%s", rowTitle(n), parent, n.Created, human(n.Alloc)), StBold},
		{"tags: " + strings.Join(n.Tags, ", "), StTag}, {"note: " + n.Note, StNormal}}
}

// pullSourceRows is how many rows the source list takes: all sources, or only the selected one while the catalog has focus.
func (m *Model) pullSourceRows() int {
	if m.pullCat && len(m.sources) > 0 {
		return 1
	}
	if len(m.sources) == 0 {
		return 1
	}
	return len(m.sources)
}

// catVis is the number of catalog rows for a body height hp (title row, sources, blank, catalog title, search box).
func (m *Model) catVis(hp int) int {
	v := hp - 2 - (m.pullSourceRows() + 5)
	if v < 1 {
		v = 1
	}
	return v
}

func (m *Model) drawPull(c *Canvas) {
	c.Put(1, 1, L.SourcesTitle, StBold)
	y := 2
	if len(m.sources) == 0 {
		c.Put(2, y, L.NoSources, StDim)
		y++
	}
	for i, s := range m.sources {
		if m.pullCat && i != m.ssel {
			continue
		}
		note := ""
		if s.Note != "" {
			note = "  (" + s.Note + ")"
		}
		line := fmt.Sprintf("%-14s %-13s %s%s", s.Name, s.Type, s.URL, note)
		if len(s.MirrorIDs) > 1 {
			line = fmt.Sprintf("%-16s %-13s mirror: %-9s %s%s", s.Name, s.Type, m.mirrorOf(s), s.URL, note)
		}
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
	c.Put(1, y, trunc(fmt.Sprintf("%s: %s", L.CatalogTitle, m.csource), m.W-14), StBold)
	if len(m.catalog) > 0 {
		m.putPos(c, y, m.csel, len(m.catalog))
	}
	y++
	box := m.filter.Text
	if m.pullCat {
		box += "_"
	} else if box == "" {
		box = L.SearchEmpty
	}
	cnt := ""
	if m.catAll != nil {
		cnt = "  (" + fmt.Sprintf(L.Matches, len(m.catalog), len(m.catAll)) + ")"
	}
	sst := StNormal
	if m.pullCat {
		sst = StBold
	}
	c.Put(1, y, trunc(L.SearchLabel+": "+box+cnt, m.W-2), sst)
	y++
	if m.catAll == nil {
		c.Put(2, y, L.NoCatalog, StDim)
		return
	}
	if len(m.catalog) == 0 {
		c.Put(2, y, L.NoMatch, StWarn)
		return
	}
	vis := m.catVis(m.H)
	sw := m.W - 46
	if sw < 34 {
		sw = 34
	}
	if sw > 60 {
		sw = 60
	}
	for i := 0; i < vis && m.ctop+i < len(m.catalog); i++ {
		it := m.catalog[m.ctop+i]
		kst := StNormal
		if it.Kind == "vm" {
			kst = StFrozen
		}
		m.putSegs(c, 1, y+i, m.W-3, []seg{
			{pad(it.Spec(), sw) + " ", StNormal}, {pad(it.Kind, 9) + " ", kst}, {pad(it.Version, 10) + " ", StFaint}, {human(it.Size), StFaint},
		}, m.pullCat && m.ctop+i == m.csel)
	}
	m.drawScroll(c, m.W-1, y, vis, len(m.catalog), m.ctop)
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
	c.Put(1, 1, trunc(title, m.W-14), StBold)
	vis := m.bh() - 1
	if top > len(lines)-vis {
		top = len(lines) - vis
	}
	if top < 0 {
		top = 0
	}
	if len(lines) > vis {
		m.putPos(c, 1, top, len(lines))
	}
	for i := 0; i < vis && top+i < len(lines); i++ {
		c.Put(1, 2+i, trunc(lines[top+i], m.W-3), StNormal)
	}
	m.drawScroll(c, m.W-1, 2, vis, len(lines), top)
}

func (m *Model) drawOp(c *Canvas) {
	o := m.op
	g := m.Th.g()
	fin, err := o.Finished()
	state, st := L.Running, StBold
	if fin {
		state, st = L.Done, StOK
		if err != nil {
			state, st = L.Failed, StErr
		}
		if o.Cancelled {
			state, st = L.Cancelled, StWarn
		}
	}
	c.Put(1, 1, trunc(o.Title+"  ["+state+"]", m.W-2), st)
	bar, _ := o.ProgressBar(m.Now(), m.barWidth(), g)
	c.Put(1, 2, trunc(bar, m.W-2), StNormal)
	lines := o.Lines()
	if fin && err != nil {
		lines = append(lines, "", L.Error+": "+err.Error())
	}
	y0 := 3
	if fin {
		// fixed result panel
		pst, verdict, mark := StOK, L.OpOK, g.ok
		switch {
		case o.Cancelled:
			pst, verdict, mark = StWarn, L.OpCancelled, g.bad
		case err != nil:
			pst, verdict, mark = StErr, L.OpFail, g.bad
		}
		c.Box(1, 3, m.W-2, 5, m.Th, pst)
		c.Put(3, 4, trunc(mark+" "+verdict+"  "+L.OpElapsed+" "+clock(o.Elapsed(m.Now())), m.W-6), pst)
		detail := L.OpSuccessText
		if err != nil {
			detail = err.Error()
		}
		c.Put(3, 5, trunc(detail, m.W-6), StNormal)
		c.Put(3, 6, trunc(L.OpDismiss+"   l: "+L.OpLog+"   Up/Down: scroll", m.W-6), StDim)
		y0 = 8
	} else {
		c.Put(1, 3, trunc(L.OpCancelHint+"   l: "+L.OpLog, m.W-2), StDim)
		y0 = 4
	}
	if m.opHideLog {
		c.Put(1, y0, L.OpLogOff, StDim)
		return
	}
	vis := m.H - 2 - y0
	if vis < 1 {
		return
	}
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
		c.Put(1, y0+i-top, trunc(l, m.W-3), s)
	}
	m.drawScroll(c, m.W-1, y0, vis, len(lines), top)
}

func (m *Model) barWidth() int {
	w := m.W - 52
	if w > 30 {
		w = 30
	}
	if w < 10 {
		w = 10
	}
	return w
}

// helpRows flattens the help pages (current screen's page first).
func (m *Model) helpRows() []seg {
	cur := 1
	switch {
	case m.op != nil:
		cur = 4
	case m.Screen == ScrDetail:
		cur = 2
	case m.Screen == ScrPull:
		cur = 3
	case m.Screen == ScrStorage || m.Screen == ScrNet || m.Screen == ScrSettings:
		cur = 5
	}
	order := []int{cur}
	for i := range L.HelpPages {
		if i != cur {
			order = append(order, i)
		}
	}
	var out []seg
	for n, i := range order {
		if i >= len(L.HelpPages) {
			continue
		}
		p := L.HelpPages[i]
		t := p.Title
		if n == 0 {
			t += "   <- " + L.HelpHere
		}
		out = append(out, seg{t, StBold})
		for _, l := range p.Lines {
			for j, w := range wrap(l, m.W-8) {
				if j > 0 {
					w = "    " + w
				}
				out = append(out, seg{"  " + w, StNormal})
			}
		}
		out = append(out, seg{"", StNormal})
	}
	return out
}

func (m *Model) drawHelp(c *Canvas) {
	c.Put(1, 1, L.HelpTitle+"  -  "+L.HelpScroll, StBold)
	rows := m.helpRows()
	vis := m.bh() - 1
	if m.helpTop > len(rows)-vis {
		m.helpTop = len(rows) - vis
	}
	if m.helpTop < 0 {
		m.helpTop = 0
	}
	for i := 0; i < vis && m.helpTop+i < len(rows); i++ {
		r := rows[m.helpTop+i]
		c.Put(1, 2+i, trunc(r.s, m.W-3), r.st)
	}
	m.drawScroll(c, m.W-1, 2, vis, len(rows), m.helpTop)
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
		btn := "[Enter/y] " + L.Yes + "    [Esc/n] " + L.No
		if d.Buttons != "" {
			btn = d.Buttons
		}
		lines = append(lines, "", btn)
	}
	h := len(lines) + 2
	if h > m.H-3 {
		h = m.H - 3
	}
	x, y := (m.W-w)/2, (m.H-h)/2
	st := StFrame
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
