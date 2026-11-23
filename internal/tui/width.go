package tui

import "strings"

// runeWidth is the number of terminal cells of r: 0 (combining / zero width), 2 (East Asian wide, emoji) or 1.
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 0x20 || r == 0x7f:
		return 1 // control characters are drawn as a space
	case r < 0x300:
		return 1
	case r >= 0x300 && r <= 0x36f, r >= 0x200b && r <= 0x200f, r >= 0xfe00 && r <= 0xfe0f:
		return 0
	case r >= 0x1100 && r <= 0x115f, r >= 0x2e80 && r <= 0xa4cf, r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff, r >= 0xfe30 && r <= 0xfe6f, r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6, r >= 0x1f300 && r <= 0x1f64f, r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

// strWidth is the display width of s in cells.
func strWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// trunc cuts s to at most n cells, ending in "~" when something was removed.
func trunc(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if strWidth(s) <= n {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > n-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	if n == 1 {
		return ""
	}
	return b.String() + "~"
}

// pad cuts or pads s with spaces to exactly n cells.
func pad(s string, n int) string {
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > n {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + strings.Repeat(" ", n-w)
}
