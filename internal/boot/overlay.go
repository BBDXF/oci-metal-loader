package boot

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"omloader/internal/config"
	"omloader/internal/snapmgr"
)

// Limits for the overlayfs lowerdir stack. The kernel allows at most 500 lower
// layers and the mount option string must fit in one page (4096 bytes).
const (
	MaxOverlayLowers  = 500
	MaxOverlayOptions = 4000
)

// SwitchSpec is what the loader init needs to exec switch_root.
type SwitchSpec struct {
	NewRoot string
	Init    []string
}

// Encode is the /run/omloader-switch format: line 1 = new root, then one argument per line (init first).
func (s SwitchSpec) Encode() string {
	return s.NewRoot + "\n" + strings.Join(s.Init, "\n") + "\n"
}

func badOverlayPath(p string) bool { return strings.ContainsAny(p, ",:\\\n") }

func runCmd(r Runner, name string, args ...string) error { _, err := r.Run(name, args...); return err }

// OverlayOptions builds the overlay mount option string (lowers newest-first, squashfs last).
// layers is base-most first; the last node is the writable one.
func OverlayOptions(layers []Layer, squashMnt string) (string, error) {
	if len(layers) == 0 {
		return "", fmt.Errorf("no layers")
	}
	var lowers []string
	for i := len(layers) - 2; i >= 0; i-- {
		lowers = append(lowers, filepath.Join(layers[i].Dir, "upper"))
	}
	lowers = append(lowers, squashMnt)
	top := layers[len(layers)-1]
	for _, p := range append(append([]string{}, lowers...), top.Dir) {
		if badOverlayPath(p) {
			return "", fmt.Errorf("path %q contains characters (, : \\) unusable in overlayfs options", p)
		}
	}
	opts := "lowerdir=" + strings.Join(lowers, ":") + ",upperdir=" + filepath.Join(top.Dir, "upper") + ",workdir=" + filepath.Join(top.Dir, "work")
	if len(lowers) > MaxOverlayLowers {
		return "", fmt.Errorf("overlay stack has %d lower layers (kernel limit %d): flatten it with `omloader snapshot flatten`", len(lowers), MaxOverlayLowers)
	}
	if len(opts) > MaxOverlayOptions {
		return "", fmt.Errorf("overlay mount options are %d bytes (limit %d) with %d lower layers: flatten the chain with `omloader snapshot flatten`", len(opts), MaxOverlayOptions, len(lowers))
	}
	return opts, nil
}

func (o Options) def() Options {
	if o.NewRoot == "" {
		o.NewRoot = "/newroot"
	}
	if o.RunDir == "" {
		o.RunDir = "/run/omloader"
	}
	if o.SwitchFile == "" {
		o.SwitchFile = "/run/omloader-switch"
	}
	if o.KernelRelease == "" {
		var u syscall.Utsname
		if syscall.Uname(&u) == nil {
			var b []byte
			for _, c := range u.Release {
				if c == 0 {
					break
				}
				b = append(b, byte(c))
			}
			o.KernelRelease = string(b)
		}
	}
	if o.ModulesSrc == "" && o.KernelRelease != "" {
		o.ModulesSrc = "/lib/modules/" + o.KernelRelease
	}
	if o.Busybox == "" {
		if p, err := exec.LookPath("busybox"); err == nil {
			o.Busybox = p
		}
	}
	if o.DataRoot == "" && o.SnapRoot != "" {
		o.DataRoot = filepath.Join(filepath.Dir(filepath.Clean(o.SnapRoot)), "data")
	}
	if o.ResolvConf == "" {
		o.ResolvConf = "/etc/resolv.conf"
	}
	return o
}

