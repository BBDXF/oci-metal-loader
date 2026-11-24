package tui

import (
	"strings"
	"time"
)

// MinAutoBootSec is the shortest countdown the TUI shows (a VNC/serial client may attach a few seconds after the console came up).
const MinAutoBootSec = 5

// AutoBoot is the pending automatic boot (boot.default_image + boot.timeout) handed over by autoboot.sh in
// /run/omloader/autoboot. The TUI owns the countdown: any key cancels, expiry boots exactly like Enter on that image.
type AutoBoot struct {
	Image, Snapshot string
	Flags           []string // boot.extra_cmdline_flags (omloader.<flag> without prefix); handed to the boot request
	Timeout         int      // seconds from the config; the effective value is max(Timeout, MinAutoBootSec)
}

// AutoBooter is optionally implemented by a Service.
type AutoBooter interface {
	AutoBoot() *AutoBoot     // nil = no automatic boot pending (none configured, or already cancelled/used in this boot)
	AutoBootCancelled() bool // another console cancelled it (polled while counting down)
	AutoBootCancel()         // record the cancel/expiry so no other console (or a restarted TUI) counts down again
}

// ArmAutoBoot prepares the countdown; it only starts with StartAutoBoot (after the first frame is on the screen).
func (m *Model) ArmAutoBoot(a AutoBoot) {
	if a.Snapshot == "" {
		a.Snapshot = "current"
	}
	if a.Timeout < MinAutoBootSec {
		a.Timeout = MinAutoBootSec
	}
	m.auto, m.autoOn = &a, false
}

// StartAutoBoot starts the armed countdown (call it from the terminal's "first frame drawn" hook).
func (m *Model) StartAutoBoot() {
	if m.auto != nil && !m.autoOn && m.autoEnd.IsZero() {
		m.autoEnd, m.autoOn = m.Now().Add(time.Duration(m.auto.Timeout)*time.Second), true
		m.mark("OMLOADER_LOADER: Booting '" + m.auto.Image + "' (" + m.auto.Snapshot + ") in " + itoa(m.auto.Timeout) + " s -- press any key for the menu")
	}
}

// AutoBootActive is true while the countdown runs.
func (m *Model) AutoBootActive() bool { return m.autoOn }

// autoLeft is the whole seconds left (rounded up).
func (m *Model) autoLeft() int {
	d := m.autoEnd.Sub(m.Now())
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// autoTick fires the boot when the countdown is over.
func (m *Model) autoTick() {
	if !m.autoOn {
		return
	}
	ab, _ := m.Svc.(AutoBooter)
	if ab != nil && ab.AutoBootCancelled() { // cancelled on another console (VNC and serial both run a TUI)
		m.autoOn = false
		m.warn(L.AutoBootCancelled)
		return
	}
	if m.autoLeft() == 0 {
		m.autoOn = false
		if ab != nil {
			ab.AutoBootCancel()
		}
		m.mark("OMLOADER_LOADER: Booting '" + m.auto.Image + "' (" + m.auto.Snapshot + ") now")
		m.bootNow(m.auto.Image, m.auto.Snapshot, false)
		m.res.Boot.Flags = strings.Join(m.auto.Flags, " ")
	}
}

// autoCancel is called for any key press while the countdown runs.
func (m *Model) autoCancel() {
	m.autoOn = false
	if ab, ok := m.Svc.(AutoBooter); ok {
		ab.AutoBootCancel()
	}
	m.mark("OMLOADER_LOADER: boot cancelled, opening the menu")
	m.warn(L.AutoBootCancelled)
}

// mark queues a plain-text line for the console log (the terminal prints it between frames; test harnesses grep for these).
func (m *Model) mark(s string) { m.marks = append(m.marks, s) }

// TakeMarks returns and clears the queued console log lines.
func (m *Model) TakeMarks() []string {
	out := m.marks
	m.marks = nil
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
