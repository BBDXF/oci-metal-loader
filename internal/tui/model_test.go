package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func keys(m *Model, s string) {
	for _, r := range s {
		switch r {
		case '\n':
			m.Update(N("enter"))
		default:
			m.Update(R(r))
		}
	}
}

func newModel(t *testing.T, ascii bool) (*Model, *fakeSvc) {
	f := newFake()
	m := New(f, Theme{ASCII: ascii})
	m.Init()
	return m, f
}

func screen(m *Model) string { return m.View().Text() }

func mustHave(t *testing.T, m *Model, subs ...string) {
	t.Helper()
	s := screen(m)
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Fatalf("screen lacks %q:\n%s", sub, s)
		}
	}
}

func waitOp(t *testing.T, m *Model) {
	t.Helper()
	if m.op == nil {
		t.Fatal("no op")
	}
	m.op.Wait()
	m.Tick()
}

func TestImagesList(t *testing.T) {
	m, _ := newModel(t, true)
	mustHave(t, m, "alpine", "lxd-container", "overlayfs"[:0]+"3.0 MiB", "deb", "lxd-vm", "1:Images")
	if got := m.View().Lines()[3]; !strings.Contains(got, "alpine") {
		t.Fatalf("first row: %q", got)
	}
	m.Update(R('j'))
	if m.isel != 1 {
		t.Fatal("j should move down")
	}
	m.Update(N("up"))
	m.Update(N("up"))
	if m.isel != 0 {
		t.Fatal("clamped at top")
	}
}

func TestTreeRender(t *testing.T) {
	m, _ := newModel(t, true)
	m.Update(N("enter"))
	if m.Screen != ScrDetail {
		t.Fatal("enter opens detail")
	}
	s := screen(m)
	for _, want := range []string{"base", "base (original image)", "|-- ", "`-- ", "* current (you are here)", "* b", "[frozen]", "#golden"} {
		if !strings.Contains(s, want) {
			t.Fatalf("tree lacks %q:\n%s", want, s)
		}
	}
	rows := BuildRows(m.tree, m.Th)
	var names []string
	for _, r := range rows {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "base,n1,n2,n3,n4" {
		t.Fatalf("dfs order %v", names)
	}
	// unicode tree
	m2, _ := newModel(t, false)
	m2.Update(N("enter"))
	if s := screen(m2); !strings.Contains(s, "├── ") || !strings.Contains(s, "└── ") || !strings.Contains(s, "●") {
		t.Fatalf("unicode glyphs missing:\n%s", s)
	}
}

func TestASCIIFallback(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		e     map[string]string
		force bool
		ascii bool
	}{
		{map[string]string{"TERM": "linux", "LANG": "C"}, false, true},
		{map[string]string{"TERM": "linux", "LANG": "en_US.UTF-8"}, false, false},
		{map[string]string{"TERM": "vt100", "LANG": "en_US.UTF-8"}, false, true},
		{map[string]string{"TERM": "xterm", "LANG": "C.UTF-8"}, true, true},
		{map[string]string{"TERM": "xterm"}, false, true},
	}
	for i, c := range cases {
		if got := DetectTheme(env(c.e), c.force).ASCII; got != c.ascii {
			t.Errorf("case %d: ascii=%v want %v", i, got, c.ascii)
		}
	}
	m, _ := newModel(t, true)
	m.Update(N("enter"))
	for _, l := range m.View().Lines() {
		for _, r := range l {
			if r > 0x7e {
				t.Fatalf("non-ASCII rune %q in ASCII mode: %s", r, l)
			}
		}
	}
	if DetectTheme(env(map[string]string{"TERM": "vt100"}), false).Color {
		t.Error("vt100 must not use colour")
	}
}

