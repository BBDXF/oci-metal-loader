//go:build linux

package tui

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func openPTY(t *testing.T) (master, slave *os.File) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no /dev/ptmx:", err)
	}
	var unlock int32
	if err := ioctl(m.Fd(), syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		t.Skip(err)
	}
	var n uint32
	if err := ioctl(m.Fd(), syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		t.Skip(err)
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skip(err)
	}
	return m, s
}

// vt is a tiny terminal emulator: absolute cursor addressing + printable text, SGR ignored.
type vt struct {
	mu   sync.Mutex
	rows [][]rune
	y, x int
	esc  string
	raw  strings.Builder
}

func newVT(w, h int) *vt {
	v := &vt{}
	for i := 0; i < h; i++ {
		v.rows = append(v.rows, []rune(strings.Repeat(" ", w)))
	}
	return v
}

var csiRe = regexp.MustCompile(`^\x1b\[([0-9;?]*)([A-Za-z])$`)

func (v *vt) feed(b []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.raw.Write(b)
	for _, r := range string(b) {
		if v.esc != "" {
			v.esc += string(r)
			if len(v.esc) > 2 && (r >= '@' && r <= '~') {
				if m := csiRe.FindStringSubmatch(v.esc); m != nil {
					switch m[2] {
					case "H":
						p := strings.Split(m[1], ";")
						v.y, v.x = 0, 0
						if len(p) == 2 {
							y, _ := strconv.Atoi(p[0])
							x, _ := strconv.Atoi(p[1])
							v.y, v.x = y-1, x-1
						}
					case "J":
						for i := range v.rows {
							v.rows[i] = []rune(strings.Repeat(" ", len(v.rows[i])))
						}
					}
				}
				v.esc = ""
			}
			continue
		}
		switch {
		case r == 0x1b:
			v.esc = "\x1b"
		case r == '\r':
			v.x = 0
		case r == '\n':
			v.y++
		case r >= 0x20 && v.y >= 0 && v.y < len(v.rows) && v.x < len(v.rows[v.y]):
			v.rows[v.y][v.x] = r
			v.x++
		}
	}
}

func (v *vt) text() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	var l []string
	for _, r := range v.rows {
		l = append(l, strings.TrimRight(string(r), " "))
	}
	return strings.Join(l, "\n")
}

func TestTerminalPTYLinuxConsole(t *testing.T) {
	master, slave := openPTY(t)
	defer master.Close()
	defer slave.Close()
	// TERM=linux style: 80x24 window, ASCII theme
	ws := struct{ Row, Col, X, Y uint16 }{24, 80, 0, 0}
	ioctl(slave.Fd(), syscall.TIOCSWINSZ, unsafe.Pointer(&ws))
	scr := newVT(80, 24)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				scr.feed(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	m := New(newFake(), Theme{ASCII: true})
	m.Init()
	term := &Terminal{In: slave, Out: slave}
	resc := make(chan Result, 1)
	go func() { resc <- term.Run(m, nil) }()
	waitFor := func(sub string) {
		t.Helper()
		for i := 0; i < 100; i++ {
			if strings.Contains(scr.text(), sub) {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("screen never showed %q:\n%s", sub, scr.text())
	}
	waitFor("lxd-container")
	waitFor("1:Images")
	master.Write([]byte("\r"))
	waitFor("snapshots=3")
	waitFor("|-- ")
	master.Write([]byte("\x1b[B")) // Down arrow
	master.Write([]byte("v\r"))
	waitFor("verify alpine: OK")
	master.Write([]byte("\r"))
	master.Write([]byte("\x1b")) // lone ESC: back to the images list after the timeout
	waitFor("OCI Metal Loader")
	master.Write([]byte("\x1b"))
	waitFor("open the shell")
	master.Write([]byte("\r"))
	select {
	case r := <-resc:
		if r.Kind != ResShell {
			t.Fatalf("result %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("TUI did not exit:\n%s", scr.text())
	}
	if strings.Contains(scr.raw.String(), "\x1b[?1049") {
		t.Fatal("alternate screen must not be used (Linux console)")
	}
}

// TestTerminalPTYLiveSearch drives the Pull screen over a pty: typing filters the catalog live, the frame is the 80x24 text
// a Linux console would show.
func TestTerminalPTYLiveSearch(t *testing.T) {
	master, slave := openPTY(t)
	defer master.Close()
	defer slave.Close()
	ws := struct{ Row, Col, X, Y uint16 }{24, 80, 0, 0}
	ioctl(slave.Fd(), syscall.TIOCSWINSZ, unsafe.Pointer(&ws))
	scr := newVT(80, 24)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				scr.feed(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	m := New(newFake(), Theme{ASCII: true})
	m.Init()
	term := &Terminal{In: slave, Out: slave}
	resc := make(chan Result, 1)
	go func() { resc <- term.Run(m, nil) }()
	waitFor := func(sub string) {
		t.Helper()
		for i := 0; i < 100; i++ {
			if strings.Contains(scr.text(), sub) {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("screen never showed %q:\n%s", sub, scr.text())
	}
	waitFor("omloader v9.9")
	master.Write([]byte("2\r"))
	waitFor("lxc:alpine/3.20/default")
	master.Write([]byte("arch"))
	waitFor("Search: arch_  (1 of 2)")
	if strings.Contains(scr.text(), "lxc:alpine") {
		t.Fatalf("alpine must be filtered out:\n%s", scr.text())
	}
	t.Logf("frame after typing 'arch':\n%s", scr.text())
	master.Write([]byte("\x7f\x7f\x7f\x7f"))
	waitFor("(2 of 2)")
	for i := 0; i < 3; i++ { // sources, images list, shell question (a lone ESC is recognised after a short timeout)
		master.Write([]byte("\x1b"))
		time.Sleep(250 * time.Millisecond)
	}
	waitFor("open the shell")
	master.Write([]byte("\r"))
	select {
	case r := <-resc:
		if r.Kind != ResShell {
			t.Fatalf("result %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("TUI did not exit:\n%s", scr.text())
	}
}
