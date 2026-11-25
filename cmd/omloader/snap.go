package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"omloader/internal/boot"
	"omloader/internal/config"
	"omloader/internal/snapmgr"
)

const snapUsage = `usage: omloader snapshot <op> [flags] <image> [args]   common flags: --root SNAPROOT --images-root IMGROOT -c omloader.yaml
Snapshots are managed here, in the loader, before the image is booted (no hot snapshots).
Model: base = the original image (read-only), current = your live state; snapshots are labelled frozen points in between.
  list <image> [--tree] [--sizes]
  create <image> <label> [--note TEXT] [--tag T]...       freeze the live state as <label>
  revert <image> <label|base> [--keep LABEL]              discard the live state, continue from <label>/base (alias: rollback)
  delete <image> <label> [--recursive] [--force]          leaf: removed; one child: merged into it (overlay only); --recursive: subtree
  gc <image> [--ephemeral] [--dry-run]                    remove leftovers of ephemeral boots
  prune <image> [--dry-run]                               snapshot.retention.keep_last / keep_tagged
  tag <image> <label> <tag>...   untag <image> <label> <tag>...   note <image> <label> <text>
  df <image>        verify <image> [--deep]
  export <image> <label> --squashfs OUT (overlay) | --raw OUT | --qcow2 OUT (dm; root, run in the loader)
  flatten <image> <label> [--name NEW]                    new image NEW (default <image>-flat-<label>); needs root
Boot: omloader boot --image <image> [--snapshot current|base|<label>] [--ephemeral]   (current continues the live state)
`

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func parseAge(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(n * 24 * float64(time.Hour)), nil
	}
	return time.ParseDuration(s)
}

