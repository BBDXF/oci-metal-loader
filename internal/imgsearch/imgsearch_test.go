package imgsearch

import (
	"reflect"
	"testing"
)

var items = []Item{
	{"Ubuntu", "noble", "cloud", "amd64", "vm"},
	{"archlinux", "current", "default", "amd64", "container"},
	{"debian", "12", "cloud", "amd64", "vm"},
	{"alpine", "3.20", "default", "amd64", "container"},
	{"arch", "x", "default", "amd64", "container"},
}

func TestMatch(t *testing.T) {
	for q, want := range map[string]int{
		"": 5, "arch": 2, "ARCH vm": 0, "archlinux container": 1, "ubuntu noble": 1,
		"debian 12 cloud": 1, "vm": 2, "lxc:debian/12": 1, "cloud vm": 2, "nomatch": 0, "default": 3,
	} {
		n := 0
		for _, it := range items {
			if Match(q, it) {
				n++
			}
		}
		if n != want {
			t.Errorf("%q: %d want %d", q, n, want)
		}
	}
}

func TestFilterRank(t *testing.T) {
	got := Filter("arch", len(items), func(i int) Item { return items[i] })
	if !reflect.DeepEqual(got, []int{4, 1}) { // exact os first, then prefix
		t.Fatalf("%v", got)
	}
}
