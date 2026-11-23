package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"omloader/internal/imgsearch"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"omloader/internal/boot"
	"omloader/internal/cache"
	"omloader/internal/config"
	"omloader/internal/fetch"
	"omloader/internal/imgimport"
	"omloader/internal/simplestreams"
)

const usageM4 = `  omloader images list [--source S] [--mirror M] [--type container|vm] [--os X] [--release R] [--variant V] [--arch amd64] [--offline] [-c omloader.yaml]
  omloader pull lxc:<os>/<release>[/<variant>] [--vm|--container] [--arch amd64] [--name N] [--out IMAGES_ROOT] [--source S] [--mirror M] [--variant V]
            [-c omloader.yaml] [--offline] [--force] [--no-probe] [--tmp DIR]
  omloader import lxd-vm PATH --name N --out IMAGES_ROOT [--no-probe]   (disk.qcow2 | dir with disk.qcow2 [+lxd.tar.xz])
  omloader source list|test [NAME] [--mirror M] [-c omloader.yaml] [--offline]   (M: official | auto | a server id from sources.yaml; docs/sources.md)
  omloader source ref IMAGE [--source dockerhub] [--mirror M] [-c omloader.yaml]    (Docker Hub image reference rewritten for a proxy mirror; experimental)
  omloader cache ls|gc [--max-size-gb N] [-c omloader.yaml]
  omloader net status [-c omloader.yaml] [--offline]
`

// m4Commands maps the online-pull command names to their handlers (wired in main.run).
var m4Commands = map[string]func(args []string, out, errw io.Writer) int{
	"pull": runPull, "images": runImages, "source": runSource, "cache": runCache, "net": runNet,
}

// loaderEnv reads /run/omloader/env (written by the live system's mount service).
func loaderEnv() map[string]string {
	m := map[string]string{}
	f, err := os.Open("/run/omloader/env")
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			m[k] = v
		}
	}
	return m
}

// loadCfg loads the global config; the default cache path /OMLOADER/cache means "cache/ on the OMLOADER partition",
// which the live system mounts at /mnt/snap.
func loadCfg(path string) (*config.Config, error) {
	r, err := config.Load(path, "")
	if err != nil {
		return nil, err
	}
	c := &r.Config
	if r.Sources["cache.path"] == config.LayerDefaults && c.Cache.Path == "/OMLOADER/cache" {
		if _, err := os.Stat("/OMLOADER"); err != nil && loaderEnv()["SNAPOK"] == "1" {
			c.Cache.Path = "/mnt/snap/cache"
		}
	}
	return c, nil
}

// defaultSource: --source, else default_source (sources.yaml), else lxc-images.
func defaultSource(c *config.Config, flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if c.DefaultSource != "" {
		return c.DefaultSource
	}
	return "lxc-images"
}

