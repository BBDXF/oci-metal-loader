package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the global omloader.yaml.
type Config struct {
	Storage      Storage           `yaml:"storage" json:"storage"`
	Snapshot     SnapshotCfg       `yaml:"snapshot" json:"snapshot"`
	Verify       Verify            `yaml:"verify" json:"verify"`
	Boot         Boot              `yaml:"boot" json:"boot"`
	Data         DataCfg           `yaml:"data" json:"data"`
	Devices      Devices           `yaml:"devices" json:"devices"`
	Network      Network           `yaml:"network" json:"network"`
	MirrorPolicy MirrorPolicy      `yaml:"mirror_policy" json:"mirror_policy"`
	Sources      map[string]Source `yaml:"sources" json:"sources"`
	// DefaultSource is the source used by pull/images list when neither --source nor a spec prefix names one (sources.yaml).
	DefaultSource string       `yaml:"default_source" json:"default_source"`
	Cache         Cache        `yaml:"cache" json:"cache"`
	Policy        Policy       `yaml:"policy" json:"policy"`
	Experimental  Experimental `yaml:"experimental" json:"experimental"`
	// GuestDefaults: console accounts/autologin applied to every image (all types) that does not set its own (see EffectiveRuntime).
	GuestDefaults GuestDefaults `yaml:"guest_defaults" json:"guest_defaults"`
}

type Storage struct {
	Images    ImagesStore `yaml:"images" json:"images"`
	Snapshots SnapStore   `yaml:"snapshots" json:"snapshots"`
}
type ImagesStore struct {
	Label []string `yaml:"label" json:"label"`
	Path  string   `yaml:"path" json:"path"`
}
type SnapStore struct {
	Label      string   `yaml:"label" json:"label"`
	Path       string   `yaml:"path" json:"path"`
	FSRequired []string `yaml:"fs_required" json:"fs_required"`
	Fallback   string   `yaml:"fallback" json:"fallback"`
	// StoreFile: the store may be an ext4 image file (label OMLOADER) on a Windows-readable host partition.
	StoreFile StoreFile `yaml:"store_file" json:"store_file"`
}

// StoreFile configures "store-file mode". Precedence in the loader: a real ext4/btrfs/xfs partition labelled
// OMLOADER wins, else a store file found on the host partition, else `fallback`.
type StoreFile struct {
	Enabled    bool      `yaml:"enabled" json:"enabled"`
	Path       string    `yaml:"path" json:"path"`               // exact file inside the host partition (checked first), e.g. /omloader/store.img
	Glob       []string  `yaml:"glob" json:"glob"`               // patterns inside the host partition; only files whose ext4 label is OMLOADER count
	HostMatch  HostMatch `yaml:"host_match" json:"host_match"`   // which partition holds the file (uuid wins over label)
	Size       string    `yaml:"size" json:"size"`               // size of `omloader store create` and of the auto-created store
	AutoCreate bool      `yaml:"auto_create" json:"auto_create"` // create omloader/store.img at boot when no store exists (default true)
	Prealloc   string    `yaml:"prealloc" json:"prealloc"`       // auto (yes on exfat/vfat) | yes | no
}

// HostMatch selects the host partition of a store file.
type HostMatch struct {
	Label string `yaml:"label" json:"label"`
	UUID  string `yaml:"uuid" json:"uuid"`
}

// ParseSize parses "512M", "20G", "1.5G", "1048576" (bytes) into bytes.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	for _, u := range []struct {
		suf string
		m   int64
	}{{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}, {"T", 1 << 40}} {
		for _, x := range []string{u.suf + "IB", u.suf + "B", u.suf} {
			if strings.HasSuffix(s, x) {
				s, mult = strings.TrimSuffix(s, x), u.m
				goto parsed
			}
		}
	}
parsed:
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return int64(f * float64(mult)), nil
}

