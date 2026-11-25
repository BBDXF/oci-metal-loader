package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"omloader/internal/cpio"
)

// GuestTools are bundled into the extra initrd (they run via the bundled ld.so, so guest libc is irrelevant).
var GuestTools = []string{"dmsetup", "kpartx", "losetup", "blkid", "insmod"}

// DefaultModules are looked up in the guest image's /lib/modules (missing/built-in ones are skipped).
var DefaultModules = []string{
	"loop", "dm-mod", "dm-snapshot", "dm-bufio", "ext4", "xfs", "exfat", "vfat", "nls_utf8", "nls_cp437", "nls_iso8859-1",
	"nvme", "virtio_blk", "virtio_pci", "virtio_scsi", "sd_mod", "ahci", "usb-storage", "uas", "xhci-pci",
}

const preinitScript = `#!/bin/sh
# omloader: rebuild loop/dm-snapshot stack, then hand over to the guest's own /init
export PATH=/bin:/sbin:/usr/bin:/usr/sbin
export DM_DISABLE_UDEV=1
mkdir -p /dev /proc /sys /run /tmp
[ -e /proc/cmdline ] || mount -t proc proc /proc
[ -e /sys/kernel ] || mount -t sysfs sysfs /sys
[ -e /dev/null ] || mount -t devtmpfs devtmpfs /dev
echo "OMLOADER_GUEST: assembling snapshot root" > /dev/console 2>&1
/omloader/omloader guest-assemble --plan /omloader/plan.json > /dev/console 2>&1 ||
  echo "OMLOADER_GUEST: guest-assemble FAILED rc=$?" > /dev/console 2>&1
exec /init "$@"
`

// dmUdevRules makes udev (systemd/dracut initrds) know the dm devices created by preinit outside udev, so systemd
// gets a .device unit for /dev/mapper/<name> (no DM cookie => stock 10-dm.rules would leave SYSTEMD_READY=0 / no symlink).
const dmUdevRules = `# omloader: dm devices assembled by preinit (no udev cookie)
SUBSYSTEM=="block", KERNEL=="dm-*", ATTR{dm/name}=="oml-*", SYMLINK+="mapper/$attr{dm/name}", TAG+="systemd", ENV{SYSTEMD_READY}="1"
`

// ExtraSpec describes the appended initrd.
type ExtraSpec struct {
	Plan        *GuestPlan
	Self        string            // static omloader binary
	Tools       map[string]string // name -> host path
	ModuleFiles []string          // host paths, insmod order
}

// BuildExtra returns the gzip'd newc archive and fills Plan.Modules / Plan.Loader.
func BuildExtra(s ExtraSpec) ([]byte, error) {
	w := cpio.New()
	self, err := os.ReadFile(s.Self)
	if err != nil {
		return nil, err
	}
	w.File(GuestDir+"/omloader", 0o755, self)
	w.File(GuestPreinit, 0o755, []byte(preinitScript))
	w.File("/etc/udev/rules.d/99-zz-omloader-dm.rules", 0o644, []byte(dmUdevRules))
	libs := map[string]string{}
	interp := ""
	names := make([]string, 0, len(s.Tools))
	for n := range s.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		data, err := os.ReadFile(s.Tools[n])
		if err != nil {
			return nil, err
		}
		w.File(GuestBinDir+"/"+n, 0o755, data)
		ip, ls, err := ELFDeps(s.Tools[n])
		if err != nil {
			return nil, err
		}
		if ip == "" {
			continue
		}
		if interp == "" {
			interp = ip
		}
		for k, v := range ls {
			libs[k] = v
		}
	}
	if interp != "" {
		base := filepath.Base(interp)
		libs[base] = interp
		s.Plan.Loader = GuestLibDir + "/" + base
	}
	lnames := make([]string, 0, len(libs))
	for n := range libs {
		lnames = append(lnames, n)
	}
	sort.Strings(lnames)
	for _, n := range lnames {
		data, err := os.ReadFile(libs[n])
		if err != nil {
			return nil, err
		}
		perm := uint32(0o644)
		if n == filepath.Base(interp) {
			perm = 0o755
		}
		w.File(GuestLibDir+"/"+n, perm, data)
	}
	s.Plan.Modules = nil
	for i, m := range s.ModuleFiles {
		data, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		dst := fmt.Sprintf("%s/modules/%03d-%s", GuestDir, i, filepath.Base(m))
		w.File(dst, 0o644, data)
		s.Plan.Modules = append(s.Plan.Modules, dst)
	}
	pj, err := marshalPlan(s.Plan)
	if err != nil {
		return nil, err
	}
	w.File(GuestPlanRel, 0o644, pj)
	return w.Gzip()
}

