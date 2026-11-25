package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"omloader/internal/baseinfo"
	"omloader/internal/boot"
	"omloader/internal/config"
	"omloader/internal/simplestreams"
	"omloader/internal/snapmgr"
	"omloader/internal/tui"
)

// exit codes of `omloader tui` (used by build/live/rootfs/usr/local/lib/omloader/tui-launch.sh)
const (
	tuiExitQuit  = 0  // quit, or boot prepared (container: /run/omloader-switch is present)
	tuiExitShell = 10 // the user asked for the shell
)

func runTUI(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(errw)
	cfg := fs.String("c", "", "global omloader.yaml")
	ir := fs.String("images-root", "", "images root")
	sr := fs.String("snap-root", "", "snapshots root")
	dr := fs.String("data-root", "", "data volumes root (default <snap-root>/../data)")
	ascii := fs.Bool("ascii", false, "ASCII only (no box drawing / UTF-8)")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return 1
	}
	if !tui.IsTTY(os.Stdin) {
		fmt.Fprintln(errw, "error: omloader tui needs a terminal on stdin")
		return 1
	}
	c, err := loadCfg(*cfg)
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	env := loaderEnv()
	*ir = resolveImagesRoot(c, *ir)
	if *sr == "" {
		*sr = c.Storage.Snapshots.Path
		if env["SNAPOK"] == "1" {
			*sr = "/mnt/snap/snapshots"
		}
	}
	svc := &realSvc{cfgPath: *cfg, imagesRoot: *ir, snapRoot: *sr, dataRoot: *dr, env: env}
	os.Setenv("DM_DISABLE_UDEV", "1")
	th := tui.EnvTheme(*ascii)
	armed := true // the automatic boot countdown only runs in the first TUI session (never again after a failed boot)
	for {
		m := tui.New(svc, th)
		m.Km.Override(os.Getenv("OMLOADER_TUI_KEYS"))
		m.Init()
		if a := svc.AutoBoot(); armed && a != nil {
			m.ArmAutoBoot(*a)
			claimAutoBoot()
		}
		armed = false
		term := &tui.Terminal{In: os.Stdin, Out: os.Stdout}
		// marker for the test harness / boot timing: printed to the console log right before the first frame
		fmt.Fprintf(os.Stdout, "OMLOADER_TUI_READY uptime=%s\r\n", uptime())
		res := term.Run(m, m.StartAutoBoot)
		switch res.Kind {
		case tui.ResShell:
			return tuiExitShell
		case tui.ResReboot, tui.ResPowerOff:
			verb := map[tui.ResultKind]string{tui.ResReboot: "reboot", tui.ResPowerOff: "poweroff"}[res.Kind]
			fmt.Fprintln(out, tui.L.PowerRebooting)
			if _, err := (&boot.ExecRunner{}).Run("systemctl", verb); err != nil {
				fmt.Fprintln(errw, "error:", err)
				continue
			}
			time.Sleep(time.Minute)
			return 0
		case tui.ResBoot:
			pof := applyBootFlags(strings.Fields(res.Boot.Flags))
			if rc := tuiBoot(svc, res.Boot, out, errw); rc == 0 {
				return 0
			} else if pof { // boot.extra_cmdline_flags poweroff_on_fail=1 (same as the shell path)
				fmt.Fprintf(out, "OMLOADER_LOADER: 'omloader boot' returned rc=%d\r\nOMLOADER_LOADER_FAILED\r\n", rc)
				(&boot.ExecRunner{}).Run("/usr/local/lib/omloader/autoboot.sh", "--poweroff")
				time.Sleep(time.Minute)
				return rc
			}
			fmt.Fprint(out, "\r\nPress Enter to return to the TUI... ")
			bufio.NewReader(os.Stdin).ReadString('\n')
		default:
			return tuiExitQuit
		}
	}
}

func uptime() string {
	b, _ := os.ReadFile("/proc/uptime")
	return strings.Fields(string(b) + " ?")[0]
}

