package boot

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Paths inside the injected initrd.
const (
	GuestDir     = "/omloader"
	GuestPlanRel = "/omloader/plan.json"
	GuestPreinit = "/omloader/preinit"
	GuestBinDir  = "/omloader/bin"
	GuestLibDir  = "/omloader/lib"
	// Storages are mounted directly under "/" (klibc run-init skips top-level mount points but fails on nested ones).
	GuestMntPrefix = "/omlmnt-"
)

// StorageRef identifies a filesystem by UUID/label (never by device name).
type StorageRef struct {
	ID     string `json:"id"`
	UUID   string `json:"uuid,omitempty"`
	Label  string `json:"label,omitempty"`
	FSType string `json:"fstype"`
	RW     bool   `json:"rw"`
	Root   string `json:"root,omitempty"` // path of the mount's root inside the fs (bind/subvol), usually "/"
	// Loop (store-file mode): this storage is a filesystem inside an image FILE on another storage (which is
	// listed earlier): attach the file with losetup, then mount the loop device. UUID/Label are informational.
	Loop *FileRef `json:"loop,omitempty"`
	// Extents (with Loop): physical byte extents of the store file on its host storage; the guest builds a
	// dm-linear device on the host block device instead of mounting the host fs + losetup (needs no loop/ntfs3).
	Extents []Extent `json:"extents,omitempty"`
	// NoMount: only locate the block device (used as dm-linear backing for Extents), never mount it.
	NoMount bool `json:"nomount,omitempty"`
}

// FileRef is a file on a storage.
type FileRef struct {
	Storage string `json:"storage"`
	Rel     string `json:"rel"`
}

// GuestLayer is one COW layer.
type GuestLayer struct {
	Node   string  `json:"node"`
	DMName string  `json:"dm_name"`
	Cow    FileRef `json:"cow"`
}

// GuestPlan is /omloader/plan.json, consumed by `omloader guest-assemble`.
type GuestPlan struct {
	Version  int          `json:"version"`
	Image    string       `json:"image"`
	Storages []StorageRef `json:"storages"`
	// BaseExtents: the base image file lives on a NoMount storage; it is exposed as a dm-linear device over these extents.
	BaseExtents []Extent     `json:"base_extents,omitempty"`
	Base        FileRef      `json:"base"`
	Sectors     int64        `json:"sectors"`
	BaseDM      string       `json:"base_dm"` // used when there are no layers (read-only linear map)
	Layers      []GuestLayer `json:"layers"`
	RootIndex   int          `json:"root_index"` // 1-based index in kpartx output
	RootDev     string       `json:"root_dev"`   // expected /dev/mapper/<partition>
	Modules     []string     `json:"modules"`    // initrd paths, insmod in order
	Loader      string       `json:"loader"`     // dynamic loader inside the initrd used to run bundled tools
}

// Layer is one dm-snapshot layer on the loader side.
type stackLayer struct{ DMName, Cow string }

// stack creates loop devices and dm devices; returns the final device and everything to undo.
func buildStack(r Runner, base, baseDM string, sectors int64, layers []stackLayer) (final string, loops, dms []string, err error) {
	out, err := r.Run("losetup", "-r", "-f", "--show", base)
	if err != nil {
		return "", loops, dms, err
	}
	dev := firstLine(out)
	loops = append(loops, dev)
	if len(layers) == 0 {
		if _, err := r.Run("dmsetup", "create", "--readonly", baseDM, "--table", fmt.Sprintf("0 %d linear %s 0", sectors, dev)); err != nil {
			return "", loops, dms, err
		}
		dms = append(dms, baseDM)
		return "/dev/mapper/" + baseDM, loops, dms, nil
	}
	for _, l := range layers {
		out, err := r.Run("losetup", "-f", "--show", l.Cow)
		if err != nil {
			return "", loops, dms, err
		}
		cow := firstLine(out)
		loops = append(loops, cow)
		if _, err := r.Run("dmsetup", "create", l.DMName, "--table", fmt.Sprintf("0 %d snapshot %s %s P 8", sectors, dev, cow)); err != nil {
			return "", loops, dms, err
		}
		dms = append(dms, l.DMName)
		dev = "/dev/mapper/" + l.DMName
	}
	return dev, loops, dms, nil
}

// parseKpartx returns /dev/mapper paths from `kpartx -a -v` output.
func parseKpartx(out string) []string {
	var parts []string
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) >= 3 && f[0] == "add" && f[1] == "map" {
			parts = append(parts, "/dev/mapper/"+f[2])
		}
	}
	return parts
}

// GuestOptions tunes GuestAssemble (mostly for tests).
type GuestOptions struct {
	MountPrefix string
	Tries       int
	Delay       time.Duration
	Out         io.Writer
	// DevExists reports whether a device node exists (default os.Stat).
	DevExists func(string) bool
}

