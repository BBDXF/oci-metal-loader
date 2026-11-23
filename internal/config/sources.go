package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// LayerSources is the layer name of sources.yaml files in --resolved output.
const LayerSources = "sources"

// Mirror selectors with a fixed meaning (everything else must be a name from sources.<n>.servers).
const (
	MirrorOfficial = "official"
	MirrorAuto     = "auto"
)

// SourcesFiles lists the sources.yaml files that exist, lowest priority first (the embedded copy is always below them):
// the copy installed in the live system, the ISO's own medium, the OMLOADER partition, then the Ventoy data partition
// (omloader/sources.yaml, same places as boot.yaml), $OML_SOURCES, and finally <dir of -c>/sources.yaml. Later files win.
func SourcesFiles(globalPath string) []string {
	cand := []string{"/etc/omloader/sources.yaml"}
	if m := mediumHostMnt(); m != "" { // the partition that holds the ISO file (Ventoy/iso-scan)
		cand = append(cand, m+"/omloader/sources.yaml")
	}
	cand = append(cand, "/mnt/snap/omloader/sources.yaml", "/mnt/images/omloader/sources.yaml", "/mnt/images/sources.yaml")
	if e := os.Getenv("OML_SOURCES"); e != "" {
		cand = append(cand, e)
	}
	if globalPath != "" {
		d := filepath.Dir(globalPath)
		cand = append(cand, filepath.Join(d, "sources.yaml"), filepath.Join(d, "omloader", "sources.yaml"))
	}
	var out []string
	for _, c := range cand {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			out = append(out, c)
		}
	}
	return out
}

