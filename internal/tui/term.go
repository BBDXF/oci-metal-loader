//go:build linux

package tui

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

// Terminal is a raw-mode terminal on a tty file.
type Terminal struct {
	In    *os.File
	Out   io.Writer
	saved syscall.Termios
	raw   bool
}

func ioctl(fd uintptr, req uintptr, p unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(p)); e != 0 {
		return e
	}
	return nil
}

// MakeRaw switches the terminal to raw mode (no echo, no line editing, no signals); Ctrl+C arrives as a key.
func (t *Terminal) MakeRaw() error {
	if err := ioctl(t.In.Fd(), syscall.TCGETS, unsafe.Pointer(&t.saved)); err != nil {
		return err
	}
	r := t.saved
	r.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	r.Oflag |= syscall.OPOST | syscall.ONLCR
	r.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	r.Cflag |= syscall.CS8
	r.Cc[syscall.VMIN], r.Cc[syscall.VTIME] = 1, 0
	if err := ioctl(t.In.Fd(), syscall.TCSETS, unsafe.Pointer(&r)); err != nil {
		return err
	}
	t.raw = true
	return nil
}

// Restore puts the terminal back.
func (t *Terminal) Restore() {
	if t.raw {
		ioctl(t.In.Fd(), syscall.TCSETS, unsafe.Pointer(&t.saved))
		t.raw = false
	}
}

// Size returns the window size; serial consoles report 0x0, then COLUMNS/LINES or 80x24.
func (t *Terminal) Size() (w, h int) {
	var ws struct{ Row, Col, X, Y uint16 }
	if ioctl(t.In.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)) == nil {
		w, h = int(ws.Col), int(ws.Row)
	}
	if w <= 0 {
		w, _ = strconv.Atoi(os.Getenv("COLUMNS"))
	}
	if h <= 0 {
		h, _ = strconv.Atoi(os.Getenv("LINES"))
	}
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	return
}

func pollIn(fd, ms int) bool {
	pfd := struct {
		fd      int32
		events  int16
		revents int16
	}{int32(fd), 1, 0}
	r, _, _ := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&pfd)), 1, uintptr(ms))
	return r > 0
}

// IsTTY reports whether f is a terminal.
func IsTTY(f *os.File) bool {
	var t syscall.Termios
	return ioctl(f.Fd(), syscall.TCGETS, unsafe.Pointer(&t)) == nil
}

// Run drives m on the terminal until the model is done. ready (may be nil) is called after the first frame.
// Ctrl+C is delivered to the model (cancels operations); SIGTERM/SIGHUP end the loop with ResQuit.
func (t *Terminal) Run(m *Model, ready func()) Result {
	if err := t.MakeRaw(); err != nil {
		fmt.Fprintln(t.Out, "tui: cannot set raw mode:", err)
		return Result{Kind: ResQuit}
	}
	defer t.Restore()
	defer fmt.Fprint(t.Out, "\x1b[?25h\x1b[0m\x1b[2J\x1b[H")
	fmt.Fprint(t.Out, "\x1b[?25l\x1b[0m"+baseSGR(m.Th)+"\x1b[2J")
	keys := make(chan []byte, 16)
	stop := make(chan struct{})
	rdone := make(chan struct{})
	// the reader polls so it can be stopped: a stray goroutine would swallow the first key typed after the TUI.
	go func() {
		defer close(rdone)
		buf := make([]byte, 256)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if !pollIn(int(t.In.Fd()), 100) {
				continue
			}
			n, err := t.In.Read(buf)
			if n > 0 {
				select {
				case keys <- append([]byte{}, buf[:n]...):
				case <-stop:
					return
				}
			}
			if err != nil {
				select {
				case keys <- nil:
				case <-stop:
				}
				return
			}
		}
	}()
	defer func() { close(stop); <-rdone }()
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)
	m.W, m.H = t.Size()
	var prev []string
	draw := func(full bool) {
		c := m.View()
		if full {
			prev = nil
		}
		if len(prev) != c.H {
			prev = make([]string, c.H)
		}
		out := ""
		for y := 0; y < c.H; y++ {
			l := c.ANSI(y, m.Th)
			if l != prev[y] {
				out += fmt.Sprintf("\x1b[%d;1H%s", y+1, l)
				prev[y] = l
			}
		}
		if out != "" {
			io.WriteString(t.Out, out)
		}
	}
	draw(true)
	if ready != nil {
		ready()
	}
	flush := func() bool { // console-log marker lines (autoboot): printed between frames, then a full redraw
		ms := m.TakeMarks()
		for _, s := range ms {
			fmt.Fprint(t.Out, "\r\n"+s+"\r\n")
		}
		return len(ms) > 0
	}
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	var pending []byte
	var escTimer <-chan time.Time
	for !m.Done() {
		select {
		case b, ok := <-keys:
			if !ok || b == nil {
				return Result{Kind: ResQuit}
			}
			pending = append(pending, b...)
			ks, rest := ParseKeys(pending)
			pending = rest
			escTimer = nil
			if len(rest) == 1 && rest[0] == 0x1b {
				escTimer = time.After(80 * time.Millisecond)
			}
			redraw := false
			for _, k := range ks {
				if k.Name == "ctrl-l" {
					redraw = true
				}
				m.Update(k)
			}
			m.Tick()
			draw(redraw || flush())
		case <-escTimer:
			escTimer = nil
			pending = nil
			m.Update(N("esc"))
			m.Tick()
			draw(flush())
		case s := <-sig:
			if s == syscall.SIGWINCH {
				m.W, m.H = t.Size()
				draw(true)
				continue
			}
			return Result{Kind: ResQuit}
		case <-tick.C:
			m.Tick()
			draw(flush()) // line-diffed: costs nothing when the frame did not change
		}
	}
	return m.Result()
}
