package snapmgr

// Store-file mode: the OMLOADER store is an ext4 image file (label OMLOADER) on a host partition
// (exFAT/NTFS/vfat/ext4/...). This file has the creation/inspection/grow logic; all external commands go through Runner.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"omloader/internal/config"
)

// StoreLabel is the ext4 label that marks a store file (and the real partition).
const StoreLabel = "OMLOADER"

const mib = int64(1 << 20)

// Ext4SB is the interesting part of an ext4 superblock.
type Ext4SB struct {
	Label        string
	UUID         string
	State        uint16 // s_state: 1 = cleanly unmounted, 2 = errors
	Incompat     uint32
	BlockSize    int64
	Blocks       int64
	FreeBlocks   int64
	NeedsRecover bool
}

// StateName reports the filesystem state: "clean", "dirty" (needs journal recovery / not cleanly unmounted) or "errors".
func (s Ext4SB) StateName() string {
	switch {
	case s.State&2 != 0:
		return "errors"
	case s.NeedsRecover || s.State&1 == 0:
		return "dirty"
	}
	return "clean"
}

// ReadExt4SB reads the superblock of an ext4 (or ext2/3) image file.
func ReadExt4SB(path string) (Ext4SB, error) {
	f, err := os.Open(path)
	if err != nil {
		return Ext4SB{}, err
	}
	defer f.Close()
	b := make([]byte, 1024)
	if _, err := f.ReadAt(b, 1024); err != nil {
		return Ext4SB{}, fmt.Errorf("%s: cannot read superblock: %w", path, err)
	}
	le := binary.LittleEndian
	if le.Uint16(b[0x38:]) != 0xEF53 {
		return Ext4SB{}, fmt.Errorf("%s: not an ext2/3/4 filesystem (no superblock magic)", path)
	}
	s := Ext4SB{
		State:      le.Uint16(b[0x3A:]),
		Incompat:   le.Uint32(b[0x60:]),
		BlockSize:  int64(1024) << le.Uint32(b[0x18:]),
		Blocks:     int64(le.Uint32(b[0x4:])),
		FreeBlocks: int64(le.Uint32(b[0xC:])),
		Label:      strings.TrimRight(string(b[0x78:0x88]), "\x00"),
	}
	if s.Incompat&0x80 != 0 { // 64bit feature: high words
		s.Blocks |= int64(le.Uint32(b[0x150:])) << 32
		s.FreeBlocks |= int64(le.Uint32(b[0x158:])) << 32
	}
	s.NeedsRecover = s.Incompat&0x4 != 0
	u := b[0x68:0x78]
	s.UUID = fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
	return s, nil
}

// Filesystem magic numbers (statfs f_type).
var fsMagic = map[int64]string{
	0x2011BAB0: "exfat", 0x4d44: "vfat", 0x5346544e: "ntfs", 0x7366746e: "ntfs3", 0x65735546: "fuse",
	0xEF53: "ext4", 0x58465342: "xfs", 0x9123683E: "btrfs", 0x01021994: "tmpfs", 0x794c7630: "overlay",
}

// FSTypeFn returns the filesystem type name of the filesystem holding path.
type FSTypeFn func(path string) (string, error)

// RealFSType uses statfs(2); unknown magics are returned as hex.
func RealFSType(path string) (string, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "", err
	}
	if n, ok := fsMagic[int64(st.Type)]; ok {
		if n == "fuse" { // fuseblk = ntfs-3g (the loader kernel has no ntfs3): name it by mountinfo
			if t := mountFSType(path); t != "" {
				return t, nil
			}
		}
		return n, nil
	}
	return fmt.Sprintf("0x%x", st.Type), nil
}

// mountFSType returns the mountinfo fstype of the longest mount point containing path ("" if unknown).
func mountFSType(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	best, bt := -1, ""
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		sep := -1
		for i, x := range f {
			if x == "-" {
				sep = i
				break
			}
		}
		if sep < 6 || len(f) < sep+2 {
			continue
		}
		mp := f[4]
		if (abs == mp || mp == "/" || strings.HasPrefix(abs, strings.TrimSuffix(mp, "/")+"/")) && len(mp) > best {
			best, bt = len(mp), f[sep+1]
		}
	}
	return bt
}

