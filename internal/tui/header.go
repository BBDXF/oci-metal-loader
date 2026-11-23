package tui

import (
	"fmt"
	"time"
)

// HeaderInfo is the cheap, always-visible loader status shown on the second row. Unknown numeric values are < 0 and
// are left out of the line.
type HeaderInfo struct {
	Version    string
	StoreKind  string // store-file | partition | ram | ""
	StoreFree  int64
	StoreTotal int64
	ImagesUsed int64  // bytes used below the images root
	Net        string // IP address(es), "" = no address
	Offline    bool   // policy / cache.offline: only cached images
	Base       string // compact Debian live base line ("Debian 12.8 bookworm, kernel ..., built ..."), "" = unknown (dev build)
}

// HeaderProvider is optionally implemented by a Service; Header must be cheap (statfs, interface list).
type HeaderProvider interface{ Header() HeaderInfo }

const headerEvery = 5 * time.Second

func (m *Model) refreshHeader(force bool) {
	hp, ok := m.Svc.(HeaderProvider)
	if !ok {
		return
	}
	now := m.Now()
	if !force && now.Sub(m.hdrAt) < headerEvery {
		return
	}
	m.hdr, m.hdrOK, m.hdrAt = hp.Header(), true, now
}

// headerText is the info line; it degrades by dropping the right-most parts when the terminal is narrow.
func (m *Model) headerText(w int) string {
	if !m.hdrOK {
		return L.AppName
	}
	h := m.hdr
	var parts []string
	ver := h.Version
	if ver == "" {
		ver = "dev"
	}
	parts = append(parts, L.HdrVersion+" "+ver)
	if h.StoreTotal > 0 {
		s := fmt.Sprintf("%s %s %s / %s", L.HdrStore, human(h.StoreFree), L.HdrFree, human(h.StoreTotal))
		if h.StoreKind != "" {
			s += " (" + h.StoreKind + ")"
		}
		parts = append(parts, s)
	} else if h.StoreKind == "ram" {
		parts = append(parts, L.HdrStore+" "+L.HdrRAM)
	}
	if h.ImagesUsed >= 0 && h.ImagesUsed > 0 {
		parts = append(parts, L.HdrImages+" "+human(h.ImagesUsed))
	}
	net := h.Net
	if net == "" {
		net = L.HdrNoNet
	}
	if h.Offline {
		net += " " + L.HdrOffline
	}
	parts = append(parts, L.HdrNet+" "+net)
	for len(parts) > 1 {
		line := joinParts(parts)
		if strWidth(line) <= w {
			return line
		}
		// drop the images usage first, then the store, keep version and network
		drop := len(parts) - 2
		if drop < 1 {
			break
		}
		parts = append(parts[:drop], parts[drop+1:]...)
	}
	return joinParts(parts)
}

func joinParts(p []string) string {
	s := ""
	for i, x := range p {
		if i > 0 {
			s += "  |  "
		}
		s += x
	}
	return s
}
