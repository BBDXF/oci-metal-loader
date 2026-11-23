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
	if !c.SourceAllowed(name) {
		return false
	}
	return c.Policy.AllowOnline || s.Type == "local"
}

// SourceAllowed applies policy.allowed_sources (empty list = every source).
func (c *Config) SourceAllowed(name string) bool {
	if len(c.Policy.AllowedSources) == 0 {
		return true
	}
	return in(name, c.Policy.AllowedSources...)
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
	gd := c.GuestDefaults
	validateRuntime("guest_defaults", Runtime{AutologinUser: gd.AutologinUser, Users: gd.Users, RootPassword: gd.RootPassword, RootPasswordHash: gd.RootPasswordHash}, add, enum)
	nopath("storage.images.path", c.Storage.Images.Path)
	nopath("storage.snapshots.path", c.Storage.Snapshots.Path)
	nopath("cache.path", c.Cache.Path)
	nopath("data.default_location", c.Data.DefaultLocation)
	for _, fs := range c.Storage.Snapshots.FSRequired {
		enum("storage.snapshots.fs_required", fs, "ext4", "btrfs", "xfs")
	}
	sf := c.Storage.Snapshots.StoreFile
	enum("storage.snapshots.store_file.prealloc", sf.Prealloc, "auto", "yes", "no")
	if _, err := ParseSize(sf.Size); err != nil {
		add("storage.snapshots.store_file.size", "%v", err)
	}
	for _, p := range append([]string{sf.Path}, sf.Glob...) {
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			add("storage.snapshots.store_file", "path/glob %q must be absolute inside the host partition (start with /)", p)
		}
		nopath("storage.snapshots.store_file", p)
	}
	if sf.Enabled && sf.Path == "" && len(sf.Glob) == 0 {
		add("storage.snapshots.store_file", "enabled but neither path nor glob is set")
	}
	if len(sf.HostMatch.Label) > 16 {
		add("storage.snapshots.store_file.host_match.label", "label longer than 16 bytes")
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
	enum("boot.ui", c.Boot.UI, "tui", "shell")
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
	enum("mirror_policy.strategy", c.MirrorPolicy.Strategy, "fallback", "fastest", "mirror-only")
	enum("cache.offline", c.Cache.Offline, "auto", "on", "off")
	if c.Cache.MaxSizeGB < 0 {
		add("cache.max_size_gb", "must be >= 0")
	}
	if u := c.Network.Proxy; u != "" && !validProxy(u) {
		add("network.proxy", "must be an http://, https:// or socks5:// URL")
	}
	for _, n := range c.Policy.AllowedSources {
		if _, ok := c.Sources[n]; !ok {
			add("policy.allowed_sources", "unknown source %q", n)
		}
	}
	for n, s := range c.Sources {
		if s.Proxy != "" && s.Proxy != "direct" && !validProxy(s.Proxy) {
			add("sources."+n+".proxy", "must be an http://, https:// or socks5:// URL or \"direct\"")
		}
		for _, m := range s.Mirrors {
			if !isRemote(m) {
				add("sources."+n+".mirrors", "mirror %q must be an http:// or https:// URL", m)
			}
		}
		for _, rw := range s.Rewrite {
			if rw.From == "" {
				add("sources."+n+".rewrite", "from is required")
			}
		}
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
	if img.Type == "vm" {
		add("image.type", "\"vm\" is no longer valid: use \"lxd-vm\"")
	} else {
		enum("image.type", img.Type, "lxd-vm", "lxd-container", "oci")
	}
	if img.Type == "oci" && !c.OCIEnabled() {
		add("image.type", "%s", OCIDisabledMsg)
	}
	enum("image.format", img.Format, "raw", "squashfs")
	enum("image.snapshots.mode", img.Snapshots.Mode, "dm-snapshot", "overlayfs")
	switch img.Type {
	case "lxd-vm":
		if img.Format != "raw" {
			add("image.format", "type lxd-vm requires format raw, got %q", img.Format)
		}
		if img.Snapshots.Mode != "dm-snapshot" {
			add("image.snapshots.mode", "type lxd-vm requires dm-snapshot, got %q", img.Snapshots.Mode)
		}
	case "lxd-container", "oci":
		if img.Format != "squashfs" {
			add("image.format", "type %s requires format squashfs, got %q", img.Type, img.Format)
		}
		if img.Snapshots.Mode != "overlayfs" {
			add("image.snapshots.mode", "type %s requires overlayfs, got %q", img.Type, img.Snapshots.Mode)
		}
	}
	validateRuntime("image.runtime", img.Runtime, add, enum)
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
	if img.Type == "lxd-vm" && img.Kernel.Path == "" && img.Kernel.Initrd != "" {
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

func validProxy(u string) bool {
	return isRemote(u) || strings.HasPrefix(u, "socks5://") || strings.HasPrefix(u, "socks5h://")
}

var nameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func badField(s string) bool { return strings.ContainsAny(s, ":\n\r\x00") }

func validateRuntime(pfx string, rt Runtime, add func(string, string, ...any), enum func(string, string, ...string)) {
	if rt.DebugShell != "" {
		enum(pfx+".debug_shell", rt.DebugShell, "auto", "always", "never")
	}
	if rt.AutologinUser != "" && !nameRe.MatchString(rt.AutologinUser) {
		add(pfx+".autologin_user", "invalid user name %q", rt.AutologinUser)
	}
	if badField(rt.RootPasswordHash) || badField(rt.RootPassword) {
		add(pfx+".root_password", "must not contain ':' or newlines")
	}
	if rt.RootPassword != "" && rt.RootPasswordHash != "" {
		add(pfx+".root_password", "set either root_password or root_password_hash, not both")
	}
	seen := map[string]bool{}
	for i, u := range rt.Users {

		if !nameRe.MatchString(u.Name) {
			add(pfx+".users", "entry %d: invalid user name %q (want [a-z_][a-z0-9_-]{0,31})", i, u.Name)
		}
		if seen[u.Name] {
			add(pfx+".users", "entry %d: duplicate user %q", i, u.Name)
		}
		seen[u.Name] = true
		if u.Password != "" && u.PasswordHash != "" {
			add(pfx+".users", "entry %d (%s): set either password or password_hash, not both", i, u.Name)
		}
		if badField(u.Password) || badField(u.PasswordHash) {
			add(pfx+".users", "entry %d (%s): password must not contain ':' or newlines", i, u.Name)
		}
		if u.PasswordHash != "" && !strings.HasPrefix(u.PasswordHash, "$") && u.PasswordHash != "!" && u.PasswordHash != "*" {
			add(pfx+".users", "entry %d (%s): password_hash must be a crypt hash ($6$...), '!' or '*'", i, u.Name)
		}
		if u.Shell != "" && (!strings.HasPrefix(u.Shell, "/") || badField(u.Shell)) {
			add(pfx+".users", "entry %d (%s): shell must be an absolute path", i, u.Name)
		}
		for _, g := range u.Groups {
			if !nameRe.MatchString(g) {
				add(pfx+".users", "entry %d (%s): invalid group name %q", i, u.Name, g)
			}
		}
		for _, k := range u.SSHKeys {
			if strings.ContainsAny(k, "\n\r") || strings.TrimSpace(k) == "" {
				add(pfx+".users", "entry %d (%s): ssh_authorized_keys entries must be single non-empty lines", i, u.Name)
			}
		}

	}
}