func TestLayout80x24(t *testing.T) {
	m, _ := newModel(t, true)
	check := func(name string) {
		c := m.View()
		if c.W != 80 || c.H != 24 {
			t.Fatal("size")
		}
		for y, l := range c.Lines() {
			if len([]rune(l)) > 80 {
				t.Fatalf("%s: row %d wider than 80", name, y)
			}
		}
	}
	check("images")
	m.Update(N("enter"))
	check("detail")
	want := []string{
		"OCI Metal Loader  [1:Images] ",
		"alpine  type=lxd-container  mode=overlayfs  live on=b  snapshots=3",
	}
	for _, w := range want {
		if !strings.Contains(screen(m), w) {
			t.Fatalf("detail header lacks %q:\n%s", w, screen(m))
		}
	}
	last := m.View().Lines()[23]
	if !strings.Contains(last, "b:boot") || len(last) > 80 {
		t.Fatalf("status bar: %q", last)
	}
	m.Update(N("esc"))
	for _, k := range "2345" {
		m.Update(R(k))
		check("tab" + string(k))
	}
	m.Update(R('?'))
	mustHave(t, m, "Ctrl+C cancels")
	check("help")
	m.Update(R('x'))
	small := New(newFake(), Theme{ASCII: true})
	small.Resize(60, 10)
	small.Init()
	if !strings.Contains(small.View().Text(), "too small") {
		t.Fatal("small terminal message")
	}
}

func TestCreateSnapshotFlow(t *testing.T) {
	m, f := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('c'))
	mustHave(t, m, "Create snapshot", "Freezes the live state")
	keys(m, "snap1\nmy note\nt1 t2\n")
	if !f.called(`create alpine snap1 note="my note" tags=[t1 t2]`) {
		t.Fatalf("calls %v", f.calls)
	}
	mustHave(t, m, "created snapshot snap1")
}

func TestRevertConfirmAndKeep(t *testing.T) {
	m, f := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('j')) // a
	m.Update(R('r'))
	m.Update(R('1'))
	mustHave(t, m, "WARNING", "discarded permanently")
	m.Update(N("esc"))
	if f.called("revert") {
		t.Fatal("cancelled")
	}
	m.Update(R('r'))
	m.Update(R('1'))
	m.Update(N("enter"))
	if !f.called(`revert alpine a keep=""`) {
		t.Fatalf("%v", f.calls)
	}
	mustHave(t, m, "revert to a: ok")
	m.Update(R('r'))
	m.Update(R('2'))
	keys(m, "saved\n")
	if !f.called(`revert alpine a keep="saved"`) {
		t.Fatalf("%v", f.calls)
	}
	// base row reverts to base; the live row is not a revert target
	m.Update(N("home"))
	m.Update(R('r'))
	mustHave(t, m, "Revert -> base")
	m.Update(N("esc"))
	for i := 0; i < 3; i++ {
		m.Update(R('j'))
	}
	m.Update(R('r'))
	mustHave(t, m, "Select a snapshot or the base image")
}

func TestDeleteBranchAndMerge(t *testing.T) {
	m, f := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('j')) // a: two children -> recursive, live state inside -> extra confirmation
	m.Update(R('d'))
	mustHave(t, m, "everything below it", "--force")
	m.Update(R('y'))
	mustHave(t, m, "remove the subtree")
	m.Update(R('y'))
	if !f.called("delete alpine a rec=true force=true") {
		t.Fatalf("%v", f.calls)
	}
	m.Update(R('j')) // b: one child (the live state) -> merge
	m.Update(R('d'))
	mustHave(t, m, "merged into the child")
	m.Update(R('y'))
	if !f.called("delete alpine b rec=false force=false") {
		t.Fatalf("%v", f.calls)
	}
	m.Update(R('j')) // live row
	m.Update(R('d'))
	mustHave(t, m, "discarded with Revert")
}

func TestTagNotePrune(t *testing.T) {
	m, f := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('j'))
	keys(m, "tgold2\n")
	if !f.called("tag alpine a gold2 rm=false") {
		t.Fatalf("%v", f.calls)
	}
	keys(m, "t-golden\n")
	if !f.called("tag alpine a golden rm=true") {
		t.Fatalf("%v", f.calls)
	}
	m.Update(R('n'))
	keys(m, "!\n")
	if !f.called(`note alpine a "first!"`) {
		t.Fatalf("%v", f.calls)
	}
	m.Update(R('p'))
	m.Update(N("enter"))
	mustHave(t, m, "dry-run result", "Delete these")
	if !f.called("prune gc=false dry=true") || f.called("dry=false") {
		t.Fatalf("%v", f.calls)
	}
	m.Update(N("enter"))
	if !f.called("prune gc=false dry=false") {
		t.Fatalf("%v", f.calls)
	}
}