// tuiBoot is the boot handoff: exactly the code path of `omloader boot` (runBoot), run in the foreground on the restored
// terminal. A container boot leaves /run/omloader-switch for the launcher script; a VM boot ends in kexec -e.
func tuiBoot(s *realSvc, req tui.BootRequest, out, errw io.Writer) int {
	fmt.Fprintln(out, tui.L.BootStarting)
	if f, err := os.OpenFile("/run/omloader/boot.lock", os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			fmt.Fprintln(errw, "error: another console is already booting an image")
			f.Close()
			return 1
		}
		defer f.Close() // lock lives as long as this process (kexec/switch-root end it)
	}
	udev := func(q string) { exec := &boot.ExecRunner{}; exec.Run("udevadm", "control", q) }
	udev("--stop-exec-queue")
	args := s.bootArgs(req.Image, req.Snapshot, req.Ephemeral)
	if req.Shell != "" {
		args = append(args, "--shell", req.Shell)
	}
	rc := runBoot(args, out, errw)
	if rc != 0 {
		udev("--start-exec-queue")
	}
	return rc
}

// ---- tui.Service on top of the library ----

type realSvc struct {
	cfgPath, imagesRoot, snapRoot, dataRoot string
	env                                     map[string]string
}

func (s *realSvc) cfg() (*config.Config, error) { return loadCfg(s.cfgPath) }

func (s *realSvc) bootArgs(image, snap string, eph bool) []string {
	a := []string{"--image", image}
	if snap != "" {
		a = append(a, "--snapshot", snap)
	}
	a = append(a, "--images-root", s.imagesRoot, "--snap-root", s.snapRoot)
	if s.cfgPath != "" {
		a = append(a, "-c", s.cfgPath)
	}
	if s.dataRoot != "" {
		a = append(a, "--data-root", s.dataRoot)
	}
	if eph {
		a = append(a, "--ephemeral")
	}
	return a
}

func (s *realSvc) BootArgs(image, snap string, eph bool) []string {
	return append([]string{"omloader", "boot"}, s.bootArgs(image, snap, eph)...)
}

func (s *realSvc) BootPreview(ctx context.Context, image, snap string, eph bool, w io.Writer) error {
	setOpCtx(ctx)
	if rc := runBoot(append(s.bootArgs(image, snap, eph), "--dry-run"), w, w); rc != 0 {
		return fmt.Errorf("dry-run failed (rc=%d)", rc)
	}
	return nil
}

func (s *realSvc) imageDir(name string) string { return filepath.Join(s.imagesRoot, name) }

// open loads the snapshot store of an image like `omloader snapshot` does.
func (s *realSvc) open(image string, create bool) (*snapmgr.Store, *config.Resolved, error) {
	g, err := config.Load(s.cfgPath, "")
	if err != nil {
		return nil, nil, err
	}
	st, err := snapmgr.Open(s.snapRoot, image, create)
	if err != nil {
		return nil, nil, err
	}
	st.Ret, st.MaxDepth = g.Config.Snapshot.Retention, g.Config.Snapshot.MaxChainDepth
	var res *config.Resolved
	if _, serr := os.Stat(filepath.Join(s.imageDir(image), "image.yaml")); serr == nil {
		if res, err = config.Load(s.cfgPath, filepath.Join(s.imageDir(image), "image.yaml")); err != nil {
			return nil, nil, err
		}
		if err := st.WithImage(res, s.imageDir(image)); err != nil {
			return nil, nil, err
		}
	}
	return st, res, nil
}

