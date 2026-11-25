package boot

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"omloader/internal/config"
)

// RawPart is one partition of a disk's partition table (sector units of 512 bytes).
type RawPart struct {
	N         int
	Start     uint64
	Size      uint64
	PartLabel string // GPT name
	MBRType   byte
}

// HDPart is a partition with everything needed for naming, mounting and the guest dm table.
type HDPart struct {
	RawPart
	FSType string // blkid TYPE ("" unknown)
	Label  string
	UUID   string
	Name   string // mount directory name (AssignNames)
	Role   string // "", "data" (the omloader data partition holding the ISO/VHDX) or "store" (hosts the store file)
	RO     bool   // mapped/mounted read-only
	Dev    string // loader-side device node (/dev/vda4)
}

// HDDisk is one disk of the loader.
type HDDisk struct {
	Name    string // kernel name: vda, sda, nvme0n1
	ID      string // GPT disk GUID or MBR signature (identifies the disk in the guest, names can differ)
	Sectors uint64
	Parts   []HDPart
	Boot    bool // holds the loader data partition
}

// ParsePartTable reads the GPT (preferred) or MBR (primary + extended chain) of a disk image/device.
func ParsePartTable(ra io.ReaderAt) (id string, parts []RawPart, err error) {
	hdr := make([]byte, 512)
	if _, err = ra.ReadAt(hdr, 512); err == nil && string(hdr[:8]) == "EFI PART" {
		diskGUID := hex.EncodeToString(hdr[56:72])
		ent := binary.LittleEndian.Uint64(hdr[72:])
		cnt := binary.LittleEndian.Uint32(hdr[80:])
		sz := binary.LittleEndian.Uint32(hdr[84:])
		if sz < 128 || sz > 4096 || cnt > 1024 {
			return "", nil, fmt.Errorf("implausible GPT header (entries=%d size=%d)", cnt, sz)
		}
		buf := make([]byte, uint64(cnt)*uint64(sz))
		if _, err := ra.ReadAt(buf, int64(ent*512)); err != nil {
			return "", nil, fmt.Errorf("GPT entries: %w", err)
		}
		for i := uint32(0); i < cnt; i++ {
			e := buf[i*sz : (i+1)*sz]
			if allZero(e[:16]) {
				continue
			}
			first, last := binary.LittleEndian.Uint64(e[32:]), binary.LittleEndian.Uint64(e[40:])
			if last < first {
				continue
			}
			var u []uint16
			for j := 56; j+1 < 128 && j+1 < len(e); j += 2 {
				c := binary.LittleEndian.Uint16(e[j:])
				if c == 0 {
					break
				}
				u = append(u, c)
			}
			parts = append(parts, RawPart{N: int(i) + 1, Start: first, Size: last - first + 1, PartLabel: string(utf16.Decode(u))})
		}
		return "gpt-" + diskGUID, parts, nil
	}
	mbr := make([]byte, 512)
	if _, err := ra.ReadAt(mbr, 0); err != nil {
		return "", nil, err
	}
	if mbr[510] != 0x55 || mbr[511] != 0xAA {
		return "", nil, fmt.Errorf("no partition table")
	}
	id = fmt.Sprintf("mbr-%08x", binary.LittleEndian.Uint32(mbr[440:]))
	var ext uint64
	for i := 0; i < 4; i++ {
		e := mbr[446+i*16 : 462+i*16]
		t, st, sz := e[4], uint64(binary.LittleEndian.Uint32(e[8:])), uint64(binary.LittleEndian.Uint32(e[12:]))
		if t == 0 || sz == 0 {
			continue
		}
		if isMBRExt(t) {
			if ext == 0 {
				ext = st
			}
			continue
		}
		if t == 0xEE {
			continue
		}
		parts = append(parts, RawPart{N: i + 1, Start: st, Size: sz, MBRType: t})
	}
	if ext != 0 {
		next, n := uint64(0), 5
		for guard := 0; guard < 128; guard++ {
			ebr := make([]byte, 512)
			if _, err := ra.ReadAt(ebr, int64((ext+next)*512)); err != nil || ebr[510] != 0x55 || ebr[511] != 0xAA {
				break
			}
			e := ebr[446:462]
			if sz := uint64(binary.LittleEndian.Uint32(e[12:])); e[4] != 0 && sz > 0 {
				parts = append(parts, RawPart{N: n, Start: ext + next + uint64(binary.LittleEndian.Uint32(e[8:])), Size: sz, MBRType: e[4]})
				n++
			}
			ne := ebr[462:478]
			if !isMBRExt(ne[4]) {
				break
			}
			next = uint64(binary.LittleEndian.Uint32(ne[8:]))
			if next == 0 {
				break
			}
		}
	}
	return id, parts, nil
}