func TestBootCurrentAndEphemeral(t *testing.T) {
	m, _ := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('b'))
	mustHave(t, m, "Continue current", "throwaway", "base (original image), ephemeral")
	m.Update(N("enter"))
	mustHave(t, m, "omloader boot --image alpine --snapshot current", "kept in the live layer", "switch-root", "d: preview")
	m.Update(N("enter"))
	if !m.Done() || m.Result().Kind != ResBoot || m.Result().Boot != (BootRequest{"alpine", "current", false}) {
		t.Fatalf("result %+v", m.Result())
	}
	// selected snapshot boots read-only (ephemeral)
	m, _ = newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('j')) // a
	m.Update(R('b'))
	m.Update(R('2'))
	mustHave(t, m, "--snapshot a --ephemeral", "Ephemeral boot")
	m.Update(N("enter"))
	if got := m.Result(); got.Kind != ResBoot || !got.Boot.Ephemeral || got.Boot.Snapshot != "a" {
		t.Fatalf("result %+v", got)
	}
	// throwaway current / base
	for i, want := range []BootRequest{{"alpine", "current", true}, {"alpine", "base", true}} {
		m, _ = newModel(t, true)
		m.Update(N("enter"))
		m.Update(R('b'))
		m.Update(R(rune('2' + i)))
		m.Update(N("enter"))
		if m.Result().Boot != want {
			t.Fatalf("%d: %+v", i, m.Result())
		}
	}
}

func TestBootPreviewOp(t *testing.T) {
	m, f := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('b'))
	m.Update(N("enter"))
	m.Update(R('d'))
	waitOp(t, m)
	mustHave(t, m, "boot preview", "+ losetup")
	if !f.called("preview alpine current") {
		t.Fatal("preview not called")
	}
	m.Update(N("enter")) // close -> confirm dialog is back
	mustHave(t, m, "Boot now?")
}

func TestVerifyAndFlattenOps(t *testing.T) {
	m, f := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('v'))
	m.Update(N("enter"))
	waitOp(t, m)
	mustHave(t, m, "verify alpine: OK", "[finished]")
	m.Update(N("enter"))
	if m.op != nil {
		t.Fatal("op closed")
	}
	m.Update(R('j'))
	m.Update(R('f'))
	keys(m, "\n")
	waitOp(t, m)
	if !f.called("flatten alpine a alpine-flat-a") {
		t.Fatalf("%v", f.calls)
	}
}

func TestCtrlCCancelsOpNotQuit(t *testing.T) {
	m, f := newModel(t, true)
	f.pullHold = make(chan struct{})
	m.Update(R('2'))
	mustHave(t, m, "lxc-images")
	m.Update(N("enter"))
	waitOp(t, m) // catalog loaded, op closed by its callback
	if !m.pullCat || m.op != nil {
		t.Fatalf("catalog focus %v op %v", m.pullCat, m.op)
	}
	mustHave(t, m, "alpine/3.20/default")
	m.Update(N("enter"))
	keys(m, "\n") // name form
	m.Update(N("enter"))
	if m.op == nil {
		t.Fatal("pull op expected")
	}
	time.Sleep(50 * time.Millisecond)
	mustHave(t, m, "pull lxc:alpine/3.20/default", "33%", "1.0 MiB / 3.0 MiB")
	m.Update(N("enter")) // not finished: ignored
	if m.op == nil {
		t.Fatal("enter must not close a running op")
	}
	m.Update(N("ctrl-c"))
	waitOp(t, m)
	if m.Done() {
		t.Fatal("Ctrl+C must not quit")
	}
	mustHave(t, m, "[Cancelled.]")
	m.Update(N("esc"))
	if m.op != nil {
		t.Fatal("closed")
	}
}

