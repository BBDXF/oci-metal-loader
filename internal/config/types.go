package config

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
	TimeoutSec int `yaml:"timeout_sec" json:"timeout_sec"`
	Retries    int `yaml:"retries" json:"retries"`
}
type MirrorPolicy struct {
	Strategy      string `yaml:"strategy" json:"strategy"`
	HealthCheck   bool   `yaml:"health_check" json:"health_check"`
	CacheTTLHours int    `yaml:"cache_ttl_hours" json:"cache_ttl_hours"`
	RegionPreset  string `yaml:"region_preset" json:"region_preset"`
}
type Source struct {
	Type string `yaml:"type" json:"type"`
	URL  string `yaml:"url" json:"url"`
}
type Cache struct {
	Path      string `yaml:"path" json:"path"`
	MaxSizeGB int    `yaml:"max_size_gb" json:"max_size_gb"`
	Offline   string `yaml:"offline" json:"offline"`
}
type Policy struct {
	AllowOnline bool `yaml:"allow_online" json:"allow_online"`
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
	Autologin  bool              `yaml:"autologin" json:"autologin"` // serial-getty autologin on ttyS0 (tests/debug)
}
type DataVolume struct {
	Name     string `yaml:"name" json:"name"`
	Mount    string `yaml:"mount" json:"mount"`
	Backend  string `yaml:"backend" json:"backend"`
	Path     string `yaml:"path" json:"path"`
	Size     string `yaml:"size" json:"size"`
	Snapshot bool   `yaml:"snapshot" json:"snapshot"`
	Shared   bool   `yaml:"shared" json:"shared"`
}