func mediumHostMnt() string {
	b, err := os.ReadFile("/run/omloader/medium.env")
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

// IsEnabled is false when the source is switched off with `enabled: false`.
func (s Source) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// IsEnabled reports whether the mirror is usable (`enabled: false` removes a built-in one).
func (m Mirror) IsEnabled() bool { return m.Enabled == nil || *m.Enabled }

// Base is one candidate server of a source in try order.
type Base struct {
	Name    string // official | extra#N | server id
	Title   string
	URL     string
	Rewrite []Rewrite // relative-path rewrites for this server
}

// Rel applies the server's relative-path rewrites.
func (b Base) Rel(rel string) string {
	for _, r := range b.Rewrite {
		if strings.HasPrefix(rel, r.From) {
			return r.To + strings.TrimPrefix(rel, r.From)
		}
	}
	return rel
}

func (m Mirror) fullURL() string {
	u := strings.TrimRight(m.URL, "/")
	if m.Path != "" {
		u += "/" + strings.Trim(m.Path, "/")
	}
	return u
}

// ServerNames returns the enabled server ids of a source, sorted.
func (c *Config) ServerNames(name string) []string {
	var out []string
	for id, m := range c.Sources[name].Servers {
		if m.IsEnabled() {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// SourceNames returns the enabled source names, sorted.
func (c *Config) SourceNames() []string {
	var out []string
	for n, s := range c.Sources {
		if s.IsEnabled() {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// SourceMirrors returns the URLs of the fallback servers of a source: its legacy `mirrors:` list plus the enabled servers
// whose region equals mirror_policy.region_preset (so region_preset: cn adds the verified CN mirrors from sources.yaml).
func (c *Config) SourceMirrors(name string) []string {
	m := append([]string{}, c.Sources[name].Mirrors...)
	for _, b := range c.presetBases(name) {
		dup := false
		for _, e := range m {
			dup = dup || e == b.URL
		}
		if !dup {
			m = append(m, b.URL)
		}
	}
	return m
}

func (c *Config) presetBases(name string) []Base {
	var out []Base
	s := c.Sources[name]
	if c.MirrorPolicy.RegionPreset == "" || c.MirrorPolicy.RegionPreset == "global" {
		return nil
	}
	for _, id := range c.ServerNames(name) {
		if m := s.Servers[id]; m.Region == c.MirrorPolicy.RegionPreset {
			out = append(out, Base{Name: id, Title: m.Title, URL: m.fullURL(), Rewrite: m.Rewrite})
		}
	}
	return out
}

// SourceBases returns the servers to try, in order, for the selected mirror ("" = the source's `mirror` setting).
//
//	official     : the source url, then legacy mirrors:, then region_preset servers
//	<server id>  : that server first, then the same fallbacks (mirror_policy.strategy mirror-only: only that server)
//	auto         : the source url, legacy mirrors and every enabled server; the fetcher ranks them by measured latency
func (c *Config) SourceBases(name, mirror string) ([]Base, error) {
	s, ok := c.Sources[name]
	if !ok {
		return nil, fmt.Errorf("unknown source %q", name)
	}
	if mirror == "" {
		mirror = s.Mirror
	}
	if mirror == "" {
		mirror = MirrorOfficial
	}
	official := Base{Name: MirrorOfficial, Title: s.Title, URL: s.URL}
	var legacy []Base
	for i, m := range s.Mirrors {
		legacy = append(legacy, Base{Name: fmt.Sprintf("extra#%d", i+1), URL: m})
	}
	var out []Base
	switch mirror {
	case MirrorOfficial:
		out = append(append([]Base{official}, legacy...), c.presetBases(name)...)
		if c.MirrorPolicy.Strategy == "mirror-only" {
			out = out[1:]
		}
	case MirrorAuto:
		out = append([]Base{official}, legacy...)
		for _, id := range c.ServerNames(name) {
			m := s.Servers[id]
			out = append(out, Base{Name: id, Title: m.Title, URL: m.fullURL(), Rewrite: m.Rewrite})
		}
		if c.MirrorPolicy.Strategy == "mirror-only" {
			out = out[1:]
		}
	default:
		m, ok := s.Servers[mirror]
		if !ok || !m.IsEnabled() {
			return nil, fmt.Errorf("source %q has no mirror %q (available: %s, %s, %s)", name, mirror, MirrorOfficial, MirrorAuto, strings.Join(c.ServerNames(name), ", "))
		}
		sel := Base{Name: mirror, Title: m.Title, URL: m.fullURL(), Rewrite: m.Rewrite}
		if c.MirrorPolicy.Strategy == "mirror-only" {
			return []Base{sel}, nil
		}
		out = append([]Base{sel, official}, legacy...)
		for _, b := range c.presetBases(name) {
			if b.Name != mirror {
				out = append(out, b)
			}
		}
	}
	return out, nil
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

// validateSources checks sources/servers/default_source (messages name the YAML path).
func (c *Config) validateSources(add func(path, f string, a ...any)) {
	if c.DefaultSource != "" {
		if s, ok := c.Sources[c.DefaultSource]; !ok || !s.IsEnabled() {
			add("default_source", "unknown or disabled source %q", c.DefaultSource)
		}
	}
	for n, s := range c.Sources {
		p := "sources." + n
		if !idRe.MatchString(n) {
			add(p, "source name %q must match %s", n, idRe)
		}
		if s.Mirror != "" && s.Mirror != MirrorOfficial && s.Mirror != MirrorAuto {
			if m, ok := s.Servers[s.Mirror]; !ok || !m.IsEnabled() {
				add(p+".mirror", "%q is not official, auto or an enabled server of this source (servers: %s)", s.Mirror, strings.Join(c.ServerNames(n), ", "))
			}
		}
		for id, m := range s.Servers {
			q := p + ".servers." + id
			if !idRe.MatchString(id) {
				add(q, "server name %q must match %s", id, idRe)
			}
			if m.URL == "" {
				add(q+".url", "required")
			} else if s.Type != "oci-registry" && !isRemote(m.URL) {
				add(q+".url", "%q must be an http:// or https:// URL", m.URL)
			} else if s.Type == "oci-registry" && (strings.ContainsAny(m.URL, " ") || strings.HasPrefix(m.URL, "http:")) {
				add(q+".url", "%q must be a registry host or an https:// URL", m.URL)
			}
			for _, rw := range m.Rewrite {
				if rw.From == "" {
					add(q+".rewrite", "from is required")
				}
			}
		}
	}
}
