// Command omloader: M0 CLI (config validation, snapshot metadata).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"omloader/internal/boot"
	"omloader/internal/config"
	"omloader/internal/imgimport"
)

const usage = `usage:
  omloader config validate [-c omloader.yaml] [-i image.yaml]
  omloader config show --resolved [-c omloader.yaml] [-i image.yaml]
  omloader boot --image NAME [--snapshot NAME|current|base|new:NAME] [-c global.yaml]
            [--images-root DIR] [--snap-root DIR] [--dry-run] [--no-kexec] [--exec-cmd-log FILE]
            [--force] [--ro] [--no-inject] [--data-root DIR]
  omloader guest-assemble --plan /omloader/plan.json   (runs inside the guest initramfs)
  omloader snapshot <op> ...   (run 'omloader snapshot' for the operations)
  omloader import lxd PATH --name N --out IMAGES_ROOT [--force] [--autologin] [--data SPEC]...
  experimental (disabled by default; --experimental-oci, OML_EXPERIMENTAL=oci or experimental.oci_images: true):
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
	if len(args) < 1 || (len(args) < 2 && args[0] != "boot" && args[0] != "guest-assemble" && args[0] != "snapshot") {
		fmt.Fprint(errw, usage)
		return 1
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
	snap := fs.String("snapshot", "", "name|current|base|new:<name>")
	cfg := fs.String("c", "", "global omloader.yaml")
	ir := fs.String("images-root", "", "images root (default storage.images.path)")
	sr := fs.String("snap-root", "", "snapshots root (default storage.snapshots.path)")
	dry := fs.Bool("dry-run", false, "print commands, change nothing")
	nok := fs.Bool("no-kexec", false, "do everything except kexec -e")
	cl := fs.String("exec-cmd-log", "", "append executed commands to FILE")
	force := fs.Bool("force", false, "allow booting a snapshot that has children")
	ro := fs.Bool("ro", false, "boot base read-only (no ephemeral COW child)")
	dataRoot := fs.String("data-root", "", "data volumes root (default <snap-root>/../data)")
	expOCI := fs.Bool("experimental-oci", false, "enable experimental Docker/OCI image support")
	noInj := fs.Bool("no-inject", false, "do not append the guest-assemble initrd")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return 1
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
	o := boot.Options{Image: *image, Snapshot: *snap, ImagesRoot: *ir, SnapRoot: *sr,
		DryRun: *dry, NoKexec: *nok, Force: *force, RO: *ro, NoInject: *noInj, DataRoot: *dataRoot, Global: *cfg, Out: out, Warn: errw}
	plan, err := boot.BuildPlan(o, res, dir)
	if err != nil {
		return fail(err)
	}
	var r boot.Runner = &boot.ExecRunner{LogFile: *cl}
	if *dry {
		r = &boot.DryRunner{Out: out}
	}
	if res.Image.Type != "vm" {
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
		fmt.Fprintln(errw, "OMLOADER_GUEST: error:", err)
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
	expOCI := fs.Bool("experimental-oci", false, "enable experimental Docker/OCI image support")
	cfg := fs.String("c", "", "global omloader.yaml (experimental.oci_images)")
	tmp := fs.String("tmp", "", "temp dir for archive extraction (default $TMPDIR)")
	pos, err := parse(fs, args)
	if err != nil {
		return 1
	}
	if len(pos) != 2 {
		fmt.Fprint(errw, usage)
		return 1
	}
	if !*expOCI && *cfg != "" {
		if g, err := config.Load(*cfg, ""); err == nil && g.Config.OCIEnabled() {
			*expOCI = true
		}
	}
	d, err := imgimport.Import(imgimport.Options{Kind: pos[0], Path: pos[1], Name: *name, Out: *dst, TmpDir: *tmp,
		Runner: &boot.ExecRunner{}, Warn: errw, Force: *force, Autologin: *autologin, Data: data, ExperimentalOCI: *expOCI})
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	fmt.Fprintln(out, "imported:", d)
	return 0
}
