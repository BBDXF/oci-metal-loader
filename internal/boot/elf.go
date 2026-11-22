package boot

import (
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LibSearchDirs are where shared libraries are looked up on the loader.
var LibSearchDirs = []string{
	"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu", "/lib64", "/usr/lib64", "/lib", "/usr/lib",
}

// ELFDeps returns the dynamic interpreter path ("" for static binaries) and a map
// soname -> host path of every transitively needed shared library.
func ELFDeps(bin string) (interp string, libs map[string]string, err error) {
	libs = map[string]string{}
	f, err := elf.Open(bin)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", bin, err)
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			b := make([]byte, p.Filesz)
			if _, err := io.ReadFull(p.Open(), b); err != nil {
				f.Close()
				return "", nil, err
			}
			interp = string(b[:len(b)-1])
		}
	}
	f.Close()
	var walk func(path string) error
	walk = func(path string) error {
		ef, err := elf.Open(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		needed, err := ef.ImportedLibraries()
		ef.Close()
		if err != nil {
			return err
		}
		for _, n := range needed {
			if _, ok := libs[n]; ok {
				continue
			}
			var found string
			for _, d := range LibSearchDirs {
				c := filepath.Join(d, n)
				if _, err := os.Stat(c); err == nil {
					found = c
					break
				}
			}
			if found == "" {
				return fmt.Errorf("library %s needed by %s not found", n, path)
			}
			libs[n] = found
			if err := walk(found); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(bin); err != nil {
		return "", nil, err
	}
	return interp, libs, nil
}
