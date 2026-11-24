package tui

import (
	"context"
	"fmt"
	"io"
	"omloader/internal/imgsearch"
	"strings"
)

// ---- pull ----

func (m *Model) loadSources() {
	s, err := m.Svc.Sources()
	if err != nil {
		m.fail(err)
		return
	}
	m.sources = s
	if m.ssel >= len(s) {
		m.ssel = 0
	}
}

// SourceRef is the source argument for the service: NAME, or NAME@MIRROR when the user picked another mirror.
func (m *Model) SourceRef(s SourceInfo) string {
	if sel := m.smirror[s.Name]; sel != "" && sel != s.Mirror {
		return s.Name + "@" + sel
	}
	return s.Name
}

func (m *Model) mirrorOf(s SourceInfo) string {
	if sel := m.smirror[s.Name]; sel != "" {
		return sel
	}
	if s.Mirror != "" {
		return s.Mirror
	}
	return "official"
}

// cycleMirror moves the selected source to its next mirror (official, auto, server ids...).
func (m *Model) cycleMirror(s SourceInfo) {
	if len(s.MirrorIDs) < 2 {
		return
	}
	cur, next := m.mirrorOf(s), 0
	for i, id := range s.MirrorIDs {
		if id == cur {
			next = (i + 1) % len(s.MirrorIDs)
		}
	}
	if m.smirror == nil {
		m.smirror = map[string]string{}
	}
	m.smirror[s.Name] = s.MirrorIDs[next]
	m.catAll, m.catalog = nil, nil // the catalog belongs to the previous mirror
}

func (m *Model) loadCatalog(src string) {
	m.csource = src
	var items []CatalogItem
	f := m.filter
	m.startOp("Loading catalog: "+src, func(ctx context.Context, w io.Writer) error {
		var err error
		items, err = m.Svc.Catalog(ctx, src, f)
		if err == nil {
			io.WriteString(w, "")
		}
		return err
	}, func(err error) {
		if err == nil {
			if items == nil {
				items = []CatalogItem{}
			}
			m.catAll, m.pullCat = items, true
			m.applyFilter()
			m.closeOp()
		}
	})
}

func (m *Model) pullKey(k Key, act string) {
	if m.pullCat {
		m.catalogKey(k, act)
		return
	}
	m.ssel = moveSel(act, m.ssel, len(m.sources), m.bodyRows())
	switch {
	case act == "back" || k.Name == "left" || k.Name == "bs":
		m.gotoTab(ScrImages)
	case act == "select":
		if m.ssel < len(m.sources) && m.sources[m.ssel].Type == "oci-registry" {
			m.dockerForm(m.sources[m.ssel]) // Docker Hub (experimental): no catalog, ask for the image reference
			return
		}
		if m.ssel < len(m.sources) {
			s := m.sources[m.ssel]
			if !s.Usable && m.Svc.Offline() {
				m.message(L.Info, false, L.PullOffline)
			}
			m.loadCatalog(m.SourceRef(s))
		}
	case k.Rune == 'm':
		if m.ssel < len(m.sources) {
			m.cycleMirror(m.sources[m.ssel])
		}
	case k.Rune == 't':
		if m.ssel < len(m.sources) {
			name := m.SourceRef(m.sources[m.ssel])
			m.startOp(L.SourceTest+": "+name, func(ctx context.Context, w io.Writer) error { return m.Svc.SourceTest(ctx, name, w) }, nil)
		}
	case k.Rune == '/':
		if m.catAll != nil {
			m.pullCat = true
		} else if m.ssel < len(m.sources) {
			m.loadCatalog(m.SourceRef(m.sources[m.ssel]))
		}
	case k.Rune == 'r':
		m.loadSources()
	}
}

// catalogKey: the search box has the keyboard (live filter). Letters, digits and space are text; navigation uses the named
// keys; Esc clears the search first and only then leaves the catalog; Enter opens the pull form of the selected image.
func (m *Model) catalogKey(k Key, act string) {
	if k.Name != "" {
		m.csel = moveSel(act, m.csel, len(m.catalog), m.catVis(m.H-1))
	}
	switch {
	case k.Name == "esc":
		if m.filter.Text != "" {
			m.filter.Text = ""
			m.applyFilter()
		} else {
			m.pullCat = false
		}
	case k.Name == "enter":
		if m.csel < len(m.catalog) {
			m.pullForm(m.catalog[m.csel])
		}
	case k.Name == "bs":
		if r := []rune(m.filter.Text); len(r) > 0 {
			m.filter.Text = string(r[:len(r)-1])
			m.applyFilter()
		}
	case k.Name == "" && k.Rune >= 0x20:
		m.filter.Text += string(k.Rune)
		m.applyFilter()
	}
	m.ctop = keepVisible(m.csel, m.ctop, m.catVis(m.H-1))
}

// applyFilter recomputes the visible catalog from catAll using the shared matcher; the selection stays on the same image
// when it still matches.
func (m *Model) applyFilter() {
	var keep *CatalogItem
	if m.csel < len(m.catalog) {
		c := m.catalog[m.csel]
		keep = &c
	}
	idx := imgsearch.Filter(m.filter.Text, len(m.catAll), func(i int) imgsearch.Item {
		c := m.catAll[i]
		return imgsearch.Item{OS: c.OS, Release: c.Release, Variant: c.Variant, Arch: c.Arch, Kind: c.Kind}
	})
	m.catalog = make([]CatalogItem, 0, len(idx))
	m.csel, m.ctop = 0, 0
	for n, i := range idx {
		m.catalog = append(m.catalog, m.catAll[i])
		if keep != nil && m.catAll[i] == *keep {
			m.csel = n
		}
	}
	m.ctop = keepVisible(m.csel, 0, m.catVis(m.H-1))
}

