package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
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

const usageM4 = `  omloader images list [--source S] [--type container|vm] [--os X] [--release R] [--variant V] [--arch amd64] [--offline] [-c omloader.yaml]
  omloader pull lxc:<os>/<release>[/<variant>] [--vm|--container] [--arch amd64] [--name N] [--out IMAGES_ROOT] [--source S]
            [-c omloader.yaml] [--offline] [--force] [--no-probe] [--tmp DIR]
  omloader import lxd-vm PATH --name N --out IMAGES_ROOT [--no-probe]   (disk.qcow2 | dir with disk.qcow2 [+lxd.tar.xz])
  omloader source list|test [NAME] [-c omloader.yaml] [--offline]
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

func defaultSource(c *config.Config, flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	return "lxc-images"
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
	srcn := fs.String("source", "", "source name (default lxc-images)")
	typ := fs.String("type", "", "container|vm")
	osn := fs.String("os", "", "distribution (e.g. ubuntu)")
	rel := fs.String("release", "", "release (e.g. noble)")
	vr := fs.String("variant", "", "variant (e.g. default, cloud)")
	arch := fs.String("arch", "amd64", "architecture")
	off := fs.Bool("offline", false, "use only the cache")
	if pos, err := parse(fs, args[1:]); err != nil || len(pos) > 0 {
		return 1
	}
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
	f, err := newFetcher(c, name, *off, errw)
	if err == nil {
		var cat *simplestreams.Catalog
		ctx, cancel := ctxTimeout()
		defer cancel()
		if cat, err = f.Catalog(ctx); err == nil {
			imgs := cat.Find(simplestreams.Query{OS: *osn, Release: *rel, Variant: *vr, Arch: *arch, Kind: *typ})
			tw := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "IMAGE\tTYPE\tVERSION\tSIZE")
			for _, i := range imgs {
				fmt.Fprintf(tw, "lxc:%s/%s/%s\t%s\t%s\t%s\n", strings.ToLower(i.OS), i.Release, i.Variant, i.Kind(), i.Version, humanSize(i.Root.Size))
			}
			tw.Flush()
			return 0
		}
	}
	fmt.Fprintln(errw, "error:", err)
	return 1
}

// parseSpec splits "lxc:os/release[/variant]".
func parseSpec(spec string) (src, osn, rel, variant string, err error) {
	src = "lxc-images"
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
	srcn := fs.String("source", "", "source name (default from the spec prefix)")
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
	c, err := loadCfg(*cfgp)
	if err != nil {
		return fail(err)
	}
	f, err := newFetcher(c, src, *off, errw)
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
	if len(args) < 1 || args[0] != "list" && args[0] != "test" {
		fmt.Fprint(errw, "usage:\n"+usageM4)
		return 1
	}
	fs := flag.NewFlagSet("source "+args[0], flag.ContinueOnError)
	fs.SetOutput(errw)
	cfgp := fs.String("c", "", "global omloader.yaml")
	off := fs.Bool("offline", false, "do not touch the network")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return 1
	}
	c, err := loadCfg(*cfgp)
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	var names []string
	for n := range c.Sources {
		if len(pos) == 0 || pos[0] == n {
			names = append(names, n)
		}
	}
	sort.Strings(names)
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
		if args[0] == "list" {
			fmt.Fprintf(out, "%s  type=%s  url=%s  strategy=%s  %s\n", n, s.Type, s.URL, c.MirrorPolicy.Strategy, usable)
			for _, m := range c.SourceMirrors(n) {
				fmt.Fprintf(out, "    mirror %s\n", m)
			}
			if s.Proxy != "" {
				fmt.Fprintf(out, "    proxy %s\n", s.Proxy)
			}
			continue
		}
		fmt.Fprintf(out, "source %s (%s):\n", n, usable)
		if s.Type != "simplestreams" && s.Type != "http" {
			fmt.Fprintf(out, "    skipped: type %s is not testable\n", s.Type)
			continue
		}
		f, err := newFetcher(c, n, *off, errw)
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
		for _, p := range f.ProbeAll(ctx) {
			if p.OK {
				fmt.Fprintf(out, "    OK    %6dms  %s\n", p.Dur.Milliseconds(), p.URL)
			} else {
				fmt.Fprintf(out, "    FAIL  %6dms  %s: %v\n", p.Dur.Milliseconds(), p.URL, p.Err)
				rc = 1
			}
		}
		cancel()
	}
	return rc
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
	var names []string
	for n := range c.Sources {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(out, "source %s:\n", n)
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