func isMBRExt(t byte) bool { return t == 0x05 || t == 0x0f || t == 0x85 }

// SanitizeName makes a mount directory name: only [A-Za-z0-9._-], everything else (spaces, slashes, non-ASCII, ...) becomes
// "_" (runs collapsed), max 32 bytes, never starting with ".". Returns "" when nothing usable is left.
func SanitizeName(s string) string {
	var b strings.Builder
	us := false
	for _, c := range s {
		ok := c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_')
		if !ok {
			if !us {
				b.WriteByte('_')
			}
			us = true
			continue
		}
		us = c == '_'
		b.WriteRune(c)
	}
	o := strings.TrimLeft(b.String(), ".")
	if len(o) > 32 {
		o = o[:32]
	}
	if strings.Trim(o, "_") == "" {
		return ""
	}
	return o
}

// AssignNames sets HDPart.Name of one disk: LABEL, else GPT PARTLABEL, else p<N>; duplicates (second and later) get _p<N>.
func AssignNames(parts []HDPart) {
	seen := map[string]bool{}
	for i := range parts {
		p := &parts[i]
		n := SanitizeName(p.Label)
		if n == "" {
			n = SanitizeName(p.PartLabel)
		}
		if n == "" {
			n = "p" + strconv.Itoa(p.N)
		}
		if seen[strings.ToLower(n)] {
			suf := "_p" + strconv.Itoa(p.N)
			if len(n)+len(suf) > 32 {
				n = n[:32-len(suf)]
			}
			n += suf
		}
		seen[strings.ToLower(n)] = true
		p.Name = n
	}
}

// MountableFS reports whether the guest/container may mount this blkid TYPE.
func MountableFS(t string) bool {
	switch t {
	case "ntfs", "ntfs3", "exfat", "vfat", "ext2", "ext3", "ext4", "xfs", "btrfs":
		return true
	}
	return false // BitLocker, crypto_LUKS, swap, LVM2_member, linux_raid_member, unknown, ""
}

// DMName is the guest device-mapper name of a disk / partition.
func DMDiskName(disk string) string        { return "oml-" + disk }
func DMPartName(disk string, n int) string { return fmt.Sprintf("oml-%sp%d", disk, n) }

// PartRO decides whether a partition is mapped/mounted read-only: everything is ro in mode ro; in mode rw the data/store
// partition stays ro unless force_rw_data.
func PartRO(cfg config.HostDisks, role string) bool {
	if cfg.Mode != "rw" {
		return true
	}
	return role != "" && !cfg.ForceRWData
}

// FstabLines renders the guest fstab entries (mountable partitions only).
func FstabLines(cfg config.HostDisks, disks []HDDisk) []string {
	var out []string
	for _, d := range disks {
		for _, p := range d.Parts {
			if !MountableFS(p.FSType) {
				continue
			}
			mode := "rw"
			if p.RO {
				mode = "ro"
			}
			opts := "nofail,x-systemd.idle-timeout=60," + mode
			if cfg.AutomountOn() {
				opts = "noauto,x-systemd.automount," + opts
			}
			out = append(out, fmt.Sprintf("/dev/mapper/%s  %s/%s/%s  auto  %s  0 0", DMPartName(d.Name, p.N), cfg.Root(), d.Name, p.Name, opts))
		}
	}
	return out
}

// HostDiskPlan is the part of plan.json that tells the guest preinit which whole disks and partitions to map.
type HostDiskPlan struct {
	Name    string       `json:"name"`
	ID      string       `json:"id"`
	Sectors uint64       `json:"sectors"`
	Parts   []HostPartPl `json:"parts"`
}

// HostPartPl is one partition dm-linear.
type HostPartPl struct {
	N     int    `json:"n"`
	Start uint64 `json:"start"`
	Size  uint64 `json:"size"`
	RO    bool   `json:"ro,omitempty"`
	Name  string `json:"name,omitempty"` // mount directory name
	FS    string `json:"fs,omitempty"`
	Role  string `json:"role,omitempty"`
}

