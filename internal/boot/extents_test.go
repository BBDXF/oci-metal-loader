package boot

import "testing"

func TestValidateExtents(t *testing.T) {
	ok := []Extent{{0, 4096, 8192, 0}, {8192, 12288, 4096, 0}, {12288, 1 << 20, 4096, fiemapLast}}
	v, err := ValidateExtents(ok, 16384)
	if err != nil || len(v) != 2 || v[0].Length != 12288 {
		t.Fatalf("%v %v", v, err)
	}
	if got := LinearTable(v, "/dev/sda1"); got != "0 24 linear /dev/sda1 8\n24 8 linear /dev/sda1 2048\n" {
		t.Fatalf("%q", got)
	}
	for name, ex := range map[string][]Extent{
		"unwritten": {{0, 4096, 16384, fiemapUnwritten}},
		"sparse":    {{0, 4096, 4096, 0}, {8192, 8192, 8192, 0}},
		"short":     {{0, 4096, 8192, 0}},
	} {
		if _, err := ValidateExtents(ex, 16384); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	var many []Extent
	for i := 0; i < MaxStoreExtents+1; i++ {
		many = append(many, Extent{uint64(i) * 512, uint64(i) * 1024, 512, 0})
	}
	if _, err := ValidateExtents(many, int64(len(many))*512); err == nil {
		t.Error("fragmented accepted")
	}
}
