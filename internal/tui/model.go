package tui

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Screen is a top-level tab (Detail is a sub-screen of Images).
type Screen int

const (
	ScrImages Screen = iota
	ScrPull
	ScrStorage
	ScrNet
	ScrSettings
	ScrDetail
)

type dlgKind int

const (
	dkMsg dlgKind = iota
	dkConfirm
	dkMenu
	dkForm
)

// Dialog is a modal box.
type Dialog struct {
	Kind     dlgKind
	Title    string
	Lines    []string
	Warn     bool
	Items    []string
	Sel      int
	Labels   []string
	Values   []string
	Fi       int
	OnOK     func()
	OnPick   func(i int)
	OnSubmit func(vals []string)
	Extra    map[rune]func()
	Replace  bool   // forms: the first printable key replaces the (default) value of the first field instead of appending
	Buttons  string // confirm dialogs: replaces the default "[Enter/y] Yes  [Esc/n] No" line
}

// TreeRow is one displayed line of the snapshot tree.
type TreeRow struct {
	Name   string // "" never; base row is Name "base" with IsBase
	IsBase bool
	Prefix string
	Node   *NodeInfo
	Kids   int
}

// Model is the whole TUI state. It has no terminal IO: tests drive it with Update/Tick and read View.
type Model struct {
	Svc    Service
	Th     Theme
	Km     Keymap
	W, H   int
	Screen Screen
	Now    func() time.Time

	res    Result
	done   bool
	status string
	stErr  bool
	stWarn bool

	hdr       HeaderInfo
	hdrOK     bool
	hdrAt     time.Time
	opHideLog bool
	helpTop   int

	images     []ImageInfo
	isel, itop int

	image      string
	tree       *TreeInfo
	rows       []TreeRow
	dsel, dtop int

	dlg  *Dialog
	help bool

	op       *Op
	opScroll int
	opResume *Dialog

	sources    []SourceInfo
	ssel       int
	catalog    []CatalogItem // filtered view
	catAll     []CatalogItem // full catalog
	csel, ctop int
	csource    string
	smirror    map[string]string // source name -> mirror chosen with `m` (absent = the configured one)
	filter     Filter
	pullCat    bool // focus on the catalog list

	store    StoreStatus
	storeErr string

	netLines, settings []string
	nsc, ssc           int
	started            bool
}

// New creates a model for an 80x24 terminal; call Init to load the first screen.
func New(svc Service, th Theme) *Model {
	return &Model{Svc: svc, Th: th, Km: DefaultKeymap(), W: 80, H: 24, Now: time.Now}
}

// Init loads the images list.
func (m *Model) Init() { m.refreshHeader(true); m.RefreshImages(); m.selectLast() }

// Result returns the outcome once Done.
func (m *Model) Result() Result { return m.res }

// Done reports that the TUI should exit.
func (m *Model) Done() bool { return m.done }

// Resize sets the terminal size.
func (m *Model) Resize(w, h int) { m.W, m.H = w, h }

// Busy is true while a background operation runs.
func (m *Model) Busy() bool {
	if m.op == nil {
		return false
	}
	f, _ := m.op.Finished()
	return !f
}

func (m *Model) say(s string)            { m.status, m.stErr, m.stWarn = s, false, false }
func (m *Model) warn(s string)           { m.status, m.stErr, m.stWarn = s, false, true }
func (m *Model) sayf(f string, a ...any) { m.say(fmt.Sprintf(f, a...)) }
func (m *Model) fail(err error) {
	m.status, m.stErr, m.stWarn = err.Error(), true, false
	m.message(L.Error, true, err.Error())
}

func (m *Model) message(title string, warn bool, lines ...string) {
	m.dlg = &Dialog{Kind: dkMsg, Title: title, Warn: warn, Lines: lines}
}

func (m *Model) confirm(title string, warn bool, lines []string, ok func()) *Dialog {
	d := &Dialog{Kind: dkConfirm, Title: title, Warn: warn, Lines: lines, OnOK: ok}
	m.dlg = d
	return d
}

func (m *Model) menu(title string, items []string, pick func(i int)) {
	m.dlg = &Dialog{Kind: dkMenu, Title: title, Items: items, OnPick: pick}
}

func (m *Model) form(title string, lines []string, labels, defaults []string, submit func(v []string)) {
	m.dlg = &Dialog{Kind: dkForm, Title: title, Lines: lines, Labels: labels, Values: append([]string{}, defaults...), OnSubmit: submit}
}

// startOp shows the operation view and runs fn in the background; onDone runs on the UI goroutine (Tick).
func (m *Model) startOp(title string, fn func(ctx context.Context, w io.Writer) error, onDone func(error)) *Op {
	m.op = StartOp(title, func(ctx context.Context, o *Op) error { return fn(ctx, o) }, onDone)
	m.opScroll, m.opHideLog = 0, false
	return m.op
}

// Tick must be called periodically (and after Op.Wait in tests): it delivers operation completions.
func (m *Model) Tick() {
	m.refreshHeader(false)
	o := m.op
	if o == nil || o.handled {
		return
	}
	if fin, err := o.Finished(); fin {
		o.handled = true
		if o.onDone != nil {
			o.onDone(err)
		}
	}
}
