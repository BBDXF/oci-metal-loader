package boot

import (
	"fmt"
	"io"
	"strings"
)

// GrowSectors returns the origin size in 512-byte sectors (1 MiB aligned) when the base (bytes) is smaller than want (bytes);
// 0 = do not grow (want 0/keep, or the base is already big enough).
func GrowSectors(base, want int64) int64 {
	want &^= (1 << 20) - 1
	if want <= 0 || base >= want {
		return 0
	}
	return want / 512
}

// growRoot grows the last partition (which must hold the root fs) of the snapshot device dev to the end of the disk and
// resizes an ext4 fs. Partition maps are rebuilt; it returns the new partition list, or nil when nothing changed.
// Every failure only warns: the boot continues unchanged.
func (o Options) growRoot(r Runner, dev string, rootIdx, nparts int, warn io.Writer) []string {
	w := func(f string, a ...any) { fmt.Fprintf(warn, "warning: vm_root_size: "+f+"; booting unchanged\n", a...) }
	if rootIdx != nparts {
		w("root partition %d is not the last of %d", rootIdx, nparts)
		return nil
	}
	if _, err := r.Run("kpartx", "-d", dev); err != nil {
		w("kpartx -d: %v", err)
		return nil
	}
	remap := func() []string {
		out, err := r.Run("kpartx", "-a", "-v", dev)
		if err != nil {
			w("kpartx -a: %v", err)
			return nil
		}
		return parseKpartx(out)
	}
	lo, _ := r.Run("sfdisk", "-d", dev)
	if strings.Contains(lo, "label: gpt") {
		if _, err := r.Run("sgdisk", "-e", dev); err != nil {
			w("sgdisk -e: %v", err)
		}
	}
	if _, err := r.Run("sh", "-c", `echo ', +' | sfdisk --no-reread --no-tell-kernel -N "$1" "$2"`, "sh", fmt.Sprint(rootIdx), dev); err != nil {
		w("sfdisk grow: %v", err)
		return remap()
	}
	parts := remap()
	if len(parts) != nparts {
		return parts
	}
	root := parts[rootIdx-1]
	bo, _ := r.Run("blkid", "-o", "value", "-s", "TYPE", root)
	switch t := firstLine(bo); t {
	case "ext4", "ext3", "ext2":
		if _, err := r.Run("e2fsck", "-fp", root); err != nil && !strings.Contains(err.Error(), "exit status 1:") {
			w("e2fsck: %v", err)
			return parts
		}
		if _, err := r.Run("resize2fs", root); err != nil {
			w("resize2fs: %v", err)
		}
	default:
		w("filesystem %q is not grown (only ext4 is supported); partition was extended", t)
	}
	return parts
}
