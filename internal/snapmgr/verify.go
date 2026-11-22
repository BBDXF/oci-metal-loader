package snapmgr

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dm persistent snapshot disk header (drivers/md/dm-snap-persistent.c): le32 magic, valid, version, chunk_size.
const (
	CowMagic   = 0x70416e53 // "SnAp"
	CowVersion = 1
)

// CowState is the result of inspecting a COW header.
type CowState int

const (
	CowFresh CowState = iota // all zero: not yet initialised (dm creates it on first use)
	CowValid
)

// ParseCowHeader validates the first bytes of a COW file.
func ParseCowHeader(b []byte) (CowState, error) {
	if len(b) < 16 {
		return 0, fmt.Errorf("header too short (%d bytes)", len(b))
	}
	zero := true
	for _, x := range b[:16] {
		if x != 0 {
			zero = false
		}
	}
	if zero {
		return CowFresh, nil
	}
	le := binary.LittleEndian
	magic, valid, version, chunk := le.Uint32(b[0:]), le.Uint32(b[4:]), le.Uint32(b[8:]), le.Uint32(b[12:])
	switch {
	case magic != CowMagic:
		return 0, fmt.Errorf("bad magic 0x%08x (want SnAp 0x%08x)", magic, CowMagic)
	case version != CowVersion:
		return 0, fmt.Errorf("unsupported snapshot version %d", version)
	case valid != 1:
		return 0, fmt.Errorf("snapshot marked invalid (valid=%d, overflowed or corrupted)", valid)
	case chunk == 0 || chunk&(chunk-1) != 0:
		return 0, fmt.Errorf("bad chunk size %d", chunk)
	}
	return CowValid, nil
}

// Problem is one verify finding.
type Problem struct{ Node, Msg string }

func (p Problem) String() string {
	if p.Node == "" {
		return p.Msg
	}
	return p.Node + ": " + p.Msg
}

// VerifyOptions tunes Verify.
type VerifyOptions struct {
	Deep bool // hash the base image (slow)
}

// Verify checks tree.json against the on-disk layers and the base checksum. Read-only.
func (s *Store) Verify(o VerifyOptions) []Problem {
	var ps []Problem
	add := func(n, f string, a ...any) { ps = append(ps, Problem{n, fmt.Sprintf(f, a...)}) }
	t := s.Tree
	for n, nd := range t.Nodes {
		if nd.Parent != "" {
			if _, ok := t.Nodes[nd.Parent]; !ok {
				add(n, "missing parent %q", nd.Parent)
			}
		}
	}
	if c := t.FindCycle(); c != "" {
		add(c, "cycle in snapshot tree")
	}
	if t.Current != "" {
		if _, ok := t.Nodes[t.Current]; !ok {
			add("", "current %q does not exist", t.Current)
		}
	}
	if s.BaseSHA != "" && t.BaseSHA256 != "" && !strings.EqualFold(s.BaseSHA, t.BaseSHA256) {
		add("", "tree.json base_sha256 %s != image sha256 %s (tree belongs to a different base)", t.BaseSHA256, s.BaseSHA)
	}
	if o.Deep && s.BaseFile != "" && s.BaseSHA != "" {
		if got, err := fileSHA(s.BaseFile); err != nil {
			add("", "base: %v", err)
		} else if !strings.EqualFold(got, s.BaseSHA) {
			add("", "base file sha256 %s != image sha256 %s", got, s.BaseSHA)
		}
	}
	if s.Mode == ModeNone {
		return ps
	}
	known := map[string]bool{"tree.json": true, "tree.json.tmp": true}
	for _, n := range t.Names() {
		switch s.Mode {
		case ModeDM:
			f := s.cowPath(n)
			known[filepath.Base(f)] = true
			st, err := os.Stat(f)
			if err != nil {
				add(n, "missing COW file %s", f)
				continue
			}
			if s.BaseSize > 0 && st.Size() < s.BaseSize {
				add(n, "COW file is smaller (%d) than the base image (%d)", st.Size(), s.BaseSize)
			}
			if h, err := readHeader(f); err != nil {
				add(n, "cannot read COW header: %v", err)
			} else if _, err := ParseCowHeader(h); err != nil {
				add(n, "invalid COW header: %v", err)
			}
		case ModeOverlay:
			known[n] = true
			for _, d := range []string{"upper", "work"} {
				p := filepath.Join(s.dir(), n, d)
				if st, err := os.Stat(p); err != nil || !st.IsDir() {
					add(n, "missing %s directory %s", d, p)
				}
			}
		}
	}
	ents, _ := os.ReadDir(s.dir())
	for _, e := range ents {
		if !known[e.Name()] {
			add("", "orphan layer %s (not in tree.json)", filepath.Join(s.dir(), e.Name()))
		}
	}
	return ps
}

// VerifyLite is the boot-time check: base checksum consistency and existence of the (frozen) ancestor layers.
func (s *Store) VerifyLite(ancestors []string) error {
	if s.BaseSHA != "" && s.Tree.BaseSHA256 != "" && !strings.EqualFold(s.BaseSHA, s.Tree.BaseSHA256) {
		return fmt.Errorf("snapshot tree %s was created for base %s but the image has sha256 %s; run `omloader snapshot verify %s`",
			s.TreePath, s.Tree.BaseSHA256, s.BaseSHA, s.Image)
	}
	for _, n := range ancestors {
		if !s.LayerExists(n) {
			return fmt.Errorf("layer of snapshot %q is missing on disk (%s); run `omloader snapshot verify %s`", n, s.layerDesc(n), s.Image)
		}
	}
	return nil
}

func readHeader(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, 16)
	n, err := f.Read(b)
	if n < 16 && err != nil {
		return nil, err
	}
	return b[:n], nil
}
