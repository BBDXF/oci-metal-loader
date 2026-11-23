package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Op is a background operation with streamed output. It implements io.Writer.
type Op struct {
	Title   string
	mu      sync.Mutex
	lines   []string
	partial string
	err     error
	fin     bool
	Started time.Time
	Ended   time.Time
	cancel  context.CancelFunc
	doneCh  chan struct{}
	onDone  func(err error)
	handled bool
	// progress (optional): bytes so far / total, polled by the UI
	Bytes     func() int64
	Total     int64
	maxBytes  int64
	lastB     int64
	lastT     time.Time
	speed     float64
	Cancelled bool
}

// StartOp runs fn in a goroutine. onDone (may be nil) runs later on the UI goroutine via Model.Tick.
func StartOp(title string, fn func(ctx context.Context, w *Op) error, onDone func(error)) *Op {
	ctx, cancel := context.WithCancel(context.Background())
	o := &Op{Title: title, Started: time.Now(), cancel: cancel, doneCh: make(chan struct{}), onDone: onDone}
	go func() {
		err := fn(ctx, o)
		o.mu.Lock()
		o.err, o.fin, o.Ended = err, true, time.Now()
		if o.partial != "" {
			o.lines = append(o.lines, o.partial)
			o.partial = ""
		}
		o.mu.Unlock()
		close(o.doneCh)
	}()
	return o
}

// Write implements io.Writer (line buffered; \r rewinds the current line).
func (o *Op) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, c := range string(p) {
		switch c {
		case '\n':
			o.lines = append(o.lines, o.partial)
			o.partial = ""
		case '\r':
			o.partial = ""
		default:
			o.partial += string(c)
		}
	}
	if len(o.lines) > 5000 {
		o.lines = o.lines[len(o.lines)-4000:]
	}
	return len(p), nil
}

// Cancel asks the operation to stop.
func (o *Op) Cancel() {
	o.mu.Lock()
	o.Cancelled = true
	o.mu.Unlock()
	o.cancel()
}

// Wait blocks until the operation finished (tests).
func (o *Op) Wait() { <-o.doneCh }

// Finished reports completion and the error.
func (o *Op) Finished() (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.fin, o.err
}

// Lines returns a copy of the output so far (including the partial line).
func (o *Op) Lines() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	l := append([]string{}, o.lines...)
	if o.partial != "" {
		l = append(l, o.partial)
	}
	return l
}

// Progress is a one-line status: elapsed time and, when known, bytes and speed.
func (o *Op) Progress(now time.Time) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	end := now
	if o.fin {
		end = o.Ended
	}
	s := fmt.Sprintf("%ds", int(end.Sub(o.Started).Seconds()))
	if o.Bytes != nil {
		b := o.Bytes()
		if b > o.maxBytes {
			o.maxBytes = b
		}
		if !o.fin && now.Sub(o.lastT) >= time.Second {
			if !o.lastT.IsZero() {
				o.speed = float64(o.maxBytes-o.lastB) / now.Sub(o.lastT).Seconds()
			}
			o.lastB, o.lastT = o.maxBytes, now
		}
		s += " " + L.PullProgress + " " + human(o.maxBytes)
		if o.Total > 0 {
			s += fmt.Sprintf(" / %s (%d%%)", human(o.Total), min64(100, o.maxBytes*100/o.Total))
		}
		if o.speed > 0 && !o.fin {
			s += " @ " + human(int64(o.speed)) + "/s"
		}
	}
	return s
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func human(n int64) string {
	f := float64(n)
	for _, u := range []string{"B", "KiB", "MiB", "GiB"} {
		if f < 1024 {
			if u == "B" {
				return fmt.Sprintf("%d B", n)
			}
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1f TiB", f)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "~"
}

func pad(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return string(r[:n])
	}
	return s + strings.Repeat(" ", n-len(r))
}
