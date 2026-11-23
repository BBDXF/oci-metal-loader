package tui

import (
	"context"
	"io"
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
			m.catalog, m.csel, m.ctop, m.pullCat = items, 0, 0, true
			m.sayf("%d image(s)", len(items))
			m.closeOp()
		}
	})
}

func (m *Model) pullKey(k Key, act string) {
	if m.pullCat {
		m.csel = moveSel(act, m.csel, len(m.catalog), m.bodyRows())
		m.ctop = keepVisible(m.csel, m.ctop, m.bodyRows()-len(m.sources)-4)
		switch {
		case act == "back":
			m.pullCat = false
		case act == "select":
			if m.csel < len(m.catalog) {
				m.pullForm(m.catalog[m.csel])
			}
		case k.Rune == '/':
			m.filterForm()
		case k.Rune == 'r':
			m.loadCatalog(m.csource)
		}
		return
	}
	m.ssel = moveSel(act, m.ssel, len(m.sources), m.bodyRows())
	switch {
	case act == "back":
		m.gotoTab(ScrImages)
	case act == "select":
		if m.ssel < len(m.sources) {
			s := m.sources[m.ssel]
			if !s.Usable && m.Svc.Offline() {
				m.message(L.Info, false, L.PullOffline)
			}
			m.loadCatalog(s.Name)
		}
	case k.Rune == 't':
		if m.ssel < len(m.sources) {
			name := m.sources[m.ssel].Name
			m.startOp(L.SourceTest+": "+name, func(ctx context.Context, w io.Writer) error { return m.Svc.SourceTest(ctx, name, w) }, nil)
		}
	case k.Rune == '/':
		m.filterForm()
	case k.Rune == 'r':
		m.loadSources()
	}
}

func (m *Model) filterForm() {
	m.form(L.FilterTitle, []string{"Empty field = any."}, []string{L.FieldOS, L.FieldRelease, L.FieldType},
		[]string{m.filter.OS, m.filter.Release, m.filter.Kind}, func(v []string) {
			m.filter = Filter{OS: strings.TrimSpace(v[0]), Release: strings.TrimSpace(v[1]), Kind: strings.TrimSpace(v[2])}
			if m.csource == "" && m.ssel < len(m.sources) {
				m.csource = m.sources[m.ssel].Name
			}
			if m.csource != "" {
				m.loadCatalog(m.csource)
			}
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
			warn := []string{"Creates an ext4 store file (label OMLOADER) on the host partition."}
			if grow {
				warn = []string{"Grows the store file offline (extend + e2fsck + resize2fs). The loader releases (unmounts) the store first: reboot afterwards."}
			}
			m.form(L.StoreWizard, warn, []string{L.StorePath, L.StoreSize}, []string{defPath, defSize}, func(v []string) {
				path, size := strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
				m.confirm(L.StoreWizard, true, []string{map[bool]string{true: "grow ", false: "create "}[grow] + path + " to " + size + "?"}, func() {
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