type Retention struct {
	KeepLast       int  `yaml:"keep_last" json:"keep_last"`
	MinFreePercent int  `yaml:"min_free_percent" json:"min_free_percent"` // space guard: refuse create/boot below this % free
	KeepTagged     bool `yaml:"keep_tagged" json:"keep_tagged"`           // prune never deletes tagged snapshots
}
type SnapshotCfg struct {
	MaxChainDepth int       `yaml:"max_chain_depth" json:"max_chain_depth"`
	Persistent    bool      `yaml:"persistent" json:"persistent"`
	Retention     Retention `yaml:"retention" json:"retention"`
}
type Verify struct {
	BaseChecksum string `yaml:"base_checksum" json:"base_checksum"`
}
type Boot struct {
	Timeout            int    `yaml:"timeout" json:"timeout"`
	DefaultSnapshot    string `yaml:"default_snapshot" json:"default_snapshot"`
	KernelCmdlineExtra string `yaml:"kernel_cmdline_extra" json:"kernel_cmdline_extra"`
	// DefaultImage: image to boot automatically (after Timeout seconds) when the kernel command line has no
	// omloader.image= (e.g. under Ventoy, which cannot pass kernel parameters). Empty = drop to the shell.
	DefaultImage string `yaml:"default_image" json:"default_image"`
	// VMFixESPFstab (default true): lxd-vm boots add `nofail,x-systemd.device-timeout=1s` to the guest's /boot/efi fstab entry (see docs).
	VMFixESPFstab bool `yaml:"vm_fix_esp_fstab" json:"vm_fix_esp_fstab"`
	// ExtraCmdlineFlags: omloader.* equivalents without the prefix, e.g. ["poweroff_on_fail=1", "experimental=oci", "selftest=1"].
	ExtraCmdlineFlags []string `yaml:"extra_cmdline_flags" json:"extra_cmdline_flags"`
	// UI: what the console shows when nothing is booted automatically: "tui" (interactive terminal UI, default) or "shell".
	// The kernel flag omloader.ui=shell|tui wins over this key.
	UI string `yaml:"ui" json:"ui"`
	// RememberLast: the TUI records the last booted image/snapshot in <data>/omloader/last-boot (silently skipped when not
	// writable); it becomes the TUI's initial selection, and default_image: last boots it automatically.
	RememberLast bool `yaml:"remember_last" json:"remember_last"`
}
type DataCfg struct {
	DefaultBackend    string `yaml:"default_backend" json:"default_backend"`
	DefaultVMBackend  string `yaml:"default_vm_backend" json:"default_vm_backend"`
	DefaultLocation   string `yaml:"default_location" json:"default_location"`
	AutoHomeVolume    bool   `yaml:"auto_home_volume" json:"auto_home_volume"`
	FallbackIfMissing string `yaml:"fallback_if_missing" json:"fallback_if_missing"`
}
type Devices struct {
	Policy       string `yaml:"policy" json:"policy"`
	WindowsNTFS  string `yaml:"windows_ntfs" json:"windows_ntfs"`
	HibernatedFS string `yaml:"hibernated_fs" json:"hibernated_fs"`
}
type Network struct {
	TimeoutSec int      `yaml:"timeout_sec" json:"timeout_sec"`
	Retries    int      `yaml:"retries" json:"retries"`
	Proxy      string   `yaml:"proxy" json:"proxy"`       // http://, https:// or socks5:// URL
	NoProxy    string   `yaml:"no_proxy" json:"no_proxy"` // comma separated hosts/domains/CIDRs
	ExtraCA    string   `yaml:"extra_ca" json:"extra_ca"` // PEM file added to the system CA pool
	DNS        []string `yaml:"dns" json:"dns"`           // informational (resolver is systemd-resolved)
}
type MirrorPolicy struct {
	Strategy      string `yaml:"strategy" json:"strategy"`
	HealthCheck   bool   `yaml:"health_check" json:"health_check"`
	CacheTTLHours int    `yaml:"cache_ttl_hours" json:"cache_ttl_hours"`
	RegionPreset  string `yaml:"region_preset" json:"region_preset"`
}
type Source struct {
	Type    string    `yaml:"type" json:"type"`
	URL     string    `yaml:"url" json:"url"`
	Mirrors []string  `yaml:"mirrors" json:"mirrors"`
	Proxy   string    `yaml:"proxy" json:"proxy"` // overrides network.proxy for this source ("direct" = none)
	Rewrite []Rewrite `yaml:"rewrite" json:"rewrite"`
	// sources.yaml additions (see docs/sources.md)
	Title   string            `yaml:"title" json:"title"`
	Note    string            `yaml:"note" json:"note"`
	Enabled *bool             `yaml:"enabled" json:"enabled"` // false hides/disables the source (how a user file "removes" a built-in one)
	Mirror  string            `yaml:"mirror" json:"mirror"`   // selected mirror: official (default) | auto | a name from servers
	Servers map[string]Mirror `yaml:"servers" json:"servers"` // named mirrors of this source
}

// Mirror is one named server of a source (sources.<name>.servers.<id>).
type Mirror struct {
	Title   string    `yaml:"title" json:"title"`
	URL     string    `yaml:"url" json:"url"`   // base URL (oci-registry: host or https://host)
	Path    string    `yaml:"path" json:"path"` // optional path appended to url
	Region  string    `yaml:"region" json:"region"`
	Note    string    `yaml:"note" json:"note"`
	Enabled *bool     `yaml:"enabled" json:"enabled"`
	Rewrite []Rewrite `yaml:"rewrite" json:"rewrite"` // prefix rewrites of the RELATIVE path (index/catalog/blob) for this server only
}