func (s *realSvc) Images() ([]tui.ImageInfo, error) {
	ents, err := os.ReadDir(s.imagesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []tui.ImageInfo
	for _, e := range ents {
		yml := filepath.Join(s.imageDir(e.Name()), "image.yaml")
		if !e.IsDir() || !fileExists(yml) {
			continue
		}
		im := tui.ImageInfo{Name: e.Name(), BaseOK: "-"}
		res, err := config.Load(s.cfgPath, yml)
		if err != nil {
			im.Err = err.Error()
			out = append(out, im)
			continue
		}
		im.Type, im.Mode = res.Image.Type, res.Image.Snapshots.Mode
		if b := res.Image.Base; b != "" && !strings.Contains(b, "://") {
			if !filepath.IsAbs(b) {
				b = filepath.Join(s.imageDir(e.Name()), b)
			}
			if fi, err := os.Stat(b); err == nil {
				im.Size = fi.Size()
			}
		}
		if res.Image.Type == "oci" {
			im.Type = "oci (exp.)"
		}
		tp := filepath.Join(s.snapRoot, e.Name(), "tree.json")
		if st, err := snapmgr.Open(s.snapRoot, e.Name(), false); err == nil {
			im.Snaps = len(st.Tree.Labels())
			if h := st.Tree.Head(); h != "" {
				im.Current = st.Tree.Display(h)
			}
			im.BaseOK = "ok"
			if t, sh := st.Tree.BaseSHA256, res.Image.SHA256; t != "" && sh != "" && !strings.EqualFold(t, sh) {
				im.BaseOK = "MISMATCH"
			}
			p := tp
			if st.Tree.Current != "" {
				if res.Image.Snapshots.Mode == snapmgr.ModeOverlay {
					p = filepath.Join(s.snapRoot, e.Name(), st.Tree.Current, "upper")
				} else {
					p = filepath.Join(s.snapRoot, e.Name(), st.Tree.Nodes[st.Tree.Current].File)
				}
			}
			if fi, err := os.Stat(p); err == nil {
				im.LastUsed = fi.ModTime()
			}
		} else if fileExists(tp) {
			im.Err = err.Error()
		}
		out = append(out, im)
	}
	return out, nil
}

func (s *realSvc) Tree(image string) (*tui.TreeInfo, error) {
	st, res, err := s.open(image, true)
	if err != nil {
		return nil, err
	}
	t := &tui.TreeInfo{Image: image, Mode: st.Mode, Current: st.Tree.Current, Head: st.Tree.Head()}
	if res != nil {
		t.Type = res.Image.Type
	}
	for _, n := range st.Tree.Names() {
		nd := st.Tree.Nodes[n]
		_, al := st.Usage(n)
		t.Nodes = append(t.Nodes, tui.NodeInfo{Name: n, Parent: nd.Parent, Label: nd.Label, Created: nd.Created, Note: nd.Note, Tags: nd.Tags,
			Alloc: al, NoLayer: st.Mode != snapmgr.ModeNone && !st.LayerExists(n), Live: n == st.Tree.Current, Ephemeral: nd.Ephemeral})
	}
	return t, nil
}

func (s *realSvc) Create(image, label, note string, tags []string) error {
	st, _, err := s.open(image, true)
	if err != nil {
		return err
	}
	return st.Create(label, note, tags)
}

func (s *realSvc) Revert(image, target, keep string) error {
	st, _, err := s.open(image, false)
	if err != nil {
		return err
	}
	return st.Revert(target, keep)
}

func (s *realSvc) DeletePlan(image, label string, rec, force bool) ([]string, error) {
	st, _, err := s.open(image, false)
	if err != nil {
		return nil, err
	}
	pl, err := st.PlanDelete(label, rec, force)
	if err != nil {
		return nil, err
	}
	return pl.Desc, nil
}

func (s *realSvc) Delete(image, label string, rec, force bool) error {
	st, _, err := s.open(image, false)
	if err != nil {
		return err
	}
	_, err = st.Delete(label, rec, force)
	return err
}

func (s *realSvc) Tag(image, label, tag string, remove bool) error {
	st, _, err := s.open(image, false)
	if err != nil {
		return err
	}
	if remove {
		err = st.Tree.Untag(label, tag)
	} else {
		err = st.Tree.Tag(label, tag)
	}
	if err != nil {
		return err
	}
	return st.Save()
}

func (s *realSvc) SetNote(image, label, note string) error {
	st, _, err := s.open(image, false)
	if err != nil {
		return err
	}
	if err := st.Tree.SetNote(label, note); err != nil {
		return err
	}
	return st.Save()
}

func (s *realSvc) Prune(image string, gc, dry bool) ([]string, error) {
	st, _, err := s.open(image, false)
	if err != nil {
		return nil, err
	}
	if gc {
		ids, err := st.GCEphemeral(dry)
		for i, id := range ids {
			ids[i] = st.Tree.Display(id)
		}
		return ids, err
	}
	return st.Prune(dry)
}

func (s *realSvc) Verify(ctx context.Context, image string, deep bool, w io.Writer) error {
	st, _, err := s.open(image, false)
	if err != nil {
		return err
	}
	ps := st.Verify(snapmgr.VerifyOptions{Deep: deep})
	for _, p := range ps {
		if p.Warn {
			fmt.Fprintln(w, "NOTE:", p)
		} else {
			fmt.Fprintln(w, "PROBLEM:", p)
		}
	}
	if n := snapmgr.CountProblems(ps); n > 0 {
		return fmt.Errorf("verify %s: %d problem(s)", image, n)
	}
	fmt.Fprintf(w, "verify %s: OK (%d snapshots, mode=%q)\n", image, len(st.Tree.Labels()), st.Mode)
	return nil
}

// streamRunner echoes every command to w and stops between commands once ctx is cancelled.
type streamRunner struct {
	ctx context.Context
	w   io.Writer
	r   *boot.ExecRunner
}

func (s streamRunner) Run(name string, args ...string) (string, error) {
	return s.RunStdin(nil, name, args...)
}

func (s streamRunner) RunStdin(in io.Reader, name string, args ...string) (string, error) {
	if err := s.ctx.Err(); err != nil {
		return "", fmt.Errorf("cancelled")
	}
	fmt.Fprintln(s.w, "+", name, strings.Join(args, " "))
	return s.r.RunStdin(in, name, args...)
}

func (s *realSvc) Flatten(ctx context.Context, image, node, name string, w io.Writer) error {
	st, res, err := s.open(image, false)
	if err != nil {
		return err
	}
	if res == nil {
		return fmt.Errorf("image.yaml of %q not found", image)
	}
	r := streamRunner{ctx, w, &boot.ExecRunner{}}
	var d string
	if node, err = st.Tree.ResolveSnapshot(node, false); err != nil {
		return err
	}
	if st.Mode == snapmgr.ModeOverlay {
		d, err = boot.FlattenOverlay(r, boot.FlattenOptions{SnapRoot: s.snapRoot, ImagesRoot: s.imagesRoot, Image: image, Node: node, NewName: name})
	} else {
		d, err = boot.FlattenDM(r, boot.DMOptions{SnapRoot: s.snapRoot, ImagesRoot: s.imagesRoot, Image: image, Node: node, NewName: name})
	}
	if err == nil {
		fmt.Fprintln(w, "flattened into new image:", d)
	}
	return err
}

func (s *realSvc) ExportDefault(image string) (string, string) {
	st, _, err := s.open(image, false)
	ext, kind := "raw", "raw (use a .qcow2 name for qcow2)"
	if err == nil && st.Mode == snapmgr.ModeOverlay {
		ext, kind = "squashfs", "squashfs"
	}
	return kind, filepath.Join(filepath.Dir(s.snapRoot), "export", image+"."+ext)
}

func (s *realSvc) Export(ctx context.Context, image, node, path string, w io.Writer) error {
	st, _, err := s.open(image, false)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	r := streamRunner{ctx, w, &boot.ExecRunner{}}
	if node, err = st.Tree.ResolveSnapshot(node, false); err != nil {
		return err
	}
	if st.Mode == snapmgr.ModeOverlay {
		err = boot.ExportSquashfs(r, s.snapRoot, image, node, path)
	} else {
		o := boot.DMOptions{SnapRoot: s.snapRoot, ImagesRoot: s.imagesRoot, Image: image, Node: node, Raw: path}
		if strings.HasSuffix(path, ".qcow2") {
			o.Raw, o.Qcow2 = "", path
		}
		err = boot.ExportDM(r, o)
	}
	if err == nil {
		fmt.Fprintln(w, "exported:", path)
	}
	return err
}

// ---- online ----

// splitSource splits the TUI's "NAME@MIRROR" source reference.
func splitSource(ref string) (name, mirror string) {
	name, mirror, _ = strings.Cut(ref, "@")
	return
}

func (s *realSvc) Offline() bool {
	c, err := s.cfg()
	return err != nil || !c.Policy.AllowOnline || c.Cache.Offline == "on"
}

func (s *realSvc) Sources() ([]tui.SourceInfo, error) {
	c, err := s.cfg()
	if err != nil {
		return nil, err
	}
	var out []tui.SourceInfo
	for _, n := range c.SourceNames() {
		src := c.Sources[n]
		si := tui.SourceInfo{Name: n, Type: src.Type, URL: src.URL, Mirrors: c.SourceMirrors(n), Usable: c.SourceUsable(n), Mirror: src.Mirror}
		if si.Mirror == "" {
			si.Mirror = config.MirrorOfficial
		}
		if ids := c.ServerNames(n); len(ids) > 0 {
			si.MirrorIDs = append([]string{config.MirrorOfficial, config.MirrorAuto}, ids...)
		}
		if !c.SourceAllowed(n) {
			si.Note = tui.L.SourceBlocked
		} else if !si.Usable {
			si.Note = tui.L.SourceOffline
		} else if src.Type == "oci-registry" {
			si.Note = "experimental"
			if !c.OCIEnabled() {
				si.Note = "experimental, disabled"
			}
		}
		out = append(out, si)
	}
	return out, nil
}

func (s *realSvc) SourceTest(ctx context.Context, name string, w io.Writer) error {
	setOpCtx(ctx)
	name, mirror := splitSource(name)
	a := []string{"test", name}
	if mirror != "" {
		a = append(a, "--mirror", mirror)
	}
	if s.cfgPath != "" {
		a = append(a, "-c", s.cfgPath)
	}
	if rc := runSource(a, w, w); rc != 0 {
		return fmt.Errorf("source %s: probe failed", name)
	}
	return nil
}

func (s *realSvc) Catalog(ctx context.Context, source string, f tui.Filter) ([]tui.CatalogItem, error) {
	c, err := s.cfg()
	if err != nil {
		return nil, err
	}
	source, mirror := splitSource(source)
	fe, err := newMirrorFetcher(c, source, mirror, false, io.Discard)
	if err != nil {
		return nil, err
	}
	cat, err := fe.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	var out []tui.CatalogItem
	for _, i := range cat.Find(simplestreams.Query{Arch: "amd64"}) {
		out = append(out, tui.CatalogItem{OS: strings.ToLower(i.OS), Release: i.Release, Variant: i.Variant, Arch: i.Arch, Kind: i.Kind(), Version: i.Version, Size: i.Root.Size})
	}
	return out, nil
}

func (s *realSvc) DefaultPullName(it tui.CatalogItem) string {
	n := strings.ToLower(it.OS + "-" + it.Release)
	if it.Variant != "default" {
		n += "-" + it.Variant
	}
	if it.Kind == "vm" {
		n += "-vm"
	}
	return n
}

func (s *realSvc) Pull(ctx context.Context, source string, it tui.CatalogItem, name string, w io.Writer) error {
	setOpCtx(ctx)
	source, mirror := splitSource(source)
	a := []string{it.Spec(), "--source", source, "--name", name, "--out", s.imagesRoot}
	if mirror != "" {
		a = append(a, "--mirror", mirror)
	}
	if it.Kind == "vm" {
		a = append(a, "--vm")
	}
	if s.cfgPath != "" {
		a = append(a, "-c", s.cfgPath)
	}
	if rc := runPull(a, w, w); rc != 0 {
		if ctx.Err() != nil {
			return fmt.Errorf("cancelled")
		}
		return fmt.Errorf("pull failed (rc=%d)", rc)
	}
	return nil
}

// PullDocker implements tui.DockerPuller (experimental OCI support).
func (s *realSvc) PullDocker(ctx context.Context, source, image, name string, w io.Writer) error {
	setOpCtx(ctx)
	source, mirror := splitSource(source)
	a := []string{"docker:" + image, "--source", source, "--out", s.imagesRoot}
	if name != "" {
		a = append(a, "--name", name)
	}
	if mirror != "" {
		a = append(a, "--mirror", mirror)
	}
	if s.cfgPath != "" {
		a = append(a, "-c", s.cfgPath)
	}
	if rc := runPull(a, w, w); rc != 0 {
		if ctx.Err() != nil {
			return fmt.Errorf("cancelled")
		}
		return fmt.Errorf("pull failed (rc=%d)", rc)
	}
	return nil
}

var _ tui.DockerPuller = (*realSvc)(nil)

func (s *realSvc) PullBytes() int64 {
	c, err := s.cfg()
	if err != nil {
		return 0
	}
	var n int64
	parts, _ := filepath.Glob(filepath.Join(c.Cache.Path, "tmp", "*.part"))
	for _, p := range parts {
		if fi, err := os.Stat(p); err == nil {
			n += fi.Size()
		}
	}
	return n
}

// ---- storage / network / settings ----

func (s *realSvc) Store() (tui.StoreStatus, error) {
	st := tui.StoreStatus{Kind: "none"}
	if c, err := s.cfg(); err == nil {
		st.MinFreePct = c.Snapshot.Retention.MinFreePercent
	}
	if t, f, pct, err := snapmgr.FreePercent(s.snapRoot, nil); err == nil {
		st.Total, st.Free, st.FreePct = int64(t), int64(f), pct
	}
	st.Path = s.snapRoot
	switch {
	case s.env["STOREFILE"] != "":
		st.Kind, st.Path = "store-file", s.env["STOREFILE"]
		if i, err := (snapmgr.StoreOps{R: &boot.ExecRunner{}}).Info(st.Path); err == nil {
			st.HostFS, st.Size, st.Alloc, st.State, st.Attached = i.HostFS, i.Size, i.Allocated, i.SB.StateName(), len(i.LoopDevs) > 0
		} else {
			st.Notes = append(st.Notes, err.Error())
		}
	case s.env["SNAPDEV"] != "":
		st.Kind, st.Path = "partition", s.env["SNAPDEV"]+" -> "+s.snapRoot
	default:
		if s.env["SNAPOK"] == "0" {
			st.Notes = append(st.Notes, "no persistent snapshot store: /mnt/snap is on the loader's RAM (ephemeral). Create a store file below.")
		}
	}
	if b, err := os.ReadFile("/run/omloader/store.log"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if l = strings.TrimPrefix(l, "OMLOADER_LOADER: "); l != "" {
				st.Notes = append(st.Notes, l)
			}
		}
	}
	if c, err := s.cfg(); err == nil && c.Storage.Snapshots.StoreFile.AutoCreate && s.env["STOREFILE"] != "" {
		st.Notes = append(st.Notes, "auto-created at first boot if missing, size "+c.Storage.Snapshots.StoreFile.Size)
	}
	if b, err := os.ReadFile(CreateStatusFile); err == nil {
		f := strings.Fields(string(b))
		switch {
		case len(f) == 3 && f[0] == "creating":
			d, _ := strconv.ParseInt(f[1], 10, 64)
			t, _ := strconv.ParseInt(f[2], 10, 64)
			st.Notes = append([]string{fmt.Sprintf("creating store: %.1f/%.1f GB (background; snapshots are in RAM until the next boot)", float64(d)/1e9, float64(t)/1e9)}, st.Notes...)
		case len(f) > 0 && f[0] == "ready":
			st.Notes = append([]string{"store ready, used from next boot"}, st.Notes...)
		}
	}
	if e := s.env["STOREERR"]; e != "" {
		st.Notes = append(st.Notes, "store error: "+e)
	}
	return st, nil
}

