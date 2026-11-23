package main

import (
	"net"
	"os"
	"strings"

	"omloader/internal/baseinfo"
	"omloader/internal/snapmgr"
	"omloader/internal/tui"
)

// version is the loader version shown in the TUI header (set with -ldflags "-X main.version=..."; `omloader version` prints it).
var version = "dev"

// Header implements tui.HeaderProvider: statfs of the snapshot store and the images partition plus the local addresses.
// Everything here is cheap (no directory walks, no network access).
func (s *realSvc) Header() tui.HeaderInfo {
	h := tui.HeaderInfo{Version: version, ImagesUsed: -1}
	if v := os.Getenv("OMLOADER_VERSION"); v != "" && version == "dev" {
		h.Version = v
	}
	if in, err := baseinfo.Read(baseFile); err == nil {
		h.Base = in.Line()
	}
	if t, f, _, err := snapmgr.FreePercent(s.snapRoot, nil); err == nil {
		h.StoreTotal, h.StoreFree = int64(t), int64(f)
	}
	switch {
	case s.env["STOREFILE"] != "":
		h.StoreKind = "store-file"
	case s.env["SNAPDEV"] != "":
		h.StoreKind = "partition"
	case s.env["SNAPOK"] == "0":
		h.StoreKind = "ram"
		h.StoreTotal = 0
	}
	if t, f, _, err := snapmgr.FreePercent(s.imagesRoot, nil); err == nil && t >= f {
		h.ImagesUsed = int64(t - f)
	}
	var ips []string
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
				ips = append(ips, ipn.IP.String())
			}
		}
	}
	h.Net = strings.Join(ips, ",")
	h.Offline = s.Offline()
	return h
}

var _ tui.HeaderProvider = (*realSvc)(nil)
