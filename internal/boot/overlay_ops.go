package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"omloader/internal/snapshot"
)

func loadTree(snapRoot, image string) (*snapshot.Tree, error) {
	if !devRe.MatchString(image) {
		return nil, fmt.Errorf("invalid image name %q", image)
	}
	return snapshot.Load(filepath.Join(snapRoot, image, "tree.json"))
}

// ExportSquashfs writes the node's upper layer (only its own changes, overlay whiteouts included)
// to a squashfs file with mksquashfs.
func ExportSquashfs(r Runner, snapRoot, image, node, out string) error {
	t, err := loadTree(snapRoot, image)
	if err != nil {
		return err
	}
	if _, ok := t.Nodes[node]; !ok {
		return fmt.Errorf("snapshot %q not found", node)
	}
	if out == "" {
		return fmt.Errorf("--squashfs OUT required")
	}
	upper := filepath.Join(snapRoot, image, node, "upper")
	if st, err := os.Stat(upper); err != nil || !st.IsDir() {
		return fmt.Errorf("snapshot %q has no upper layer at %s (never booted?)", node, upper)
	}
	_, err = r.Run("mksquashfs", upper, out, "-noappend", "-quiet", "-no-progress", "-comp", "gzip")
	return err
}

// FlattenOptions for FlattenOverlay.
type FlattenOptions struct {
	SnapRoot, ImagesRoot, Image, Node string
	NewName                           string // new image name (default <image>-flat-<node>)
	DryRun                            bool
}

// FlattenOverlay merges the base squashfs + the node's chain of uppers (overlay mount, read-only)
// into one new squashfs and registers it as a NEW image <NewName> (fresh empty snapshot tree).
// The original image and its snapshots are left untouched. Returns the new image dir.
// (dm-snapshot / vm flatten is not implemented: TODO.)
func FlattenOverlay(r Runner, o FlattenOptions) (string, error) {
	t, err := loadTree(o.SnapRoot, o.Image)
	if err != nil {
		return "", err
	}
	chain, err := t.Chain(o.Node)
	if err != nil {
		return "", err
	}
	imgDir := filepath.Join(o.ImagesRoot, o.Image)
	yb, err := os.ReadFile(filepath.Join(imgDir, "image.yaml"))
	if err != nil {
		return "", err
	}
	var y map[string]any
	if err := yaml.Unmarshal(yb, &y); err != nil {
		return "", err
	}
	if m, _ := y["snapshots"].(map[string]any); y["type"] == "lxd-vm" || (m != nil && m["mode"] == "dm-snapshot") {
		return "", fmt.Errorf("flatten of dm-snapshot (vm) images is not implemented (TODO)")
	}
	base, _ := y["base"].(string)
	if base == "" {
		return "", fmt.Errorf("image.yaml has no base")
	}
	if !filepath.IsAbs(base) {
		base = filepath.Join(imgDir, base)
	}
	name := o.NewName
	if name == "" {
		name = o.Image + "-flat-" + o.Node
	}
	if !devRe.MatchString(name) {
		return "", fmt.Errorf("invalid new image name %q", name)
	}
	newDir := filepath.Join(o.ImagesRoot, name)
	if _, err := os.Stat(newDir); err == nil {
		return "", fmt.Errorf("%s already exists", newDir)
	}
	var lowers []string
	for i := len(chain) - 1; i >= 0; i-- {
		lowers = append(lowers, filepath.Join(o.SnapRoot, o.Image, chain[i], "upper"))
	}
	tmpOut, err := r.Run("mktemp", "-d", "/tmp/omloader-flat.XXXXXX")
	if err != nil {
		return "", err
	}
	tmp := firstLine(tmpOut)
	sq, ov := filepath.Join(tmp, "base"), filepath.Join(tmp, "merged")
	lowers = append(lowers, sq)
	for _, p := range append([]string{base}, lowers...) {
		if badOverlayPath(p) {
			return "", fmt.Errorf("path %q has characters unusable in overlay options", p)
		}
	}
	opts := "lowerdir=" + strings.Join(lowers, ":")
	if len(lowers) > MaxOverlayLowers || len(opts) > MaxOverlayOptions {
		return "", fmt.Errorf("chain too long to merge in one overlay mount (%d layers)", len(lowers))
	}
	staged := filepath.Join(tmp, "base.squashfs.new")
	var mounted []string
	cleanup := func() {
		for i := len(mounted) - 1; i >= 0; i-- {
			r.Run("umount", mounted[i])
		}
		r.Run("rm", "-rf", tmp)
	}
	if err := runCmd(r, "mkdir", "-p", sq, ov); err != nil {
		cleanup()
		return "", err
	}
	if err := runCmd(r, "mount", "-t", "squashfs", "-o", "loop,ro", base, sq); err != nil {
		cleanup()
		return "", err
	}
	mounted = append(mounted, sq)
	if err := runCmd(r, "mount", "-t", "overlay", "-o", opts, "overlay", ov); err != nil {
		cleanup()
		return "", err
	}
	mounted = append(mounted, ov)
	if _, err := r.Run("mksquashfs", ov, staged, "-noappend", "-quiet", "-no-progress", "-comp", "gzip"); err != nil {
		cleanup()
		return "", err
	}
	if err := runCmd(r, "mkdir", "-p", newDir); err != nil {
		cleanup()
		return "", err
	}
	if err := runCmd(r, "mv", staged, filepath.Join(newDir, "base.squashfs")); err != nil {
		cleanup()
		return "", err
	}
	cleanup()
	if o.DryRun {
		return newDir, nil
	}
	sum, err := fileSHA(filepath.Join(newDir, "base.squashfs"))
	if err != nil {
		return "", err
	}
	y["name"], y["base"], y["sha256"] = name, "base.squashfs", sum
	out, err := yaml.Marshal(y)
	if err != nil {
		return "", err
	}
	return newDir, os.WriteFile(filepath.Join(newDir, "image.yaml"), out, 0o644)
}

func fileSHA(p string) (string, error) {
	// reuse verifySHA's hashing without a want value
	return sha256Hex(p)
}