// ToPlan converts discovered disks into plan entries.
func HostDisksPlan(disks []HDDisk) []HostDiskPlan {
	var out []HostDiskPlan
	for _, d := range disks {
		pl := HostDiskPlan{Name: d.Name, ID: d.ID, Sectors: d.Sectors}
		for _, p := range d.Parts {
			pl.Parts = append(pl.Parts, HostPartPl{N: p.N, Start: p.Start, Size: p.Size, RO: p.RO, Name: p.Name, FS: p.FSType, Role: p.Role})
		}
		out = append(out, pl)
	}
	return out
}

// DiskTable is the dm table of the whole disk over the real block device; PartTable of a partition over the whole-disk dm.
func DiskTable(sectors uint64, dev string) string {
	return fmt.Sprintf("0 %d linear %s 0\n", sectors, dev)
}
func PartTable(p HostPartPl, diskDM string) string {
	return fmt.Sprintf("0 %d linear %s %d\n", p.Size, diskDM, p.Start)
}

// RebaseExtents shifts store-file extents (bytes, relative to the host partition) by the partition start (sectors) so they
// address the whole-disk dm instead.
func RebaseExtents(ex []Extent, startSectors uint64) []Extent {
	out := make([]Extent, len(ex))
	for i, e := range ex {
		e.Physical += startSectors * 512
		out[i] = e
	}
	return out
}

// README renders /mnt/disks/README.txt.
func HostDisksReadme(cfg config.HostDisks, disks []HDDisk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "omloader host disks (mode %s, %s)\n\n", cfg.Mode, map[bool]string{true: "all disks", false: "boot disk only"}[cfg.AllDisks()])
	b.WriteString("This machine's own disks appear here as <disk>/<name>, mounted on first access.\n")
	if cfg.Mode == "rw" {
		b.WriteString("Mode rw: partitions are writable except the omloader data/store partition (unless force_rw_data).\n")
	} else {
		b.WriteString("Mode ro: everything is read-only.\n")
	}
	b.WriteString("\nDO NOT delete, move or edit omloader/store.img or anything under omloader/ on the omloader data partition:\n")
	b.WriteString("the running guest's snapshots and root live there.\n")
	b.WriteString("NTFS: if Windows used Fast Startup/hibernation the volume may be unclean; disable it in Windows before writing (rw).\n\n")
	for _, d := range disks {
		for _, p := range d.Parts {
			note := ""
			switch p.Role {
			case "data":
				note = "  <- omloader data partition (always read-only unless force_rw_data)"
			case "store":
				note = "  <- omloader store (always read-only unless force_rw_data)"
			}
			if !MountableFS(p.FSType) {
				note += "  [not mounted: " + orStr(p.FSType, "unknown") + "]"
			}
			fmt.Fprintf(&b, "%s/%s/%s  (%s%d, %s)%s\n", cfg.Root(), d.Name, p.Name, d.Name, p.N, orStr(p.FSType, "?"), note)
		}
	}
	return b.String()
}

func orStr(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

// ---- loader-side discovery ----

// DiscoverOpts: sysfs/dev roots are overridable for tests.
type DiscoverOpts struct {
	Sys, Dev   string
	DataSource string          // block device of the loader's data partition mount ("" unknown)
	StoreUUIDs map[string]bool // UUIDs of partitions hosting the snapshot/store files
}

// DiscoverHostDisks reads the partition tables of the selected disks of the loader and classifies the partitions.
func DiscoverHostDisks(r Runner, cfg config.HostDisks, o DiscoverOpts) ([]HDDisk, error) {
	if o.Sys == "" {
		o.Sys = "/sys"
	}
	if o.Dev == "" {
		o.Dev = "/dev"
	}
	bootDisk := ""
	if o.DataSource != "" {
		bootDisk = parentDisk(o.Sys, filepath.Base(o.DataSource))
	}
	ents, err := os.ReadDir(filepath.Join(o.Sys, "block"))
	if err != nil {
		return nil, err
	}
	var disks []HDDisk
	for _, e := range ents {
		n := e.Name()
		if skipDisk(n) {
			continue
		}
		isBoot := n == bootDisk
		if !cfg.AllDisks() && !isBoot {
			continue
		}
		f, err := os.Open(filepath.Join(o.Dev, n))
		if err != nil {
			continue
		}
		id, raw, err := ParsePartTable(f)
		f.Close()
		if err != nil {
			continue
		}
		var secs uint64
		if b, err := os.ReadFile(filepath.Join(o.Sys, "block", n, "size")); err == nil {
			secs, _ = strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		}
		d := HDDisk{Name: n, ID: id, Sectors: secs, Boot: isBoot}
		for _, rp := range raw {
			p := HDPart{RawPart: rp, Dev: filepath.Join(o.Dev, partNode(o.Sys, n, rp.N))}
			if bo, err := r.Run("blkid", "-o", "export", p.Dev); err == nil {
				for _, ln := range strings.Split(bo, "\n") {
					k, v, _ := strings.Cut(strings.TrimSpace(ln), "=")
					switch k {
					case "TYPE":
						p.FSType = v
					case "LABEL":
						p.Label = v
					case "UUID":
						p.UUID = v
					}
				}
			}
			if o.DataSource != "" && p.Dev == o.DataSource {
				p.Role = "data"
			} else if p.UUID != "" && o.StoreUUIDs[p.UUID] {
				p.Role = "store"
			}
			p.RO = PartRO(cfg, p.Role)
			d.Parts = append(d.Parts, p)
		}
		AssignNames(d.Parts)
		disks = append(disks, d)
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Name < disks[j].Name })
	return disks, nil
}

