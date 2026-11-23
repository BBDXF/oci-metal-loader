package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type fakeSvc struct {
	mu        sync.Mutex
	trees     map[string]*TreeInfo
	calls     []string
	offline   bool
	pullHold  chan struct{}
	verifyErr error
	cjk       bool
}

func (f *fakeSvc) Header() HeaderInfo {
	return HeaderInfo{Version: "v9.9", StoreKind: "store-file", StoreFree: 3 << 30, StoreTotal: 8 << 30, ImagesUsed: 2 << 30, Net: "10.0.0.5", Offline: f.offline}
}

func newFake() *fakeSvc {
	return &fakeSvc{trees: map[string]*TreeInfo{
		"alpine": {Image: "alpine", Mode: "overlayfs", Type: "lxd-container", Current: "n3", Head: "n2", Nodes: []NodeInfo{
			{Name: "n1", Label: "a", Created: "2026-01-01T10:00:00Z", Note: "first", Tags: []string{"golden"}, Alloc: 4096},
			{Name: "n2", Label: "b", Parent: "n1", Created: "2026-01-02T10:00:00Z", Alloc: 8192},
			{Name: "n3", Parent: "n2", Created: "2026-01-02T12:00:00Z", Live: true, Alloc: 1024},
			{Name: "n4", Label: "c", Parent: "n1", Created: "2026-01-03T10:00:00Z"},
		}},
		"deb": {Image: "deb", Mode: "dm-snapshot", Type: "lxd-vm"},
	}}
}

