package snapmgr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BootIDPath is the kernel's per-boot random id (a variable for tests).
var BootIDPath = "/proc/sys/kernel/random/boot_id"

type useInfo struct {
	BootID string `json:"boot_id"`
	Since  string `json:"since"`
	Target string `json:"target,omitempty"`
}

func (s *Store) lockPath() string { return filepath.Join(s.dir(), "in-use.json") }

func bootID() string {
	b, _ := os.ReadFile(BootIDPath)
	return strings.TrimSpace(string(b))
}

// MarkInUse records that this kernel boot handed the image over to a running guest (container guests run on the
// loader's kernel, so the marker is valid for the rest of this boot). Best effort: kexec'd VM guests are not detectable.
func (s *Store) MarkInUse(target string) error {
	id := bootID()
	if id == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(useInfo{BootID: id, Since: time.Now().UTC().Format(time.RFC3339), Target: target})
	return os.WriteFile(s.lockPath(), b, 0o644)
}

// InUse reports whether a guest handed over by the current kernel boot is still (assumed) running.
func (s *Store) InUse() (bool, string) {
	b, err := os.ReadFile(s.lockPath())
	if err != nil {
		return false, ""
	}
	var u useInfo
	if json.Unmarshal(b, &u) != nil || u.BootID == "" || u.BootID != bootID() {
		return false, ""
	}
	return true, fmt.Sprintf("booted at %s by this system", u.Since)
}
