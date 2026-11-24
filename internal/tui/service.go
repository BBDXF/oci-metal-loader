package tui

import (
	"context"
	"io"
	"time"
)

// ImageInfo is one row of the images list.
type ImageInfo struct {
	Name, Type string // type: lxd-vm | lxd-container | oci (experimental)
	Mode       string // dm-snapshot | overlayfs
	Size       int64  // base image size (bytes)
	BaseOK     string // "ok" | "MISMATCH" | "-" (no tree.json yet)
	Snaps      int
	Current    string // label of the snapshot the live state sits on ("" = base)
	LastUsed   time.Time
	Err        string // image.yaml / tree.json problem
}

// NodeInfo is one layer of the snapshot tree: a snapshot (Label set), the live state (Live) or an ephemeral boot.
type NodeInfo struct {
	Name, Parent string // Name = node id; Parent "" = on the base image
	Label        string // snapshot label ("" for the live state / ephemeral nodes)
	Created      string
	Note         string
	Tags         []string
	Alloc        int64 // on-disk bytes
	NoLayer      bool  // layer missing on disk
	Live         bool  // the live state ("current")
	Ephemeral    bool
}

// TreeInfo is the snapshot tree of one image.
type TreeInfo struct {
	Image, Mode, Type string
	Current           string // id of the live node, "" = none yet (it is created at the next boot)
	Head              string // id of the snapshot the live state sits on, "" = base
	Nodes             []NodeInfo
}

// SourceInfo is one configured online source.
type SourceInfo struct {
	Name, Type, URL string
	Mirrors         []string
	Usable          bool
	Note            string   // blocked / offline-only reason
	Mirror          string   // configured mirror selection (official | auto | server id)
	MirrorIDs       []string // selectable mirrors: official, auto, then the server ids of sources.yaml
}

// The `source` string passed to SourceTest/Catalog/Pull is "NAME" or "NAME@MIRROR" (mirror override, see SourceRef).

// CatalogItem is one pullable image.
type CatalogItem struct {
	OS, Release, Variant, Arch, Kind, Version string // Kind: container | vm
	Size                                      int64
}

// Spec is the pull spec lxc:<os>/<release>/<variant>.
func (c CatalogItem) Spec() string { return "lxc:" + c.OS + "/" + c.Release + "/" + c.Variant }

// Filter is a free-text, multi-keyword search (see internal/imgsearch).
type Filter struct{ Text string }

// StoreStatus describes the snapshot store.
type StoreStatus struct {
	Kind        string // partition | store-file | none | tmpfs ...
	Path        string
	HostFS      string
	Size, Alloc int64
	Total, Free int64
	FreePct     float64
	MinFreePct  int
	State       string // clean | dirty | ...
	Attached    bool
	Notes       []string
}

// Service is everything the model needs from the loader (library-backed in cmd/omloader, fake in tests). Methods that can
// take long get a context and a writer for streamed output.
type Service interface {
	Images() ([]ImageInfo, error)
	Tree(image string) (*TreeInfo, error)
	// Create freezes the live state as snapshot label. Revert discards the live state and continues from target (label or
	// "base"); keep != "" first freezes the old live state under that label.
	Create(image, label, note string, tags []string) error
	Revert(image, target, keep string) error
	// DeletePlan describes what deleting a snapshot would do (lines) or why it is refused.
	DeletePlan(image, label string, recursive, force bool) ([]string, error)
	Delete(image, label string, recursive, force bool) error
	Tag(image, label, tag string, remove bool) error
	SetNote(image, label, note string) error
	// Prune applies the retention policy, or (gc) removes ephemeral boot leftovers.
	Prune(image string, gc, dry bool) ([]string, error)
	Verify(ctx context.Context, image string, deep bool, w io.Writer) error
	Flatten(ctx context.Context, image, node, name string, w io.Writer) error
	ExportDefault(image string) (kind, path string)
	Export(ctx context.Context, image, node, path string, w io.Writer) error
	// BootArgs is the `omloader boot` command line that will be run (display only).
	BootArgs(image, snapshot string, ephemeral bool) []string
	BootPreview(ctx context.Context, image, snapshot string, ephemeral bool, w io.Writer) error

	Sources() ([]SourceInfo, error)
	SourceTest(ctx context.Context, name string, w io.Writer) error
	Catalog(ctx context.Context, source string, f Filter) ([]CatalogItem, error)
	Pull(ctx context.Context, source string, it CatalogItem, name string, w io.Writer) error
	DefaultPullName(it CatalogItem) string
	// PullBytes reports bytes downloaded so far (partial files in the cache) for the progress line.
	PullBytes() int64
	Offline() bool

	Store() (StoreStatus, error)
	StoreDefaults() (path, size string)
	StoreCreate(ctx context.Context, path, size string, w io.Writer) error
	StoreGrow(ctx context.Context, path, size string, w io.Writer) error

	NetStatus(ctx context.Context, probe bool, w io.Writer) error
	Settings() ([]string, error)
}

// BootRequest asks the caller to run `omloader boot` after the TUI has restored the terminal.
type BootRequest struct {
	Image, Snapshot string
	Ephemeral       bool
	Flags           string // auto-boot only: boot.extra_cmdline_flags (poweroff_on_fail=1, experimental=oci)
}

// ResultKind is how the model ended.
type ResultKind int

const (
	ResNone ResultKind = iota
	ResQuit            // leave to the shell
	ResShell
	ResReboot
	ResPowerOff
	ResBoot
)

// Result is the model's final outcome.
type Result struct {
	Kind ResultKind
	Boot BootRequest
}