// NoSparse reports filesystems that cannot hold sparse files (the whole file must be allocated).
// (ntfs-3g "fuseblk" and ntfs3 support sparse files.)
func NoSparse(fs string) bool { return fs == "exfat" || fs == "vfat" }

// vfat file size limit.
const vfatMax = int64(4<<30) - 1

// StoreOps bundles the injectable parts.
type StoreOps struct {
	R      Runner
	FSType FSTypeFn // default RealFSType
}

func (o StoreOps) fstype(p string) string {
	fn := o.FSType
	if fn == nil {
		fn = RealFSType
	}
	t, err := fn(nearestExisting(p))
	if err != nil {
		return "unknown"
	}
	return t
}

// CreateOpts for StoreCreate.
type CreateOpts struct {
	Path     string
	Size     int64
	Label    string
	Prealloc string // auto|yes|no
	Force    bool   // replace an existing file
}

func roundMiB(n int64) int64 { return (n + mib - 1) / mib * mib }

func (o StoreOps) allocate(path string, size int64, prealloc bool) error {
	if !prealloc {
		_, err := o.R.Run("truncate", "-s", strconv.FormatInt(size, 10), path)
		return err
	}
	// fallocate (exfat/ntfs/ext4/xfs); filesystems without it (vfat, older exfat) get real zeros
	if _, err := o.R.Run("fallocate", "-l", strconv.FormatInt(size, 10), path); err == nil {
		return nil
	}
	_, err := o.R.Run("dd", "if=/dev/zero", "of="+path, "bs=1M", "count="+strconv.FormatInt(size/mib, 10), "conv=fsync", "status=none")
	return err
}

func resolvePrealloc(mode, fs string) (bool, error) {
	switch mode {
	case "", "auto":
		// NTFS: allocate fully too (a sparse store file on ntfs3 / kernel 6.12 lost data written after the last
		// poweroff without unmount: ext4 journal superblock zeroed)
		return NoSparse(fs) || fs == "ntfs" || fs == "ntfs3" || fs == "fuseblk", nil
	case "yes":
		return true, nil
	case "no":
		if NoSparse(fs) {
			return false, fmt.Errorf("host filesystem %s has no sparse files: --prealloc no is not possible", fs)
		}
		return false, nil
	}
	return false, fmt.Errorf("invalid prealloc mode %q (want auto|yes|no)", mode)
}

// StoreCreate creates <path> as an ext4 image (with journal) labelled c.Label.
func (o StoreOps) StoreCreate(c CreateOpts) (host string, prealloc bool, err error) {
	if c.Label == "" {
		c.Label = StoreLabel
	}
	if len(c.Label) > 16 {
		return "", false, fmt.Errorf("label %q longer than 16 bytes", c.Label)
	}
	if c.Size < 16*mib {
		return "", false, fmt.Errorf("size %d too small (minimum 16 MiB)", c.Size)
	}
	size := roundMiB(c.Size)
	if _, serr := os.Stat(c.Path); serr == nil && !c.Force {
		return "", false, fmt.Errorf("%s already exists (use `store grow` or --force)", c.Path)
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return "", false, err
	}
	host = o.fstype(c.Path)
	if host == "vfat" && size > vfatMax {
		return host, false, fmt.Errorf("vfat cannot hold files of 4 GiB or more (requested %d MiB); use exFAT/NTFS or a smaller store", size/mib)
	}
	if prealloc, err = resolvePrealloc(c.Prealloc, host); err != nil {
		return host, false, err
	}
	tmp := c.Path + ".new"
	os.Remove(tmp)
	if err := o.allocate(tmp, size, prealloc); err != nil {
		os.Remove(tmp)
		return host, prealloc, err
	}
	// journal stays on (crash safety); lazy init keeps creation fast; nodiscard keeps a preallocated file allocated; -m 1: small root reserve
	ext := "lazy_itable_init=1,lazy_journal_init=1"
	if prealloc {
		ext += ",nodiscard"
	}
	if _, err := o.R.Run("mkfs.ext4", "-q", "-F", "-L", c.Label, "-m", "1", "-E", ext, tmp); err != nil {
		os.Remove(tmp)
		return host, prealloc, err
	}
	if _, serr := os.Stat(tmp); serr == nil { // a fake runner may not create the file
		if err := os.Rename(tmp, c.Path); err != nil {
			return host, prealloc, err
		}
	}
	o.R.Run("sync")
	return host, prealloc, nil
}

