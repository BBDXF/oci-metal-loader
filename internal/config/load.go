package config

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"omloader/configs"

	"gopkg.in/yaml.v3"
)

// Layer names used in source tracking.
const (
	LayerDefaults = "defaults"
	LayerGlobal   = "global"
	LayerImage    = "image"
)

// Resolved is the merged result of all layers.
type Resolved struct {
	Config  Config
	Image   *Image            // nil when no image.yaml was given
	Sources map[string]string // leaf path -> layer name ("image." prefix for image fields)
	Lines   map[string]string // leaf path -> "file:line" of the winning value
	merged  map[string]any
}

type layer struct {
	name  string
	file  string
	data  map[string]any
	lines map[string]int
}

func parseLayer(name, file string, raw []byte, strict any) (*layer, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(strict); err != nil && err.Error() != "EOF" {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	l := &layer{name: name, file: file, data: map[string]any{}, lines: map[string]int{}}
	if len(n.Content) > 0 {
		if m, ok := nodeToAny(n.Content[0], "", l.lines).(map[string]any); ok {
			l.data = m
		} else {
			return nil, fmt.Errorf("%s: top level must be a mapping", file)
		}
	}
	return l, nil
}

func nodeToAny(n *yaml.Node, path string, lines map[string]int) any {
	if n.Kind == yaml.MappingNode {
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			p := join(path, k)
			lines[p] = n.Content[i].Line
			m[k] = nodeToAny(n.Content[i+1], p, lines)
		}
		return m
	}
	var v any
	_ = n.Decode(&v)
	return v
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}

// merge deep-merges src into dst, recording the source of every leaf in src.
func (r *Resolved) merge(dst map[string]any, l *layer, src map[string]any, path, prefix string) {
	for k, v := range src {
		p := join(path, k)
		if sm, ok := v.(map[string]any); ok {
			dm, ok := dst[k].(map[string]any)
			if !ok {
				dm = map[string]any{}
				dst[k] = dm
			}
			r.merge(dm, l, sm, p, prefix)
			continue
		}
		dst[k] = v
		r.Sources[prefix+p] = l.name
		r.Lines[prefix+p] = fmt.Sprintf("%s:%d", l.file, l.lines[p])
	}
}

func decodeStrict(m map[string]any, out any) error {
	b, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	return dec.Decode(out)
}

// Load merges defaults < global (optional) < image (optional) and validates.
func Load(globalPath, imagePath string) (*Resolved, error) {
	r := &Resolved{Sources: map[string]string{}, Lines: map[string]string{}}
	def, err := parseLayer(LayerDefaults, "defaults.yaml", configs.Defaults, &Config{})
	if err != nil {
		return nil, err
	}
	r.merged = map[string]any{}
	r.merge(r.merged, def, def.data, "", "")
	if globalPath != "" {
		raw, err := os.ReadFile(globalPath)
		if err != nil {
			return nil, err
		}
		g, err := parseLayer(LayerGlobal, globalPath, raw, &Config{})
		if err != nil {
			return nil, err
		}
		r.merge(r.merged, g, g.data, "", "")
	}
	if err := decodeStrict(r.merged, &r.Config); err != nil {
		return nil, err
	}
	if imagePath != "" {
		raw, err := os.ReadFile(imagePath)
		if err != nil {
			return nil, err
		}
		il, err := parseLayer(LayerImage, imagePath, raw, &Image{})
		if err != nil {
			return nil, err
		}
		// overlapping section: snapshot.max_chain_depth seeds snapshots.max_chain_depth
		seed := map[string]any{"snapshots": map[string]any{"max_chain_depth": r.Config.Snapshot.MaxChainDepth}}
		imap := map[string]any{}
		r.Sources["image.snapshots.max_chain_depth"] = r.Sources["snapshot.max_chain_depth"]
		r.Lines["image.snapshots.max_chain_depth"] = r.Lines["snapshot.max_chain_depth"]
		imap["snapshots"] = seed["snapshots"]
		r.merge(imap, il, il.data, "", "image.")
		r.merged["image"] = imap
		r.Image = &Image{}
		if err := decodeStrict(imap, r.Image); err != nil {
			return nil, err
		}
	}
	if errs := r.Validate(); len(errs) > 0 {
		return r, errs
	}
	return r, nil
}

// Flat returns sorted "path: value  # layer (file:line)" lines for --resolved.
func (r *Resolved) Flat() []string {
	var out []string
	var walk func(v any, path string)
	walk = func(v any, path string) {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			for k, c := range m {
				walk(c, join(path, k))
			}
			return
		}
		src := r.Sources[path]
		if src == "" {
			src = "?"
		}
		out = append(out, fmt.Sprintf("%s: %v  # %s (%s)", path, fmtVal(v), src, r.Lines[path]))
	}
	walk(r.merged, "")
	sort.Strings(out)
	return out
}

func fmtVal(v any) string {
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\n", " "))
}
