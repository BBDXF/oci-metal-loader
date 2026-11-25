package boot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"omloader/internal/config"
	"omloader/internal/snapmgr"
	"omloader/internal/snapshot"
)

// Options for Boot.
type Options struct {
	Image      string
	Snapshot   string // "" (= boot.default_snapshot, normally current), current, base, or a snapshot label
	Ephemeral  bool   // boot a throwaway child even for current
	ImagesRoot string
	SnapRoot   string
	DryRun     bool
	NoKexec    bool
	RO         bool // --snapshot base: mount the base read-only instead of an ephemeral child
	NoInject   bool // do not append the guest-assemble initrd

	// overlay (oci / lxd-container) handoff; empty = loader defaults
	NewRoot       string             // /newroot
	RunDir        string             // /run/omloader
	SwitchFile    string             // /run/omloader-switch
	ModulesSrc    string             // /lib/modules/<release> of the loader kernel
	KernelRelease string             // default uname -r
	Busybox       string             // static busybox of the loader, bind-mounted into the guest at /.omloader/busybox (default: first of PATH)
	Shell         string             // runtime.shell override for this boot only: auto | image | busybox (omloader.shell=, boot --shell, TUI)
	DataRoot      string             // data volumes root (default <dirname snap-root>/data)
	SpaceFn       snapmgr.StatfsFunc // statfs override (tests)
	ResolvConf    string             // source for guest /etc/resolv.conf (default /etc/resolv.conf)
	Global        string
	Out           io.Writer
	Warn          io.Writer

	// test / environment hooks
	SelfBinary    string // default os.Executable()
	MountInfoPath string // default /proc/self/mountinfo
	TmpDir        string // default os.TempDir()
	Capture       *Captured
	Tools         map[string]string // override tool lookup (tests)
}

// Captured exposes what Execute generated (for tests).
type Captured struct {
	Plan    *GuestPlan
	Extra   []byte
	Cmdline string
}

// Layer is one snapshot layer.
type Layer struct {
	Node    string
	Dir     string // overlay mode: <snap-root>/<image>/<node> (holds upper/ and work/)
	CowFile string
	DMName  string
}

// Plan is the resolved boot plan (no side effects).
type Plan struct {
	BaseFile string
	BaseSize int64
	Sectors  int64
	Layers   []Layer
	Tree     *snapshot.Tree
	TreePath string
	NewTree  bool   // tree must be saved
	ReadOnly bool   // base booted read-only (no COW)
	Target   string // node id booted ("" = base read-only)
	// Ephemeral: Target is a throwaway child (discarded by the next boot/snapshot operation).
	Ephemeral bool
}

var devRe = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)

