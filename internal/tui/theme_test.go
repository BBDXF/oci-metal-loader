package tui

import (
	"strings"
	"testing"
)

func envOf(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

func TestDetectThemeDepth(t *testing.T) {
	utf := map[string]string{"LANG": "en_US.UTF-8"}
	with := func(extra map[string]string) Theme {
		e := map[string]string{}
		for k, v := range utf {
			e[k] = v
		}
		for k, v := range extra {
			e[k] = v
		}
		return DetectTheme(envOf(e), false)
	}
	cases := []struct {
		name     string
		th       Theme
		depth    int
		console  bool
		round    bool
		ascii    bool
		colorOn  bool
		wantDesc string
	}{
		{"truecolor", with(map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}), 24, false, true, false, true, ""},
		{"24bit", with(map[string]string{"TERM": "xterm", "COLORTERM": "24bit"}), 24, false, true, false, true, ""},
		{"256", with(map[string]string{"TERM": "xterm-256color"}), 256, false, true, false, true, ""},
		{"xterm", with(map[string]string{"TERM": "xterm"}), 16, false, true, false, true, ""},
		{"linux console", with(map[string]string{"TERM": "linux", "COLORTERM": "truecolor"}), 16, true, false, false, true, ""},
		{"vt100", with(map[string]string{"TERM": "vt100"}), 0, false, false, true, false, ""},
		{"no utf8", DetectTheme(envOf(map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}), false), 24, false, false, true, true, ""},
	}
	for _, c := range cases {
		if c.th.depth() != c.depth || c.th.Console != c.console || c.th.Round != c.round || c.th.ASCII != c.ascii || c.th.Color != c.colorOn {
			t.Errorf("%s: %+v", c.name, c.th)
		}
	}
}

func TestSGRDepths(t *testing.T) {
	tc := Theme{Depth: 24, Color: true}
	if got := sgr(StSel, tc); !strings.Contains(got, "38;2;") || !strings.Contains(got, "48;2;94;129;172") {
		t.Fatalf("truecolor sel: %q", got)
	}
	t256 := Theme{Depth: 256, Color: true}
	if got := sgr(StNormal, t256); !strings.Contains(got, "38;5;") || !strings.Contains(got, "48;5;") || strings.Contains(got, "38;2") {
		t.Fatalf("256: %q", got)
	}
	if sgr(StNormal, Theme{Color: true}) != "\x1b[0m" || sgr(StTitle, Theme{Color: true}) != "\x1b[0;1;37;44m" {
		t.Fatal("16-colour look must stay plain + blue title block")
	}
	if sgr(StTitle, Theme{}) != "\x1b[0;7m" || sgr(StKeys, Theme{}) != "\x1b[0;7m" {
		t.Fatal("mono title/keys are reverse video")
	}
	for _, st := range []Style{StSel, StErr, StOK, StWarn, StCur, StTitle, StTabOn, StKeys, StFrame, StFaint} {
		if strings.Contains(sgr(st, Theme{}), "3") && strings.Contains(sgr(st, Theme{}), "38") {
			t.Fatalf("mono leaks colour for %v", st)
		}
	}
	if baseSGR(tc) == "\x1b[0m" || baseSGR(Theme{Color: true}) != "\x1b[0m" {
		t.Fatal("rich themes clear with the dark background, 16-colour keeps the default")
	}
}

func TestTo256(t *testing.T) {
	for _, c := range []struct {
		c    rgb
		want int
	}{{rgb{0, 0, 0}, 16}, {rgb{255, 255, 255}, 231}, {rgb{255, 0, 0}, 196}, {rgb{0x80, 0x80, 0x80}, 244}} {
		if got := to256(c.c); got != c.want {
			t.Errorf("%v -> %d want %d", c.c, got, c.want)
		}
	}
	if n := to256(nordBG); n < 16 || n > 255 {
		t.Fatal(n)
	}
}

func TestGlyphSets(t *testing.T) {
	r := Theme{Round: true}.g()
	if r.tl != "╭" || r.br != "╯" || r.cur != "●" {
		t.Fatalf("rounded: %+v", r)
	}
	c := Theme{Console: true}.g()
	for _, s := range []string{c.tl, c.tr, c.bl, c.br, c.h, c.v, c.cur, c.ok, c.bad, c.spin, c.thumb, c.branch, c.last} {
		for _, ch := range s {
			if ch > 0x7f && !strings.ContainsRune("─│┌┐└┘├█", ch) {
				t.Fatalf("console glyph %q outside the CP437 subset", ch)
			}
		}
	}
	a := Theme{ASCII: true}.g()
	for _, s := range []string{a.tl, a.h, a.v, a.cur, a.thumb, a.spin} {
		for _, ch := range s {
			if ch > 0x7f {
				t.Fatal("ascii")
			}
		}
	}
}

func TestRoundedBoxAndTitleBlock(t *testing.T) {
	m := New(newFake(), Theme{Round: true, Depth: 24, Color: true})
	m.Init()
	m.Update(R('P'))
	s := screen(m)
	if !strings.Contains(s, "╭") || !strings.Contains(s, "╯") {
		t.Fatalf("rounded dialog frame:\n%s", s)
	}
	c := m.View()
	if c.Cells[0][5].St != StTitle {
		t.Fatalf("tab row is the title block: %v", c.Cells[0][5].St)
	}
	if c.Cells[m.H-1][3].St != StKeys {
		t.Fatal("key bar style")
	}
}

func TestWideLayoutFrames(t *testing.T) {
	m := New(newFake(), Theme{Round: true, Depth: 24, Color: true})
	m.Init()
	m.Resize(120, 36)
	goldenFrame(t, "wide_images", m)
	m.Update(R('o'))
	m.Update(R('j'))
	goldenFrame(t, "wide_detail", m)
	if !strings.Contains(screen(m), "Selected") || !strings.Contains(screen(m), "parent:") {
		t.Fatal("side panel")
	}
	for _, l := range m.View().Lines() {
		if strWidth(l) > 120 {
			t.Fatalf("too wide: %q", l)
		}
	}
	m.Resize(100, 30)
	if m.sideDetail() {
		t.Fatal("100 columns keep the bottom pane")
	}
	goldenFrame(t, "mid_detail", m)
	// 80x24 never uses the side panel
	m.Resize(80, 24)
	if strings.Contains(screen(m), "Selected") {
		t.Fatal("80x24 has no side panel")
	}
}
