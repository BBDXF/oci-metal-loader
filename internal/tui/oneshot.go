package tui

import "time"

// Prefs are the one-key-flow settings of the service (config `boot.remember_last`).
type Prefs struct {
	RememberLast bool // record the last booted image/snapshot (boot.remember_last)
}

// PrefsProvider is optionally implemented by a Service.
type PrefsProvider interface{ Prefs() Prefs }

// LastBooter is optionally implemented by a Service: the last booted image/snapshot (selection default, `default_image: last`).
// RememberBoot must fail silently when the state file is not writable.
type LastBooter interface {
	LastBoot() (image, snapshot string)
	RememberBoot(image, snapshot string, ephemeral bool)
}

func (m *Model) prefs() Prefs {
	if p, ok := m.Svc.(PrefsProvider); ok {
		return p.Prefs()
	}
	return Prefs{}
}

// defaultSnapLabel is the default label of a manually created snapshot: snap-YYYYMMDD-HHMM (local time of the loader).
func defaultSnapLabel(t time.Time) string { return "snap-" + t.Format("20060102-1504") }