func (s *realSvc) StoreDefaults() (string, string) {
	c, err := s.cfg()
	if err != nil {
		return "", "20G"
	}
	sf := c.Storage.Snapshots.StoreFile
	p := sf.Path
	if p == "" {
		p = "/mnt/images/omloader/store.img"
	} else if !strings.HasPrefix(p, "/mnt/") {
		p = "/mnt/images" + p
	}
	return p, sf.Size
}

func (s *realSvc) storeOps(ctx context.Context, w io.Writer) snapmgr.StoreOps {
	return snapmgr.StoreOps{R: streamRunner{ctx, w, &boot.ExecRunner{}}}
}

func (s *realSvc) StoreCreate(ctx context.Context, path, size string, w io.Writer) error {
	c, err := s.cfg()
	if err != nil {
		return err
	}
	n, err := config.ParseSize(size)
	if err != nil {
		return err
	}
	host, pre, err := s.storeOps(ctx, w).StoreCreate(snapmgr.CreateOpts{Path: path, Size: n, Label: snapmgr.StoreLabel, Prealloc: c.Storage.Snapshots.StoreFile.Prealloc})
	if err == nil {
		fmt.Fprintf(w, "created %s on %s (preallocated=%v). Reboot the loader to use it as the snapshot store.\n", path, host, pre)
	}
	return err
}