// LoadPlan reads a plan.json.
func LoadPlan(path string) (*GuestPlan, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p := &GuestPlan{}
	if err := json.Unmarshal(b, p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if p.Version != 1 {
		return nil, fmt.Errorf("%s: unsupported plan version %d", path, p.Version)
	}
	return p, nil
}

// GuestAssemble runs inside the guest initramfs: load modules, mount storages,
// recreate the loop/dm stack and partition maps. Returns the root device.
func GuestAssemble(p *GuestPlan, r Runner, o GuestOptions) (string, error) {
	if o.MountPrefix == "" {
		o.MountPrefix = GuestMntPrefix
	}
	if o.Tries == 0 {
		o.Tries = 60
	}
	if o.Delay == 0 {
		o.Delay = 500 * time.Millisecond
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.DevExists == nil {
		o.DevExists = func(s string) bool { _, err := os.Stat(s); return err == nil }
	}
	logf := func(f string, a ...any) { fmt.Fprintf(o.Out, "OMLOADER_GUEST: "+f+"\n", a...) }
	for _, m := range p.Modules {
		if _, err := r.Run("insmod", m); err != nil {
			logf("insmod %s: %v (continuing)", m, err)
		}
	}
	mounts := map[string]string{}
	devs := map[string]string{}
	for _, s := range p.Storages {
		var dev string
		var lastErr error
		if s.Loop != nil && len(s.Extents) > 0 {
			hd, ok := devs[s.Loop.Storage]
			if !ok {
				return "", fmt.Errorf("storage %s: host storage %q of the store file has no device (plan order)", s.ID, s.Loop.Storage)
			}
			name := "oml-store-" + s.ID
			if _, err := r.Run("dmsetup", "create", name, "--table", LinearTable(s.Extents, hd)); err != nil {
				return "", fmt.Errorf("storage %s: dm-linear over %s: %w", s.ID, hd, err)
			}
			dev = "/dev/mapper/" + name
			logf("storage %s: store file mapped by %d extents on %s as %s", s.ID, len(s.Extents), hd, dev)
		} else if s.Loop != nil {
			m, ok := mounts[s.Loop.Storage]
			if !ok {
				return "", fmt.Errorf("storage %s: host storage %q of the store file is not mounted (plan order)", s.ID, s.Loop.Storage)
			}
			file := filepath.Join(m, s.Loop.Rel)
			out, err := r.Run("losetup", "-f", "--show", file)
			if err != nil {
				return "", fmt.Errorf("storage %s: attaching store file %s: %w", s.ID, file, err)
			}
			dev = firstLine(out)
			logf("storage %s: store file %s attached as %s", s.ID, file, dev)
		}
		for i := 0; i < o.Tries && dev == ""; i++ {
			if s.UUID != "" {
				if out, err := r.Run("blkid", "-U", s.UUID); err == nil {
					dev = firstLine(out)
				} else {
					lastErr = err
				}
			}
			if dev == "" && s.Label != "" {
				if out, err := r.Run("blkid", "-L", s.Label); err == nil {
					dev = firstLine(out)
				} else {
					lastErr = err
				}
			}
			if dev == "" && i == 2 {
				logf("storage %s not yet visible (last error: %v)", s.ID, lastErr)
			}
			if dev == "" {
				time.Sleep(o.Delay)
			}
		}
		if dev == "" {
			return "", fmt.Errorf("storage %s (uuid=%s label=%s) not found", s.ID, s.UUID, s.Label)
		}
		devs[s.ID] = dev
		if s.NoMount {
			logf("storage %s: %s (not mounted; raw store backing)", s.ID, dev)
			continue
		}
		mnt := o.MountPrefix + s.ID
		if err := os.MkdirAll(mnt, 0o755); err != nil {
			return "", err
		}
		mode := "ro"
		if s.RW {
			mode = "rw"
		}
		args := []string{"-o", mode}
		if s.FSType != "" {
			args = append([]string{"-t", s.FSType}, args...)
		}
		if _, err := r.Run("mount", append(args, dev, mnt)...); err != nil {
			return "", err
		}
		logf("storage %s: %s mounted %s on %s", s.ID, dev, mode, mnt)
		mounts[s.ID] = filepath.Join(mnt, s.Root)
	}
	path := func(f FileRef) (string, error) {
		m, ok := mounts[f.Storage]
		if !ok {
			return "", fmt.Errorf("unknown storage %q", f.Storage)
		}
		return filepath.Join(m, f.Rel), nil
	}
	var base string
	var err error
	if len(p.BaseExtents) > 0 {
		hd, ok := devs[p.Base.Storage]
		if !ok {
			return "", fmt.Errorf("base storage %q has no device", p.Base.Storage)
		}
		if _, err := r.Run("dmsetup", "create", "oml-basefile", "--table", LinearTable(p.BaseExtents, hd)); err != nil {
			return "", fmt.Errorf("dm-linear over base file: %w", err)
		}
		base = "/dev/mapper/oml-basefile"
	} else if base, err = path(p.Base); err != nil {
		return "", err
	}
	var layers []stackLayer
	for _, l := range p.Layers {
		c, err := path(l.Cow)
		if err != nil {
			return "", err
		}
		layers = append(layers, stackLayer{DMName: l.DMName, Cow: c})
	}
	final, _, _, err := buildStack(r, base, p.BaseDM, p.Sectors, layers)
	if err != nil {
		return "", err
	}
	out, err := r.Run("kpartx", "-a", "-v", final)
	if err != nil {
		return "", err
	}
	parts := parseKpartx(out)
	if p.RootIndex < 1 || p.RootIndex > len(parts) {
		return "", fmt.Errorf("root partition %d not found among %v", p.RootIndex, parts)
	}
	root := parts[p.RootIndex-1]
	if p.RootDev != "" && root != p.RootDev {
		logf("warning: root device %s differs from plan %s", root, p.RootDev)
	}
	for i := 0; i < o.Tries && !o.DevExists(root); i++ {
		time.Sleep(o.Delay)
	}
	if !o.DevExists(root) {
		return "", fmt.Errorf("root device %s did not appear", root)
	}
	logf("root ready: %s", root)
	return root, nil
}

func marshalPlan(p *GuestPlan) ([]byte, error) { return json.MarshalIndent(p, "", "  ") }
