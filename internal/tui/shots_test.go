package tui

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// shotSvc is the sample service of the simulated screenshots: catalog rows are real `omloader images list` output
// (testdata/real_catalog.txt), the installed images are ones pulled from it.
type shotSvc struct {
	*fakeSvc
	cat []CatalogItem
}

func loadRealCatalog(t *testing.T) []CatalogItem {
	b, err := os.ReadFile("testdata/real_catalog.txt")
	if err != nil {
		t.Fatal(err)
	}
	var out []CatalogItem
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) != 5 || strings.HasPrefix(ln, "#") {
			continue
		}
		p := strings.Split(strings.TrimPrefix(f[0], "lxc:"), "/")
		sz, _ := strconv.ParseFloat(f[3], 64)
		if f[4] == "GiB" {
			sz *= 1 << 30
		} else {
			sz *= 1 << 20
		}
		out = append(out, CatalogItem{OS: p[0], Release: p[1], Variant: p[2], Arch: "amd64", Kind: f[1], Version: f[2], Size: int64(sz)})
	}
	return out
}

func (s shotSvc) Catalog(ctx context.Context, src string, f Filter) ([]CatalogItem, error) {
	return s.cat, nil
}
func (s shotSvc) Images() ([]ImageInfo, error) {
	return []ImageInfo{
		{Name: "ubuntu-noble", Type: "lxd-vm", Mode: "dm-snapshot", Size: 293<<20 + 2<<17, BaseOK: "ok", Snaps: 5, Current: "before-upgrade", LastUsed: time.Date(2026, 11, 22, 21, 5, 0, 0, time.UTC)},
		{Name: "debian-bookworm", Type: "lxd-container", Mode: "overlayfs", Size: 106<<20 + 410<<10, BaseOK: "ok", Snaps: 1, Current: "clean-install", LastUsed: time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)},
		{Name: "alpine-3.22", Type: "lxd-container", Mode: "overlayfs", Size: 3<<20 + 307<<10, BaseOK: "ok", LastUsed: time.Date(2026, 11, 18, 18, 2, 0, 0, time.UTC)},
	}, nil
}
func (s shotSvc) Sources() ([]SourceInfo, error) {
	return []SourceInfo{
		{Name: "lxc-images", Type: "simplestreams", URL: "https://images.linuxcontainers.org", Usable: true, Mirror: "official", MirrorIDs: []string{"official", "auto", "tuna", "nju", "cernet"}},
		{Name: "ubuntu-releases", Type: "simplestreams", URL: "https://cloud-images.ubuntu.com/releases", Usable: true, Mirror: "official", MirrorIDs: []string{"official", "auto", "nju"}},
		{Name: "ubuntu-daily", Type: "simplestreams", URL: "https://cloud-images.ubuntu.com/daily", Usable: true, Mirror: "official", MirrorIDs: []string{"official", "auto", "tuna", "nju"}},
		{Name: "dockerhub", Type: "oci-registry", URL: "registry-1.docker.io", Usable: true, Mirror: "official", MirrorIDs: []string{"official", "auto", "1ms"}},
	}, nil
}
func (s shotSvc) Store() (StoreStatus, error) {
	return StoreStatus{Kind: "store-file", Path: "/mnt/ventoy/omloader/store.img", HostFS: "exfat", Size: 40 << 30, Alloc: 40 << 30, Total: 39 << 30, Free: 34 << 30, FreePct: 87, MinFreePct: 10, State: "clean"}, nil
}
func (s shotSvc) Header() HeaderInfo {
	return HeaderInfo{Version: "v0.9", StoreKind: "store-file", StoreFree: 34 << 30, StoreTotal: 39 << 30, ImagesUsed: 3 << 29, Net: "192.168.1.23"}
}