func skipDisk(n string) bool {
	for _, p := range []string{"loop", "ram", "dm-", "sr", "zram", "nbd", "md", "fd"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// partNode finds the kernel name of partition number n of disk (sysfs "partition" files), default disk+n / disk+pn.
func partNode(sys, disk string, n int) string {
	ents, _ := os.ReadDir(filepath.Join(sys, "block", disk))
	for _, e := range ents {
		if b, err := os.ReadFile(filepath.Join(sys, "block", disk, e.Name(), "partition")); err == nil && strings.TrimSpace(string(b)) == strconv.Itoa(n) {
			return e.Name()
		}
	}
	if last := disk[len(disk)-1]; last >= '0' && last <= '9' {
		return fmt.Sprintf("%sp%d", disk, n)
	}
	return fmt.Sprintf("%s%d", disk, n)
}

// parentDisk returns the whole-disk kernel name of a block device name (itself when it is a disk).
func parentDisk(sys, name string) string {
	p, err := filepath.EvalSymlinks(filepath.Join(sys, "class", "block", name))
	if err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(p, "partition")); err == nil {
		return filepath.Base(filepath.Dir(p))
	}
	return filepath.Base(p)
}

// DataMountSource returns the block device (e.g. /dev/vda4) of the mount at mountPoint from a mountinfo text ("" when none).
func DataMountSource(mountinfo, mountPoint string) string {
	if mountPoint == "" {
		return ""
	}
	src := ""
	for _, m := range parseMountInfo(mountinfo) {
		if m.Point == mountPoint && strings.HasPrefix(m.Source, "/dev/") {
			src = m.Source
		}
	}
	return src
}

// MediumHostMnt reads MEDIUM_HOST_MNT from /run/omloader/medium.env (or the override path).
func MediumHostMnt(envPath string) string {
	if envPath == "" {
		envPath = "/run/omloader/medium.env"
	}
	b, err := os.ReadFile(envPath)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "MEDIUM_HOST_MNT="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// loaderDiscover is DiscoverHostDisks with the live loader's data partition and storages.
func loaderDiscover(r Runner, cfg config.HostDisks, mountInfoPath string, store map[string]bool) ([]HDDisk, string) {
	mi := mountInfoPath
	if mi == "" {
		mi = "/proc/self/mountinfo"
	}
	b, _ := os.ReadFile(mi)
	mm := MediumHostMnt("")
	src := DataMountSource(string(b), mm)
	disks, _ := DiscoverHostDisks(r, cfg, DiscoverOpts{DataSource: src, StoreUUIDs: store})
	return disks, mm
}

// ---- guest side (preinit) ----

type hdState struct {
	bySys map[string]*HostDiskPlan // kernel name in THIS boot -> plan disk (whole-disk dm created)
}

// assembleHostDisks maps every planned disk found in the guest (matched by GPT GUID / MBR signature) as one dm-linear over
// the whole disk (oml-<disk>) plus one per partition on top of it. wantSys: kernel name that must appear (the store host disk), "" = any.
func assembleHostDisks(p *GuestPlan, r Runner, o GuestOptions, logf func(string, ...any), wantSys string) *hdState {
	st := &hdState{bySys: map[string]*HostDiskPlan{}}
	list := o.ListDisks
	if list == nil {
		list = func() []string {
			ents, _ := os.ReadDir("/sys/block")
			var n []string
			for _, e := range ents {
				if !skipDisk(e.Name()) {
					n = append(n, e.Name())
				}
			}
			return n
		}
	}
	open := o.OpenDisk
	if open == nil {
		open = func(n string) (io.ReaderAt, func(), error) {
			f, err := os.Open("/dev/" + n)
			if err != nil {
				return nil, nil, err
			}
			return f, func() { f.Close() }, nil
		}
	}
	done := map[string]bool{}
	for i := 0; i < o.Tries; i++ {
		for _, n := range list() {
			if _, ok := st.bySys[n]; ok || done[n] {
				continue
			}
			ra, closeFn, err := open(n)
			if err != nil {
				continue
			}
			id, _, err := ParsePartTable(ra)
			closeFn()
			if err != nil {
				continue
			}
			for k := range p.HostDisks {
				hd := &p.HostDisks[k]
				if hd.ID != id {
					continue
				}
				done[n] = true
				dn := DMDiskName(hd.Name)
				if _, err := r.Run("dmsetup", "create", dn, "--table", DiskTable(hd.Sectors, "/dev/"+n)); err != nil {
					logf("host disk %s (%s): dm-linear failed: %v (skipped)", hd.Name, n, err)
					break
				}
				for _, pt := range hd.Parts {
					args := []string{"create", DMPartName(hd.Name, pt.N), "--table", PartTable(pt, "/dev/mapper/"+dn)}
					if pt.RO {
						args = append(args[:1], append([]string{"--readonly"}, args[1:]...)...)
					}
					if _, err := r.Run("dmsetup", args...); err != nil {
						logf("host disk %s partition %d: %v (skipped)", hd.Name, pt.N, err)
					}
				}
				st.bySys[n] = hd
				logf("host disk %s mapped as %s (+%d partitions)", n, dn, len(hd.Parts))
			}
		}
		if wantSys == "" && len(st.bySys) > 0 || wantSys != "" && st.bySys[wantSys] != nil {
			break
		}
		if o.Delay > 0 {
			time.Sleep(o.Delay)
		}
	}
	return st
}

// sysParent returns (whole-disk kernel name, partition start in sectors) of a block device node like /dev/vda4.
func sysParent(sys, dev string) (string, uint64, bool) {
	name := filepath.Base(dev)
	p, err := filepath.EvalSymlinks(filepath.Join(sys, "class", "block", name))
	if err != nil {
		return "", 0, false
	}
	b, err := os.ReadFile(filepath.Join(p, "start"))
	if err != nil {
		return "", 0, false
	}
	start, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if _, err := os.Stat(filepath.Join(p, "partition")); err != nil {
		return "", 0, false
	}
	return filepath.Base(filepath.Dir(p)), start, true
}

// DisksFromPlan is the inverse of HostDisksPlan (names, fs and roles only; used for fstab/README).
func DisksFromPlan(pl []HostDiskPlan) []HDDisk {
	var out []HDDisk
	for _, d := range pl {
		hd := HDDisk{Name: d.Name, ID: d.ID, Sectors: d.Sectors}
		for _, p := range d.Parts {
			hd.Parts = append(hd.Parts, HDPart{RawPart: RawPart{N: p.N, Start: p.Start, Size: p.Size}, Name: p.Name, FSType: p.FS, Role: p.Role, RO: p.RO})
		}
		out = append(out, hd)
	}
	return out
}

// Markers of the fstab block omloader manages in the guest.
const (
	HostDisksBegin = "# BEGIN omloader host-disks"
	HostDisksEnd   = "# END omloader host-disks"
)

// ReplaceFstabBlock removes the managed block from fstab and appends lines (none = just remove).
func ReplaceFstabBlock(fstab string, lines []string) string {
	var keep []string
	skip := false
	for _, l := range strings.Split(fstab, "\n") {
		switch {
		case strings.TrimSpace(l) == HostDisksBegin:
			skip = true
		case strings.TrimSpace(l) == HostDisksEnd:
			skip = false
		case !skip:
			keep = append(keep, l)
		}
	}
	out := strings.TrimRight(strings.Join(keep, "\n"), "\n")
	if out != "" {
		out += "\n"
	}
	if len(lines) > 0 {
		out += HostDisksBegin + "\n" + strings.Join(lines, "\n") + "\n" + HostDisksEnd + "\n"
	}
	return out
}

// ApplyHostDisks writes/refreshes (or removes, when off) the guest's fstab block and /mnt/disks/README.txt. root must be writable.
func ApplyHostDisks(root string, cfg config.HostDisks, pl []HostDiskPlan, out io.Writer) error {
	old, had := readGuest(root, "/etc/fstab")
	if !cfg.Enabled() || len(pl) == 0 {
		if had && strings.Contains(old, HostDisksBegin) {
			return guestWriteKeep(root, "/etc/fstab", ReplaceFstabBlock(old, nil), 0o644, -1)
		}
		return nil
	}
	disks := DisksFromPlan(pl)
	lines := FstabLines(cfg, disks)
	if err := guestWriteKeep(root, "/etc/fstab", ReplaceFstabBlock(old, lines), 0o644, -1); err != nil {
		return err
	}
	// the initramfs copy of this rule dies at switch_root: the real root's udev needs it too (/dev/mapper/oml-* links, ID_FS_*)
	if err := guestWrite(root, "/etc/udev/rules.d/99-zz-omloader-dm.rules", []byte(dmUdevRules), 0o644); err != nil {
		return err
	}
	if err := guestWrite(root, filepath.Join(cfg.Root(), "README.txt"), []byte(HostDisksReadme(cfg, disks)), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "OMLOADER: host disks: %d fstab entries under %s (mode %s)\n", len(lines), cfg.Root(), cfg.Mode)
	return nil
}

// bindHostDisks (containers): bind the loader's own mount of the data partition, and loader mounts of the other partitions
// (ro unless mode rw), into the new root at <mount_root>/<disk>/<name>.
func (o Options) bindHostDisks(r Runner, cfg config.HostDisks, warn io.Writer) {
	if !cfg.Enabled() {
		return
	}
	mi := o.MountInfoPath
	if mi == "" {
		mi = "/proc/self/mountinfo"
	}
	b, _ := os.ReadFile(mi)
	mounts := parseMountInfo(string(b))
	mm := MediumHostMnt("")
	src := DataMountSource(string(b), mm)
	disks, err := DiscoverHostDisks(r, cfg, DiscoverOpts{DataSource: src})
	if err != nil {
		fmt.Fprintf(warn, "warning: host disks: %v\n", err)
		return
	}
	var pl []HostDiskPlan = HostDisksPlan(disks)
	for _, d := range disks {
		for _, p := range d.Parts {
			if !MountableFS(p.FSType) {
				continue
			}
			rel := filepath.Join(cfg.Root(), d.Name, p.Name)
			dst, err := walkInRoot(o.NewRoot, rel, true)
			if err != nil {
				fmt.Fprintf(warn, "warning: host disks: %s: %v\n", rel, err)
				continue
			}
			from := ""
			if p.Role == "data" && mm != "" && dirExists(mm) {
				from = mm
			}
			for _, m := range mounts {
				if from == "" && m.Source == p.Dev && m.Root == "/" {
					from = m.Point
				}
			}
			if from == "" {
				from = filepath.Join("/run/omloader/hd", d.Name, p.Name)
				mo := "ro"
				if !p.RO {
					mo = "rw"
				}
				args := []string{"-o", mo}
				if p.FSType == "ntfs" {
					args = append([]string{"-t", "ntfs3"}, args...)
				}
				if err := runCmd(r, "mkdir", "-p", from); err != nil {
					fmt.Fprintf(warn, "warning: host disks: %v\n", err)
					continue
				}
				if err := runCmd(r, "mount", append(args, p.Dev, from)...); err != nil {
					fmt.Fprintf(warn, "warning: host disks: cannot mount %s (%v); skipped\n", p.Dev, err)
					continue
				}
			}
			if err := runCmd(r, "mount", "--bind", from, dst); err != nil {
				fmt.Fprintf(warn, "warning: host disks: bind %s: %v\n", rel, err)
				continue
			}
			if p.RO {
				runCmd(r, "mount", "-o", "remount,bind,ro", dst)
			}
		}
	}
	if err := ApplyHostDisksReadme(o.NewRoot, cfg, pl); err != nil {
		fmt.Fprintf(warn, "warning: host disks: %v\n", err)
	}
}

// ApplyHostDisksReadme writes only the README (containers have no fstab entries).
func ApplyHostDisksReadme(root string, cfg config.HostDisks, pl []HostDiskPlan) error {
	return guestWrite(root, filepath.Join(cfg.Root(), "README.txt"), []byte(HostDisksReadme(cfg, DisksFromPlan(pl))), 0o644)
}
