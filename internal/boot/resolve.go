package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxSymlinks = 40

func components(p string) []string {
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c != "" && c != "." {
			out = append(out, c)
		}
	}
	return out
}

// ResolveInRoot resolves p (absolute within the image) to a host path under root,
// following symlinks as if root were "/": absolute link targets restart at root,
// ".." never climbs above root. Returns an error on loops (>40 links) or missing parts.
func ResolveInRoot(root, p string) (string, error) {
	var stack []string
	queue := components(p)
	links := 0
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == ".." {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		cand := filepath.Join(append([]string{root}, append(append([]string{}, stack...), c)...)...)
		fi, err := os.Lstat(cand)
		if err != nil {
			return "", fmt.Errorf("resolve %s in image: %w", p, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			links++
			if links > maxSymlinks {
				return "", fmt.Errorf("resolve %s in image: too many symlinks", p)
			}
			t, err := os.Readlink(cand)
			if err != nil {
				return "", err
			}
			if strings.HasPrefix(t, "/") {
				stack = nil
			}
			queue = append(components(t), queue...)
			continue
		}
		stack = append(stack, c)
	}
	return filepath.Join(append([]string{root}, stack...)...), nil
}