// StoreInfo describes a store file.
type StoreInfo struct {
	Path      string
	HostFS    string
	Size      int64
	Allocated int64
	SB        Ext4SB
	LoopDevs  []string
}

// Info collects facts about a store file.
func (o StoreOps) Info(path string) (StoreInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return StoreInfo{}, err
	}
	if !st.Mode().IsRegular() {
		return StoreInfo{}, fmt.Errorf("%s is not a regular file", path)
	}
	i := StoreInfo{Path: path, Size: st.Size(), HostFS: o.fstype(path)}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		i.Allocated = sys.Blocks * 512
	}
	if i.SB, err = ReadExt4SB(path); err != nil {
		return i, err
	}
	i.LoopDevs = o.loopsOf(path)
	return i, nil
}

func (o StoreOps) loopsOf(path string) []string {
	out, err := o.R.Run("losetup", "-j", path, "--noheadings", "-O", "NAME")
	if err != nil {
		return nil
	}
	var l []string
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "/dev/") {
			l = append(l, f)
		}
	}
	return l
}

// StoreCheck runs e2fsck -n (read-only). ok=false when e2fsck reports problems (exit status > 0).
func (o StoreOps) StoreCheck(path string) (ok bool, out string, err error) {
	if loops := o.loopsOf(path); len(loops) > 0 {
		return false, "", fmt.Errorf("%s is attached to %s (mounted by the loader); a check of a mounted filesystem is unreliable: `umount %s && losetup -d %s` first, or pass --release", path, loops[0], loops[0], loops[0])
	}
	out, err = o.R.Run("e2fsck", "-n", path)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return false, out + err.Error(), nil
		}
		return false, out, err
	}
	return true, out, nil
}

// Release unmounts and detaches the loader's loop device(s) of a store file.
func (o StoreOps) Release(path string) error {
	for _, d := range o.loopsOf(path) {
		if _, err := o.R.Run("umount", d); err != nil && !strings.Contains(err.Error(), "not mounted") {
			return err
		}
		if _, err := o.R.Run("losetup", "-d", d); err != nil {
			return err
		}
	}
	return nil
}

// StoreGrow enlarges the image offline: extend the file (truncate, or real allocation on non-sparse hosts), e2fsck -fp, resize2fs.
func (o StoreOps) StoreGrow(path string, newSize int64, release bool) (hostFS string, err error) {
	if release {
		if err := o.Release(path); err != nil {
			return "", err
		}
	}
	inf, err := o.Info(path)
	if err != nil {
		return "", err
	}
	if len(inf.LoopDevs) > 0 {
		d := inf.LoopDevs[0]
		return inf.HostFS, fmt.Errorf("%s is in use (%s): grow is offline only; run `umount %s && losetup -d %s` or pass --release", path, d, d, d)
	}
	size := roundMiB(newSize)
	if size <= inf.Size {
		return inf.HostFS, fmt.Errorf("new size %d MiB is not larger than the current %d MiB (shrinking is not supported)", size/mib, inf.Size/mib)
	}
	if inf.HostFS == "vfat" && size > vfatMax {
		return inf.HostFS, fmt.Errorf("vfat cannot hold files of 4 GiB or more")
	}
	if NoSparse(inf.HostFS) {
		if _, err := o.R.Run("fallocate", "-l", strconv.FormatInt(size, 10), path); err != nil {
			if _, err := o.R.Run("dd", "if=/dev/zero", "of="+path, "bs=1M", "seek="+strconv.FormatInt(inf.Size/mib, 10),
				"count="+strconv.FormatInt((size-inf.Size)/mib, 10), "conv=notrunc,fsync", "status=none"); err != nil {
				return inf.HostFS, err
			}
		}
	} else if _, err := o.R.Run("truncate", "-s", strconv.FormatInt(size, 10), path); err != nil {
		return inf.HostFS, err
	}
	if _, err := o.R.Run("e2fsck", "-fp", path); err != nil {
		var ee *exec.ExitError
		if !(errors.As(err, &ee) && ee.ExitCode() == 1) { // exit 1 = errors corrected
			return inf.HostFS, err
		}
	}
	if _, err := o.R.Run("resize2fs", path); err != nil {
		return inf.HostFS, err
	}
	o.R.Run("sync")
	return inf.HostFS, nil
}