// ExecuteOverlay mounts the squashfs base + overlay stack at NewRoot, applies the
// firstboot adapter and writes the switch file. It does NOT switch_root: the
// loader init does `exec switch_root` itself (so busybox is PID 1 when it runs).
func ExecuteOverlay(o Options, res *config.Resolved, p *Plan, r Runner) (*SwitchSpec, error) {
	o = o.def()
	img := res.Image
	warn, out := o.Warn, o.Out
	if warn == nil {
		warn = io.Discard
	}
	if out == nil {
		out = io.Discard
	}
	o.Out = out
	if err := o.preflight(res, p); err != nil {
		return nil, err
	}
	var opts string
	squash := filepath.Join(o.RunDir, "base")
	if !p.ReadOnly {
		var err error
		if opts, err = OverlayOptions(p.Layers, squash); err != nil {
			return nil, err
		}
	}
	if badOverlayPath(p.BaseFile) {
		return nil, fmt.Errorf("base path %q contains unusable characters", p.BaseFile)
	}
	if !o.DryRun && p.NewTree {
		if err := os.MkdirAll(filepath.Dir(p.TreePath), 0o755); err != nil {
			return nil, err
		}
		if err := p.Tree.Save(p.TreePath); err != nil {
			return nil, err
		}
	}
	var mounted []string
	cleanup := func() {
		for i := len(mounted) - 1; i >= 0; i-- {
			r.Run("umount", mounted[i])
		}
	}
	fail := func(err error) (*SwitchSpec, error) { cleanup(); return nil, err }
	mount := func(dst string, args ...string) error {
		if err := runCmd(r, "mount", append(args, dst)...); err != nil {
			return err
		}
		mounted = append(mounted, dst)
		return nil
	}

	if err := runCmd(r, "mkdir", "-p", squash, o.NewRoot); err != nil {
		return nil, err
	}
	if err := mount(squash, "-t", "squashfs", "-o", "loop,ro", p.BaseFile); err != nil {
		return fail(err)
	}
	if p.ReadOnly {
		fmt.Fprintln(warn, "warning: --ro: booting the squashfs read-only (no adapter, no persistence)")
		if err := mount(o.NewRoot, "--bind", squash); err != nil {
			return fail(err)
		}
	} else {
		for _, l := range p.Layers {
			if err := runCmd(r, "mkdir", "-p", filepath.Join(l.Dir, "upper"), filepath.Join(l.Dir, "work")); err != nil {
				return fail(err)
			}
		}
		if err := mount(o.NewRoot, "-t", "overlay", "-o", opts, "overlay"); err != nil {
			return fail(err)
		}
	}
	spec := &SwitchSpec{NewRoot: o.NewRoot}
	if o.DryRun {
		fmt.Fprintf(out, "# adapter, modules bind and switch file %s (not done in dry-run)\n", o.SwitchFile)
		return spec, nil
	}
	if p.ReadOnly {
		spec.Init = []string{"/sbin/init"}
		if len(img.Runtime.Entrypoint) > 0 {
			spec.Init = append(append([]string{}, img.Runtime.Entrypoint...), img.Runtime.Cmd...)
		}
	} else {
		applied, err := Adapt(AdaptInput{Root: o.NewRoot, Image: o.Image, Runtime: img.Runtime, Busybox: o.Busybox, ResolvConf: o.ResolvConf, Warn: warn})
		if err != nil {
			return fail(fmt.Errorf("firstboot adapter: %w", err))
		}
		if applied {
			fmt.Fprintln(out, "OMLOADER: firstboot adapter applied")
		}
		if err := o.bindData(r, img.Data, warn); err != nil {
			return fail(err)
		}
		if err := o.bindModules(r, warn); err != nil {
			return fail(err)
		}
		if spec.Init, err = SelectInit(o.NewRoot, img.Runtime, warn); err != nil {
			return fail(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(o.SwitchFile), 0o755); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(o.SwitchFile, []byte(spec.Encode()), 0o600); err != nil {
		return fail(err)
	}
	fmt.Fprintf(out, "OMLOADER: overlay root ready at %s (layers=%d), init=%s\n", o.NewRoot, len(p.Layers), strings.Join(spec.Init, " "))
	return spec, nil
}

// bindModules bind-mounts the loader's module tree read-only into the guest so it can modprobe after the
// root switch. The loader is a normal Debian live system (systemctl switch-root, no initramfs cleanup of the
// old root), so a plain bind of /lib/modules/<release> stays valid; nothing is copied.
func (o Options) bindModules(r Runner, warn io.Writer) error {
	if o.KernelRelease == "" || o.ModulesSrc == "" {
		return nil
	}
	if st, err := os.Stat(o.ModulesSrc); err != nil || !st.IsDir() {
		fmt.Fprintf(warn, "warning: loader modules %s not found; guest cannot modprobe\n", o.ModulesSrc)
		return nil
	}
	dst, err := walkInRoot(o.NewRoot, "/lib/modules/"+o.KernelRelease, true)
	if err != nil {
		return err
	}
	if err := runCmd(r, "mount", "--bind", o.ModulesSrc, dst); err != nil {
		return err
	}
	return runCmd(r, "mount", "-o", "remount,bind,ro", dst)
}

// bindData mounts the image's data volumes into the new root. They live outside the snapshot layers,
// so they survive rollback/delete/flatten of snapshots ("snapshot: false" is the supported mode; true is a TODO and warns).
func (o Options) bindData(r Runner, vols []config.DataVolume, warn io.Writer) error {
	for _, d := range vols {
		if d.Snapshot {
			fmt.Fprintf(warn, "warning: data volume %q: snapshot: true is not implemented (TODO); treated as persistent\n", d.Name)
		}
		dst, err := walkInRoot(o.NewRoot, d.Mount, true)
		if err != nil {
			return err
		}
		switch d.Backend {
		case "dir":
			src := d.Path
			if src == "" {
				src = filepath.Join(o.DataRoot, o.Image, d.Name)
			}
			if err := runCmd(r, "mkdir", "-p", src); err != nil {
				return err
			}
			if err := runCmd(r, "mount", "--bind", src, dst); err != nil {
				return err
			}
		case "ext4-file":
			src := d.Path
			if src == "" {
				src = filepath.Join(o.DataRoot, o.Image, d.Name+".img")
			}
			size := int64(1 << 30)
			if d.Size != "" {
				if size, err = snapmgr.ParseSize(d.Size); err != nil {
					return fmt.Errorf("data volume %q: %w", d.Name, err)
				}
			}
			created, err := snapmgr.EnsureExt4File(r, src, size, d.Name)
			if err != nil {
				return fmt.Errorf("data volume %q: %w", d.Name, err)
			}
			if created {
				fmt.Fprintf(o.Out, "OMLOADER: created data volume %s (%s)\n", src, snapmgr.Human(size))
			}
			if err := runCmd(r, "mount", "-t", "ext4", "-o", "loop", src, dst); err != nil {
				return err
			}
		default:
			fmt.Fprintf(warn, "warning: data volume %q backend %q not implemented (TODO); skipped\n", d.Name, d.Backend)
		}
	}
	return nil
}
