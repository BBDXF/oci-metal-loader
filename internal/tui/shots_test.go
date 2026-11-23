package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSimShots dumps ANSI screens when OML_SHOTS_DIR is set (simulated screenshots; not part of normal runs).
func TestSimShots(t *testing.T) {
	dir := os.Getenv("OML_SHOTS_DIR")
	if dir == "" {
		t.Skip("OML_SHOTS_DIR not set")
	}
	th := Theme{Color: true, Depth: 24, Round: true}
	mk := func(w, h int) (*Model, *fakeSvc) {
		f := newFake()
		f.trees = map[string]*TreeInfo{
			"ubuntu-24.04": {Image: "ubuntu-24.04", Mode: "dm-snapshot", Type: "lxd-vm", Current: "n6", Head: "n3", Nodes: []NodeInfo{
				{Name: "n1", Label: "clean-install", Created: "2026-03-01T10:00:00Z", Note: "fresh from lxc-images", Tags: []string{"golden"}, Alloc: 900 << 20},
				{Name: "n2", Label: "dev-tools", Parent: "n1", Created: "2026-03-02T10:00:00Z", Alloc: 1200 << 20},
				{Name: "n3", Label: "before-upgrade", Parent: "n2", Created: "2026-03-05T09:00:00Z", Note: "kernel 6.8", Alloc: 300 << 20},
				{Name: "n4", Label: "upgrade-24.10", Parent: "n3", Created: "2026-03-06T09:00:00Z", Alloc: 2 << 30},
				{Name: "n5", Label: "docker-test", Parent: "n2", Created: "2026-03-07T09:00:00Z", Tags: []string{"wip"}, Alloc: 700 << 20},
				{Name: "n6", Parent: "n3", Created: "2026-03-08T12:00:00Z", Live: true, Alloc: 80 << 20},
			}},
			"debian-12": {Image: "debian-12", Mode: "overlayfs", Type: "lxd-container"},
			"alpine":    {Image: "alpine", Mode: "overlayfs", Type: "lxd-container"},
		}
		m := New(f, th)
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
		m.OpenImage("ubuntu-24.04")
		dump(m, "2-tree-"+sfx)
		m.Update(N("down"))
		m.Update(N("down"))
		m.Update(R('r'))
		dump(m, "3-revert-"+sfx)
		m, _ = mk(sz[0], sz[1])
		m.Update(R('2'))
		dump(m, "4-pull-"+sfx)
		m.Update(N("enter"))
		dump(m, "4b-catalog-"+sfx)
		m, _ = mk(sz[0], sz[1])
		m.Update(R('3'))
		dump(m, "5-storage-"+sfx)
	}
}
