package tui

import (
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
	mustHave(t, m, "alpine", "lxd-container", "overlayfs"[:0]+"3.0 MiB", "deb", "vm", "1:Images")
	if got := m.View().Lines()[2]; !strings.Contains(got, "alpine") {
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
	for _, want := range []string{"base", "|-- ", "`-- ", "* b", "[frozen]", "[leaf]", "#golden"} {
		if !strings.Contains(s, want) {
			t.Fatalf("tree lacks %q:\n%s", want, s)
		}
	}
	rows := BuildRows(m.tree, m.Th)
	var names []string
	for _, r := range rows {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "base,a,b,c" {
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
		"alpine  type=lxd-container  mode=overlayfs  current=b  snapshots=3",
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
	m.Update(R('j'))
	m.Update(R('c'))
	mustHave(t, m, "Create snapshot", "From: a")
	keys(m, "snap1\nmy note\nt1 t2\n")
	if !f.called(`create alpine snap1 from=a note="my note" tags=[t1 t2]`) {
		t.Fatalf("calls %v", f.calls)
	}
	mustHave(t, m, "snap1", "created snapshot snap1")
}

func TestRollbackDiscardConfirm(t *testing.T) {
	m, f := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('j'))
	m.Update(R('r'))
	m.Update(R('2'))
	mustHave(t, m, "WARNING", "permanently deletes")
	m.Update(N("esc"))
	if f.called("rollback") {
		t.Fatal("cancelled")
	}
	m.Update(R('r'))
	m.Update(R('2'))
	m.Update(N("enter"))
	if !f.called("rollback alpine a discard=true") {
		t.Fatalf("%v", f.calls)
	}
	mustHave(t, m, "rollback to a: ok")
}

func TestDeleteRecursiveConfirm(t *testing.T) {
	m, f := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('j'))
	m.Update(R('d'))
	mustHave(t, m, "descendants", "Delete: c, a")
	m.Update(R('y'))
	if !f.called("delete alpine a rec=true force=false") {
		t.Fatalf("%v", f.calls)
	}
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

func TestBootCurrentAndForce(t *testing.T) {
	m, _ := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('b'))
	mustHave(t, m, "current snapshot (b)", "new snapshot", "base image, ephemeral")
	m.Update(N("enter"))
	mustHave(t, m, "omloader boot --image alpine --snapshot current", "switch-root", "d: preview")
	m.Update(N("enter"))
	if !m.Done() || m.Result().Kind != ResBoot || m.Result().Boot != (BootRequest{"alpine", "current", false}) {
		t.Fatalf("result %+v", m.Result())
	}
	// frozen node needs the explicit warning
	m, _ = newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('j')) // a (has children)
	m.Update(R('b'))
	m.Update(R('2')) // selected snapshot (a)
	mustHave(t, m, "WARNING", "--force")
	m.Update(N("esc"))
	if m.Done() {
		t.Fatal("esc must cancel")
	}
	m.Update(R('b'))
	m.Update(R('2'))
	m.Update(N("enter")) // accept the warning
	mustHave(t, m, "--snapshot a --force")
	m.Update(N("enter"))
	if got := m.Result(); got.Kind != ResBoot || !got.Boot.Force || got.Boot.Snapshot != "a" {
		t.Fatalf("result %+v", got)
	}
}

func TestBootNewNamedAndBase(t *testing.T) {
	m, _ := newModel(t, true)
	m.Update(N("enter"))
	m.Update(R('b'))
	m.Update(R('2')) // new: (items: current, new, base)
	keys(m, "try1\n")
	m.Update(N("enter"))
	if got := m.Result().Boot; got.Snapshot != "new:try1" {
		t.Fatalf("%+v", got)
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
	if !f.called("flatten alpine a alpine-a") {
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
	mustHave(t, m, "pull lxc:alpine/3.20/default", "downloaded 1.0 MiB / 3.0 MiB")
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
	m, f := newModel(t, true)
	m.Update(R('2'))
	m.Update(R('/'))
	keys(m, "alpine\n3.20\nvm\n")
	waitOp(t, m)
	if !f.called(`catalog lxc-images {OS:alpine Release:3.20 Kind:vm}`) {
		t.Fatalf("%v", f.calls)
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