// DockerPuller is optionally implemented by a Service: `omloader pull docker:IMAGE` (experimental OCI support, crane).
type DockerPuller interface {
	PullDocker(ctx context.Context, source, image, name string, w io.Writer) error
}

// dockerForm asks for a Docker image reference (nginx:alpine, jrei/systemd-debian:12) and pulls it through the source's mirrors.
func (m *Model) dockerForm(s SourceInfo) {
	dp, ok := m.Svc.(DockerPuller)
	if !ok {
		m.message(L.Info, false, "Docker pull is not available here.")
		return
	}
	src := m.SourceRef(s)
	m.form("Pull Docker image (experimental)", []string{"source: " + src, "EXPERIMENTAL: on by default; switch off with experimental.oci_images: false / omloader.experimental=off."},
		[]string{"Image (e.g. nginx:alpine)", L.PullName + " (optional)"}, []string{"", ""}, func(v []string) {
			image, name := strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
			if image == "" {
				m.warn("No image given.")
				return
			}
			m.startOp("pull docker:"+image, func(ctx context.Context, w io.Writer) error {
				return dp.PullDocker(ctx, src, image, name, w)
			}, func(err error) {
				m.RefreshImages()
				if err == nil {
					m.sayf("pulled docker:%s", image)
				}
			})
		})
}

func (m *Model) pullForm(it CatalogItem) {
	lines := []string{it.Spec() + "  " + it.Kind + "  " + it.Version + "  " + human(it.Size), "source: " + m.csource}
	if m.Svc.Offline() {
		lines = append(lines, L.PullOffline)
	}
	m.form(L.PullTitle, lines, []string{L.PullName}, []string{m.Svc.DefaultPullName(it)}, func(v []string) {
		name := strings.TrimSpace(v[0])
		src := m.csource
		m.confirm(L.PullConfirm, false, append(lines, "name: "+name), func() {
			op := m.startOp("pull "+it.Spec(), func(ctx context.Context, w io.Writer) error {
				return m.Svc.Pull(ctx, src, it, name, w)
			}, func(err error) {
				m.RefreshImages()
				if err == nil {
					m.sayf("pulled %s as %s", it.Spec(), name)
				}
			})
			op.Bytes, op.Total = m.Svc.PullBytes, it.Size
		})
	})
}

// ---- storage ----

func (m *Model) loadStore() {
	s, err := m.Svc.Store()
	m.store, m.storeErr = s, ""
	if err != nil {
		m.storeErr = err.Error()
	}
}

func (m *Model) storageKey(k Key, act string) {
	switch {
	case act == "back":
		m.gotoTab(ScrImages)
	case k.Rune == 'r':
		m.loadStore()
		m.say(L.Refreshed)
	case k.Rune == 'c':
		defPath, defSize := m.Svc.StoreDefaults()
		if m.store.Kind == "store-file" && m.store.Path != "" {
			defPath = m.store.Path
		}
		m.menu(L.StoreWizard, []string{L.StoreCreate, L.StoreGrow, L.Cancel}, func(i int) {
			if i > 1 {
				return
			}
			grow := i == 1
			warn := []string{L.StoreCreateWarn}
			if grow {
				warn = []string{L.StoreGrowWarn}
			}
			m.form(L.StoreWizard, warn, []string{L.StorePath, L.StoreSize}, []string{defPath, defSize}, func(v []string) {
				path, size := strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
				m.confirm(L.StoreWizard, true, []string{map[bool]string{true: fmt.Sprintf(L.StoreGrowAsk, path, size), false: fmt.Sprintf(L.StoreCreateAsk, path, size)}[grow]}, func() {
					m.startOp("store "+map[bool]string{true: "grow", false: "create"}[grow]+" "+path, func(ctx context.Context, w io.Writer) error {
						if grow {
							return m.Svc.StoreGrow(ctx, path, size, w)
						}
						return m.Svc.StoreCreate(ctx, path, size, w)
					}, func(error) { m.loadStore() })
				})
			})
		})
	}
}

// ---- network / settings ----

func (m *Model) runNetStatus(probe bool) {
	var o *Op
	if probe {
		o = m.startOp("network status + source probes", func(ctx context.Context, w io.Writer) error { return m.Svc.NetStatus(ctx, true, w) }, nil)
		_ = o
		return
	}
	var b strings.Builder
	if err := m.Svc.NetStatus(context.Background(), false, &b); err != nil {
		m.netLines = []string{err.Error()}
		return
	}
	m.netLines = strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	m.nsc = 0
}

func (m *Model) netKey(k Key, act string) {
	m.nsc = moveSel(act, m.nsc, len(m.netLines), m.bodyRows())
	switch {
	case act == "back":
		m.gotoTab(ScrImages)
	case k.Rune == 'r':
		m.runNetStatus(false)
		m.say(L.Refreshed)
	case k.Rune == 't':
		m.runNetStatus(true)
	}
}

func (m *Model) settingsKey(k Key, act string) {
	m.ssc = moveSel(act, m.ssc, len(m.settings), m.bodyRows())
	if act == "back" {
		m.gotoTab(ScrImages)
	}
}
