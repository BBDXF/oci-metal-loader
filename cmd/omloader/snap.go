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
  list <image> [--tree] [--sizes]
  create <image> <name> [--from NODE|base] [--note TEXT] [--tag T]...
  rollback <image> <node|base> [--discard]
  delete <image> <node> [--recursive] [--force]
  gc <image> [--tmp] [--older-than 24h] [--dry-run]
  prune <image> [--dry-run]                 (snapshot.retention.keep_last / keep_tagged)
  tag <image> <node> <tag>...   untag <image> <node> <tag>...   note <image> <node> <text>
  df <image>        verify <image> [--deep]
  export <image> <node> --squashfs OUT (overlay) | --raw OUT | --qcow2 OUT (dm; root, run in the loader)
  flatten <image> <node> [--name NEW]       (needs root: run in the loader)
  merge <image> <node>                      (unsupported in v1: validates only)
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
	from := fs.String("from", "", "parent snapshot (default current; 'base')")
	note := fs.String("note", "", "note")
	var tags multi
	fs.Var(&tags, "tag", "tag (repeatable)")
	discard := fs.Bool("discard", false, "rollback: wipe the leaf's own data")
	rec := fs.Bool("recursive", false, "delete: include descendants")
	force := fs.Bool("force", false, "allow deleting the current snapshot")
	_ = fs.Bool("tmp", true, "gc: ephemeral tmp-* snapshots (default)")
	older := fs.String("older-than", "24h", "gc: minimum age (e.g. 90m, 24h, 2d)")
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
	need := map[string]int{"list": 1, "create": 2, "rollback": 2, "delete": 2, "gc": 1, "prune": 1, "df": 1, "verify": 1,
		"export": 2, "flatten": 2, "merge": 2}
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
	st, err := snapmgr.Open(*root, image, op == "create")
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
		if err := st.Create(pos[1], *from, *note, tags); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "create %s: ok (current=%q)\n", pos[1], st.Tree.Current)
	case "rollback":
		if err := st.Rollback(pos[1], *discard); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "rollback %s: ok (current=%q)%s\n", pos[1], st.Tree.Current, map[bool]string{true: " data discarded", false: ""}[*discard])
	case "delete":
		del, err := st.Delete(pos[1], *rec, *force)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "delete: removed %s (current=%q)\n", strings.Join(del, " "), st.Tree.Current)
	case "gc", "prune":
		var del []string
		if op == "gc" {
			age, err := parseAge(*older)
			if err != nil {
				return fail(fmt.Errorf("--older-than: %w", err))
			}
			del, err = st.GC(age, *dry)
			if err != nil {
				return fail(err)
			}
		} else if del, err = st.Prune(*dry); err != nil {
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
		for _, p := range ps {
			fmt.Fprintln(out, "PROBLEM:", p)
		}
		if len(ps) > 0 {
			fmt.Fprintf(errw, "verify %s: %d problem(s)\n", image, len(ps))
			return 1
		}
		fmt.Fprintf(out, "verify %s: OK (%d snapshots, mode=%q)\n", image, len(st.Tree.Nodes), st.Mode)
	case "merge":
		p, err := st.Tree.PlanMerge(pos[1])
		if err != nil {
			return fail(err)
		}
		return fail(fmt.Errorf("snapshot merge (%s into %s) is not supported in v1; use `omloader snapshot flatten %s %s`", p.Node, p.Into, image, pos[1]))
	case "export", "flatten":
		if res == nil {
			return fail(fmt.Errorf("image.yaml of %q not found under %s (use --images-root)", image, *ir))
		}
		if _, ok := st.Tree.Nodes[pos[1]]; !ok {
			return fail(fmt.Errorf("snapshot %q not found", pos[1]))
		}
		o := boot.DMOptions{SnapRoot: *root, ImagesRoot: *ir, Image: image, Node: pos[1], NewName: *nn, Raw: *raw, Qcow2: *qc}
		switch {
		case op == "export" && st.Mode == snapmgr.ModeOverlay:
			err = boot.ExportSquashfs(r, *root, image, pos[1], *sq)
		case op == "export":
			err = boot.ExportDM(r, o)
		case st.Mode == snapmgr.ModeOverlay:
			var d string
			if d, err = boot.FlattenOverlay(r, boot.FlattenOptions{SnapRoot: *root, ImagesRoot: *ir, Image: image, Node: pos[1], NewName: *nn, DryRun: *dry}); err == nil {
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
