package main

import (
	"flag"
	"fmt"
	"io"

	"omloader/internal/boot"
	"omloader/internal/config"
	"omloader/internal/snapmgr"
)

const storeUsage = `usage:
  omloader store create --path FILE [--size 20G] [--label OMLOADER] [--prealloc auto|yes|no] [--force] [-c cfg]
  omloader store info   --path FILE          (host fs, size, allocated, label, state clean/dirty/errors)
  omloader store check  --path FILE [--release]   (e2fsck -n, offline)
  omloader store grow   --path FILE --size N [--release]   (offline: extend + e2fsck -fp + resize2fs)
  omloader store find   --host-root DIR [--host-dev DEV] [-c cfg]   (first store file with ext4 label OMLOADER; used by the loader)
`

func runStore(args []string, out, errw io.Writer, r boot.Runner) int {
	fail := func(err error) int { fmt.Fprintln(errw, "error:", err); return 1 }
	if len(args) < 1 {
		fmt.Fprint(errw, storeUsage)
		return 1
	}
	op := args[0]
	fs := flag.NewFlagSet("store "+op, flag.ContinueOnError)
	fs.SetOutput(errw)
	path := fs.String("path", "", "store file")
	size := fs.String("size", "", "size, e.g. 20G")
	label := fs.String("label", snapmgr.StoreLabel, "ext4 label")
	pre := fs.String("prealloc", "", "auto|yes|no (default storage.snapshots.store_file.prealloc)")
	force := fs.Bool("force", false, "create: replace an existing file")
	release := fs.Bool("release", false, "check/grow: umount + losetup -d the loader's loop device of this file first")
	cfg := fs.String("c", "", "global omloader.yaml")
	hroot := fs.String("host-root", "", "find: mount point of the host partition")
	hdev := fs.String("host-dev", "", "find: host partition device (checked against store_file.host_match)")
	if pos, err := parse(fs, args[1:]); err != nil || len(pos) > 0 {
		fmt.Fprint(errw, storeUsage)
		return 1
	}
	g, err := config.Load(*cfg, "")
	if err != nil {
		return fail(err)
	}
	sf := g.Config.Storage.Snapshots.StoreFile
	ops := snapmgr.StoreOps{R: r}
	need := func() bool {
		if *path == "" {
			fmt.Fprintln(errw, "error: --path FILE required")
			return false
		}
		return true
	}
	switch op {
	case "create":
		if !need() {
			return 1
		}
		if *size == "" {
			*size = sf.Size
		}
		if *pre == "" {
			*pre = sf.Prealloc
		}
		n, err := config.ParseSize(*size)
		if err != nil {
			return fail(err)
		}
		host, prealloc, err := ops.StoreCreate(snapmgr.CreateOpts{Path: *path, Size: n, Label: *label, Prealloc: *pre, Force: *force})
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "created %s: %d MiB ext4 label %s on %s (preallocated=%v)\n", *path, (n+(1<<20)-1)>>20, *label, host, prealloc)
		return 0
	case "info":
		if !need() {
			return 1
		}
		i, err := ops.Info(*path)
		if err != nil {
			return fail(err)
		}
		att := "no"
		if len(i.LoopDevs) > 0 {
			att = fmt.Sprint(i.LoopDevs)
		}
		fmt.Fprintf(out, "path: %s\nhost_fs: %s\nsize: %d (%s)\nallocated: %d (%s)\nlabel: %s\nuuid: %s\nstate: %s\nfs_blocks: %d free %d (block %d)\nattached: %s\n",
			i.Path, i.HostFS, i.Size, humanSize(i.Size), i.Allocated, humanSize(i.Allocated), i.SB.Label, i.SB.UUID, i.SB.StateName(),
			i.SB.Blocks, i.SB.FreeBlocks, i.SB.BlockSize, att)
		return 0
	case "check":
		if !need() {
			return 1
		}
		if *release {
			if err := ops.Release(*path); err != nil {
				return fail(err)
			}
		}
		ok, o, err := ops.StoreCheck(*path)
		if err != nil {
			return fail(err)
		}
		fmt.Fprint(out, o)
		if !ok {
			fmt.Fprintln(errw, "store check: PROBLEMS FOUND")
			return 1
		}
		fmt.Fprintln(out, "store check: OK")
		return 0
	case "grow":
		if !need() {
			return 1
		}
		n, err := config.ParseSize(*size)
		if err != nil {
			return fail(err)
		}
		host, err := ops.StoreGrow(*path, n, *release)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "grown %s to %d MiB (host %s)\n", *path, (n+(1<<20)-1)>>20, host)
		return 0
	case "find":
		if *hroot == "" {
			fmt.Fprintln(errw, "error: --host-root required")
			return 1
		}
		if !sf.Enabled {
			fmt.Fprintln(errw, "store_file disabled in config")
			return 1
		}
		if *hdev != "" {
			bo, err := r.Run("blkid", "-o", "export", *hdev)
			if err != nil {
				return fail(err)
			}
			if !snapmgr.HostMatches(bo, sf.HostMatch) {
				fmt.Fprintf(errw, "host partition %s does not match storage.snapshots.store_file.host_match (%+v)\n", *hdev, sf.HostMatch)
				return 3
			}
		}
		l := snapmgr.FindStoreFiles(*hroot, sf, *label)
		if len(l) == 0 {
			fmt.Fprintln(errw, "no store file (ext4, label "+*label+") found")
			return 1
		}
		for _, f := range l[1:] {
			fmt.Fprintln(errw, "warning: more than one store file; ignoring", f)
		}
		fmt.Fprintln(out, l[0])
		return 0
	}
	fmt.Fprint(errw, storeUsage)
	return 1
}

func init() {
	m4Commands["store"] = func(args []string, out, errw io.Writer) int {
		return runStore(args, out, errw, &boot.ExecRunner{})
	}
}