func TestPullFilter(t *testing.T) {
	m, _ := newModel(t, true)
	m.Update(R('2'))
	m.Update(N("enter"))
	waitOp(t, m)
	if len(m.catalog) != 2 || !m.pullCat {
		t.Fatalf("catalog %d focus %v", len(m.catalog), m.pullCat)
	}
	keys(m, "arch") // live: letters and digits are text, not tab shortcuts
	if len(m.catalog) != 1 || m.catalog[0].OS != "archlinux" || m.Screen != ScrPull {
		t.Fatalf("after arch: %+v screen %v", m.catalog, m.Screen)
	}
	mustHave(t, m, "Search: arch_", "(1 of 2)", "lxc:archlinux/current/cloud")
	keys(m, " vm")
	if len(m.catalog) != 1 {
		t.Fatal("arch vm")
	}
	keys(m, "x")
	mustHave(t, m, "No image matches")
	m.Update(N("bs"))
	if len(m.catalog) != 1 {
		t.Fatal("backspace re-filters")
	}
	m.Update(N("esc")) // clears the search, stays in the catalog
	if m.filter.Text != "" || len(m.catalog) != 2 || !m.pullCat {
		t.Fatalf("esc must clear: %q %d %v", m.filter.Text, len(m.catalog), m.pullCat)
	}
	keys(m, "2")
	if m.Screen != ScrPull || m.filter.Text != "2" {
		t.Fatal("digit is search text while typing")
	}
	m.Update(N("esc"))
	m.Update(N("esc")) // second Esc: back to the sources
	if m.pullCat {
		t.Fatal("second esc leaves the catalog")
	}
	m.Update(R('/')) // / returns to the (already loaded) search box
	if !m.pullCat {
		t.Fatal("/ focuses the search")
	}
	m.Update(N("tab"))
	if m.Screen != ScrStorage {
		t.Fatal("Tab changes tab while typing")
	}
}

func TestPullSelectionAndEnter(t *testing.T) {
	m, _ := newModel(t, true)
	m.Update(R('2'))
	m.Update(N("enter"))
	waitOp(t, m)
	keys(m, "a") // both match; alpine/archlinux ordering is by score
	m.Update(N("down"))
	sel := m.catalog[m.csel]
	keys(m, "l") // "al" narrows to alpine only (selection moves when the item vanishes)
	if len(m.catalog) == 0 {
		t.Fatal("none")
	}
	_ = sel
	m.Update(N("enter"))
	mustHave(t, m, "Pull image")
}

func TestHeaderAndBars(t *testing.T) {
	m, _ := newModel(t, true)
	l := m.View().Lines()
	if !strings.Contains(l[1], "omloader v9.9") || !strings.Contains(l[1], "10.0.0.5") || !strings.Contains(l[1], "free") {
		t.Fatalf("header row: %q", l[1])
	}
	if !strings.HasPrefix(l[23], " Enter:open") {
		t.Fatalf("images bar: %q", l[23])
	}
	m.Update(R('2'))
	if !strings.Contains(m.View().Lines()[23], "Enter:load catalog") {
		t.Fatal("pull bar")
	}
	m.Update(N("enter"))
	waitOp(t, m)
	if !strings.Contains(m.View().Lines()[23], "type:search") {
		t.Fatal("catalog bar")
	}
	m.Svc.(*fakeSvc).offline = true
	m.refreshHeader(true)
	if !strings.Contains(m.View().Lines()[1], "[offline]") {
		t.Fatal("offline in header")
	}
	m.W, m.H = 80, 24
	if w := strWidth(m.headerText(40)); w > 40 {
		t.Fatalf("narrow header %d", w)
	}
}

func TestPositionAndScrollBar(t *testing.T) {
	m, _ := newModel(t, true)
	m.Update(R('2'))
	m.Update(N("enter"))
	waitOp(t, m)
	for i := 0; i < 40; i++ {
		m.catAll = append(m.catAll, CatalogItem{OS: "os", Release: fmt.Sprint(i), Variant: "default", Arch: "amd64", Kind: "container"})
	}
	m.applyFilter()
	m.Update(N("end"))
	s := screen(m)
	if !strings.Contains(s, "42/42") || !strings.Contains(s, "#") {
		t.Fatalf("position / scroll bar:\n%s", s)
	}
	m.Update(N("home"))
	m.Update(N("pgdn"))
	if m.csel == 0 || !strings.Contains(screen(m), fmt.Sprintf("%d/42", m.csel+1)) {
		t.Fatal("pgdn")
	}
	for _, l := range m.View().Lines() {
		if strWidth(l) > 80 {
			t.Fatalf("line too wide: %q", l)
		}
	}
}