// BuildPlan loads config/tree, resolves the snapshot chain and verifies the base.
func BuildPlan(o Options, res *config.Resolved, imageDir string) (*Plan, error) {
	img := res.Image
	if !devRe.MatchString(o.Image) {
		return nil, fmt.Errorf("invalid image name %q", o.Image)
	}
	if img.Type != "lxd-vm" && img.Type != "oci" && img.Type != "lxd-container" {
		return nil, fmt.Errorf("boot supports types lxd-vm, oci, lxd-container; got %q", img.Type)
	}
	if img.Type == "oci" && !res.Config.OCIEnabled() {
		return nil, fmt.Errorf("%s", config.OCIDisabledMsg)
	}
	isVM := img.Type == "lxd-vm"
	base := img.Base
	if strings.Contains(base, "://") {
		return nil, fmt.Errorf("remote base %q not supported by boot", base)
	}
	if !filepath.IsAbs(base) {
		base = filepath.Join(imageDir, base)
	}
	bst, err := os.Stat(base)
	if err != nil {
		return nil, fmt.Errorf("base image: %w", err)
	}
	if res.Config.Verify.BaseChecksum != "skip" && img.SHA256 != "" {
		if err := verifySHA(base, img.SHA256); err != nil {
			return nil, err
		}
	}
	p := &Plan{BaseFile: base, BaseSize: bst.Size(), Sectors: bst.Size() / 512}
	if isVM && p.Sectors == 0 {
		return nil, fmt.Errorf("base image %s is empty", base)
	}
	p.TreePath = filepath.Join(o.SnapRoot, o.Image, "tree.json")
	st, err := snapmgr.Open(o.SnapRoot, o.Image, true)
	if err != nil {
		return nil, err
	}
	if err := st.WithImage(res, imageDir); err != nil {
		return nil, err
	}
	st.Statfs, st.IgnoreLock = o.SpaceFn, true
	_, terr := os.Stat(p.TreePath)
	p.NewTree = os.IsNotExist(terr)
	if o.DryRun {
		// plan only: forget leftover ephemeral nodes in memory, change nothing on disk
		st.Tree = st.Tree.Clone()
		st.Tree.Remove(st.Tree.Ephemerals()...)
	} else if !p.NewTree {
		if _, err := st.GCEphemeral(false); err != nil {
			return nil, err
		}
	}
	p.Tree = st.Tree
	target := o.Snapshot
	if target == "" {
		target = res.Config.Boot.DefaultSnapshot
	}
	if target == "" {
		target = snapshot.Current
	}
	eph := o.Ephemeral
	parent := ""
	switch target {
	case snapshot.Base:
		eph, parent = true, ""
	case snapshot.Current:
		if eph {
			parent = p.Tree.Current
			if parent == "" {
				parent = p.Tree.LiveParent
			}
		} else {
			var created bool
			if target, created = p.Tree.EnsureLive(); created {
				p.NewTree = true
			}
		}
	default:
		id, err := p.Tree.ResolveSnapshot(target, false)
		if err != nil {
			return nil, err
		}
		eph, parent = true, id
	}
	p.Ephemeral = eph
	if eph {
		if target == snapshot.Base && o.RO {
			p.ReadOnly = true
			target = ""
		} else {
			target = p.Tree.AddNode(parent, true)
			p.NewTree = true
		}
	}
	p.Target = target
	if target != "" {
		chain, err := p.Tree.Chain(target)
		if err != nil {
			return nil, err
		}
		if max := img.Snapshots.MaxChainDepth; max > 0 && len(chain) > max {
			return nil, fmt.Errorf("chain depth %d exceeds max_chain_depth %d: delete or prune snapshots, or flatten (`omloader snapshot flatten %s <label>` builds a flat copy)", len(chain), max, o.Image)
		}
		for _, n := range chain {
			if !devRe.MatchString(n) {
				return nil, fmt.Errorf("invalid snapshot name %q", n)
			}
			p.Layers = append(p.Layers, Layer{
				Node:    n,
				Dir:     filepath.Join(o.SnapRoot, o.Image, n),
				CowFile: filepath.Join(o.SnapRoot, o.Image, p.Tree.Nodes[n].File),
				DMName:  "oml-" + o.Image + "-" + n,
			})
		}
	}
	return p, nil
}