func runSnap(args []string, out, errw io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(errw, "error:", err); return 1 }
	if len(args) < 1 {
		fmt.Fprint(errw, snapUsage)
		return 1
	}
	op := args[0]
	fs := flag.NewFlagSet("snapshot "+op, flag.ContinueOnError)
	fs.SetOutput(errw)
	root := fs.String("root", "", "snapshots root (default storage.snapshots.path)")
	ir := fs.String("images-root", "", "images root (default storage.images.path)")
	cfg := fs.String("c", "", "global omloader.yaml")
	tree := fs.Bool("tree", false, "tree view")
	sizes := fs.Bool("sizes", false, "show on-disk sizes")
	keep := fs.String("keep", "", "revert: first freeze the live state as this snapshot")
	note := fs.String("note", "", "note")
	var tags multi
	fs.Var(&tags, "tag", "tag (repeatable)")
	rec := fs.Bool("recursive", false, "delete: include descendants")
	force := fs.Bool("force", false, "allow deleting the current snapshot")
	_ = fs.Bool("ephemeral", true, "gc: remove ephemeral boot leftovers (default)")
	dry := fs.Bool("dry-run", false, "plan only")
	deep := fs.Bool("deep", false, "verify: hash the base image")
	sq := fs.String("squashfs", "", "export (overlay): output squashfs")
	raw := fs.String("raw", "", "export (dm): output raw image")
	qc := fs.String("qcow2", "", "export (dm): output qcow2 (needs qemu-img)")
	nn := fs.String("name", "", "flatten: new image name")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return 1
	}
	need := map[string]int{"list": 1, "create": 2, "revert": 2, "rollback": 2, "delete": 2, "gc": 1, "prune": 1, "df": 1, "verify": 1,
		"export": 2, "flatten": 2}
	minPos, ok := need[op]
	if !ok && (op == "tag" || op == "untag" || op == "note") {
		minPos, ok = 3, true
	}
	if !ok || len(pos) < minPos {
		fmt.Fprint(errw, snapUsage)
		return 1
	}
	image := pos[0]
	if strings.ContainsAny(image, "/\\") || image == ".." {
		return fail(fmt.Errorf("invalid image name %q", image))
	}
	g, err := config.Load(*cfg, "")
	if err != nil {
		return fail(err)
	}
	if *root == "" {
		*root = g.Config.Storage.Snapshots.Path
	}
	if *ir == "" {
		*ir = g.Config.Storage.Images.Path
	}
	st, err := snapmgr.Open(*root, image, op == "create" || op == "list")
	if err != nil {
		return fail(err)
	}
	st.Ret, st.MaxDepth = g.Config.Snapshot.Retention, g.Config.Snapshot.MaxChainDepth
	imgDir := filepath.Join(*ir, image)
	var res *config.Resolved
	if _, serr := os.Stat(filepath.Join(imgDir, "image.yaml")); serr == nil {
		if res, err = config.Load(*cfg, filepath.Join(imgDir, "image.yaml")); err != nil {
			return fail(err)
		}
		if err := st.WithImage(res, imgDir); err != nil {
			return fail(err)
		}
	}
	var r boot.Runner = &boot.ExecRunner{}
	if *dry {
		r = &boot.DryRunner{Out: out}
	}
	save := func(err error) int {
		if err == nil {
			err = st.Save()
		}
		if err != nil {
			return fail(err)
		}
		return 0
	}
	switch op {
	case "list":
		fmt.Fprint(out, st.List(snapmgrList(*tree, *sizes)))
	case "create":
		if err := st.Create(pos[1], *note, tags); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "create %s: ok (the live state continues in a new empty layer on top)\n", pos[1])
	case "revert", "rollback":
		if err := st.Revert(pos[1], *keep); err != nil {
			return fail(err)
		}
		if pos[1] == "base" {
			boot.ClearInit(imgDir) // reset to base: the first-boot init is asked again
		}
		msg := "the old live state was discarded"
		if *keep != "" {
			msg = "the old live state was kept as " + *keep
		}
		fmt.Fprintf(out, "revert %s: ok (%s; the next boot starts from %s)\n", pos[1], msg, pos[1])
	case "delete":
		what, err := st.Delete(pos[1], *rec, *force)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "delete: %s\n", what)
	case "gc", "prune":
		var del []string
		if op == "gc" {
			del, err = st.GCEphemeral(*dry)
			for i, d := range del {
				del[i] = st.Tree.Display(d)
			}
		} else {
			del, err = st.Prune(*dry)
		}
		if err != nil {
			return fail(err)
		}
		verb := "deleted"
		if *dry {
			verb = "would delete"
		}
		fmt.Fprintf(out, "%s %s: %s %d snapshot(s): %s\n", op, image, verb, len(del), strings.Join(del, " "))
	case "tag":
		return save(st.Tree.Tag(pos[1], pos[2:]...))
	case "untag":
		return save(st.Tree.Untag(pos[1], pos[2:]...))
	case "note":
		return save(st.Tree.SetNote(pos[1], strings.Join(pos[2:], " ")))
	case "df":
		s, err := st.DF()
		if err != nil {
			return fail(err)
		}
		fmt.Fprint(out, s)
	case "verify":
		ps := st.Verify(snapmgr.VerifyOptions{Deep: *deep})
		bad := snapmgr.CountProblems(ps)
		for _, p := range ps {
			if p.Warn {
				fmt.Fprintln(out, "NOTE:", p)
			} else {
				fmt.Fprintln(out, "PROBLEM:", p)
			}
		}
		if bad > 0 {
			fmt.Fprintf(errw, "verify %s: %d problem(s)\n", image, bad)
			return 1
		}
		fmt.Fprintf(out, "verify %s: OK (%d snapshots, mode=%q)\n", image, len(st.Tree.Labels()), st.Mode)
	case "export", "flatten":
		if res == nil {
			return fail(fmt.Errorf("image.yaml of %q not found under %s (use --images-root)", image, *ir))
		}
		id, rerr := st.Tree.ResolveSnapshot(pos[1], false)
		if rerr != nil {
			return fail(rerr)
		}
		if *nn == "" {
			*nn = image + "-flat-" + pos[1]
		}
		o := boot.DMOptions{SnapRoot: *root, ImagesRoot: *ir, Image: image, Node: id, NewName: *nn, Raw: *raw, Qcow2: *qc}
		switch {
		case op == "export" && st.Mode == snapmgr.ModeOverlay:
			err = boot.ExportSquashfs(r, *root, image, id, *sq)
		case op == "export":
			err = boot.ExportDM(r, o)
		case st.Mode == snapmgr.ModeOverlay:
			var d string
			if d, err = boot.FlattenOverlay(r, boot.FlattenOptions{SnapRoot: *root, ImagesRoot: *ir, Image: image, Node: id, NewName: *nn, DryRun: *dry}); err == nil {
				fmt.Fprintln(out, "flattened into new image:", d)
			}
		default:
			var d string
			if d, err = boot.FlattenDM(r, o); err == nil {
				fmt.Fprintln(out, "flattened into new image:", d)
			}
		}
		if err != nil {
			return fail(err)
		}
	default:
		fmt.Fprint(errw, snapUsage)
		return 1
	}
	return 0
}

func snapmgrList(tree, sizes bool) snapmgr.ListOptions {
	return snapmgr.ListOptions{Tree: tree, Sizes: sizes}
}
