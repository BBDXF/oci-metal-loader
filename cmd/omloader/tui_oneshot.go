package main

import (
	"os"
	"path/filepath"
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