// FindStoreFiles lists candidate store files below hostRoot (the mount point of the host partition): the exact
// Path first, then every glob match in sorted order; only regular files whose ext4 label is `label` are returned.
func FindStoreFiles(hostRoot string, sf config.StoreFile, label string) []string {
	if label == "" {
		label = StoreLabel
	}
	var pats []string
	if sf.Path != "" {
		pats = append(pats, sf.Path)
	}
	pats = append(pats, sf.Glob...)
	seen := map[string]bool{}
	var res []string
	for _, p := range pats {
		m, _ := filepath.Glob(filepath.Join(hostRoot, p))
		sort.Strings(m)
		for _, f := range m {
			if seen[f] {
				continue
			}
			seen[f] = true
			st, err := os.Stat(f)
			if err != nil || !st.Mode().IsRegular() || st.Size() < 2048 {
				continue
			}
			if sb, err := ReadExt4SB(f); err == nil && sb.Label == label {
				res = append(res, f)
			}
		}
	}
	return res
}

// HostMatches checks `blkid -o export` output of the host device against host_match (uuid wins over label).
func HostMatches(blkidExport string, m config.HostMatch) bool {
	var label, uuid string
	for _, ln := range strings.Split(blkidExport, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(ln), "=")
		switch k {
		case "LABEL":
			label = v
		case "UUID":
			uuid = v
		}
	}
	if m.UUID != "" {
		return strings.EqualFold(m.UUID, uuid)
	}
	return m.Label == "" || m.Label == label
}

// StoreCandidate is one file examined by FindStoreFilesDetail; Skip is empty for a usable one.
type StoreCandidate struct{ Path, Skip string }

// FindStoreFilesDetail is FindStoreFiles plus the reason every other candidate was skipped. Order is deterministic:
// Path, then each glob pattern in config order, matches sorted within a pattern.
func FindStoreFilesDetail(hostRoot string, sf config.StoreFile, label string) []StoreCandidate {
	if label == "" {
		label = StoreLabel
	}
	pats := append([]string{}, sf.Glob...)
	if sf.Path != "" {
		pats = append([]string{sf.Path}, pats...)
	}
	seen := map[string]bool{}
	var res []StoreCandidate
	for _, p := range pats {
		m, _ := filepath.Glob(filepath.Join(hostRoot, p))
		sort.Strings(m)
		for _, f := range m {
			if seen[f] {
				continue
			}
			seen[f] = true
			st, err := os.Stat(f)
			switch {
			case err != nil || !st.Mode().IsRegular():
				continue
			case st.Size() < 2048:
				res = append(res, StoreCandidate{f, "too small"})
				continue
			}
			sb, err := ReadExt4SB(f)
			switch {
			case err != nil:
				res = append(res, StoreCandidate{f, "not an ext4 image"})
			case sb.Label != label:
				res = append(res, StoreCandidate{f, fmt.Sprintf("label %q != %q", sb.Label, label)})
			default:
				res = append(res, StoreCandidate{Path: f})
			}
		}
	}
	return res
}

const autoMargin = int64(1 << 30)
const autoMin = int64(4 << 30)

// PlanAutoCreate decides the size of an auto-created store: want if free >= want+1 GiB, else what fits
// (free-1 GiB) when that is >= 4 GiB, else skip with a reason.
func PlanAutoCreate(free, want int64) (size int64, reason string) {
	if free >= want+autoMargin {
		return want, ""
	}
	if fit := (free - autoMargin) &^ (mib - 1); fit >= autoMin {
		return fit, fmt.Sprintf("only %d MiB free: shrinking the store to %d MiB", free/mib, fit/mib)
	}
	return 0, fmt.Sprintf("not enough free space (%d MiB free; need >= %d MiB + 1 GiB margin)", free/mib, autoMin/mib)
}
