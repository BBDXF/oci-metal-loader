package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"omloader/internal/tui"
)

// Prefs implements tui.PrefsProvider (boot.remember_last).
func (s *realSvc) Prefs() tui.Prefs {
	c, err := s.cfg()
	return tui.Prefs{RememberLast: err == nil && c.Boot.RememberLast}
}

// lastBootFiles are the candidate places of the tiny state file: next to boot.yaml on the images/data partition first, then
// next to the snapshot store. autoboot.sh reads the same names (boot.default_image: last).
func (s *realSvc) lastBootFiles() []string {
	var out []string
	for _, root := range []string{s.imagesRoot, s.snapRoot} {
		if root != "" {
			out = append(out, filepath.Join(filepath.Dir(root), "omloader", "last-boot"))
		}
	}
	return out
}

// LastBoot returns the image and snapshot recorded by the previous boot ("" when none).
func (s *realSvc) LastBoot() (image, snapshot string) {
	for _, f := range s.lastBootFiles() {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		l := strings.Split(strings.TrimSpace(string(b)), "\n")
		if len(l) > 0 && strings.TrimSpace(l[0]) != "" {
			image = strings.TrimSpace(l[0])
			if len(l) > 1 {
				snapshot = strings.TrimSpace(l[1])
			}
			return
		}
	}
	return "", ""
}

// RememberBoot records the boot target: line 1 image, line 2 snapshot (current | label | base), line 3 "ephemeral" or empty.
// Not writable (read-only data partition, no such directory) is silently ignored.
func (s *realSvc) RememberBoot(image, snapshot string, ephemeral bool) {
	body := image + "\n" + snapshot + "\n"
	if ephemeral {
		body += "ephemeral\n"
	}
	for _, f := range s.lastBootFiles() {
		if _, err := os.Stat(filepath.Dir(f)); err != nil {
			if os.Mkdir(filepath.Dir(f), 0o755) != nil { // the partition itself must exist and be writable
				continue
			}
		}
		tmp := f + ".tmp"
		if os.WriteFile(tmp, []byte(body), 0o644) == nil && os.Rename(tmp, f) == nil {
			return
		}
		os.Remove(tmp)
	}
}

var _ tui.PrefsProvider = (*realSvc)(nil)
var _ tui.LastBooter = (*realSvc)(nil)

// autobootFile is written by autoboot.sh (ui=tui): IMAGE=/SNAPSHOT=/TIMEOUT= lines. The TUI that finds it owns the countdown and
// claims it by creating autobootFile+".claimed" (otherwise the shell watchdog boots after the grace period).
var autobootFile = "/run/omloader/autoboot"

// AutoBoot implements tui.AutoBooter: the pending automatic boot, nil when there is none.
func (s *realSvc) AutoBoot() *tui.AutoBoot {
	if s.AutoBootCancelled() {
		return nil // cancelled or already used in this boot (a console was left and started again)
	}
	b, err := os.ReadFile(autobootFile)
	if err != nil {
		return nil
	}
	a := &tui.AutoBoot{Timeout: 10}
	for _, ln := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(ln), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "IMAGE":
			a.Image = v
		case "SNAPSHOT":
			a.Snapshot = v
		case "FLAGS":
			a.Flags = strings.Fields(v)
		case "TIMEOUT":
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				a.Timeout = n
			}
		}
	}
	if a.Image == "" {
		return nil
	}
	return a
}

// AutoBootCancelled / AutoBootCancel: every console (tty1/VNC and serial each run a TUI) counts down; a key on any of them cancels
// all of them, and the boot itself is serialised by boot.lock (tuiBoot).
func (s *realSvc) AutoBootCancelled() bool {
	_, err := os.Stat(autobootFile + ".cancelled")
	return err == nil
}
func (s *realSvc) AutoBootCancel() { os.WriteFile(autobootFile+".cancelled", []byte("1\n"), 0o644) }

// claimAutoBoot tells the shell watchdog that the TUI took over.
func claimAutoBoot() { os.WriteFile(autobootFile+".claimed", []byte("tui\n"), 0o644) }

var _ tui.AutoBooter = (*realSvc)(nil)

// applyBootFlags gives the TUI auto-boot the same effect as the shell path: experimental=X sets OML_EXPERIMENTAL, poweroff_on_fail=1
// is returned (the caller powers off when the boot fails). Other flags only matter earlier in the boot and are ignored here.
func applyBootFlags(flags []string) (poweroffOnFail bool) {
	for _, f := range flags {
		k, v, _ := strings.Cut(f, "=")
		switch k {
		case "experimental":
			if v != "" {
				os.Setenv("OML_EXPERIMENTAL", v)
			}
		case "poweroff_on_fail":
			poweroffOnFail = v == "1"
		}
	}
	return
}
