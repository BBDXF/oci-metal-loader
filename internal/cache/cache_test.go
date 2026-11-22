package cache

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestGC(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for i, s := range []string{a, b} {
		os.MkdirAll(c.Part(s)[:len(c.Part(s))-70], 0o755)
		os.WriteFile(c.Part(s), make([]byte, 100), 0o644)
		if err := c.Commit(c.Part(s), s); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(time.Duration(i-5) * time.Hour)
		os.Chtimes(c.Blob(s), old, old)
	}
	if !c.Has(b) || c.Has("zz") {
		t.Fatal("has")
	}
	rm, _ := c.GC(150)
	if len(rm) != 1 || rm[0].Name != a {
		t.Fatalf("%+v", rm)
	}
	if c.Has(a) || !c.Has(b) {
		t.Fatal("lru")
	}
	c.PutMeta("k", []byte("v"))
	if v, age, err := c.GetMeta("k"); err != nil || string(v) != "v" || age > time.Minute {
		t.Fatal(err)
	}
}