// TestSimShots dumps ANSI screens when OML_SHOTS_DIR is set (simulated screenshots; not part of normal runs).
func TestSimShots(t *testing.T) {
	dir := os.Getenv("OML_SHOTS_DIR")
	if dir == "" {
		t.Skip("OML_SHOTS_DIR not set")
	}
	th := Theme{Color: true, Depth: 24, Round: true}
	cat := loadRealCatalog(t)
	mk := func(w, h int) (*Model, *fakeSvc) {
		f := newFake()
		f.trees = map[string]*TreeInfo{
			"ubuntu-noble": {Image: "ubuntu-noble", Mode: "dm-snapshot", Type: "lxd-vm", Current: "n6", Head: "n3", Nodes: []NodeInfo{
				{Name: "n1", Label: "clean-install", Created: "2026-11-01T10:00:00Z", Note: "fresh from lxc-images", Tags: []string{"golden"}, Alloc: 900 << 20},
				{Name: "n2", Label: "dev-tools", Parent: "n1", Created: "2026-11-02T10:00:00Z", Alloc: 1200 << 20},
				{Name: "n3", Label: "before-upgrade", Parent: "n2", Created: "2026-11-05T09:00:00Z", Note: "kernel 6.8", Alloc: 300 << 20},
				{Name: "n4", Label: "upgrade-24.10", Parent: "n3", Created: "2026-11-06T09:00:00Z", Alloc: 2 << 30},
				{Name: "n5", Label: "docker-test", Parent: "n2", Created: "2026-11-07T09:00:00Z", Tags: []string{"wip"}, Alloc: 700 << 20},
				{Name: "n6", Parent: "n3", Created: "2026-11-08T12:00:00Z", Live: true, Alloc: 80 << 20},
			}},
			"debian-bookworm": {Image: "debian-bookworm", Mode: "overlayfs", Type: "lxd-container"},
			"alpine-3.22":     {Image: "alpine-3.22", Mode: "overlayfs", Type: "lxd-container"},
		}
		m := New(shotSvc{f, cat}, th)
		m.Resize(w, h)
		m.Init()
		return m, f
	}
	dump := func(m *Model, name string) {
		c := m.View()
		s := ""
		for y := range c.Cells {
			s += c.ANSI(y, th) + "\n"
		}
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, name+".ans"), []byte(s), 0o644)
	}
	_ = time.Now
	for _, sz := range [][3]int{{120, 35, 0}, {80, 25, 1}} {
		sfx := "120x35"
		if sz[2] == 1 {
			sfx = "80x25"
		}
		m, _ := mk(sz[0], sz[1])
		dump(m, "1-images-"+sfx)
		m.OpenImage("ubuntu-noble")
		dump(m, "2-tree-"+sfx)
		m.Update(N("down"))
		m.Update(N("down"))
		m.Update(R('r'))
		dump(m, "3-revert-"+sfx)
		m, _ = mk(sz[0], sz[1])
		m.Update(R('2'))
		dump(m, "4-pull-"+sfx)
		m.Update(N("enter"))
		if m.op != nil {
			m.op.Wait()
			m.Tick()
		}
		keys(m, "ubuntu noble")
		dump(m, "4b-catalog-"+sfx)
		m, _ = mk(sz[0], sz[1])
		m.Update(R('3'))
		dump(m, "5-storage-"+sfx)
	}
}

type baseSvc struct{ *fakeSvc }

func (b baseSvc) Header() HeaderInfo {
	h := b.fakeSvc.Header()
	h.Base = "Debian 12.8 bookworm, kernel 6.1.0-28-amd64, built 2026-11-23"
	return h
}

func TestHelpShowsBase(t *testing.T) {
	m := New(baseSvc{newFake()}, Theme{ASCII: true})
	m.Init()
	m.Update(R('?'))
	mustHave(t, m, "omloader v9.9, Debian 12.8 bookworm, kernel 6.1.0-28-amd64, built 2026-11-23")
	m2, _ := newModel(t, true)
	m2.Update(R('?'))
	if strings.Contains(screen(m2), "omloader v9.9,") {
		t.Fatal("no Base line without base info")
	}
}