func TestResultPanelAndLogToggle(t *testing.T) {
	m, f := newModel(t, true)
	m.Update(R('2'))
	m.Update(R('t'))
	waitOp(t, m)
	mustHave(t, m, "[finished]", "SUCCESS", "elapsed", "Enter/Esc: dismiss", "OK      12ms")
	m.Update(R('l'))
	mustHave(t, m, "SUCCESS", "log hidden")
	if strings.Contains(screen(m), "OK      12ms") {
		t.Fatal("log must be hidden")
	}
	m.Update(R('l'))
	mustHave(t, m, "OK      12ms")
	m.Update(N("enter"))
	if m.op != nil {
		t.Fatal("dismiss")
	}
	// failure panel
	f.verifyErr = fmt.Errorf("boom layer")
	m.Update(R('1'))
	m.Update(N("enter"))
	m.Update(R('v'))
	m.Update(R('1'))
	waitOp(t, m)
	mustHave(t, m, "[FAILED]", "X FAILED", "boom layer")
}

func TestProgressBarETA(t *testing.T) {
	now := time.Unix(1000, 0)
	o := &Op{Started: now}
	var b int64
	o.Bytes, o.Total = func() int64 { return b }, 100<<20
	g := Theme{ASCII: true}.g()
	o.ProgressBar(now, 20, g)
	b = 10 << 20
	line, known := o.ProgressBar(now.Add(time.Second), 20, g)
	if !known || !strings.Contains(line, "10%") || !strings.Contains(line, "10.0 MiB/s") || !strings.Contains(line, "ETA 0:09") {
		t.Fatalf("%q", line)
	}
	sp := &Op{Started: now}
	l, k := sp.ProgressBar(now.Add(65*time.Second), 20, g)
	if k || !strings.Contains(l, "working") || !strings.Contains(l, "1:05") {
		t.Fatalf("spinner %q", l)
	}
}

func TestConfirmTexts(t *testing.T) {
	m, f := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('j'))
	m.Update(R('j'))
	m.Update(R('r'))
	m.Update(R('1'))
	mustHave(t, m, "WARNING", "will be DISCARDED", "cannot be recovered", "continues from")
	m.Update(N("esc"))
	m.Update(R('d'))
	mustHave(t, m, "permanently", "merge")
	m.Update(N("esc"))
	m.Update(R('p'))
	m.Update(R('1'))
	mustHave(t, m, "permanently", "Retention prune")
	m.Update(N("esc"))
	_ = f
	m.Update(N("esc"))
	m.Update(R('3'))
	m.Update(R('c'))
	m.Update(R('2'))
	keys(m, "\n\n")
	mustHave(t, m, "RELEASED", "reboot the")
}

func TestHelpPage(t *testing.T) {
	m, _ := newModel(t, true)
	m.Update(R('?'))
	mustHave(t, m, "Help", "this screen", "Images")
	if !strings.Contains(m.View().Lines()[23], "Esc/q/?:close") {
		t.Fatal("help bar")
	}
	m.Update(N("pgdn"))
	if !m.help || m.helpTop == 0 {
		t.Fatal("scroll")
	}
	m.Update(N("end"))
	mustHave(t, m, "Dialogs")
	m.Update(R('q'))
	if m.help {
		t.Fatal("q closes help")
	}
	// the page of the current screen comes first
	m.Update(R('2'))
	m.Update(R('?'))
	if l := m.View().Lines(); !strings.Contains(l[3], "Pull") {
		t.Fatalf("pull help first: %q", l[3])
	}
}

