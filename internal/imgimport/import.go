package imgimport

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"omloader/internal/boot"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+-]*$`)

// Options for Import.
type Options struct {
	Kind   string // oci-dir | oci-archive | docker-archive | lxd
	Path   string
	Name   string
	Out    string // images root
	TmpDir string
	Runner boot.StdinRunner
	Warn   io.Writer
	Force  bool // overwrite an existing image dir
	// Autologin sets runtime.autologin (serial-getty autologin on ttyS0; for tests/debug).
	Autologin bool
	// Data declares extra data volumes: "name:/mount:backend[:size[:snapshot]]" (replaces an imported VOLUME with the same mount).
	Data []string
}

// ParseData parses --data specs.
func ParseData(specs []string) ([]map[string]any, error) {
	var out []map[string]any
	for _, sp := range specs {
		f := strings.Split(sp, ":")
		if len(f) < 3 || len(f) > 5 || f[0] == "" || !strings.HasPrefix(f[1], "/") || strings.Contains(f[1], "..") {
			return nil, fmt.Errorf("bad --data %q (want name:/mount:backend[:size[:snapshot]])", sp)
		}
		switch f[2] {
		case "dir", "ext4-file":
		default:
			return nil, fmt.Errorf("bad --data %q: backend must be dir or ext4-file", sp)
		}
		m := map[string]any{"name": f[0], "mount": f[1], "backend": f[2], "snapshot": false, "shared": false}
		if len(f) > 3 && f[3] != "" {
			m["size"] = f[3]
		}
		if len(f) > 4 {
			m["snapshot"] = f[4] == "true"
		}
		out = append(out, m)
	}
	return out, nil
}

func mergeData(cur []map[string]any, extra []map[string]any) []map[string]any {
	for _, e := range extra {
		repl := false
		for i, c := range cur {
			if c["mount"] == e["mount"] {
				cur[i], repl = e, true
			}
		}
		if !repl {
			cur = append(cur, e)
		}
	}
	return cur
}

// MksquashfsArgs are the flags for creating base.squashfs from a tar on stdin.
// uid/gid/xattrs come from the tar headers (no -all-root), so this runs rootless.
func MksquashfsArgs(out string) []string {
	return []string{"-", out, "-tar", "-noappend", "-quiet", "-no-progress", "-comp", "gzip"}
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Import converts the image to <out>/<name>/{base.squashfs,image.yaml}.
func Import(o Options) (string, error) {
	if !nameRe.MatchString(o.Name) {
		return "", fmt.Errorf("invalid --name %q", o.Name)
	}
	if o.Out == "" || o.Path == "" {
		return "", fmt.Errorf("--out and a source path are required")
	}
	if o.Warn == nil {
		o.Warn = io.Discard
	}
	dir := filepath.Join(o.Out, o.Name)
	if _, err := os.Stat(dir); err == nil && !o.Force {
		return "", fmt.Errorf("%s already exists (use --force to replace)", dir)
	}
	tmp := dir + ".importing"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	sq := filepath.Join(tmp, "base.squashfs")
	var yml map[string]any
	extra, err := ParseData(o.Data)
	if err != nil {
		return "", err
	}
	if o.Kind == "lxd" {
		yml, err = importLXD(o, sq)
	} else {
		yml, err = importOCI(o, sq)
	}
	if err != nil {
		return "", err
	}
	if len(extra) > 0 {
		cur, _ := yml["data"].([]map[string]any)
		yml["data"] = mergeData(cur, extra)
	}
	sum, err := sha256File(sq)
	if err != nil {
		return "", err
	}
	yml["sha256"] = sum
	b, err := yaml.Marshal(yml)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, "image.yaml"), b, 0o644); err != nil {
		return "", err
	}
	if o.Force {
		os.RemoveAll(dir)
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	ok = true
	return dir, nil
}

func baseYAML(o Options, typ string) map[string]any {
	return map[string]any{
		"name": o.Name, "type": typ, "format": "squashfs", "base": "base.squashfs",
		"snapshots": map[string]any{"mode": "overlayfs"},
	}
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]+`)