func (s *realSvc) StoreGrow(ctx context.Context, path, size string, w io.Writer) error {
	n, err := config.ParseSize(size)
	if err != nil {
		return err
	}
	host, err := s.storeOps(ctx, w).StoreGrow(path, n, true)
	if err == nil {
		fmt.Fprintf(w, "grown %s (host %s). The store was released: reboot the loader to mount it again.\n", path, host)
	}
	return err
}

func (s *realSvc) NetStatus(ctx context.Context, probe bool, w io.Writer) error {
	setOpCtx(ctx)
	a := []string{"status"}
	if !probe {
		a = append(a, "--offline")
	}
	if s.cfgPath != "" {
		a = append(a, "-c", s.cfgPath)
	}
	runNet(a, w, w)
	return nil
}

func (s *realSvc) Settings() ([]string, error) {
	r, err := config.Load(s.cfgPath, "")
	if err != nil {
		return nil, err
	}
	head := []string{fmt.Sprintf("omloader %s", version)}
	if in, err := baseinfo.Read(baseFile); err == nil && in["BUILD_DATE"] != "" {
		head[0] += " (built " + in["BUILD_DATE"] + ")"
	}
	head = append(head, "", fmt.Sprintf("images root: %s", s.imagesRoot), fmt.Sprintf("snapshots root: %s", s.snapRoot), "")
	return append(head, r.Flat()...), nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

var _ tui.Service = (*realSvc)(nil)
