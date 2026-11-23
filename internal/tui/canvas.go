package tui

import "strings"

// Style of a cell.
type Style uint8

const (
	StNormal Style = iota
	StSel          // reverse video
	StBold
	StDim
	StErr
	StOK
	StWarn
	StBar    // tab/status bar
	StCur    // live state marker/row (green bold)
	StFrozen // frozen snapshot (cyan)
	StTag    // tags (magenta)
	StFaint  // sizes, secondary text (dim)
	StEph    // ephemeral (yellow)
	StBase   // base image (bold)
	StTitle  // title bar block (tab row)
	StTabOn  // active tab inside the title block
	StKeys   // bottom key bar
	StFrame  // box frames
)

// Cell is one screen cell.
type Cell struct {
	R  rune
	St Style
}

// Canvas is a W x H grid of cells.
type Canvas struct {
	W, H  int
	Cells [][]Cell
	oy    int // vertical offset applied by Put/Fill (body drawing below the header)
}

// NewCanvas allocates a blank canvas.
func NewCanvas(w, h int) *Canvas {
	c := &Canvas{W: w, H: h, Cells: make([][]Cell, h)}
	for y := range c.Cells {
		c.Cells[y] = make([]Cell, w)
		for x := range c.Cells[y] {
			c.Cells[y][x] = Cell{' ', StNormal}
		}
	}
	return c
}

// Put writes s at (x,y), clipped to the canvas; control characters become spaces. Wide (CJK) runes take two cells
// (the second holds R == 0 and is skipped on output); zero-width runes are dropped.
func (c *Canvas) Put(x, y int, s string, st Style) {
	y += c.oy
	if y < 0 || y >= c.H {
		return
	}
	for _, r := range s {
		w := runeWidth(r)
		if w == 0 {
			continue
		}
		if x+w > c.W {
			return
		}
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		if x >= 0 {
			c.Cells[y][x] = Cell{r, st}
			if w == 2 {
				c.Cells[y][x+1] = Cell{0, st}
			}
		}
		x += w
	}
}

// Fill paints a rectangle's style/blank.
func (c *Canvas) Fill(x, y, w, h int, st Style) {
	y += c.oy
	for j := y; j < y+h && j < c.H; j++ {
		for i := x; i < x+w && i < c.W; i++ {
			if i >= 0 && j >= 0 {
				c.Cells[j][i] = Cell{' ', st}
			}
		}
	}
}

// Box draws a frame.
func (c *Canvas) Box(x, y, w, h int, th Theme, st Style) {
	g := th.g()
	c.Fill(x, y, w, h, StNormal)
	for i := 1; i < w-1; i++ {
		c.Put(x+i, y, g.h, st)
		c.Put(x+i, y+h-1, g.h, st)
	}
	for j := 1; j < h-1; j++ {
		c.Put(x, y+j, g.v, st)
		c.Put(x+w-1, y+j, g.v, st)
	}
	c.Put(x, y, g.tl, st)
	c.Put(x+w-1, y, g.tr, st)
	c.Put(x, y+h-1, g.bl, st)
	c.Put(x+w-1, y+h-1, g.br, st)
}

// Lines returns the plain text of every row (trailing spaces trimmed) - used by tests.
func (c *Canvas) Lines() []string {
	out := make([]string, c.H)
	for y, row := range c.Cells {
		var b strings.Builder
		for _, cl := range row {
			if cl.R != 0 {
				b.WriteRune(cl.R)
			}
		}
		out[y] = strings.TrimRight(b.String(), " ")
	}
	return out
}

// Text is Lines joined.
func (c *Canvas) Text() string { return strings.Join(c.Lines(), "\n") }

// ANSI renders row y with escape sequences (one string per row, no trailing newline).
func (c *Canvas) ANSI(y int, th Theme) string {
	var b strings.Builder
	cur := Style(255)
	for _, cl := range c.Cells[y] {
		if cl.St != cur {
			b.WriteString(sgr(cl.St, th))
			cur = cl.St
		}
		if cl.R != 0 {
			b.WriteRune(cl.R)
		}
	}
	b.WriteString("\x1b[0m")
	return b.String()
}
