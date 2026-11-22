package boot

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"omloader/internal/snapshot"
)

// DMOptions for FlattenDM / ExportDM. These need root (loop + dm): run them from the loader.
type DMOptions struct {
	SnapRoot, ImagesRoot, Image, Node string
	NewName                           string                       // flatten: new image name (default <image>-flat-<node>)
	Raw, Qcow2                        string                       // export targets
	Which                             func(string) (string, error) // default exec.LookPath
	Copy                              func(src, dst string) error  // sparse copy (default: Go implementation; the loader's busybox dd lacks conv=sparse)
}

type dmImage struct {
	tree   *snapshot.Tree
	chain  []string
	yaml   map[string]any
	imgDir string
	base   string
	size   int64
}

func loadDMImage(o DMOptions) (*dmImage, error) {
	t, err := loadTree(o.SnapRoot, o.Image)
	if err != nil {
		return nil, err
	}
	chain, err := t.Chain(o.Node)
	if err != nil {
		return nil, err
	}
	d := &dmImage{tree: t, chain: chain, imgDir: filepath.Join(o.ImagesRoot, o.Image)}
	yb, err := os.ReadFile(filepath.Join(d.imgDir, "image.yaml"))
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(yb, &d.yaml); err != nil {
		return nil, err
	}
	if d.yaml["type"] != "vm" {
		return nil, fmt.Errorf("image %s is not a vm image (type %v)", o.Image, d.yaml["type"])
	}
	base, _ := d.yaml["base"].(string)
	if base == "" {
		return nil, fmt.Errorf("image.yaml has no base")
	}
	if !filepath.IsAbs(base) {
		base = filepath.Join(d.imgDir, base)
	}
	st, err := os.Stat(base)
	if err != nil {
		return nil, err
	}
	d.base, d.size = base, st.Size()
	for _, n := range chain {
		if _, err := os.Stat(filepath.Join(o.SnapRoot, o.Image, t.Nodes[n].File)); err != nil {
			return nil, fmt.Errorf("layer of %q missing: %w (run `omloader snapshot verify %s`)", n, err, o.Image)
		}
	}
	return d, nil
}

// withStack assembles base + chain (loop + dm snapshot stack), runs fn on the final device and tears everything down.
func withStack(r Runner, o DMOptions, d *dmImage, fn func(dev string) error) error {
	var layers []stackLayer
	for _, n := range d.chain {
		layers = append(layers, stackLayer{DMName: "oml-x-" + o.Image + "-" + n, Cow: filepath.Join(o.SnapRoot, o.Image, d.tree.Nodes[n].File)})
	}
	dev, loops, dms, err := buildStack(r, d.base, "oml-x-"+o.Image+"-base", d.size/512, layers)
	defer func() {
		for i := len(dms) - 1; i >= 0; i-- {
			r.Run("dmsetup", "remove", dms[i])
		}
		for i := len(loops) - 1; i >= 0; i-- {
			r.Run("losetup", "-d", loops[i])
		}
	}()
	if err != nil {
		return err
	}
	return fn(dev)
}

// SparseCopy copies src to dst skipping all-zero 64KiB blocks (holes), preserving the size.
func SparseCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	buf := make([]byte, 64<<10)
	var off int64
	for {
		n, rerr := io.ReadFull(in, buf)
		if n > 0 {
			if allZero(buf[:n]) {
				off += int64(n)
			} else {
				if _, err := out.WriteAt(buf[:n], off); err != nil {
					out.Close()
					return err
				}
				off += int64(n)
			}
		}
		if rerr != nil {
			if rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
				out.Close()
				return rerr
			}
			break
		}
	}
	if err := out.Truncate(off); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

func (o DMOptions) copyFn() func(src, dst string) error {
	if o.Copy != nil {
		return o.Copy
	}
	return SparseCopy
}

// ExportDM writes the state of a snapshot as a raw (or qcow2) disk image.
func ExportDM(r Runner, o DMOptions) error {
	if (o.Raw == "") == (o.Qcow2 == "") {
		return fmt.Errorf("give exactly one of --raw OUT or --qcow2 OUT")
	}
	if o.Qcow2 != "" {
		which := o.Which
		if which == nil {
			which = exec.LookPath
		}
		if _, err := which("qemu-img"); err != nil {
			return fmt.Errorf("qemu-img is not available in this environment (the loader does not ship it): export with --raw and convert on a host: qemu-img convert -O qcow2 out.img out.qcow2")
		}
	}
	d, err := loadDMImage(o)
	if err != nil {
		return err
	}
	return withStack(r, o, d, func(dev string) error {
		if o.Raw != "" {
			return o.copyFn()(dev, o.Raw)
		}
		_, err := r.Run("qemu-img", "convert", "-f", "raw", "-O", "qcow2", dev, o.Qcow2)
		return err
	})
}

// FlattenDM builds a new standalone image <images-root>/<NewName>/base.raw from base + chain(Node),
// with a copy of image.yaml (new name and sha256). The original image and its snapshots are untouched.
func FlattenDM(r Runner, o DMOptions) (string, error) {
	d, err := loadDMImage(o)
	if err != nil {
		return "", err
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
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(newDir, "base.raw")
	fail := func(err error) (string, error) { os.RemoveAll(newDir); return "", err }
	if err := withStack(r, o, d, func(dev string) error { return o.copyFn()(dev, out) }); err != nil {
		return fail(err)
	}
	if _, err := r.Run("sync"); err != nil {
		return fail(err)
	}
	sum, err := sha256Hex(out)
	if err != nil {
		return fail(err)
	}
	d.yaml["name"], d.yaml["base"], d.yaml["sha256"] = name, "base.raw", sum
	b, err := yaml.Marshal(d.yaml)
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "image.yaml"), b, 0o644); err != nil {
		return fail(err)
	}
	return newDir, nil
}