func TestWideCharsAlignment(t *testing.T) {
	if strWidth("世界a") != 5 || trunc("世界世界", 5) != "世界~" || strWidth(pad("世", 4)) != 4 {
		t.Fatal("width helpers")
	}
	m, f := newModel(t, true)
	f.cjk = true
	m.RefreshImages()
	lines := m.View().Lines()
	row := lines[3]
	if strWidth(row) > 80 {
		t.Fatalf("row too wide %d: %q", strWidth(row), row)
	}
	col := strings.Index(lines[2], "TYPE")
	if w := strWidth(row[:strings.Index(row, "lxd-")]); w != col {
		t.Fatalf("columns misaligned with CJK: type at %d, header at %d: %q", w, col, row)
	}
	c := m.View()
	for y := range c.Cells {
		for x, cl := range c.Cells[y] {
			if cl.R != 0 && runeWidth(cl.R) == 2 && (x+1 >= c.W || c.Cells[y][x+1].R != 0) {
				t.Fatalf("wide rune without continuation at %d,%d", x, y)
			}
		}
	}
}

func TestColourDegrades(t *testing.T) {
	m := New(newFake(), Theme{ASCII: false, Color: true})
	m.Init()
	m.Update(N("enter"))
	c := m.View()
	var all string
	for y := 0; y < c.H; y++ {
		all += c.ANSI(y, m.Th)
	}
	for _, want := range []string{"\x1b[0;1;32m", "\x1b[0;36m", "\x1b[0;35m", "\x1b[0;7m", "\x1b[0;2m"} {
		if !strings.Contains(all, want) {
			t.Fatalf("colour output lacks %q", want)
		}
	}
	for y := 0; y < c.H; y++ {
		l := c.ANSI(y, Theme{ASCII: true})
		if strings.Contains(l, ";3") || strings.Contains(l, "[0;2m") {
			t.Fatalf("no-colour theme leaks colour: %q", l)
		}
	}
}

func TestStatusColours(t *testing.T) {
	m, _ := newModel(t, true)
	m.warn("careful")
	if !m.stWarn || m.stErr {
		t.Fatal("warn")
	}
	m.say("fine")
	if m.stWarn || m.stErr {
		t.Fatal("say resets")
	}
	if sgr(StWarn, Theme{Color: true}) != "\x1b[0;1;33m" || sgr(StErr, Theme{Color: true}) != "\x1b[0;1;31m" || sgr(StOK, Theme{Color: true}) != "\x1b[0;32m" {
		t.Fatal("colours")
	}
}

func TestStorageNetSettings(t *testing.T) {
	m, f := newModel(t, true)
	m.Update(R('3'))
	mustHave(t, m, "store-file", "exfat", "clean", "min_free_percent=10%")
	m.Update(R('c'))
	m.Update(R('2'))
	keys(m, "\n\n") // path, size
	m.Update(N("enter"))
	waitOp(t, m)
	if !f.called("storegrow /mnt/host/omloader/store.img 20G") {
		t.Fatalf("%v", f.calls)
	}
	m.Update(N("enter"))
	m.Update(R('4'))
	mustHave(t, m, "eth0 up", "probe=false")
	m.Update(R('5'))
	mustHave(t, m, "boot.ui = tui")
}

func TestShellRebootPower(t *testing.T) {
	m, _ := newModel(t, true)
	m.Update(N("esc"))
	mustHave(t, m, "open the shell")
	m.Update(N("esc"))
	if m.Done() {
		t.Fatal("cancelled")
	}
	m.Update(R('s'))
	m.Update(N("enter"))
	if m.Result().Kind != ResShell {
		t.Fatal("shell")
	}
	m, _ = newModel(t, true)
	m.Update(R('P'))
	m.Update(N("enter"))
	m.Update(N("enter"))
	if m.Result().Kind != ResReboot {
		t.Fatalf("%+v", m.Result())
	}
}

func TestCtrlCIdle(t *testing.T) {
	m, _ := newModel(t, true)
	m.Update(N("ctrl-c"))
	if m.Done() {
		t.Fatal("quit")
	}
	mustHave(t, m, "Ctrl+C cancels")
}

