// Package imgimport converts OCI / docker / LXD images to omloader squashfs images (rootless).
package imgimport

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

// LayerOpener returns an (optionally compressed) tar stream for one layer.
type LayerOpener func() (io.ReadCloser, error)

type tarStream struct {
	io.Reader
	closers []io.Closer
}

func (t *tarStream) Close() error {
	var first error
	for _, c := range t.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// FileOpener opens a layer file, transparently un-gzipping it. zstd/xz are rejected with a clear error.
func FileOpener(p string) LayerOpener {
	return func() (io.ReadCloser, error) {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		br := bufio.NewReaderSize(f, 1<<16)
		magic, _ := br.Peek(6)
		ts := &tarStream{closers: []io.Closer{f}}
		switch {
		case len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
			zr, err := gzip.NewReader(br)
			if err != nil {
				f.Close()
				return nil, err
			}
			ts.Reader = zr
			ts.closers = append([]io.Closer{zr}, ts.closers...)
		case len(magic) >= 4 && magic[0] == 0x28 && magic[1] == 0xb5 && magic[2] == 0x2f && magic[3] == 0xfd:
			f.Close()
			return nil, fmt.Errorf("%s: zstd-compressed layers are not supported (re-export with gzip/uncompressed layers)", p)
		case len(magic) >= 6 && string(magic[:6]) == "\xfd7zXZ\x00":
			f.Close()
			return nil, fmt.Errorf("%s: xz-compressed layers are not supported", p)
		default:
			ts.Reader = br
		}
		return ts, nil
	}
}

type entry struct {
	hdr   tar.Header
	layer int
	ord   int
}

func cleanName(n string) string {
	n = path.Clean("/" + n)
	return strings.TrimPrefix(n, "/")
}

// MergeLayers applies layers in order (OCI semantics: whiteouts, opaque dirs) and writes one merged tar to w.
// Layers are read twice (index pass, emit pass).
func MergeLayers(layers []LayerOpener, w io.Writer, warn io.Writer) error {
	if warn == nil {
		warn = io.Discard
	}
	final := map[string]*entry{}
	for li, open := range layers {
		rc, err := open()
		if err != nil {
			return fmt.Errorf("layer %d: %w", li, err)
		}
		tr := tar.NewReader(rc)
		var ents []*entry
		whiteout := map[string]bool{} // exact paths removed from lower layers (incl. subtree)
		opaque := map[string]bool{}   // dirs whose lower children are removed
		for ord := 0; ; ord++ {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return fmt.Errorf("layer %d: %w", li, err)
			}
			name := cleanName(h.Name)
			if name == "" {
				continue // root dir entry
			}
			dir, base := path.Split(name)
			dir = strings.TrimSuffix(dir, "/")
			if base == ".wh..wh..opq" {
				opaque[dir] = true
				continue
			}
			if strings.HasPrefix(base, ".wh.") {
				whiteout[path.Join(dir, strings.TrimPrefix(base, ".wh."))] = true
				continue
			}
			switch h.Typeflag {
			case tar.TypeReg, tar.TypeRegA, tar.TypeDir, tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			default:
				fmt.Fprintf(warn, "warning: layer %d: skipping %s (tar type %q)\n", li, name, h.Typeflag)
				continue
			}
			hh := *h
			hh.Name = name
			if h.Typeflag == tar.TypeLink {
				hh.Linkname = cleanName(h.Linkname)
			}
			ents = append(ents, &entry{hdr: hh, layer: li, ord: ord})
		}
		rc.Close()
		// a non-dir entry replacing a path also removes that path's lower subtree
		replaced := map[string]bool{}
		for _, e := range ents {
			if e.hdr.Typeflag != tar.TypeDir {
				replaced[e.hdr.Name] = true
			}
		}
		if len(whiteout)+len(opaque)+len(replaced) > 0 && len(final) > 0 {
			for k := range final {
				if whiteout[k] {
					delete(final, k)
					continue
				}
				// strict ancestors
				for a := path.Dir(k); a != "." && a != "/"; a = path.Dir(a) {
					if whiteout[a] || opaque[a] || replaced[a] {
						delete(final, k)
						break
					}
				}
			}
		}
		for _, e := range ents {
			final[e.hdr.Name] = e
		}
	}

	// ensure every ancestor exists as a directory
	dirs := map[string]*tar.Header{}
	for k, e := range final {
		if e.hdr.Typeflag == tar.TypeDir {
			h := e.hdr
			dirs[k] = &h
		}
	}
	for k := range final {
		for a := path.Dir(k); a != "." && a != "/"; a = path.Dir(a) {
			if _, ok := dirs[a]; ok {
				break
			}
			if e, ok := final[a]; ok && e.hdr.Typeflag != tar.TypeDir {
				break // parent is a non-dir (inconsistent layer); leave it
			}
			dirs[a] = &tar.Header{Typeflag: tar.TypeDir, Name: a, Mode: 0o755, Format: tar.FormatPAX}
		}
	}
	tw := tar.NewWriter(w)
	var dn []string
	for k := range dirs {
		dn = append(dn, k)
	}
	sort.Strings(dn)
	for _, k := range dn {
		h := *dirs[k]
		h.Name = k + "/"
		if err := tw.WriteHeader(&h); err != nil {
			return err
		}
	}
	// phase B: non-dir, non-hardlink entries, streamed layer by layer
	for li, open := range layers {
		rc, err := open()
		if err != nil {
			return err
		}
		tr := tar.NewReader(rc)
		for ord := 0; ; ord++ {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return err
			}
			e, ok := final[cleanName(h.Name)]
			if !ok || e.layer != li || e.ord != ord || e.hdr.Typeflag == tar.TypeDir || e.hdr.Typeflag == tar.TypeLink {
				continue
			}
			hh := e.hdr
			if err := tw.WriteHeader(&hh); err != nil {
				rc.Close()
				return err
			}
			if hh.Typeflag == tar.TypeReg || hh.Typeflag == tar.TypeRegA {
				if _, err := io.Copy(tw, tr); err != nil {
					rc.Close()
					return err
				}
			}
		}
		rc.Close()
	}
	// phase C: hardlinks whose target survived
	var ln []string
	for k, e := range final {
		if e.hdr.Typeflag == tar.TypeLink {
			ln = append(ln, k)
		}
	}
	sort.Strings(ln)
	for _, k := range ln {
		hh := final[k].hdr
		t, ok := final[hh.Linkname]
		if !ok || t.hdr.Typeflag == tar.TypeDir {
			fmt.Fprintf(warn, "warning: dropping hardlink %s -> %s (target missing)\n", k, hh.Linkname)
			continue
		}
		if err := tw.WriteHeader(&hh); err != nil {
			return err
		}
	}
	return tw.Close()
}