// newMirrorFetcher is newFetcher plus the --mirror selection ("" = the source's configured mirror).
func newMirrorFetcher(c *config.Config, src, mirror string, offline bool, errw io.Writer) (*fetch.Fetcher, error) {
	f, err := fetch.New(c, src, offline, errw)
	if err != nil {
		return nil, err
	}
	if mirror != "" {
		if err := f.SetMirror(mirror); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// opCtx is the parent context of the network operations; the TUI sets it so that a running pull/probe can be cancelled.
var (
	opCtxMu sync.Mutex
	opCtx   = context.Background()
)

func setOpCtx(c context.Context) { opCtxMu.Lock(); opCtx = c; opCtxMu.Unlock() }

func ctxTimeout() (context.Context, context.CancelFunc) {
	opCtxMu.Lock()
	defer opCtxMu.Unlock()
	return context.WithCancel(opCtx)
}

func humanSize(n int64) string {
	f := float64(n)
	for _, u := range []string{"B", "KiB", "MiB", "GiB"} {
		if f < 1024 {
			if u == "B" {
				return fmt.Sprintf("%d B", n)
			}
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1f TiB", f)
}

func newFetcher(c *config.Config, src string, offline bool, errw io.Writer) (*fetch.Fetcher, error) {
	return fetch.New(c, src, offline, errw)
}

func runImages(args []string, out, errw io.Writer) int {
	if len(args) < 1 || args[0] != "list" {
		fmt.Fprint(errw, "usage:\n"+usageM4)
		return 1
	}
	fs := flag.NewFlagSet("images list", flag.ContinueOnError)
	fs.SetOutput(errw)
	cfgp := fs.String("c", "", "global omloader.yaml")
	srcn := fs.String("source", "", "source name (default: default_source, lxc-images)")
	mirn := fs.String("mirror", "", "mirror: official | auto | server id (default: the source's `mirror`)")
	typ := fs.String("type", "", "container|vm")
	osn := fs.String("os", "", "distribution (e.g. ubuntu)")
	rel := fs.String("release", "", "release (e.g. noble)")
	vr := fs.String("variant", "", "variant (e.g. default, cloud)")
	arch := fs.String("arch", "amd64", "architecture")
	off := fs.Bool("offline", false, "use only the cache")
	srch := fs.String("search", "", "keywords (all must match os/release/variant/arch/type; e.g. \"arch vm\"); positional words also work")
	pos, perr := parse(fs, args[1:])
	if perr != nil {
		return 1
	}
	query := strings.TrimSpace(*srch + " " + strings.Join(pos, " "))
	if *typ != "" && *typ != "container" && *typ != "vm" {
		fmt.Fprintln(errw, "error: --type must be container or vm")
		return 1
	}
	c, err := loadCfg(*cfgp)
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	name := defaultSource(c, *srcn)
	f, err := newMirrorFetcher(c, name, *mirn, *off, errw)
	if err == nil {
		var cat *simplestreams.Catalog
		ctx, cancel := ctxTimeout()
		defer cancel()
		if cat, err = f.Catalog(ctx); err == nil {
			imgs := cat.Find(simplestreams.Query{OS: *osn, Release: *rel, Variant: *vr, Arch: *arch, Kind: *typ})
			tw := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "IMAGE\tTYPE\tVERSION\tSIZE")
			for _, k := range imgsearch.Filter(query, len(imgs), func(n int) imgsearch.Item {
				i := imgs[n]
				return imgsearch.Item{OS: i.OS, Release: i.Release, Variant: i.Variant, Arch: i.Arch, Kind: i.Kind()}
			}) {
				i := imgs[k]
				fmt.Fprintf(tw, "%s:%s/%s/%s\t%s\t%s\t%s\n", specPrefix(c, name), strings.ToLower(i.OS), i.Release, i.Variant, i.Kind(), i.Version, humanSize(i.Root.Size))
			}
			tw.Flush()
			return 0
		}
	}
	fmt.Fprintln(errw, "error:", err)
	return 1
}

// specPrefix is the image-spec prefix printed by `images list`: "lxc" for the default source, else the source name.
func specPrefix(c *config.Config, source string) string {
	if source == defaultSource(c, "") {
		return "lxc"
	}
	return source
}

// parseSpec splits "<prefix>:os/release[/variant]"; prefix lxc (or none) = the default source, anything else names a source.
// src is "" for the default source.
func parseSpec(spec string) (src, osn, rel, variant string, err error) {
	if p, rest, ok := strings.Cut(spec, ":"); ok {
		if p != "lxc" {
			src = p
		}
		spec = rest
	}
	f := strings.Split(spec, "/")
	if len(f) < 2 || len(f) > 3 || f[0] == "" || f[1] == "" {
		return "", "", "", "", fmt.Errorf("bad image spec %q (want lxc:<os>/<release>[/<variant>])", spec)
	}
	osn, rel = f[0], f[1]
	if len(f) == 3 {
		variant = f[2]
	}
	return
}

func resolveImagesRoot(c *config.Config, flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if r := loaderEnv()["IMAGES_ROOT"]; r != "" {
		return r
	}
	return c.Storage.Images.Path
}

func runPull(args []string, out, errw io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(errw, "error:", err); return 1 }
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	fs.SetOutput(errw)
	cfgp := fs.String("c", "", "global omloader.yaml")
	srcn := fs.String("source", "", "source name (default from the spec prefix, else default_source)")
	mirn := fs.String("mirror", "", "mirror: official | auto | server id (default: the source's `mirror`)")
	vrn := fs.String("variant", "", "variant (default, cloud, desktop, ...); overrides the third part of the spec")
	vm := fs.Bool("vm", false, "pull the VM image")
	ct := fs.Bool("container", false, "pull the container image (default)")
	arch := fs.String("arch", "amd64", "architecture")
	name := fs.String("name", "", "image name (default <os>-<release>[-<variant>][-vm])")
	dst := fs.String("out", "", "images root (default: the loader's images partition, else storage.images.path)")
	off := fs.Bool("offline", false, "use only the cache (no network)")
	force := fs.Bool("force", false, "replace an existing image")
	noProbe := fs.Bool("no-probe", false, "VM: skip kernel/initrd/root-partition detection (needs root)")
	tmp := fs.String("tmp", "", "temp dir (default $TMPDIR)")
	pos, err := parse(fs, args)
	if err != nil {
		return 1
	}
	if len(pos) != 1 || *vm && *ct {
		fmt.Fprint(errw, "usage:\n"+usageM4)
		return 1
	}
	src, osn, rel, variant, err := parseSpec(pos[0])
	if err != nil {
		return fail(err)
	}
	if *srcn != "" {
		src = *srcn
	}
	if *vrn != "" {
		variant = *vrn
	}
	c, err := loadCfg(*cfgp)
	if err != nil {
		return fail(err)
	}
	src = defaultSource(c, src)
	f, err := newMirrorFetcher(c, src, *mirn, *off, errw)
	if err != nil {
		return fail(err)
	}
	kind := "container"
	if *vm {
		kind = "vm"
	}
	if variant == "" {
		variant = "default"
	}
	ctx, cancel := ctxTimeout()
	defer cancel()
	cat, err := f.Catalog(ctx)
	if err != nil {
		return fail(err)
	}
	img, err := cat.Latest(simplestreams.Query{OS: osn, Release: rel, Variant: variant, Arch: *arch, Kind: kind})
	if err != nil {
		return fail(err)
	}
	if *name == "" {
		*name = strings.ToLower(img.OS + "-" + img.Release)
		if img.Variant != "default" {
			*name += "-" + img.Variant
		}
		if img.VM {
			*name += "-vm"
		}
	}
	root := resolveImagesRoot(c, *dst)
	fmt.Fprintf(out, "pull: %s %s version %s (%s) from source %s%s\n", img.Product, img.Kind(), img.Version, humanSize(img.Root.Size), src, map[bool]string{true: " [offline]"}[f.Offline])
	meta, err := f.Blob(ctx, img.Meta.Path, img.Meta.SHA256, img.Meta.Size)
	if err != nil {
		return fail(err)
	}
	rootf, err := f.Blob(ctx, img.Root.Path, img.Root.SHA256, img.Root.Size)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintln(out, "pull: sha256 verified:", img.Meta.SHA256[:16]+"…", img.Root.SHA256[:16]+"…")
	work, err := os.MkdirTemp(*tmp, "omloader-pull.")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(work)
	rootName, kindArg := "rootfs.squashfs", "lxd"
	if img.VM {
		rootName, kindArg = "disk.qcow2", "lxd-vm"
	}
	if err := os.Symlink(meta, filepath.Join(work, "lxd.tar.xz")); err != nil {
		return fail(err)
	}
	if err := os.Symlink(rootf, filepath.Join(work, rootName)); err != nil {
		return fail(err)
	}
	d, err := imgimport.Import(imgimport.Options{Kind: kindArg, Path: work, Name: *name, Out: root, TmpDir: *tmp,
		Runner: &boot.ExecRunner{}, Warn: errw, Force: *force, NoProbe: *noProbe})
	if err != nil {
		return fail(err)
	}
	fmt.Fprintln(out, "imported:", d)
	return 0
}

func runSource(args []string, out, errw io.Writer) int {
	if len(args) < 1 || args[0] != "list" && args[0] != "test" && args[0] != "ref" {
		fmt.Fprint(errw, "usage:\n"+usageM4)
		return 1
	}
	fs := flag.NewFlagSet("source "+args[0], flag.ContinueOnError)
	fs.SetOutput(errw)
	cfgp := fs.String("c", "", "global omloader.yaml")
	off := fs.Bool("offline", false, "do not touch the network")
	mirn := fs.String("mirror", "", "mirror: official | auto | server id (default: the source's `mirror`)")
	srcn := fs.String("source", "", "source (ref: default dockerhub)")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return 1
	}
	c, err := loadCfg(*cfgp)
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	if args[0] == "ref" {
		return runSourceRef(c, pos, *srcn, *mirn, out, errw)
	}
	var names []string
	for _, n := range c.SourceNames() {
		if len(pos) == 0 || pos[0] == n {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(errw, "error: no such source")
		return 1
	}
	rc := 0
	for _, n := range names {
		s := c.Sources[n]
		usable := "usable"
		if !c.SourceAllowed(n) {
			usable = "blocked (policy.allowed_sources)"
		} else if !c.SourceUsable(n) {
			usable = "offline only (policy.allow_online: false)"
		}
		sel := s.Mirror
		if *mirn != "" {
			sel = *mirn
		}
		if sel == "" {
			sel = config.MirrorOfficial
		}
		if args[0] == "list" {
			def := ""
			if n == defaultSource(c, "") {
				def = "  [default]"
			}
			fmt.Fprintf(out, "%s  type=%s  url=%s  mirror=%s  strategy=%s  %s%s\n", n, s.Type, s.URL, sel, c.MirrorPolicy.Strategy, usable, def)
			if s.Title != "" {
				fmt.Fprintf(out, "    %s\n", s.Title)
			}
			for _, id := range c.ServerNames(n) {
				m := s.Servers[id]
				mark := " "
				if id == sel {
					mark = "*"
				}
				fmt.Fprintf(out, "   %s mirror %-9s %-6s %s  %s\n", mark, id, m.Region, strings.TrimRight(m.URL, "/")+"/"+strings.Trim(m.Path, "/"), m.Title)
			}
			for _, m := range s.Mirrors {
				fmt.Fprintf(out, "      mirror (extra)   %s\n", m)
			}
			if s.Proxy != "" {
				fmt.Fprintf(out, "    proxy %s\n", s.Proxy)
			}
			continue
		}
		fmt.Fprintf(out, "source %s (%s, mirror=%s):\n", n, usable, sel)
		var probes []fetch.Probe
		switch s.Type {
		case "simplestreams", "http":
			f, err := newMirrorFetcher(c, n, *mirn, *off, errw)
			if err != nil {
				fmt.Fprintf(out, "    %v\n", err)
				rc = 1
				continue
			}
			if f.Offline {
				fmt.Fprintln(out, "    skipped: offline mode")
				continue
			}
			ctx, cancel := ctxTimeout()
			probes = f.ProbeAll(ctx)
			cancel()
		case "oci-registry":
			if *off || c.Cache.Offline == "on" || !c.Policy.AllowOnline {
				fmt.Fprintln(out, "    skipped: offline mode")
				continue
			}
			ctx, cancel := ctxTimeout()
			probes, err = fetch.ProbeRegistry(ctx, c, n, *mirn)
			cancel()
			if err != nil {
				fmt.Fprintf(out, "    %v\n", err)
				rc = 1
				continue
			}
		default:
			fmt.Fprintf(out, "    skipped: type %s is not testable\n", s.Type)
			continue
		}
		best := -1
		for i, p := range probes {
			if p.OK && (best < 0 || p.Dur < probes[best].Dur) {
				best = i
			}
		}
		for i, p := range probes {
			star := " "
			if i == best && len(probes) > 1 {
				star = "*"
			}
			if p.OK {
				fmt.Fprintf(out, "   %sOK    %6dms  %-9s %s\n", star, p.Dur.Milliseconds(), p.Name, p.URL)
			} else {
				fmt.Fprintf(out, "    FAIL  %6dms  %-9s %s: %v\n", p.Dur.Milliseconds(), p.Name, p.URL, p.Err)
				rc = 1
			}
		}
		if best >= 0 && len(probes) > 1 {
			fmt.Fprintf(out, "    fastest: %s\n", probes[best].Name)
		}
	}
	return rc
}

// runSourceRef prints a Docker Hub image reference rewritten for a Hub proxy: omloader has no registry client, so the
// reference is meant for `skopeo copy docker://REF oci-archive:x.tar` on a host, then `omloader import oci x.tar`.
func runSourceRef(c *config.Config, pos []string, src, mirror string, out, errw io.Writer) int {
	if len(pos) != 1 {
		fmt.Fprint(errw, "usage:\n"+usageM4)
		return 1
	}
	if src == "" {
		src = "dockerhub"
	}
	s, ok := c.Sources[src]
	if !ok || s.Type != "oci-registry" {
		fmt.Fprintf(errw, "error: %q is not an oci-registry source\n", src)
		return 1
	}
	bs, err := c.SourceBases(src, mirror)
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	host := bs[0].URL
	if mirror == config.MirrorAuto {
		ctx, cancel := ctxTimeout()
		ps, _ := fetch.ProbeRegistry(ctx, c, src, mirror)
		cancel()
		host = ""
		for _, p := range ps {
			if p.OK && (host == "" || p.Dur < bestDur(ps, host)) {
				host = p.Base
			}
		}
		if host == "" {
			fmt.Fprintln(errw, "error: no registry server answered")
			return 1
		}
	}
	fmt.Fprintln(out, config.RewriteImageRef(pos[0], host))
	return 0
}

func bestDur(ps []fetch.Probe, base string) time.Duration {
	for _, p := range ps {
		if p.Base == base {
			return p.Dur
		}
	}
	return 0
}

func runCache(args []string, out, errw io.Writer) int {
	if len(args) < 1 || args[0] != "ls" && args[0] != "gc" {
		fmt.Fprint(errw, "usage:\n"+usageM4)
		return 1
	}
	fs := flag.NewFlagSet("cache "+args[0], flag.ContinueOnError)
	fs.SetOutput(errw)
	cfgp := fs.String("c", "", "global omloader.yaml")
	max := fs.Int("max-size-gb", -1, "gc: keep at most this many GB (default cache.max_size_gb)")
	if pos, err := parse(fs, args[1:]); err != nil || len(pos) > 0 {
		return 1
	}
	c, err := loadCfg(*cfgp)
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	ca := &cache.Cache{Dir: c.Cache.Path}
	if args[0] == "ls" {
		es, _ := ca.List()
		var total int64
		for _, e := range es {
			total += e.Size
			fmt.Fprintf(out, "%-7s %10s  %s  %s\n", e.Kind, humanSize(e.Size), e.ModTime.Format("2006-01-02 15:04"), e.Name)
		}
		fmt.Fprintf(out, "%d entries, %s in %s\n", len(es), humanSize(total), ca.Dir)
		return 0
	}
	if *max < 0 {
		*max = c.Cache.MaxSizeGB
	}
	rm, err := ca.GC(int64(*max) << 30)
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	var freed int64
	for _, e := range rm {
		freed += e.Size
		fmt.Fprintf(out, "removed %s %s (%s)\n", e.Kind, e.Name, humanSize(e.Size))
	}
	fmt.Fprintf(out, "gc: removed %d entries, freed %s (limit %d GB)\n", len(rm), humanSize(freed), *max)
	return 0
}

func defaultRoute() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan()
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) > 2 && fl[1] == "00000000" {
			gw, _ := strconv.ParseUint(fl[2], 16, 32)
			return fmt.Sprintf("default via %d.%d.%d.%d dev %s", gw&0xff, gw>>8&0xff, gw>>16&0xff, gw>>24&0xff, fl[0])
		}
	}
	return ""
}

