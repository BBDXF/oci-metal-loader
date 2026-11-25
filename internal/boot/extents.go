package boot

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// MaxStoreExtents bounds the dm-linear table built from a store file's physical extents.
const MaxStoreExtents = 4096

// Extent is one contiguous run of a file, in bytes (logical = offset in the file, physical = offset on the host fs device).
type Extent struct {
	Logical  uint64 `json:"l"`
	Physical uint64 `json:"p"`
	Length   uint64 `json:"n"`
	Flags    uint32 `json:"f,omitempty"`
}

const (
	fiemapLast       = 0x1
	fiemapDelalloc   = 0x4
	fiemapEncoded    = 0x8
	fiemapUnwritten  = 0x800
	fiemapNotAligned = 0x100
	fiemapInline     = 0x200
	fiemapShared     = 0x2000
)

// FileExtents queries FS_IOC_FIEMAP for path (with sync flag) and returns its extents plus the file size.
func FileExtents(path string) ([]Extent, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	const cnt = 512
	var out []Extent
	var start uint64
	for {
		buf := make([]byte, 32+cnt*56)
		binary.LittleEndian.PutUint64(buf[0:], start)
		binary.LittleEndian.PutUint64(buf[8:], ^uint64(0)>>1)
		binary.LittleEndian.PutUint32(buf[16:], 1) // FIEMAP_FLAG_SYNC
		binary.LittleEndian.PutUint32(buf[24:], cnt)
		const FS_IOC_FIEMAP = 0xC020660B
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), FS_IOC_FIEMAP, uintptr(unsafe.Pointer(&buf[0]))); e != 0 {
			return nil, 0, fmt.Errorf("FIEMAP %s: %v", path, e)
		}
		n := int(binary.LittleEndian.Uint32(buf[20:]))
		if n == 0 {
			break
		}
		last := false
		for i := 0; i < n; i++ {
			b := buf[32+i*56:]
			e := Extent{Logical: binary.LittleEndian.Uint64(b[0:]), Physical: binary.LittleEndian.Uint64(b[8:]),
				Length: binary.LittleEndian.Uint64(b[16:]), Flags: binary.LittleEndian.Uint32(b[40:])}
			out = append(out, e)
			start = e.Logical + e.Length
			if e.Flags&fiemapLast != 0 {
				last = true
			}
		}
		if last || len(out) > MaxStoreExtents+cnt {
			break
		}
	}
	return out, st.Size(), nil
}

// ValidateExtents checks the list is a complete, fully written mapping of size bytes, merges adjacent runs,
// and returns the result in 512-byte sector units (Logical/Physical/Length are bytes on input and output).
func ValidateExtents(ex []Extent, size int64) ([]Extent, error) {
	if size <= 0 || size%512 != 0 {
		return nil, fmt.Errorf("store file size %d is not a positive multiple of 512", size)
	}
	var out []Extent
	var pos uint64
	for _, e := range ex {
		if e.Flags&(fiemapUnwritten|0x2|fiemapDelalloc|fiemapEncoded|fiemapInline|fiemapShared|fiemapNotAligned) != 0 {
			return nil, fmt.Errorf("extent at %d has unsupported flags %#x (unwritten/delalloc/encoded/inline/shared)", e.Logical, e.Flags)
		}
		if e.Logical != pos {
			return nil, fmt.Errorf("store file is sparse or unordered at offset %d (extent starts at %d)", pos, e.Logical)
		}
		if e.Physical%512 != 0 || e.Length%512 != 0 && e.Logical+e.Length < uint64(size) {
			return nil, fmt.Errorf("extent at %d not sector aligned", e.Logical)
		}
		pos += e.Length
		if n := len(out); n > 0 && out[n-1].Physical+out[n-1].Length == e.Physical {
			out[n-1].Length += e.Length
		} else {
			out = append(out, Extent{Logical: e.Logical, Physical: e.Physical, Length: e.Length})
		}
	}
	if pos < uint64(size) {
		return nil, fmt.Errorf("store file is sparse: only %d of %d bytes are allocated", pos, size)
	}
	// trim to whole sectors of size
	if pos > uint64(size) {
		out[len(out)-1].Length -= pos - uint64(size)
	}
	if len(out) > MaxStoreExtents {
		return nil, fmt.Errorf("store file has %d extents (max %d): too fragmented", len(out), MaxStoreExtents)
	}
	return out, nil
}

// LinearTable renders a dm-linear table for dev from validated extents.
func LinearTable(ex []Extent, dev string) string {
	s := ""
	for _, e := range ex {
		s += fmt.Sprintf("%d %d linear %s %d\n", e.Logical/512, e.Length/512, dev, e.Physical/512)
	}
	return s
}
