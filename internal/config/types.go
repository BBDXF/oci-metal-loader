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
	Cache        Cache             `yaml:"cache" json:"cache"`
	Policy       Policy            `yaml:"policy" json:"policy"`
	Experimental Experimental      `yaml:"experimental" json:"experimental"`
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
	Enabled   bool      `yaml:"enabled" json:"enabled"`
	Path      string    `yaml:"path" json:"path"`             // exact file inside the host partition (checked first), e.g. /omloader/store.img
	Glob      []string  `yaml:"glob" json:"glob"`             // patterns inside the host partition; only files whose ext4 label is OMLOADER count
	HostMatch HostMatch `yaml:"host_match" json:"host_match"` // which partition holds the file (uuid wins over label)
	Size      string    `yaml:"size" json:"size"`             // default size of `omloader store create`
	Prealloc  string    `yaml:"prealloc" json:"prealloc"`     // auto (yes on exfat/vfat) | yes | no
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
	MaxChainDepth      int       `yaml:"max_chain_depth" json:"max_chain_depth"`
	Persistent         bool      `yaml:"persistent" json:"persistent"`
	AutoSnapshotOnBoot bool      `yaml:"auto_snapshot_on_boot" json:"auto_snapshot_on_boot"`
	Retention          Retention `yaml:"retention" json:"retention"`
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
	// ExtraCmdlineFlags: omloader.* equivalents without the prefix, e.g. ["poweroff_on_fail=1", "experimental=oci", "selftest=1"].
	ExtraCmdlineFlags []string `yaml:"extra_cmdline_flags" json:"extra_cmdline_flags"`
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
}

// Rewrite replaces a URL prefix (applied to every candidate URL of the source).
type Rewrite struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
}

// RegionPresets are UNVERIFIED mirror presets added to source "lxc-images" by mirror_policy.region_preset.
// Content is always digest-verified against the upstream index, but the mirror URLs themselves are not guaranteed.
var RegionPresets = map[string][]string{
	"cn": {"https://mirrors.tuna.tsinghua.edu.cn/lxc-images", "https://mirror.nju.edu.cn/lxc-images"},
}

// SourceMirrors returns the configured mirrors of a source plus (for lxc-images) the region preset.
func (c *Config) SourceMirrors(name string) []string {
	m := append([]string{}, c.Sources[name].Mirrors...)
	if name == "lxc-images" {
		for _, p := range RegionPresets[c.MirrorPolicy.RegionPreset] {
			dup := false
			for _, e := range m {
				dup = dup || e == p
			}
			if !dup {
				m = append(m, p)
			}
		}
	}
	return m
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
	Autologin  bool              `yaml:"autologin" json:"autologin"` // console autologin on tty1 and ttyS0 (systemd guests)
	// AutologinUser is the account for autologin (default root).
	AutologinUser    string `yaml:"autologin_user" json:"autologin_user"`
	Users            []User `yaml:"users" json:"users"`
	RootPasswordHash string `yaml:"root_password_hash" json:"root_password_hash"`
	RootPassword     string `yaml:"root_password" json:"root_password"` // plain text: hashed (SHA-512 crypt) by the loader; prefer root_password_hash
	// DebugShell: auto (busybox shell on the console when the guest has no getty/login), always, never.
	DebugShell string `yaml:"debug_shell" json:"debug_shell"`
	// ShutdownSync (default true) installs the hooks that remount the loader partitions read-only at poweroff.
	ShutdownSync *bool `yaml:"shutdown_sync" json:"shutdown_sync"`
}

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

// Experimental gates features that are not part of the project's focus (LXD images).
type Experimental struct {
	OCIImages bool `yaml:"oci_images" json:"oci_images"` // Docker/OCI image support (import + boot); off by default
}

// OCIDisabledMsg is the error shown when OCI/Docker support is used without opt-in.
const OCIDisabledMsg = "OCI/Docker images are experimental and disabled by default; set experimental.oci_images: true, pass --experimental-oci or set OML_EXPERIMENTAL=oci"

// EnvOCI reports whether OML_EXPERIMENTAL (comma separated) contains "oci".
func EnvOCI() bool {
	for _, f := range strings.Split(os.Getenv("OML_EXPERIMENTAL"), ",") {
		if strings.TrimSpace(f) == "oci" {
			return true
		}
	}
	return false
}

// OCIEnabled is true when the config key or the environment opts in to OCI images.
func (c *Config) OCIEnabled() bool { return c.Experimental.OCIImages || EnvOCI() }