func nameservers() []string {
	var ns []string
	for _, p := range []string{"/run/systemd/resolve/resolv.conf", "/etc/resolv.conf"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Fields(l); len(f) == 2 && f[0] == "nameserver" {
				ns = append(ns, f[1])
			}
		}
		if len(ns) > 0 {
			break
		}
	}
	return ns
}

func runNet(args []string, out, errw io.Writer) int {
	if len(args) < 1 || args[0] != "status" {
		fmt.Fprint(errw, "usage:\n"+usageM4)
		return 1
	}
	fs := flag.NewFlagSet("net status", flag.ContinueOnError)
	fs.SetOutput(errw)
	cfgp := fs.String("c", "", "global omloader.yaml")
	off := fs.Bool("offline", false, "do not probe the network")
	if pos, err := parse(fs, args[1:]); err != nil || len(pos) > 0 {
		return 1
	}
	c, err := loadCfg(*cfgp)
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	fmt.Fprintln(out, "interfaces:")
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		st := "down"
		if i.Flags&net.FlagUp != 0 {
			st = "up"
		}
		var addrs []string
		as, _ := i.Addrs()
		for _, a := range as {
			addrs = append(addrs, a.String())
		}
		fmt.Fprintf(out, "  %-12s %-4s %s %s\n", i.Name, st, i.HardwareAddr, strings.Join(addrs, " "))
	}
	r := defaultRoute()
	if r == "" {
		r = "(none)"
	}
	fmt.Fprintf(out, "default route: %s\ndns: %s\n", r, strings.Join(nameservers(), " "))
	px := c.Network.Proxy
	if px == "" {
		px = "(none; environment HTTP(S)_PROXY honoured)"
	}
	fmt.Fprintf(out, "proxy: %s  no_proxy: %q  extra_ca: %q\n", px, c.Network.NoProxy, c.Network.ExtraCA)
	fmt.Fprintf(out, "policy: allow_online=%v allowed_sources=%v  cache: path=%s offline=%s\n", c.Policy.AllowOnline, c.Policy.AllowedSources, c.Cache.Path, c.Cache.Offline)
	for _, n := range c.SourceNames() {
		fmt.Fprintf(out, "source %s:\n", n)
		if c.Sources[n].Type == "oci-registry" {
			fmt.Fprintln(out, "    skipped: registry (omloader source test "+n+")")
			continue
		}
		f, err := newFetcher(c, n, *off, errw)
		if err != nil {
			fmt.Fprintf(out, "    %v\n", err)
			continue
		}
		if f.Offline {
			fmt.Fprintln(out, "    skipped: offline mode")
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.Network.TimeoutSec+1)*time.Second)
		for _, p := range f.ProbeAll(ctx) {
			if p.OK {
				fmt.Fprintf(out, "    OK    %6dms  %s\n", p.Dur.Milliseconds(), p.URL)
			} else {
				fmt.Fprintf(out, "    FAIL  %6dms  %s: %v\n", p.Dur.Milliseconds(), p.URL, p.Err)
			}
		}
		cancel()
	}
	return 0
}
