package imgimport

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"omloader/internal/boot"
)

// KernelInfo is the result of probing a raw VM disk.
type KernelInfo struct {
	RootPartition int    // 1-based, order of kpartx output
	Kernel        string // absolute path inside the root filesystem (a real file, not a symlink name)
	Initrd        string
}

var vmCmdline = "console=ttyS0,115200 console=tty0"

// importLXDVM converts an LXD VM disk (qcow2 inside an LXD image: <dir>/disk.qcow2 [+ lxd.tar.xz], or a disk file path)
// to <tmp>/base.raw and, unless NoProbe, detects kernel/initrd/root partition by attaching the raw disk read-only.
func importLXDVM(o Options, tmp string) (map[string]any, error) {
	if o.Runner == nil {
		return nil, fmt.Errorf("no runner")
	}
	disk, meta := o.Path, ""
	if st, err := os.Stat(o.Path); err == nil && st.IsDir() {
		disk = ""
		for _, n := range []string{"disk.qcow2", "rootfs.img", "disk.raw", "disk.img"} {
			if _, err := os.Stat(filepath.Join(o.Path, n)); err == nil {
				disk = filepath.Join(o.Path, n)
				break
			}
		}
		if disk == "" {
			return nil, fmt.Errorf("no disk.qcow2 in %s", o.Path)
		}
	} else if strings.HasSuffix(o.Path, ".tar.xz") {
		meta = o.Path
		disk = filepath.Join(filepath.Dir(o.Path), "disk.qcow2")
	}
	if _, err := os.Stat(disk); err != nil {
		return nil, fmt.Errorf("lxd vm disk: %w", err)
	}
	if meta == "" {
		for _, n := range []string{"lxd.tar.xz", "incus.tar.xz"} {
			if _, err := os.Stat(filepath.Join(filepath.Dir(disk), n)); err == nil {
				meta = filepath.Join(filepath.Dir(disk), n)
				break
			}
		}
	}
	if meta != "" {
		if mb, err := readMetadata(o, meta); err == nil {
			var md struct {
				Architecture string `yaml:"architecture"`
			}
			if yaml.Unmarshal(mb, &md) == nil && md.Architecture != "" && md.Architecture != "x86_64" && md.Architecture != "amd64" {
				return nil, fmt.Errorf("lxd image architecture %q is not x86_64", md.Architecture)
			}
		}
	}
	raw := filepath.Join(tmp, "base.raw")
	if _, err := o.Runner.Run("qemu-img", "convert", "-O", "raw", disk, raw); err != nil {
		return nil, err
	}
	y := map[string]any{"name": o.Name, "type": "lxd-vm", "format": "raw", "base": "base.raw",
		"snapshots": map[string]any{"mode": "dm-snapshot"}}
	k := map[string]any{"cmdline": vmCmdline}
	y["kernel"] = k
	if o.NoProbe {
		fmt.Fprintf(o.Warn, "note: --no-probe: fill kernel.path, kernel.initrd and kernel.root_partition in image.yaml (or re-run without --no-probe inside the loader)\n")
		return y, nil
	}
	ki, err := Probe(o.Runner, raw, o.Warn)
	if err != nil {
		return nil, fmt.Errorf("kernel probe failed (use --no-probe to skip; it needs root: losetup/kpartx/mount): %w", err)
	}
	k["path"], k["root_partition"] = ki.Kernel, ki.RootPartition
	if ki.Initrd != "" {
		k["initrd"] = ki.Initrd
	}
	return y, nil
}

