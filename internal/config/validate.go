package config

import (
	"fmt"
	"regexp"
	"strings"
)

// ErrorList is a list of validation errors.
type ErrorList []string

func (e ErrorList) Error() string { return strings.Join(e, "\n") }

var sha256Re = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func in(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

func hasDotDot(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if s == ".." {
			return true
		}
	}
	return false
}

// SourceUsable reports whether a configured source can be used under policy.
func (c *Config) SourceUsable(name string) bool {
	s, ok := c.Sources[name]
	if !ok {
		return false
	}
	return c.Policy.AllowOnline || s.Type == "local"
}

func isRemote(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// Validate checks the resolved config and image; messages include file:line.
func (r *Resolved) Validate() ErrorList {
	var errs ErrorList
	add := func(path, format string, a ...any) {
		loc := r.Lines[path]
		if loc == "" {
			loc = "-"
		}
		errs = append(errs, fmt.Sprintf("%s: %s: %s", loc, path, fmt.Sprintf(format, a...)))
	}
	enum := func(path, v string, set ...string) {
		if !in(v, set...) {
			add(path, "invalid value %q, want one of %v", v, set)
		}
	}
	nopath := func(path, v string) {
		if hasDotDot(v) {
			add(path, "path %q must not contain '..'", v)
		}
	}
	c := &r.Config
	nopath("storage.images.path", c.Storage.Images.Path)
	nopath("storage.snapshots.path", c.Storage.Snapshots.Path)
	nopath("cache.path", c.Cache.Path)
	nopath("data.default_location", c.Data.DefaultLocation)
	for _, fs := range c.Storage.Snapshots.FSRequired {
		enum("storage.snapshots.fs_required", fs, "ext4", "btrfs", "xfs")
	}
	enum("storage.snapshots.fallback", c.Storage.Snapshots.Fallback, "tmpfs", "none")
	if c.Snapshot.MaxChainDepth < 1 {
		add("snapshot.max_chain_depth", "must be >= 1")
	}
	if c.Snapshot.Retention.KeepLast < 1 {
		add("snapshot.retention.keep_last", "must be >= 1")
	}
	if p := c.Snapshot.Retention.MinFreePercent; p < 0 || p > 99 {
		add("snapshot.retention.min_free_percent", "must be 0..99")
	}
	enum("verify.base_checksum", c.Verify.BaseChecksum, "sha256", "skip")
	if c.Boot.Timeout < 0 {
		add("boot.timeout", "must be >= 0")
	}
	backends := []string{"dir", "ext4-file", "partition", "disk", "network"}
	enum("data.default_backend", c.Data.DefaultBackend, backends...)
	enum("data.default_vm_backend", c.Data.DefaultVMBackend, backends...)
	enum("data.fallback_if_missing", c.Data.FallbackIfMissing, "system-layer", "fail", "tmpfs")
	enum("devices.policy", c.Devices.Policy, "isolate", "passthrough", "hide")
	enum("devices.windows_ntfs", c.Devices.WindowsNTFS, "ro_or_hide", "ro", "hide", "rw")
	enum("devices.hibernated_fs", c.Devices.HibernatedFS, "refuse", "ro", "ignore")
	if c.Network.TimeoutSec < 1 {
		add("network.timeout_sec", "must be >= 1")
	}
	if c.Network.Retries < 0 {
		add("network.retries", "must be >= 0")
	}
	enum("mirror_policy.strategy", c.MirrorPolicy.Strategy, "fallback", "round-robin", "fastest")
	enum("cache.offline", c.Cache.Offline, "auto", "on", "off")
	if c.Cache.MaxSizeGB < 0 {
		add("cache.max_size_gb", "must be >= 0")
	}
	for n, s := range c.Sources {
		enum("sources."+n+".type", s.Type, "simplestreams", "oci-registry", "http", "local")
		if s.URL == "" {
			add("sources."+n+".url", "required")
		}
	}

	img := r.Image
	if img == nil {
		return errs
	}
	if img.Name == "" {
		add("image.name", "required")
	}
	enum("image.type", img.Type, "vm", "lxd-container", "oci")
	enum("image.format", img.Format, "raw", "squashfs")
	enum("image.snapshots.mode", img.Snapshots.Mode, "dm-snapshot", "overlayfs")
	switch img.Type {
	case "vm":
		if img.Format != "raw" {
			add("image.format", "type vm requires format raw, got %q", img.Format)
		}
		if img.Snapshots.Mode != "dm-snapshot" {
			add("image.snapshots.mode", "type vm requires dm-snapshot, got %q", img.Snapshots.Mode)
		}
	case "lxd-container", "oci":
		if img.Format != "squashfs" {
			add("image.format", "type %s requires format squashfs, got %q", img.Type, img.Format)
		}
		if img.Snapshots.Mode != "overlayfs" {
			add("image.snapshots.mode", "type %s requires overlayfs, got %q", img.Type, img.Snapshots.Mode)
		}
	}
	if img.Base == "" {
		add("image.base", "required")
	} else if isRemote(img.Base) && !c.Policy.AllowOnline {
		add("image.base", "remote base %q unusable: policy.allow_online is false", img.Base)
	}
	if img.SHA256 != "" && !sha256Re.MatchString(img.SHA256) {
		add("image.sha256", "must be 64 hex chars")
	}
	nopath("image.base", img.Base)
	nopath("image.kernel.path", img.Kernel.Path)
	nopath("image.kernel.initrd", img.Kernel.Initrd)
	nopath("image.snapshots.path", img.Snapshots.Path)
	if img.Snapshots.MaxChainDepth < 1 {
		add("image.snapshots.max_chain_depth", "must be >= 1")
	}
	if img.Type == "vm" && img.Kernel.Path == "" && img.Kernel.Initrd != "" {
		add("image.kernel.path", "required when initrd is set")
	}
	for i, d := range img.Data {
		p := fmt.Sprintf("image.data[%d]", i)
		if r.Lines[p] == "" {
			r.Lines[p] = r.Lines["image.data"]
		}
		if d.Name == "" {
			add("image.data", "entry %d: name required", i)
		}
		if !strings.HasPrefix(d.Mount, "/") {
			add("image.data", "entry %d (%s): mount must be absolute", i, d.Name)
		}
		if !in(d.Backend, "dir", "ext4-file", "partition", "disk", "network") {
			add("image.data", "entry %d (%s): invalid backend %q", i, d.Name, d.Backend)
		}
		if hasDotDot(d.Path) || hasDotDot(d.Mount) {
			add("image.data", "entry %d (%s): path/mount must not contain '..'", i, d.Name)
		}
	}
	return errs
}
