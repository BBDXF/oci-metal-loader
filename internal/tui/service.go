package tui

import (
	"context"
	"io"
	"time"
)

// ImageInfo is one row of the images list.
type ImageInfo struct {
	Name, Type string // type: vm | lxd-container | oci (experimental)
	Mode       string // dm-snapshot | overlayfs
	Size       int64  // base image size (bytes)
	BaseOK     string // "ok" | "MISMATCH" | "-" (no tree.json yet)
	Snaps      int
	Current    string // "" = base
	LastUsed   time.Time
	Err        string // image.yaml / tree.json problem
}

// NodeInfo is one snapshot.
type NodeInfo struct {
	Name, Parent string // Parent "" = on the base image
	Created      string
	Note         string
	Tags         []string
	Alloc        int64 // on-disk bytes
	NoLayer      bool  // layer missing on disk
}

// TreeInfo is the snapshot tree of one image.
type TreeInfo struct {
	Image, Mode, Type, Current string
	Nodes                      []NodeInfo
}

// SourceInfo is one configured online source.
type SourceInfo struct {
	Name, Type, URL string
	Mirrors         []string
	Usable          bool
	Note            string // blocked / offline-only reason
}

// CatalogItem is one pullable image.
type CatalogItem struct {
	OS, Release, Variant, Kind, Version string // Kind: container | vm
	Size                                int64
}

// Spec is the pull spec lxc:<os>/<release>/<variant>.
func (c CatalogItem) Spec() string { return "lxc:" + c.OS + "/" + c.Release + "/" + c.Variant }

// Filter narrows the catalog.
type Filter struct{ OS, Release, Kind string }

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
	Create(image, name, from, note string, tags []string) error
	Rollback(image, node string, discard bool) error
	DeletePlan(image, node string, recursive, force bool) ([]string, error)
	Delete(image, node string, recursive, force bool) error
	Tag(image, node, tag string, remove bool) error
	SetNote(image, node, note string) error
	Prune(image string, gc, dry bool) ([]string, error)
	Verify(ctx context.Context, image string, deep bool, w io.Writer) error
	Flatten(ctx context.Context, image, node, name string, w io.Writer) error
	ExportDefault(image string) (kind, path string)
	Export(ctx context.Context, image, node, path string, w io.Writer) error
	// BootArgs is the `omloader boot` command line that will be run (display only).
	BootArgs(image, snapshot string, force bool) []string
	BootPreview(ctx context.Context, image, snapshot string, force bool, w io.Writer) error

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
	Force           bool
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