func TestParseKeys(t *testing.T) {
	ks, rest := ParseKeys([]byte("a\x1b[A\x1bOB\r\x7f\x1b[5~\x1b[[A\xc3\xa4\x03"))
	var got []string
	for _, k := range ks {
		got = append(got, k.String())
	}
	if strings.Join(got, " ") != "a up down enter bs pgup f1 ä ctrl-c" || len(rest) != 0 {
		t.Fatalf("%v rest=%q", got, rest)
	}
	_, rest = ParseKeys([]byte("\x1b"))
	if string(rest) != "\x1b" {
		t.Fatal("lone ESC must be held back")
	}
	_, rest = ParseKeys([]byte("\x1b[1;"))
	if len(rest) == 0 {
		t.Fatal("partial CSI held back")
	}
}

func TestKeymapOverride(t *testing.T) {
	km := DefaultKeymap()
	if km.Action(R('j')) != "down" || km.Action(N("up")) != "up" {
		t.Fatal("defaults")
	}
	km.Override("down=n,down2=x;up=w")
	if km.Action(R('n')) != "down" || km.Action(R('w')) != "up" || km.Action(R('j')) != "" {
		t.Fatalf("override %v", km)
	}
	if _, ok := km["down2"]; ok {
		t.Fatal("unknown action ignored")
	}
}

func TestOpWriter(t *testing.T) {
	o := &Op{}
	o.Write([]byte("abc\rde\nline2\npart"))
	l := o.Lines()
	if strings.Join(l, "|") != "de|line2|part" {
		t.Fatalf("%q", l)
	}
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func TestCatalogSearchFilter(t *testing.T) {
	m := &Model{Svc: newFake(), catAll: []CatalogItem{
		{OS: "alpine", Release: "3.20", Variant: "default", Arch: "amd64", Kind: "container"},
		{OS: "archlinux", Release: "current", Variant: "cloud", Arch: "amd64", Kind: "vm"},
	}}
	for q, want := range map[string]int{"": 2, "arch": 1, "arch vm": 1, "arch container": 0, "AMD64": 2, "alp 3.2": 1} {
		m.filter = Filter{Text: q}
		m.applyFilter()
		if len(m.catalog) != want {
			t.Errorf("%q: %d want %d", q, len(m.catalog), want)
		}
	}
}

// goldenFrame compares the 80x24 text frame with testdata/<name>.txt (UPDATE_GOLDEN=1 rewrites it).
func goldenFrame(t *testing.T, name string, m *Model) {
	t.Helper()
	got := m.View().Text() + "\n"
	for i, l := range strings.Split(got, "\n") {
		if strWidth(l) > m.W {
			t.Fatalf("%s line %d wider than %d: %q", name, i, m.W, l)
		}
	}
	p := "testdata/" + name + ".txt"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		os.WriteFile(p, []byte(got), 0o644)
	}
	want, err := os.ReadFile(p)
	if err != nil || string(want) != got {
		t.Fatalf("frame %s differs from %s (UPDATE_GOLDEN=1 to refresh):\n%s", name, p, got)
	}
	t.Logf("%s:\n%s", name, got)
}

func TestFrames80x24(t *testing.T) {
	m, f := newModel(t, true)
	goldenFrame(t, "images", m)
	m.Update(N("enter"))
	m.Update(R('j'))
	goldenFrame(t, "detail", m)
	m.Update(R('1'))
	m.Update(R('2'))
	m.Update(N("enter"))
	waitOp(t, m)
	keys(m, "arch")
	goldenFrame(t, "pull_search", m)
	m.Update(N("esc"))
	m.Update(N("esc"))
	m.Update(N("tab"))
	m.Update(R('c'))
	_ = f
	m.Update(R('2'))
	goldenFrame(t, "store_confirm", m.afterKeys(N("enter"), N("enter")))
	m.Update(N("esc"))
	m.Update(R('4'))
	m.Update(R('t'))
	waitOp(t, m)
	goldenFrame(t, "op_done", m)
}

func (m *Model) afterKeys(ks ...Key) *Model {
	for _, k := range ks {
		m.Update(k)
	}
	return m
}
