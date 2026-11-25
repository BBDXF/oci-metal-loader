package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"omloader/internal/boot"
	"omloader/internal/config"
	"omloader/internal/snapmgr"
)

const storeUsage = `usage:
  omloader store create --path FILE [--size 20G] [--label OMLOADER] [--prealloc auto|yes|no] [--force] [-c cfg]
  omloader store info   --path FILE          (host fs, size, allocated, label, state clean/dirty/errors)
  omloader store check  --path FILE [--release]   (e2fsck -n, offline)
  omloader store grow   --path FILE --size N [--release]   (offline: extend + e2fsck -fp + resize2fs)
  omloader store autocreate --host-root DIR [-c cfg]   (loader, boot time: create omloader/store.img if no store exists; exit 0 = created)
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
		var chosen string
		for _, c := range snapmgr.FindStoreFilesDetail(*hroot, sf, *label) {
			switch {
			case c.Skip != "":
				fmt.Fprintf(errw, "store file %s skipped: %s\n", c.Path, c.Skip)
			case chosen == "":
				chosen = c.Path
			default:
				fmt.Fprintf(errw, "store file %s skipped: %s is already chosen (first match wins)\n", c.Path, chosen)
			}
		}
		if chosen == "" {
			fmt.Fprintln(errw, "no store file (ext4, label "+*label+") found")
			return 1
		}
		fmt.Fprintf(errw, "store file %s chosen (ext4 label %s)\n", chosen, *label)
		fmt.Fprintln(out, chosen)
		return 0
	case "autocreate":
		if *hroot == "" {
			fmt.Fprintln(errw, "error: --host-root required")
			return 1
		}
		return storeAutoCreate(*hroot, sf, *label, *pre, ops, out)
	}
	fmt.Fprint(errw, storeUsage)
	return 1
}

func init() {
	m4Commands["store"] = func(args []string, out, errw io.Writer) int {
		return runStore(args, out, errw, &boot.ExecRunner{})
	}
}

// storeAutoCreate creates <host>/omloader/store.img when no usable store file exists. Never fatal: exit 1 = not created
// (reason printed as OMLOADER_LOADER lines).
func storeAutoCreate(root string, sf config.StoreFile, label, pre string, ops snapmgr.StoreOps, out io.Writer) int {
	say := func(f string, a ...any) { fmt.Fprintf(out, "OMLOADER_LOADER: "+f+"\n", a...) }
	if !sf.Enabled || !sf.AutoCreate {
		say("store auto-create disabled (storage.snapshots.store_file.auto_create=false)")
		return 1
	}
	if l := snapmgr.FindStoreFiles(root, sf, label); len(l) > 0 {
		say("store file %s already exists: not creating another", l[0])
		return 1
	}
	path := filepath.Join(root, "omloader", "store.img")
	os.Remove(path + ".new") // partial leftover of an interrupted creation
	if _, err := os.Stat(path); err == nil {
		say("%s exists but is not an ext4 image labelled %s: not overwriting it (remove or relabel it with e2label)", path, label)
		return 1
	}
	want, err := config.ParseSize(sf.Size)
	if err != nil {
		say("store auto-create skipped: bad size %q: %v", sf.Size, err)
		return 1
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(root, &st); err != nil {
		say("store auto-create skipped: statfs: %v", err)
		return 1
	}
	size, why := snapmgr.PlanAutoCreate(int64(st.Bavail)*int64(st.Bsize), want)
	if size == 0 {
		say("store auto-create skipped: %s", why)
		return 1
	}
	if why != "" {
		say("%s", why)
	}
	say("creating store %s ... %d GiB (ext4, label %s; this can take a while)", path, size>>30, label)
	if pre == "" {
		pre = sf.Prealloc
	}
	host, prealloc, err := ops.StoreCreate(snapmgr.CreateOpts{Path: path, Size: size, Label: label, Prealloc: pre})
	if err != nil {
		os.Remove(path + ".new")
		say("store auto-create FAILED: %v (snapshots stay in RAM)", err)
		return 1
	}
	say("store created: %s %d GiB on %s (preallocated=%v)", path, size>>30, host, prealloc)
	return 0
}
