// Package baseinfo reads /etc/omloader-release, the build-time description of the Debian live base (written by
// build/mk-iso-inner.sh; absent on dev builds and outside the live system).
package baseinfo

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// Path is where the live system carries the file.
const Path = "/etc/omloader-release"

// Info is the parsed KEY=VALUE file.
type Info map[string]string

// Read parses path (KEY=VALUE lines, # comments, optional quotes). A missing file returns (nil, err).
func Read(path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	in := Info{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		in[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return in, sc.Err()
}

// Line is the compact one-liner, "Debian 12.8 bookworm, kernel 6.1.115, built 2026-11-23" (unknown parts are left out).
func (in Info) Line() string {
	if len(in) == 0 {
		return ""
	}
	parts := []string{strings.TrimSpace("Debian " + in["DEBIAN_VERSION"] + " " + in["DEBIAN_CODENAME"])}
	if k := in["KERNEL_VERSION"]; k != "" {
		parts = append(parts, "kernel "+k)
	}
	if d := in["BUILD_DATE"]; d != "" {
		parts = append(parts, "built "+d)
	}
	return strings.Join(parts, ", ")
}

// Long returns the keys in a stable order as "KEY=value" lines (known keys first).
func (in Info) Long() []string {
	order := []string{"OMLOADER_VERSION", "BUILD_DATE", "GIT_COMMIT", "DEBIAN_VERSION", "DEBIAN_CODENAME", "KERNEL_VERSION",
		"KERNEL_PACKAGE", "KEXEC_TOOLS", "QEMU_UTILS", "BASE_IMAGE", "DEBIAN_MIRROR"}
	seen := map[string]bool{}
	var out []string
	for _, k := range order {
		if v, ok := in[k]; ok {
			out = append(out, k+"="+v)
			seen[k] = true
		}
	}
	var rest []string
	for k := range in {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		out = append(out, k+"="+in[k])
	}
	return out
}

// JSON is the indented JSON object of all keys.
func (in Info) JSON() ([]byte, error) { return json.MarshalIndent(map[string]string(in), "", "  ") }