// Rewrite replaces a URL prefix (applied to every candidate URL of the source).
type Rewrite struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
}

type Cache struct {
	Path      string `yaml:"path" json:"path"`
	MaxSizeGB int    `yaml:"max_size_gb" json:"max_size_gb"`
	Offline   string `yaml:"offline" json:"offline"`
}
type Policy struct {
	AllowOnline    bool     `yaml:"allow_online" json:"allow_online"`
	AllowedSources []string `yaml:"allowed_sources" json:"allowed_sources"` // empty = all configured sources
}

// Image is a per-image image.yaml.
type Image struct {
	Name      string       `yaml:"name" json:"name"`
	Type      string       `yaml:"type" json:"type"`
	Format    string       `yaml:"format" json:"format"`
	Base      string       `yaml:"base" json:"base"`
	SHA256    string       `yaml:"sha256" json:"sha256"`
	Kernel    Kernel       `yaml:"kernel" json:"kernel"`
	Snapshots ImageSnaps   `yaml:"snapshots" json:"snapshots"`
	Runtime   Runtime      `yaml:"runtime" json:"runtime"`
	Data      []DataVolume `yaml:"data" json:"data"`
}
type Kernel struct {
	Path          string   `yaml:"path" json:"path"`
	Initrd        string   `yaml:"initrd" json:"initrd"`
	Cmdline       string   `yaml:"cmdline" json:"cmdline"`
	RootPartition int      `yaml:"root_partition" json:"root_partition"`
	InjectModules []string `yaml:"inject_modules" json:"inject_modules"`
}
type ImageSnaps struct {
	Path          string `yaml:"path" json:"path"`
	MaxChainDepth int    `yaml:"max_chain_depth" json:"max_chain_depth"`
	Mode          string `yaml:"mode" json:"mode"`
}
type Runtime struct {
	Entrypoint []string          `yaml:"entrypoint" json:"entrypoint"`
	Cmd        []string          `yaml:"cmd" json:"cmd"`
	WorkingDir string            `yaml:"workdir" json:"workdir"`
	Env        map[string]string `yaml:"env" json:"env"`
	Network    string            `yaml:"network" json:"network"`
	Hostname   string            `yaml:"hostname" json:"hostname"`
	Autologin  *bool             `yaml:"autologin" json:"autologin"` // console autologin on tty1 and ttyS0; nil = not set (guest_defaults decide)
	// AutologinUser is the account for autologin (default root).
	AutologinUser    string `yaml:"autologin_user" json:"autologin_user"`
	Users            []User `yaml:"users" json:"users"`
	RootPasswordHash string `yaml:"root_password_hash" json:"root_password_hash"`
	RootPassword     string `yaml:"root_password" json:"root_password"` // plain text: hashed (SHA-512 crypt) by the loader; prefer root_password_hash
	// Shell chooses the console shell omloader provides when the guest has no getty/login (or no init at all, e.g. nginx:alpine):
	// auto (default: the image's own shell when it has one, else an injected static busybox), image (never inject busybox) or busybox (force it).
	Shell string `yaml:"shell" json:"shell"`
	// DebugShell is the old spelling: auto, always (= shell: busybox), never (= shell: image). `shell` wins when both are set.
	DebugShell string `yaml:"debug_shell" json:"debug_shell"`
	// ShutdownSync (default true) installs the hooks that remount the loader partitions read-only at poweroff.
	ShutdownSync *bool `yaml:"shutdown_sync" json:"shutdown_sync"`
	// FromDefaults is set by EffectiveRuntime when accounts/autologin come from guest_defaults (not read from YAML).
	FromDefaults bool `yaml:"-" json:"-"`
}

// AutologinOn reports the effective runtime.autologin.
func (r Runtime) AutologinOn() bool { return r.Autologin != nil && *r.Autologin }

// User is a guest account created offline by the firstboot adapter.
type User struct {
	Name         string   `yaml:"name" json:"name"`
	PasswordHash string   `yaml:"password_hash" json:"password_hash"`
	Password     string   `yaml:"password" json:"password"` // plain text: hashed by the loader; prefer password_hash
	Groups       []string `yaml:"groups" json:"groups"`
	Shell        string   `yaml:"shell" json:"shell"`
	Sudo         bool     `yaml:"sudo" json:"sudo"`
	SSHKeys      []string `yaml:"ssh_authorized_keys" json:"ssh_authorized_keys"`
}

// SyncOnShutdown reports the effective runtime.shutdown_sync (default true).
func (r Runtime) SyncOnShutdown() bool { return r.ShutdownSync == nil || *r.ShutdownSync }

