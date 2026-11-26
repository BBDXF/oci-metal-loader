// Command omloader: M0 CLI (config validation, snapshot metadata).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"omloader/internal/baseinfo"
	"omloader/internal/boot"
	"omloader/internal/config"
	"omloader/internal/imgimport"
)

const usage = `usage:
  omloader version [--long|--json]   (--long/--json add the Debian live base info from /etc/omloader-release)
  omloader config validate [-c omloader.yaml] [-i image.yaml]
  omloader config show --resolved [-c omloader.yaml] [-i image.yaml]
  omloader config get [-c omloader.yaml] KEY   (print one resolved value, e.g. boot.default_image; for scripts)
  omloader boot --image NAME [--accept-defaults] [--snapshot current|base|LABEL] [--ephemeral] [-c global.yaml]
            [--images-root DIR] [--snap-root DIR] [--dry-run] [--no-kexec] [--exec-cmd-log FILE]
            [--force] [--ro] [--no-inject] [--data-root DIR] [--shell auto|image|busybox]
  omloader tui [-c omloader.yaml] [--images-root DIR] [--snap-root DIR] [--data-root DIR] [--ascii]   (interactive terminal UI; docs/tui.md)
  omloader guest-assemble --plan /omloader/plan.json   (runs inside the guest initramfs)
  omloader snapshot <op> ...   (run 'omloader snapshot' for the operations)
  omloader store create|info|check|grow|find ...   (ext4 store FILE on exFAT/NTFS: see docs/store-file.md)
  omloader import lxd PATH --name N --out IMAGES_ROOT [--force] [--autologin] [--data SPEC]...
` + usageM4 + `  EXPERIMENTAL Docker/OCI images (on by default; switch off with experimental.oci_images: false, OML_EXPERIMENTAL=off or omloader.experimental=off):
    omloader import oci-dir|oci-archive|docker-archive PATH --name N --out IMAGES_ROOT ...   and  boot of type: oci images
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// parse allows flags before or after positional args.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func run(args []string, out, errw io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(errw, "error:", err); return 1 }
	if len(args) >= 1 && (args[0] == "version" || args[0] == "--version") {
		return runVersion(args[1:], out, errw)
	}
	if len(args) < 1 || (len(args) < 2 && args[0] != "boot" && args[0] != "guest-assemble" && args[0] != "snapshot" && args[0] != "tui") {
		fmt.Fprint(errw, usage)
		return 1
	}
	if h, ok := m4Commands[args[0]]; ok {
		return h(args[1:], out, errw)
	}
	if args[0] == "tui" {
		return runTUI(args[1:], out, errw)
	}
	if args[0] == "guest-assemble" {
		return runGuest(args[1:], out, errw)
	}
	if args[0] == "boot" {
		return runBoot(args[1:], out, errw)
	}
	if args[0] == "import" {
		return runImport(args[1:], out, errw)
	}
	if args[0] == "snapshot" {
		return runSnap(args[1:], out, errw)
	}
	if args[0] == "config" && args[1] == "get" {
		return runConfigGet(args[2:], out, errw)
	}
	fs := flag.NewFlagSet(args[0]+" "+args[1], flag.ContinueOnError)
	fs.SetOutput(errw)
	switch args[0] + " " + args[1] {
	case "config validate", "config show":
		cfg := fs.String("c", "", "global omloader.yaml")
		img := fs.String("i", "", "image.yaml")
		resolved := fs.Bool("resolved", false, "show resolved values with source layer")
		if _, err := parse(fs, args[2:]); err != nil {
			return 1
		}
		r, err := config.Load(*cfg, *img)
		if err != nil {
			return fail(err)
		}
		if args[1] == "validate" {
			fmt.Fprintln(out, "OK")
			return 0
		}
		if !*resolved {
			return fail(fmt.Errorf("config show requires --resolved"))
		}
		fmt.Fprintln(out, strings.Join(r.Flat(), "\n"))
		return 0
	}
	fmt.Fprint(errw, usage)
	return 1
}

func runBoot(args []string, out, errw io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(errw, "error:", err); return 1 }
	fs := flag.NewFlagSet("boot", flag.ContinueOnError)
	fs.SetOutput(errw)
	image := fs.String("image", "", "image name")
	snap := fs.String("snapshot", "", "current (default: continue the live state) | base | <snapshot label> (the latter two boot an ephemeral child)")
	eph := fs.Bool("ephemeral", false, "throwaway boot: nothing is kept (also valid with current)")
	cfg := fs.String("c", "", "global omloader.yaml")
	ir := fs.String("images-root", "", "images root (default storage.images.path)")
	sr := fs.String("snap-root", "", "snapshots root (default storage.snapshots.path)")
	dry := fs.Bool("dry-run", false, "print commands, change nothing")
	nok := fs.Bool("no-kexec", false, "do everything except kexec -e")
	cl := fs.String("exec-cmd-log", "", "append executed commands to FILE")
	ro := fs.Bool("ro", false, "with --snapshot base: mount the original read-only (no ephemeral COW child)")
	dataRoot := fs.String("data-root", "", "data volumes root (default <snap-root>/../data)")
	expOCI := fs.Bool("experimental-oci", false, "force-enable the experimental Docker/OCI image support (on by default; accepted for compatibility)")
	noInj := fs.Bool("no-inject", false, "do not append the guest-assemble initrd")
	acceptDefaults := fs.Bool("accept-defaults", false, "apply the prefilled first-boot init (time zone, hostname, ...) without asking (also: omloader.init=accept, guest.init: accept-defaults)")
	shell := fs.String("shell", "", "console shell for this boot: auto | image | busybox (overrides runtime.shell; default: the omloader.shell= kernel flag)")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return 1
	}
	if *shell == "" {
		*shell = kernelArg("omloader.shell")
	}
	if *shell != "" && *shell != "auto" && *shell != "image" && *shell != "busybox" {
		return fail(fmt.Errorf("--shell must be auto, image or busybox"))
	}
	if *image == "" || strings.ContainsAny(*image, "/\\") || *image == ".." {
		return fail(fmt.Errorf("--image NAME required (no path separators)"))
	}
	if *expOCI {
		os.Setenv("OML_EXPERIMENTAL", "oci")
	}
	g, err := config.Load(*cfg, "")
	if err != nil {
		return fail(err)
	}
	if *ir == "" {
		*ir = g.Config.Storage.Images.Path
	}
	if *sr == "" {
		*sr = g.Config.Storage.Snapshots.Path
	}
	dir := filepath.Join(*ir, *image)
	res, err := config.Load(*cfg, filepath.Join(dir, "image.yaml"))
	if err != nil {
		return fail(err)
	}
	ini, skipInit, err := boot.ResolveInit(&res.Config, boot.ImageHints{Templates: res.Image.LXDTemplates}, *image, dir,
		*acceptDefaults || kernelArg("omloader.init") == "accept", *dry)
	if err != nil {
		return fail(err)
	}
	o := boot.Options{Init: ini, SkipInit: skipInit, Hints: boot.ImageHints{Templates: res.Image.LXDTemplates}, Image: *image, Snapshot: *snap, ImagesRoot: *ir, SnapRoot: *sr,
		Shell: *shell, DryRun: *dry, NoKexec: *nok, Ephemeral: *eph, RO: *ro, NoInject: *noInj, DataRoot: *dataRoot, Global: *cfg, Out: out, Warn: errw}
	plan, err := boot.BuildPlan(o, res, dir)
	if err != nil {
		return fail(err)
	}
	desc := "continuing the live state"
	if plan.Ephemeral {
		desc = "ephemeral boot (nothing is kept)"
	}
	fmt.Fprintf(out, "omloader: booting %s: %s [layer %s]\n", *image, desc, plan.Target)
	var r boot.Runner = &boot.ExecRunner{LogFile: *cl}
	if *dry {
		r = &boot.DryRunner{Out: out}
	} else {
		CancelAutoCreate(out)
	}
	if res.Image.Type != "lxd-vm" {
		if _, err := boot.ExecuteOverlay(o, res, plan, r); err != nil {
			return fail(err)
		}
		return 0
	}
	if _, err := boot.Execute(o, res, plan, r); err != nil {
		return fail(err)
	}
	return 0
}

func runGuest(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("guest-assemble", flag.ContinueOnError)
	fs.SetOutput(errw)
	plan := fs.String("plan", boot.GuestPlanRel, "plan.json")
	if _, err := parse(fs, args); err != nil {
		return 1
	}
	p, err := boot.LoadPlan(*plan)
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	r := &boot.ExecRunner{BinDir: boot.GuestBinDir, LibDir: boot.GuestLibDir, Loader: p.Loader, Env: []string{"DM_DISABLE_UDEV=1"}}
	root, err := boot.GuestAssemble(p, r, boot.GuestOptions{Out: out})
	if err != nil {
		fmt.Fprintln(errw, "OMLOADER_GUEST: error:", strings.ReplaceAll(err.Error(), "\n", " | "))
		return 1
	}
	fmt.Fprintln(out, "OMLOADER_GUEST: root =", root)
	return 0
}

func runImport(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(errw)
	name := fs.String("name", "", "image name")
	dst := fs.String("out", "", "images root")
	force := fs.Bool("force", false, "replace an existing image")
	autologin := fs.Bool("autologin", false, "set runtime.autologin (serial-getty autologin on ttyS0)")
	var data multi
	fs.Var(&data, "data", "data volume name:/mount:backend[:size[:snapshot]] (repeatable)")
	expOCI := fs.Bool("experimental-oci", false, "force-enable the experimental Docker/OCI image support (on by default; accepted for compatibility)")
	cfg := fs.String("c", "", "global omloader.yaml (experimental.oci_images)")
	tmp := fs.String("tmp", "", "temp dir for archive extraction (default $TMPDIR)")
	noProbe := fs.Bool("no-probe", false, "lxd-vm: skip kernel/initrd/root-partition detection (needs root)")
	pos, err := parse(fs, args)
	if err != nil {
		return 1
	}
	if len(pos) != 2 {
		fmt.Fprint(errw, usage)
		return 1
	}
	ociOff := false
	if !*expOCI {
		if g, err := config.Load(*cfg, ""); err == nil && !g.Config.OCIEnabled() {
			ociOff = true
		}
	}
	d, err := imgimport.Import(imgimport.Options{Kind: pos[0], Path: pos[1], Name: *name, Out: *dst, TmpDir: *tmp,
		Runner: &boot.ExecRunner{}, Warn: errw, Force: *force, NoProbe: *noProbe, Autologin: *autologin, Data: data, ExperimentalOCI: *expOCI, OCIDisabled: ociOff})
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	fmt.Fprintln(out, "imported:", d)
	return 0
}

// baseFile is the live base description; a variable so tests can point it elsewhere.
var baseFile = baseinfo.Path

// runVersion prints the loader version; --long adds the live base (/etc/omloader-release), --json prints both as JSON.
// Without the file (dev builds, not in the live system) only the loader version is shown.
func runVersion(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(errw)
	long := fs.Bool("long", false, "also print the Debian live base info")
	asJSON := fs.Bool("json", false, "JSON output (loader version + base info)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return 2
	}
	in, _ := baseinfo.Read(baseFile)
	switch {
	case *asJSON:
		m := map[string]any{"omloader": version, "base": map[string]string(in)}
		if in == nil {
			m["base"] = nil
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		fmt.Fprintln(out, string(b))
	case *long:
		fmt.Fprintln(out, "omloader", version)
		if in == nil {
			fmt.Fprintln(out, "base: unknown ("+baseFile+" not present; not running from the live ISO)")
			break
		}
		fmt.Fprintln(out, "base:", in.Line())
		for _, l := range in.Long() {
			fmt.Fprintln(out, "  "+l)
		}
	default:
		fmt.Fprintln(out, "omloader", version)
		if in != nil {
			fmt.Fprintln(out, "base:", in.Line())
		}
	}
	return 0
}

// kernelArg returns the value of NAME=value on the kernel command line ("" when absent).
func kernelArg(name string) string {
	b, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return ""
	}
	for _, a := range strings.Fields(string(b)) {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
	}
	return ""
}
