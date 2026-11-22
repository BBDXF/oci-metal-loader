// Package simplestreams parses the Simplestreams index/catalog format served by
// https://images.linuxcontainers.org (streams/v1/index.json + images.json) and selects LXD images.
package simplestreams

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// IndexPath is the well-known index location relative to the server base URL.
const IndexPath = "streams/v1/index.json"

type Index struct {
	Format string                `json:"format"`
	Index  map[string]IndexEntry `json:"index"`
}
type IndexEntry struct {
	Datatype string   `json:"datatype"`
	Path     string   `json:"path"`
	Format   string   `json:"format"`
	Products []string `json:"products"`
}

type Catalog struct {
	Products map[string]*Product `json:"products"`
}
type Product struct {
	Aliases      string              `json:"aliases"`
	Arch         string              `json:"arch"`
	OS           string              `json:"os"`
	Release      string              `json:"release"`
	ReleaseTitle string              `json:"release_title"`
	Variant      string              `json:"variant"`
	Versions     map[string]*Version `json:"versions"`
}
type Version struct {
	Items map[string]*Item `json:"items"`
}
type Item struct {
	Ftype  string `json:"ftype"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Path   string `json:"path"`
}

// ParseIndex decodes streams/v1/index.json.
func ParseIndex(r io.Reader) (*Index, error) {
	var ix Index
	if err := json.NewDecoder(r).Decode(&ix); err != nil {
		return nil, fmt.Errorf("simplestreams index: %w", err)
	}
	if len(ix.Index) == 0 {
		return nil, fmt.Errorf("simplestreams index: no entries")
	}
	return &ix, nil
}

// ImagePaths returns the paths of all "image-downloads" catalogs, sorted.
func (ix *Index) ImagePaths() []string {
	var out []string
	for _, e := range ix.Index {
		if e.Datatype == "image-downloads" && e.Path != "" {
			out = append(out, e.Path)
		}
	}
	sort.Strings(out)
	return out
}

// ParseCatalog decodes an images.json catalog.
func ParseCatalog(r io.Reader) (*Catalog, error) {
	var c Catalog
	if err := json.NewDecoder(r).Decode(&c); err != nil {
		return nil, fmt.Errorf("simplestreams catalog: %w", err)
	}
	return &c, nil
}

// Merge adds the products of o to c.
func (c *Catalog) Merge(o *Catalog) {
	if c.Products == nil {
		c.Products = map[string]*Product{}
	}
	for k, v := range o.Products {
		c.Products[k] = v
	}
}

// File is a downloadable artifact with its expected digest.
type File struct {
	Ftype  string
	Path   string // relative to the server base
	SHA256 string
	Size   int64
}

// Image is the latest version of a product usable as a container or VM.
type Image struct {
	Product, OS, Release, Arch, Variant, Version string
	VM                                           bool
	Meta                                         File // lxd.tar.xz (metadata.yaml)
	Root                                         File // container: squashfs; VM: qcow2 disk
}

// Kind is "container" or "vm".
func (i Image) Kind() string {
	if i.VM {
		return "vm"
	}
	return "container"
}

// Query filters products; empty fields match anything. Arch defaults to amd64 in the callers.
type Query struct {
	OS, Release, Variant, Arch string
	Kind                       string // "", "container", "vm"
}

func aliasMatch(p *Product, rel string) bool {
	if rel == "" || p.Release == rel {
		return true
	}
	for _, a := range strings.Split(p.Aliases, ",") {
		a = strings.TrimSpace(a)
		if a == rel || strings.HasSuffix(a, "/"+rel) && !strings.Contains(strings.TrimSuffix(a, "/"+rel), "/") {
			return true
		}
	}
	return false
}

// kinds classifies a version: container (lxd.tar.xz + squashfs) and/or vm (lxd.tar.xz + qcow2 disk).
func pick(v *Version) (meta, sq, disk *Item) {
	for _, it := range v.Items {
		switch it.Ftype {
		case "lxd.tar.xz", "incus.tar.xz":
			if meta == nil {
				meta = it
			}
		case "squashfs":
			sq = it
		case "disk-kvm.img", "disk1.img":
			disk = it
		}
	}
	return
}

func file(it *Item) File {
	return File{Ftype: it.Ftype, Path: it.Path, SHA256: it.SHA256, Size: it.Size}
}

// Find returns the latest version of every matching product, sorted by product key (one entry per kind).
func (c *Catalog) Find(q Query) []Image {
	var out []Image
	for key, p := range c.Products {
		if q.OS != "" && !strings.EqualFold(p.OS, q.OS) || q.Arch != "" && p.Arch != q.Arch ||
			q.Variant != "" && p.Variant != q.Variant || !aliasMatch(p, q.Release) {
			continue
		}
		var vers []string
		for v := range p.Versions {
			vers = append(vers, v)
		}
		sort.Strings(vers)
		for kind := 0; kind < 2; kind++ { // 0 container, 1 vm: latest version that provides this kind
			if q.Kind == "container" && kind == 1 || q.Kind == "vm" && kind == 0 {
				continue
			}
			for i := len(vers) - 1; i >= 0; i-- {
				meta, sq, disk := pick(p.Versions[vers[i]])
				root := sq
				if kind == 1 {
					root = disk
				}
				if meta == nil || root == nil {
					continue
				}
				out = append(out, Image{Product: key, OS: p.OS, Release: p.Release, Arch: p.Arch, Variant: p.Variant,
					Version: vers[i], VM: kind == 1, Meta: file(meta), Root: file(root)})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Product != out[j].Product {
			return out[i].Product < out[j].Product
		}
		return !out[i].VM && out[j].VM
	})
	return out
}

// Latest returns the single best match or an error describing what is missing.
func (c *Catalog) Latest(q Query) (*Image, error) {
	r := c.Find(q)
	if len(r) == 0 {
		return nil, fmt.Errorf("no image matches os=%q release=%q variant=%q arch=%q type=%q", q.OS, q.Release, q.Variant, q.Arch, q.Kind)
	}
	if len(r) > 1 {
		var names []string
		for _, i := range r {
			names = append(names, i.Product)
		}
		return nil, fmt.Errorf("ambiguous: %d images match (%s)", len(r), strings.Join(names, ", "))
	}
	return &r[0], nil
}