func importOCI(o Options, sq string) (map[string]any, error) {
	if o.Runner == nil {
		return nil, fmt.Errorf("no runner")
	}
	src, err := Open(o.Kind, o.Path, o.TmpDir)
	if err != nil {
		return nil, err
	}
	if src.Cleanup != nil {
		defer src.Cleanup()
	}
	if a := src.Config.Architecture; a != "" && a != "amd64" {
		return nil, fmt.Errorf("image architecture %q is not amd64", a)
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(MergeLayers(src.Layers, pw, o.Warn)) }()
	_, err = o.Runner.RunStdin(pr, "mksquashfs", MksquashfsArgs(sq)...)
	pr.CloseWithError(err)
	if err != nil {
		return nil, err
	}
	y := baseYAML(o, "oci")
	c := src.Config.Config
	rt := map[string]any{"hostname": o.Name, "network": "dhcp"}
	if o.Autologin {
		rt["autologin"] = true
	}
	if len(c.Entrypoint) > 0 {
		rt["entrypoint"] = c.Entrypoint
	}
	if len(c.Cmd) > 0 {
		rt["cmd"] = c.Cmd
	}
	if c.WorkingDir != "" {
		rt["workdir"] = c.WorkingDir
	}
	env := map[string]string{}
	for _, e := range c.Env {
		if k, v, ok := strings.Cut(e, "="); ok {
			env[k] = v
		}
	}
	if len(env) > 0 {
		rt["env"] = env
	}
	y["runtime"] = rt
	var vols []string
	for v := range c.Volumes {
		vols = append(vols, v)
	}
	sort.Strings(vols)
	var data []map[string]any
	for _, v := range vols {
		if !strings.HasPrefix(v, "/") || strings.Contains(v, "..") {
			continue
		}
		n := "vol-" + strings.Trim(nonAlnum.ReplaceAllString(v, "-"), "-")
		data = append(data, map[string]any{"name": n, "mount": v, "backend": "dir", "snapshot": false, "shared": false})
	}
	if len(data) > 0 {
		y["data"] = data
	}
	return y, nil
}

func importLXD(o Options, sq string) (map[string]any, error) {
	meta, root := o.Path, ""
	if st, err := os.Stat(o.Path); err == nil && st.IsDir() {
		for _, n := range []string{"lxd.tar.xz", "incus.tar.xz", "meta.tar.xz"} {
			if _, err := os.Stat(filepath.Join(o.Path, n)); err == nil {
				meta = filepath.Join(o.Path, n)
				break
			}
		}
		root = filepath.Join(o.Path, "rootfs.squashfs")
	} else {
		root = filepath.Join(filepath.Dir(o.Path), "rootfs.squashfs")
	}
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("lxd image needs rootfs.squashfs next to the metadata tarball: %w", err)
	}
	mb, err := readMetadata(o, meta)
	if err != nil {
		return nil, err
	}
	var md struct {
		Architecture string            `yaml:"architecture"`
		Properties   map[string]string `yaml:"properties"`
	}
	if err := yaml.Unmarshal(mb, &md); err != nil {
		return nil, fmt.Errorf("metadata.yaml: %w", err)
	}
	if a := md.Architecture; a != "" && a != "x86_64" && a != "amd64" {
		return nil, fmt.Errorf("lxd image architecture %q is not x86_64", a)
	}
	if err := copyFileTo(root, sq); err != nil {
		return nil, err
	}
	y := baseYAML(o, "lxd-container")
	rt := map[string]any{"hostname": o.Name, "network": "dhcp"}
	if o.Autologin {
		rt["autologin"] = true
	}
	y["runtime"] = rt
	return y, nil
}

func readMetadata(o Options, p string) ([]byte, error) {
	if strings.HasSuffix(p, ".xz") {
		if o.Runner == nil {
			return nil, fmt.Errorf("no runner")
		}
		out, err := o.Runner.Run("tar", "-xJOf", p, "metadata.yaml")
		if err != nil {
			return nil, err
		}
		return []byte(out), nil
	}
	rc, err := FileOpener(p)()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("metadata.yaml not found in %s", p)
		}
		if cleanName(h.Name) == "metadata.yaml" {
			return io.ReadAll(tr)
		}
	}
}

func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
