// Package imgsearch is the single keyword matcher for catalog images (TUI Pull screen and `images list --search`).
package imgsearch

import (
	"sort"
	"strings"
)

// Item is the searchable description of one image.
type Item struct {
	OS, Release, Variant, Arch, Kind string // Kind: container | vm
}

func (i Item) fields() []string {
	os := strings.ToLower(i.OS)
	rel := strings.ToLower(i.Release)
	v := strings.ToLower(i.Variant)
	full := "lxc:" + os + "/" + rel + "/" + v
	return []string{os, rel, v, strings.ToLower(i.Arch), strings.ToLower(i.Kind), full}
}

// Terms splits a query on whitespace and lowercases it.
func Terms(q string) []string { return strings.Fields(strings.ToLower(q)) }

// Score reports whether every term is a substring of some field; lower scores rank first
// (0 exact field match, 1 prefix, 2 substring; summed over terms).
func Score(terms []string, it Item) (int, bool) {
	fs := it.fields()
	total := 0
	for _, t := range terms {
		best := -1
		for _, f := range fs {
			s := -1
			switch {
			case f == t:
				s = 0
			case strings.HasPrefix(f, t):
				s = 1
			case strings.Contains(f, t):
				s = 2
			}
			if s >= 0 && (best < 0 || s < best) {
				best = s
			}
		}
		if best < 0 {
			return 0, false
		}
		total += best
	}
	return total, true
}

// Match reports whether it matches the query (empty query matches everything).
func Match(query string, it Item) bool {
	_, ok := Score(Terms(query), it)
	return ok
}

// Filter returns the indexes of matching items, best matches first (stable for equal scores).
func Filter(query string, n int, at func(int) Item) []int {
	terms := Terms(query)
	type r struct{ idx, score int }
	var rs []r
	for i := 0; i < n; i++ {
		if s, ok := Score(terms, at(i)); ok {
			rs = append(rs, r{i, s})
		}
	}
	sort.SliceStable(rs, func(a, b int) bool { return rs[a].score < rs[b].score })
	out := make([]int, len(rs))
	for i, x := range rs {
		out[i] = x.idx
	}
	return out
}
