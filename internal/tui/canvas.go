package tui

import (
	"os"
	"strings"
)

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
	StBar // tab/status bar
)

// Theme decides glyphs and colours.
type Theme struct {
	ASCII bool // no UTF-8 box drawing
	Color bool
}

// DetectTheme picks ASCII when forced or when the locale/terminal do not promise UTF-8; colour unless TERM is dumb/vt100.
func DetectTheme(getenv func(string) string, forceASCII bool) Theme {
	loc := strings.ToUpper(getenv("LC_ALL") + " " + getenv("LC_CTYPE") + " " + getenv("LANG"))
	if g := getenv("LC_ALL"); g != "" {
		loc = strings.ToUpper(g)
	}
	utf := strings.Contains(loc, "UTF-8") || strings.Contains(loc, "UTF8")
	term := getenv("TERM")
	t := Theme{ASCII: forceASCII || !utf || term == "" || term == "dumb" || strings.HasPrefix(term, "vt")}
	t.Color = term != "" && term != "dumb" && !strings.HasPrefix(term, "vt")
	return t
}

// EnvTheme is DetectTheme over the process environment.
func EnvTheme(forceASCII bool) Theme { return DetectTheme(os.Getenv, forceASCII) }

type glyphs struct{ h, v, tl, tr, bl, br, branch, last, pipe, blank, cur, up string }

func (t Theme) g() glyphs {
	if t.ASCII {
		return glyphs{"-", "|", "+", "+", "+", "+", "|-- ", "`-- ", "|   ", "    ", "*", "^"}
	}
	return glyphs{"─", "│", "┌", "┐", "└", "┘", "├── ", "└── ", "│   ", "    ", "●", "↑"}
}

// Cell is one screen cell.
type Cell struct {
	R  rune
	St Style
}

// Canvas is a W x H grid of cells.
type Canvas struct {
	W, H  int
	Cells [][]Cell
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

// Put writes s at (x,y), clipped to the canvas; control characters become spaces.
func (c *Canvas) Put(x, y int, s string, st Style) {
	if y < 0 || y >= c.H {
		return
	}
	for _, r := range s {
		if x >= c.W {
			return
		}
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		if x >= 0 {
			c.Cells[y][x] = Cell{r, st}
		}
		x++
	}
}

// Fill paints a rectangle's style/blank.
func (c *Canvas) Fill(x, y, w, h int, st Style) {
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
			b.WriteRune(cl.R)
		}
		out[y] = strings.TrimRight(b.String(), " ")
	}
	return out
}

// Text is Lines joined.
func (c *Canvas) Text() string { return strings.Join(c.Lines(), "\n") }

func sgr(st Style, th Theme) string {
	switch st {
	case StSel:
		return "\x1b[0;7m"
	case StBar:
		return "\x1b[0;7m"
	case StBold:
		return "\x1b[0;1m"
	case StDim:
		if th.Color {
			return "\x1b[0;36m"
		}
		return "\x1b[0m"
	case StErr:
		if th.Color {
			return "\x1b[0;1;31m"
		}
		return "\x1b[0;1m"
	case StOK:
		if th.Color {
			return "\x1b[0;32m"
		}
		return "\x1b[0m"
	case StWarn:
		if th.Color {
			return "\x1b[0;1;33m"
		}
		return "\x1b[0;1m"
	}
	return "\x1b[0m"
}

// ANSI renders row y with escape sequences (one string per row, no trailing newline).
func (c *Canvas) ANSI(y int, th Theme) string {
	var b strings.Builder
	cur := Style(255)
	for _, cl := range c.Cells[y] {
		if cl.St != cur {
			b.WriteString(sgr(cl.St, th))
			cur = cl.St
		}
		b.WriteRune(cl.R)
	}
	b.WriteString("\x1b[0m")
	return b.String()
}
