package tui

import (
	"fmt"
	"os"
	"strings"
)

// Theme decides glyphs and colours.
type Theme struct {
	ASCII   bool // no UTF-8 box drawing
	Color   bool // at least 8 colours (legacy switch; Depth refines it)
	Depth   int  // 0 = unset (Color decides: 16 or mono), 8, 16, 256, 24 (truecolor)
	Console bool // Linux virtual console (TERM=linux): 16 colours, CP437-safe glyph subset only
	Round   bool // rounded frame corners (UTF-8 terminals that are not the Linux console)
}

// depth is the effective colour depth: 0 mono, 8/16 basic ANSI, 256, 24.
func (t Theme) depth() int {
	switch {
	case t.Depth != 0:
		return t.Depth
	case t.Color:
		return 16
	}
	return 0
}

// DetectTheme picks ASCII when forced or when the locale/terminal do not promise UTF-8. Colour depth: none for dumb/vt*,
// truecolor with COLORTERM=truecolor|24bit, 256 with TERM=*256color, 16 on the Linux console (TERM=linux) and otherwise.
func DetectTheme(getenv func(string) string, forceASCII bool) Theme {
	loc := strings.ToUpper(getenv("LC_ALL") + " " + getenv("LC_CTYPE") + " " + getenv("LANG"))
	if g := getenv("LC_ALL"); g != "" {
		loc = strings.ToUpper(g)
	}
	utf := strings.Contains(loc, "UTF-8") || strings.Contains(loc, "UTF8")
	term := getenv("TERM")
	t := Theme{ASCII: forceASCII || !utf || term == "" || term == "dumb" || strings.HasPrefix(term, "vt")}
	t.Console = term == "linux"
	switch ct := strings.ToLower(getenv("COLORTERM")); {
	case term == "" || term == "dumb" || strings.HasPrefix(term, "vt"):
		t.Depth = 0
	case t.Console:
		t.Depth = 16
	case ct == "truecolor" || ct == "24bit":
		t.Depth = 24
	case strings.Contains(term, "256color"):
		t.Depth = 256
	default:
		t.Depth = 16
	}
	t.Color = t.Depth >= 8
	t.Round = !t.ASCII && !t.Console
	return t
}

// EnvTheme is DetectTheme over the process environment.
func EnvTheme(forceASCII bool) Theme { return DetectTheme(os.Getenv, forceASCII) }

type glyphs struct{ h, v, tl, tr, bl, br, branch, last, pipe, blank, cur, up, track, thumb, ok, bad, spin string }