type DataVolume struct {
	Name     string `yaml:"name" json:"name"`
	Mount    string `yaml:"mount" json:"mount"`
	Backend  string `yaml:"backend" json:"backend"`
	Path     string `yaml:"path" json:"path"`
	Size     string `yaml:"size" json:"size"`
	Snapshot bool   `yaml:"snapshot" json:"snapshot"`
	Shared   bool   `yaml:"shared" json:"shared"`
}

// Experimental labels features that are not part of the project's focus (Docker/OCI images).
type Experimental struct {
	OCIImages bool `yaml:"oci_images" json:"oci_images"` // Docker/OCI image support (import + boot); EXPERIMENTAL, on by default (default from defaults.yaml); false switches it off
}

// OCIDisabledMsg is the error shown when OCI/Docker support has been switched off.
const OCIDisabledMsg = "Docker/OCI images are experimental and have been disabled here (experimental.oci_images: false or OML_EXPERIMENTAL=off / omloader.experimental=off); re-enable with experimental.oci_images: true, --experimental-oci or OML_EXPERIMENTAL=oci"

// EnvOCI reports whether OML_EXPERIMENTAL (comma separated) contains "oci".
func EnvOCI() bool { return envHas("oci") }

// EnvOCIOff reports whether OML_EXPERIMENTAL switches the OCI support off (off, none, 0 or false) and does not also say "oci".
func EnvOCIOff() bool {
	return !EnvOCI() && (envHas("off") || envHas("none") || envHas("0") || envHas("false"))
}

func envHas(w string) bool {
	for _, f := range strings.Split(os.Getenv("OML_EXPERIMENTAL"), ",") {
		if strings.TrimSpace(f) == w {
			return true
		}
	}
	return false
}

// OCIEnabled: OML_EXPERIMENTAL=oci forces on, =off forces off, otherwise experimental.oci_images (default true).
func (c *Config) OCIEnabled() bool {
	if EnvOCI() {
		return true
	}
	if EnvOCIOff() {
		return false
	}
	return c.Experimental.OCIImages
}

// ShellMode is the effective console shell policy: auto | image | busybox.
func (rt Runtime) ShellMode() string {
	switch {
	case rt.Shell != "":
		return rt.Shell
	case rt.DebugShell == "always":
		return "busybox"
	case rt.DebugShell == "never":
		return "image"
	}
	return "auto"
}

// GuestDefaults are the accounts and console autologin that every image (lxd-container, lxd-vm, experimental oci) gets at boot
// unless the image sets its own (merge rules: EffectiveRuntime). The password is public by design.
type GuestDefaults struct {
	Autologin        *bool  `yaml:"autologin" json:"autologin"`
	AutologinUser    string `yaml:"autologin_user" json:"autologin_user"`
	RootPassword     string `yaml:"root_password" json:"root_password"`
	RootPasswordHash string `yaml:"root_password_hash" json:"root_password_hash"`
	Users            []User `yaml:"users" json:"users"`
	// Hostname of the guest: "" or "image" = the image name sanitised to a DNS label, "keep" = do not touch the guest, else the literal name.
	Hostname string `yaml:"hostname" json:"hostname"`
}

// EffectiveRuntime merges guest_defaults into an image's runtime (all image types; the image always wins):
//   - users: a non-nil image list (even `users: []`) replaces the default list; nil takes the defaults.
//   - root password: root_password / root_password_hash of the image (any non-empty) replace the default pair.
//     To lock root instead, set root_password_hash: '!'.
//   - autologin + autologin_user: if the image sets either, both come from the image (autologin_user empty = root);
//     otherwise both come from the defaults. A default autologin_user that does not exist in the final user list falls back to root.
func EffectiveRuntime(g GuestDefaults, rt Runtime) Runtime {
	if rt.Users == nil {
		rt.Users = g.Users
		rt.FromDefaults = rt.FromDefaults || len(g.Users) > 0
	}
	if rt.RootPassword == "" && rt.RootPasswordHash == "" {
		rt.RootPassword, rt.RootPasswordHash = g.RootPassword, g.RootPasswordHash
		rt.FromDefaults = rt.FromDefaults || g.RootPassword != "" || g.RootPasswordHash != ""
	}
	if rt.Autologin == nil && rt.AutologinUser == "" {
		rt.Autologin, rt.AutologinUser = g.Autologin, g.AutologinUser
		if rt.AutologinUser != "" && rt.AutologinUser != "root" {
			found := false
			for _, u := range rt.Users {
				found = found || u.Name == rt.AutologinUser
			}
			if !found {
				rt.AutologinUser = ""
			}
		}
	}
	return rt
}
