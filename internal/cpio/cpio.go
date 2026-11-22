// Package cpio writes "newc" (070701) archives, optionally gzip'd, for use as Linux initramfs segments.
package cpio

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"path"
	"strings"
)

// Writer builds a newc archive in memory.
type Writer struct {
	buf  bytes.Buffer
	ino  uint32
	dirs map[string]bool
}

// New returns an empty archive writer.
func New() *Writer { return &Writer{dirs: map[string]bool{}} }

func (w *Writer) pad() {
	for w.buf.Len()%4 != 0 {
		w.buf.WriteByte(0)
	}
}

func (w *Writer) entry(name string, mode uint32, data []byte) {
	w.ino++
	nlink := 1
	if mode&0o170000 == 0o040000 {
		nlink = 2
	}
	fmt.Fprintf(&w.buf, "070701%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X",
		w.ino, mode, 0, 0, nlink, 0, len(data), 0, 0, 0, 0, len(name)+1, 0)
	w.buf.WriteString(name)
	w.buf.WriteByte(0)
	w.pad()
	w.buf.Write(data)
	w.pad()
}

func clean(name string) string { return strings.TrimPrefix(path.Clean("/"+name), "/") }

// Dir adds a directory and all missing parents.
func (w *Writer) Dir(name string) {
	name = clean(name)
	if name == "" || w.dirs[name] {
		return
	}
	w.Dir(path.Dir(name))
	w.dirs[name] = true
	w.entry(name, 0o040755, nil)
}

// File adds a regular file (perm e.g. 0o755), creating parent dirs.
func (w *Writer) File(name string, perm uint32, data []byte) {
	name = clean(name)
	if d := path.Dir(name); d != "." {
		w.Dir(d)
	}
	w.entry(name, 0o100000|perm&0o7777, data)
}

// Symlink adds a symbolic link.
func (w *Writer) Symlink(name, target string) {
	name = clean(name)
	if d := path.Dir(name); d != "." {
		w.Dir(d)
	}
	w.entry(name, 0o120777, []byte(target))
}

// Bytes finishes the archive (trailer) and returns it uncompressed.
func (w *Writer) Bytes() []byte {
	w.entry("TRAILER!!!", 0, nil)
	return w.buf.Bytes()
}

// Gzip finishes and returns the archive gzip-compressed.
func (w *Writer) Gzip() ([]byte, error) {
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(w.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