func (f *fakeSvc) rec(s string, a ...any) {
	f.mu.Lock()
	f.calls = append(f.calls, fmt.Sprintf(s, a...))
	f.mu.Unlock()
}
func (f *fakeSvc) called(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func (f *fakeSvc) Images() ([]ImageInfo, error) {
	n := "alpine"
	if f.cjk {
		n = "世界镜像"
	}
	return []ImageInfo{
		{Name: n, Type: "lxd-container", Mode: "overlayfs", Size: 3 << 20, BaseOK: "ok", Snaps: 3, Current: "b", LastUsed: time.Date(2026, 1, 3, 11, 0, 0, 0, time.UTC)},
		{Name: "deb", Type: "lxd-vm", Mode: "dm-snapshot", Size: 2 << 30, BaseOK: "-"},
	}, nil
}
func (f *fakeSvc) Tree(i string) (*TreeInfo, error) {
	t, ok := f.trees[i]
	if !ok {
		return nil, errors.New("no such image")
	}
	cp := *t
	cp.Nodes = append([]NodeInfo{}, t.Nodes...)
	return &cp, nil
}
func (f *fakeSvc) Create(i, label, note string, tags []string) error {
	f.rec("create %s %s note=%q tags=%v", i, label, note, tags)
	return nil
}
func (f *fakeSvc) Revert(i, target, keep string) error {
	f.rec("revert %s %s keep=%q", i, target, keep)
	return nil
}
func (f *fakeSvc) DeletePlan(i, n string, rec, force bool) ([]string, error) {
	if rec {
		return []string{"remove the subtree: c, b, " + n}, nil
	}
	return []string{"merge " + n + " into current, then remove " + n}, nil
}
func (f *fakeSvc) Delete(i, n string, rec, force bool) error {
	f.rec("delete %s %s rec=%v force=%v", i, n, rec, force)
	return nil
}
func (f *fakeSvc) Tag(i, n, tag string, rm bool) error {
	f.rec("tag %s %s %s rm=%v", i, n, tag, rm)
	return nil
}
func (f *fakeSvc) SetNote(i, n, note string) error { f.rec("note %s %s %q", i, n, note); return nil }
func (f *fakeSvc) Prune(i string, gc, dry bool) ([]string, error) {
	f.rec("prune gc=%v dry=%v", gc, dry)
	return []string{"c"}, nil
}
func (f *fakeSvc) Verify(ctx context.Context, i string, deep bool, w io.Writer) error {
	f.rec("verify %s deep=%v", i, deep)
	fmt.Fprintln(w, "verify alpine: OK (3 snapshots)")
	return f.verifyErr
}
func (f *fakeSvc) Flatten(ctx context.Context, i, n, name string, w io.Writer) error {
	f.rec("flatten %s %s %s", i, n, name)
	fmt.Fprintln(w, "flattening")
	return nil
}
func (f *fakeSvc) ExportDefault(i string) (string, string) {
	return "squashfs", "/mnt/snap/export/" + i + ".squashfs"
}
func (f *fakeSvc) Export(ctx context.Context, i, n, p string, w io.Writer) error {
	f.rec("export %s %s %s", i, n, p)
	return nil
}
func (f *fakeSvc) BootArgs(i, s string, eph bool) []string {
	a := []string{"omloader", "boot", "--image", i, "--snapshot", s}
	if eph {
		a = append(a, "--ephemeral")
	}
	return a
}
func (f *fakeSvc) BootPreview(ctx context.Context, i, s string, eph bool, w io.Writer) error {
	f.rec("preview %s %s", i, s)
	fmt.Fprintln(w, "+ losetup ...")
	return nil
}
func (f *fakeSvc) Sources() ([]SourceInfo, error) {
	return []SourceInfo{{Name: "lxc-images", Type: "simplestreams", URL: "https://images.example", Usable: true}}, nil
}
func (f *fakeSvc) SourceTest(ctx context.Context, n string, w io.Writer) error {
	fmt.Fprintln(w, "    OK      12ms  https://images.example")
	return nil
}
func (f *fakeSvc) Catalog(ctx context.Context, s string, fl Filter) ([]CatalogItem, error) {
	f.rec("catalog %s %+v", s, fl)
	return []CatalogItem{{OS: "alpine", Release: "3.20", Variant: "default", Arch: "amd64", Kind: "container", Version: "20260101", Size: 3 << 20},
		{OS: "archlinux", Release: "current", Variant: "cloud", Arch: "amd64", Kind: "vm", Version: "20260101", Size: 3 << 20}}, nil
}
func (f *fakeSvc) Pull(ctx context.Context, s string, it CatalogItem, name string, w io.Writer) error {
	f.rec("pull %s %s %s", s, it.Spec(), name)
	fmt.Fprintln(w, "pull: sha256 verified")
	if f.pullHold != nil {
		select {
		case <-f.pullHold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (f *fakeSvc) DefaultPullName(it CatalogItem) string { return it.OS + "-" + it.Release }
func (f *fakeSvc) PullBytes() int64                      { return 1 << 20 }
func (f *fakeSvc) Offline() bool                         { return f.offline }
func (f *fakeSvc) Store() (StoreStatus, error) {
	return StoreStatus{Kind: "store-file", Path: "/mnt/host/omloader/store.img", HostFS: "exfat", Size: 20 << 30, Alloc: 20 << 30, Total: 19 << 30, Free: 15 << 30, FreePct: 79, MinFreePct: 10, State: "clean"}, nil
}
func (f *fakeSvc) StoreDefaults() (string, string) { return "/mnt/host/omloader/store.img", "20G" }
func (f *fakeSvc) StoreCreate(ctx context.Context, p, s string, w io.Writer) error {
	f.rec("storecreate %s %s", p, s)
	return nil
}
func (f *fakeSvc) StoreGrow(ctx context.Context, p, s string, w io.Writer) error {
	f.rec("storegrow %s %s", p, s)
	return nil
}
func (f *fakeSvc) NetStatus(ctx context.Context, probe bool, w io.Writer) error {
	fmt.Fprintf(w, "interfaces:\n  eth0 up 10.0.2.15/24\ndefault route: via 10.0.2.2\nprobe=%v\n", probe)
	return nil
}
func (f *fakeSvc) Settings() ([]string, error) {
	return []string{"boot.ui = tui    [defaults]", "boot.timeout = 5    [defaults]"}, nil
}