// g returns the glyph set: plain ASCII, the Linux-console-safe subset (light box drawing and the full block exist in CP437 and
// in Terminus; ●, ✔, braille and rounded corners do not), or the full UTF-8 set with rounded frames.
func (t Theme) g() glyphs {
	switch {
	case t.ASCII:
		return glyphs{"-", "|", "+", "+", "+", "+", "|-- ", "`-- ", "|   ", "    ", "*", "^", "|", "#", "OK", "X", `|/-\`}
	case t.Console:
		return glyphs{"─", "│", "┌", "┐", "└", "┘", "├── ", "└── ", "│   ", "    ", "*", "^", "│", "█", "OK", "X", `|/-\`}
	case t.Round:
		return glyphs{"─", "│", "╭", "╮", "╰", "╯", "├── ", "╰── ", "│   ", "    ", "●", "↑", "│", "█", "✔", "✘", "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"}
	}
	return glyphs{"─", "│", "┌", "┐", "└", "┘", "├── ", "└── ", "│   ", "    ", "●", "↑", "│", "█", "✔", "✘", "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"}
}

type rgb struct{ r, g, b uint8 }

func hex(v uint32) rgb { return rgb{uint8(v >> 16), uint8(v >> 8), uint8(v)} }

// pal is the rich (256 / truecolor) look of a style: Nord-like dark palette.
type pal struct {
	fg, bg rgb
	bold   bool
}

var (
	nordBG  = hex(0x2e3440)
	nordFG  = hex(0xd8dee9)
	nordHi  = hex(0xeceff4)
	nordBar = hex(0x3b4252)
	nordAcc = hex(0x5e81ac)
)

func palette(st Style) pal {
	switch st {
	case StSel:
		return pal{nordHi, nordAcc, true}
	case StBold:
		return pal{nordHi, nordBG, true}
	case StBase:
		return pal{nordHi, nordBG, true}
	case StDim:
		return pal{hex(0x81a1c1), nordBG, false}
	case StErr:
		return pal{hex(0xbf616a), nordBG, true}
	case StOK:
		return pal{hex(0xa3be8c), nordBG, false}
	case StWarn:
		return pal{hex(0xebcb8b), nordBG, true}
	case StBar, StKeys:
		return pal{nordFG, nordBar, false}
	case StTitle:
		return pal{nordBG, hex(0x81a1c1), true}
	case StTabOn:
		return pal{hex(0x88c0d0), nordBG, true}
	case StCur:
		return pal{hex(0xa3be8c), nordBG, true}
	case StFrozen:
		return pal{hex(0x88c0d0), nordBG, false}
	case StTag:
		return pal{hex(0xb48ead), nordBG, false}
	case StFaint:
		return pal{hex(0x7b88a1), nordBG, false}
	case StEph:
		return pal{hex(0xd08770), nordBG, false}
	case StFrame:
		return pal{hex(0x81a1c1), nordBG, false}
	}
	return pal{nordFG, nordBG, false}
}

// to256 maps an RGB colour to the nearest xterm-256 palette index (6x6x6 cube or the grey ramp).
func to256(c rgb) int {
	cube := func(v uint8) int {
		if v < 48 {
			return 0
		}
		if v < 115 {
			return 1
		}
		return int((int(v) - 35) / 40)
	}
	level := [6]int{0, 95, 135, 175, 215, 255}
	ri, gi, bi := cube(c.r), cube(c.g), cube(c.b)
	idx := 16 + 36*ri + 6*gi + bi
	dist := func(a, b, c2, x, y, z int) int { return (a-x)*(a-x) + (b-y)*(b-y) + (c2-z)*(c2-z) }
	best, bd := idx, dist(int(c.r), int(c.g), int(c.b), level[ri], level[gi], level[bi])
	avg := (int(c.r) + int(c.g) + int(c.b)) / 3
	gi2 := (avg - 8 + 5) / 10
	if gi2 < 0 {
		gi2 = 0
	}
	if gi2 > 23 {
		gi2 = 23
	}
	gv := 8 + 10*gi2
	if d := dist(int(c.r), int(c.g), int(c.b), gv, gv, gv); d < bd {
		best = 232 + gi2
	}
	return best
}

func sgrRich(st Style, th Theme) string {
	p := palette(st)
	attr := "0"
	if p.bold {
		attr = "0;1"
	}
	if th.depth() == 24 {
		return fmt.Sprintf("\x1b[%s;38;2;%d;%d;%d;48;2;%d;%d;%dm", attr, p.fg.r, p.fg.g, p.fg.b, p.bg.r, p.bg.g, p.bg.b)
	}
	return fmt.Sprintf("\x1b[%s;38;5;%d;48;5;%dm", attr, to256(p.fg), to256(p.bg))
}

// baseSGR is the style used to clear the screen (the dark background of the rich palettes; default colours otherwise).
func baseSGR(th Theme) string { return sgr(StNormal, th) }

func sgr(st Style, th Theme) string {
	if d := th.depth(); d == 24 || d == 256 {
		return sgrRich(st, th)
	}
	switch st {
	case StSel, StBar, StKeys:
		return "\x1b[0;7m"
	case StTitle:
		if th.Color {
			return "\x1b[0;1;37;44m"
		}
		return "\x1b[0;7m"
	case StTabOn:
		if th.Color {
			return "\x1b[0;1;30;46m"
		}
		return "\x1b[0;7;1m"
	case StBold, StBase:
		return "\x1b[0;1m"
	case StDim:
		if th.Color {
			return "\x1b[0;36m"
		}
		return "\x1b[0m"
	case StFrame:
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
	case StCur:
		if th.Color {
			return "\x1b[0;1;32m"
		}
		return "\x1b[0;1m"
	case StFrozen:
		if th.Color {
			return "\x1b[0;36m"
		}
		return "\x1b[0m"
	case StTag:
		if th.Color {
			return "\x1b[0;35m"
		}
		return "\x1b[0m"
	case StEph:
		if th.Color {
			return "\x1b[0;33m"
		}
		return "\x1b[0m"
	case StFaint:
		if th.Color {
			return "\x1b[0;2m"
		}
		return "\x1b[0m"
	}
	return "\x1b[0m"
}