// ConcatInitrd appends extra (own cpio segment) to the guest initrd, 4-byte aligned.
func ConcatInitrd(guest, extra []byte) []byte {
	out := append([]byte{}, guest...)
	for len(out)%4 != 0 {
		out = append(out, 0)
	}
	return append(out, extra...)
}

// moduleFiles asks kmod (on the loader) for the dependency-ordered module files in the image.
func moduleFiles(r Runner, root, kver string, names []string) []string {
	var files []string
	seen := map[string]bool{}
	for _, n := range names {
		out, err := r.Run("modprobe", "-d", root, "-S", kver, "--show-depends", n)
		if err != nil {
			continue
		}
		for _, ln := range strings.Split(out, "\n") {
			f := strings.Fields(ln)
			if len(f) < 2 || f[0] != "insmod" {
				continue
			}
			p := f[1]
			if i := strings.Index(p, "/lib/modules/"); i >= 0 {
				p = p[i:]
			} else {
				continue
			}
			host, err := ResolveInRoot(root, p)
			if err != nil || seen[host] {
				continue
			}
			seen[host] = true
			files = append(files, host)
		}
	}
	return files
}

// kernelVersion derives the version from vmlinuz-<ver> or the only /lib/modules entry.
func kernelVersion(root, kernelReal string) (string, error) {
	b := filepath.Base(kernelReal)
	md, err := ResolveInRoot(root, "/lib/modules")
	if err != nil {
		return "", fmt.Errorf("cannot determine kernel version: %w", err)
	}
	if strings.HasPrefix(b, "vmlinuz-") {
		// Arch names the image vmlinuz-linux (a flavour, not a version): only trust the suffix if a module dir has that name
		v := strings.TrimPrefix(b, "vmlinuz-")
		if dirExists(filepath.Join(md, v)) {
			return v, nil
		}
	}
	ents, err := os.ReadDir(md)
	if err != nil {
		return "", err
	}
	var vs []string
	for _, e := range ents {
		if e.IsDir() {
			vs = append(vs, e.Name())
		}
	}
	if len(vs) != 1 {
		return "", fmt.Errorf("cannot determine kernel version (%d candidates in /lib/modules)", len(vs))
	}
	return vs[0], nil
}

// missingStoreModules lists the modules a store-file plan needs in the guest (loop, every host/loop filesystem)
// that the guest kernel neither ships nor has built in. Plans without a store file need nothing (existing behaviour).
func missingStoreModules(r Runner, root, kver string, st []StorageRef) []string {
	need := false
	for _, s := range st {
		need = need || s.Loop != nil && len(s.Extents) == 0
	}
	if !need {
		return nil
	}
	want := []string{"loop"}
	seen := map[string]bool{"loop": true}
	for _, s := range st {
		if s.FSType != "" && !seen[s.FSType] {
			seen[s.FSType] = true
			want = append(want, s.FSType)
		}
	}
	var miss []string
	for _, n := range want {
		if _, err := r.Run("modprobe", "-d", root, "-S", kver, "--show-depends", n); err != nil {
			miss = append(miss, n)
		}
	}
	return miss
}