var kpartxMap = regexp.MustCompile(`^add map (\S+)`)

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// Probe attaches raw read-only (losetup + kpartx), mounts every ext2/3/4/xfs partition read-only and looks for a kernel.
// Everything goes through r so it is unit-testable; it needs root for real.
func Probe(r boot.Runner, raw string, warn io.Writer) (*KernelInfo, error) {
	lo, err := r.Run("losetup", "--find", "--show", "-r", raw)
	if err != nil {
		return nil, err
	}
	loop := firstLine(lo)
	defer r.Run("losetup", "-d", loop)
	ko, err := r.Run("kpartx", "-a", "-v", "-r", loop)
	if err != nil {
		return nil, err
	}
	defer func() { // udev may still hold the maps for a moment
		for i := 0; i < 5; i++ {
			if _, err := r.Run("kpartx", "-d", loop); err == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()
	var parts []string
	for _, l := range strings.Split(ko, "\n") {
		if m := kpartxMap.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			parts = append(parts, "/dev/mapper/"+m[1])
		}
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("no partitions found in the VM disk")
	}
	for i, dev := range parts {
		to, err := r.Run("blkid", "-o", "value", "-s", "TYPE", dev)
		if err != nil {
			continue
		}
		opts := ""
		switch t := firstLine(to); t {
		case "ext4", "ext3", "ext2":
			opts = "ro,noload"
		case "xfs":
			opts = "ro,norecovery,nouuid"
		default:
			continue
		}
		ki, err := probePart(r, dev, opts)
		if err != nil {
			fmt.Fprintf(warn, "note: partition %d: %v\n", i+1, err)
			continue
		}
		ki.RootPartition = i + 1
		return ki, nil
	}
	return nil, fmt.Errorf("no ext4/xfs partition with /boot/vmlinuz* or /vmlinuz found")
}

func probePart(r boot.Runner, dev, opts string) (*KernelInfo, error) {
	mo, err := r.Run("mktemp", "-d", "/tmp/omloader-probe.XXXXXX")
	if err != nil {
		return nil, err
	}
	mnt := firstLine(mo)
	if _, err := r.Run("mount", "-o", opts, dev, mnt); err != nil {
		r.Run("rmdir", mnt)
		return nil, err
	}
	defer func() { r.Run("umount", mnt); r.Run("rmdir", mnt) }()
	k, i, ok := FindKernel(mnt)
	if !ok {
		return nil, fmt.Errorf("no kernel found")
	}
	return &KernelInfo{Kernel: k, Initrd: i}, nil
}

var numRe = regexp.MustCompile(`\d+|\D+`)

// versionLess is a natural-order comparison ("6.8.0-9" < "6.8.0-10").
func versionLess(a, b string) bool {
	pa, pb := numRe.FindAllString(a, -1), numRe.FindAllString(b, -1)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] == pb[i] {
			continue
		}
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		if ea == nil && eb == nil {
			return na < nb
		}
		return pa[i] < pb[i]
	}
	return len(pa) < len(pb)
}

func isFile(root, p string) bool {
	h, err := boot.ResolveInRoot(root, p)
	if err != nil {
		return false
	}
	st, err := os.Stat(h)
	return err == nil && st.Mode().IsRegular()
}

// FindKernel looks for the newest /boot/vmlinuz-<ver> (else /boot/vmlinuz, /vmlinuz) in the tree rooted at root and the
// matching initrd. Symlinks are resolved inside root only (ResolveInRoot); returned paths are in-image absolute paths.
func FindKernel(root string) (kernel, initrd string, ok bool) {
	var vers []string
	if bd, err := boot.ResolveInRoot(root, "/boot"); err == nil {
		if es, err := os.ReadDir(bd); err == nil {
			for _, e := range es {
				if v, found := strings.CutPrefix(e.Name(), "vmlinuz-"); found && !strings.HasSuffix(v, ".old") && isFile(root, "/boot/"+e.Name()) {
					vers = append(vers, v)
				}
			}
		}
	}
	if len(vers) > 0 {
		sort.Slice(vers, func(i, j int) bool { return versionLess(vers[i], vers[j]) })
		v := vers[len(vers)-1]
		kernel = "/boot/vmlinuz-" + v
		for _, c := range []string{"/boot/initrd.img-" + v, "/boot/initramfs-" + v + ".img", "/boot/initrd-" + v + ".img", "/boot/initrd-" + v} {
			if isFile(root, c) {
				return kernel, c, true
			}
		}
		return kernel, "", true
	}
	for _, c := range [][2]string{{"/boot/vmlinuz", "/boot/initrd.img"}, {"/vmlinuz", "/initrd.img"}} {
		if isFile(root, c[0]) {
			if isFile(root, c[1]) {
				return c[0], c[1], true
			}
			return c[0], "", true
		}
	}
	return "", "", false
}