func sha256Hex(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifySHA(path, want string) error {
	got, err := sha256Hex(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("base checksum mismatch for %s: got %s, want %s", path, got, want)
	}
	return nil
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
}

// rewriteCmdline strips root/ro/rw/rdinit/plan tokens and appends the omloader ones.
func rewriteCmdline(base, rootDev string, ro bool) string {
	var out []string
	for _, t := range strings.Fields(base) {
		if strings.HasPrefix(t, "root=") || t == "ro" || t == "rw" || strings.HasPrefix(t, "rdinit=") || strings.HasPrefix(t, "omloader.plan=") {
			continue
		}
		out = append(out, t)
	}
	mode := "rw"
	if ro {
		mode = "ro"
	}
	out = append(out, "root="+rootDev, mode, "rdinit="+GuestPreinit, "omloader.plan="+GuestPlanRel)
	return strings.Join(out, " ")
}

// Execute runs the plan through r. It returns the final block device path.
func Execute(o Options, res *config.Resolved, p *Plan, r Runner) (string, error) {
	img := res.Image
	warn := o.Warn
	if warn == nil {
		warn = io.Discard
	}
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	if err := o.preflight(res, p); err != nil {
		return "", err
	}
	if len(img.Data) > 0 {
		fmt.Fprintln(warn, "warning: data volumes are not implemented for vm images (TODO); ignored")
	}
	if len(img.Kernel.InjectModules) > 0 {
		fmt.Fprintln(warn, "warning: kernel.inject_modules is ignored (TODO); modules are taken from the guest image automatically")
	}
	if !o.DryRun && p.NewTree {
		if err := os.MkdirAll(filepath.Dir(p.TreePath), 0o755); err != nil {
			return "", err
		}
		if err := p.Tree.Save(p.TreePath); err != nil {
			return "", err
		}
	}
	baseDM := "oml-" + o.Image + "-base"
	var layers []stackLayer
	for _, l := range p.Layers {
		if _, serr := os.Stat(l.CowFile); os.IsNotExist(serr) {
			if !o.DryRun {
				if err := os.MkdirAll(filepath.Dir(l.CowFile), 0o755); err != nil {
					return "", err
				}
			}
			if _, err := r.Run("truncate", "-s", fmt.Sprint(p.BaseSize), l.CowFile); err != nil {
				return "", err
			}
		}
		layers = append(layers, stackLayer{DMName: l.DMName, Cow: l.CowFile})
	}
	final, loops, dms, err := buildStack(r, p.BaseFile, baseDM, p.Sectors, layers)
	var mnt string
	mounted := false
	partsMapped := false
	cleanup := func() {
		if mounted {
			r.Run("umount", mnt)
		}
		if mnt != "" {
			r.Run("rmdir", mnt)
		}
		if partsMapped {
			r.Run("kpartx", "-d", final)
		}
		for i := len(dms) - 1; i >= 0; i-- {
			r.Run("dmsetup", "remove", dms[i])
		}
		for i := len(loops) - 1; i >= 0; i-- {
			r.Run("losetup", "-d", loops[i])
		}
		mounted, partsMapped, mnt = false, false, ""
	}
	if err != nil {
		cleanup()
		return "", err
	}
	fail := func(err error) (string, error) { cleanup(); return final, err }

	kout, err := r.Run("kpartx", "-a", "-v", final)
	if err != nil {
		return fail(err)
	}
	partsMapped = true
	parts := parseKpartx(kout)
	if len(parts) == 0 {
		return fail(fmt.Errorf("no partitions found on %s", final))
	}
	rootIdx := 0
	if n := img.Kernel.RootPartition; n > 0 {
		if n > len(parts) {
			return fail(fmt.Errorf("kernel.root_partition %d out of range (%d partitions)", n, len(parts)))
		}
		rootIdx = n
	} else {
		for i, pt := range parts {
			bo, err := r.Run("blkid", "-o", "value", "-s", "TYPE", pt)
			if err != nil {
				continue
			}
			if t := firstLine(bo); t == "ext4" || t == "xfs" {
				rootIdx = i + 1
				break
			}
		}
	}
	if rootIdx == 0 {
		return fail(fmt.Errorf("no ext4/xfs root partition found; set kernel.root_partition"))
	}
	root := parts[rootIdx-1]
	mo, err := r.Run("mktemp", "-d", "/tmp/omloader-mnt.XXXXXX")
	if err != nil {
		return fail(err)
	}
	mnt = firstLine(mo)
	if _, err := r.Run("mount", "-o", "ro", root, mnt); err != nil {
		return fail(err)
	}
	mounted = true
	if img.Kernel.Path == "" {
		return fail(fmt.Errorf("kernel.path is required for boot"))
	}
	realRoot := dirExists(mnt) // false for pure dry-runs with a placeholder mount dir
	kernelPath, initrdPath := mnt+img.Kernel.Path, ""
	if img.Kernel.Initrd != "" {
		initrdPath = mnt + img.Kernel.Initrd
	}
	var kernelReal string
	if realRoot {
		if kernelReal, err = ResolveInRoot(mnt, img.Kernel.Path); err != nil {
			return fail(err)
		}
		kernelPath = kernelReal
		if img.Kernel.Initrd != "" {
			if initrdPath, err = ResolveInRoot(mnt, img.Kernel.Initrd); err != nil {
				return fail(err)
			}
		}
	}
	base := strings.TrimSpace(img.Kernel.Cmdline + " " + res.Config.Boot.KernelCmdlineExtra)
	base = strings.ReplaceAll(base, "{ROOT}", root)
	cmdline := base
	initrdArg := initrdPath
	if !o.NoInject {
		cmdline = rewriteCmdline(base, root, p.ReadOnly)
		initrdArg = filepath.Join(os.TempDir(), "omloader-initrd.XXXXXX")
		if o.TmpDir != "" {
			initrdArg = filepath.Join(o.TmpDir, "omloader-initrd.XXXXXX")
		}
		gp, extra, err := o.prepareGuest(r, res, p, mnt, kernelReal, rootIdx, root, final, realRoot)
		if err != nil {
			return fail(err)
		}
		if o.Capture != nil {
			o.Capture.Plan, o.Capture.Extra = gp, extra
		}
		if realRoot && extra != nil && !o.DryRun {
			guest, err := os.ReadFile(initrdPath)
			if err != nil {
				return fail(err)
			}
			f, err := os.CreateTemp(o.TmpDir, "omloader-initrd.")
			if err != nil {
				return fail(err)
			}
			defer os.Remove(f.Name())
			if _, err := f.Write(ConcatInitrd(guest, extra)); err != nil {
				f.Close()
				return fail(err)
			}
			f.Close()
			initrdArg = f.Name()
		} else {
			fmt.Fprintf(out, "# extra initrd (guest-assemble) %d bytes appended to %s (not written in dry-run)\n", len(extra), initrdPath)
		}
	}
	if o.Capture != nil {
		o.Capture.Cmdline = cmdline
	}
	args := []string{"-l", kernelPath}
	if initrdArg != "" {
		args = append(args, "--initrd="+initrdArg)
	}
	args = append(args, "--command-line="+cmdline)
	if _, err := r.Run("kexec", args...); err != nil {
		return fail(err)
	}
	if realRoot && !o.DryRun {
		// kernel + initrd are loaded in memory now: provision default accounts into the live COW layer, then unmount cleanly
		hostName, _ := config.ResolveHostname(res.Config.GuestDefaults, img.Runtime, o.Image) // "" when keep
		ProvisionVMGuest(r, mnt, config.EffectiveRuntime(res.Config.GuestDefaults, img.Runtime), res.Config.Boot.VMFixESPFstab, hostName, out, warn)
	}
	cleanup()
	if !o.NoKexec && !o.DryRun {
		r.Run("sync")
		if _, err := r.Run("kexec", "-e"); err != nil {
			return final, err
		}
	} else if o.DryRun {
		r.Run("sync")
		r.Run("kexec", "-e")
	}
	return final, nil
}

func dirExists(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

// prepareGuest builds plan.json and the extra initrd.
func (o Options) prepareGuest(r Runner, res *config.Resolved, p *Plan, mnt, kernelReal string, rootIdx int, rootDev, final string, realRoot bool) (*GuestPlan, []byte, error) {
	tr, err := newTracker(r, o.MountInfoPath, o.DryRun)
	if err != nil {
		return nil, nil, err
	}
	gp := &GuestPlan{Version: 1, Image: o.Image, Sectors: p.Sectors, BaseDM: "oml-" + o.Image + "-base", RootIndex: rootIdx, RootDev: rootDev}
	if gp.Base, err = tr.ref(p.BaseFile, false); err != nil {
		return nil, nil, err
	}
	for _, l := range p.Layers {
		c, err := tr.ref(l.CowFile, true)
		if err != nil {
			return nil, nil, err
		}
		gp.Layers = append(gp.Layers, GuestLayer{Node: l.Node, DMName: l.DMName, Cow: c})
	}
	// A NoMount host storage (store file reached by dm-linear) also holds the base image: map that file too, or
	// fall back to mounting the host fs and losetup (needs the guest's fs driver + loop).
	if !o.DryRun {
		for _, s := range tr.list {
			if !s.NoMount {
				continue
			}
			ok := false
			if gp.Base.Storage == s.ID {
				if ex, size, e := FileExtents(p.BaseFile); e == nil {
					if v, e := ValidateExtents(ex, size); e == nil {
						gp.BaseExtents, ok = v, true
					}
				}
			} else {
				ok = true
			}
			for _, l := range gp.Layers {
				if l.Cow.Storage == s.ID {
					ok = false
				}
			}
			if !ok {
				s.NoMount = false
				for _, d := range tr.list {
					if d.Loop != nil && d.Loop.Storage == s.ID {
						d.Extents = nil
					}
				}
				gp.BaseExtents = nil
			}
		}
	}
	for _, s := range tr.list {
		gp.Storages = append(gp.Storages, *s)
	}
	if !realRoot {
		return gp, nil, nil
	}
	kver, err := kernelVersion(mnt, kernelReal)
	if err != nil {
		return nil, nil, err
	}
	names := append([]string{}, DefaultModules...)
	for _, s := range gp.Storages {
		if s.FSType != "" {
			names = append(names, s.FSType)
		}
	}
	files := moduleFiles(r, mnt, kver, names)
	if miss := missingStoreModules(r, mnt, kver, gp.Storages); len(miss) > 0 {
		return nil, nil, fmt.Errorf("store-file mode: the guest kernel %s lacks the module(s) %s (needed to reach the snapshot store file inside the guest initramfs); "+
			"boot with a store on a real OMLOADER partition, or use a guest kernel that ships them", kver, strings.Join(miss, ", "))
	}
	self := o.SelfBinary
	if self == "" {
		if self, err = os.Executable(); err != nil {
			return nil, nil, err
		}
	}
	tools := map[string]string{}
	for _, t := range GuestTools {
		if tp, ok := o.Tools[t]; ok {
			tools[t] = tp
			continue
		}
		path, err := exec.LookPath(t)
		if err != nil {
			return nil, nil, fmt.Errorf("tool %s needed for the guest initrd: %w", t, err)
		}
		tools[t] = path // copied under its own name (kmod dispatches on argv[0])
	}
	extra, err := BuildExtra(ExtraSpec{Plan: gp, Self: self, Tools: tools, ModuleFiles: files})
	if err != nil {
		return nil, nil, err
	}
	return gp, extra, nil
}

// preflight runs the space guard and the verify-lite check before anything is created or mounted.
func (o Options) preflight(res *config.Resolved, p *Plan) error {
	if o.DryRun {
		return nil
	}
	st := &snapmgr.Store{Root: o.SnapRoot, Image: o.Image, Mode: res.Image.Snapshots.Mode, BaseSHA: res.Image.SHA256,
		Ret: res.Config.Snapshot.Retention, Tree: p.Tree, TreePath: p.TreePath, Statfs: o.SpaceFn}
	if err := st.Guard(); err != nil {
		return err
	}
	var anc []string
	for i := 0; i+1 < len(p.Layers); i++ {
		anc = append(anc, p.Layers[i].Node)
	}
	return st.VerifyLite(anc)
}
