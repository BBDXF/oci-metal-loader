package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"omloader/internal/boot"
	"omloader/internal/config"
	"omloader/internal/fetch"
	"omloader/internal/imgimport"
)

// craneBin is the registry client (go-containerregistry `crane`, bundled static in the live ISO as /usr/local/bin/crane).
var craneBin = "crane"

// dockerOpts are the flags of `omloader pull docker:IMAGE`.
type dockerOpts struct {
	image, source, mirror, arch, name, root, tmp, cfgPath string
	offline, force, experimental                          bool
}

// safeName turns an image reference into a file/image name part.
var unsafeRe = regexp.MustCompile(`[^a-z0-9._-]+`)

func dockerImageName(ref string) string {
	n := strings.ToLower(ref)
	if i := strings.LastIndex(n, "@"); i >= 0 {
		n = n[:i]
	}
	n = strings.TrimPrefix(n, "docker.io/")
	n = strings.TrimPrefix(n, "library/")
	n = strings.ReplaceAll(n, "/", "-")
	n = strings.ReplaceAll(n, ":", "-")
	return strings.Trim(unsafeRe.ReplaceAllString(n, "-"), "-")
}

// dockerOrder returns the registry hosts to try, best first: with --mirror auto by measured latency (failed probes dropped),
// otherwise the order of the selected mirror list (official, then the region preset).
func dockerOrder(ctx context.Context, c *config.Config, src, mirror string, errw io.Writer) ([]config.Base, error) {
	bs, err := c.SourceBases(src, mirror)
	if err != nil {
		return nil, err
	}
	if mirror != config.MirrorAuto {
		return bs, nil
	}
	ps, err := fetch.ProbeRegistry(ctx, c, src, mirror)
	if err != nil {
		return nil, err
	}
	byName := map[string]config.Base{}
	for _, b := range bs {
		byName[b.Name] = b
	}
	var ok []fetch.Probe
	for _, p := range ps {
		if p.OK {
			ok = append(ok, p)
		}
	}
	if len(ok) == 0 {
		return nil, errors.New("no registry server answered (try --mirror official, or check the network)")
	}
	sort.SliceStable(ok, func(i, j int) bool { return ok[i].Dur < ok[j].Dur })
	var out []config.Base
	for _, p := range ok {
		out = append(out, byName[p.Name])
	}
	return out, nil
}

// runPullDocker is `omloader pull docker:IMAGE` (experimental): crane pull through the fastest working Hub mirror into the
// cache, then the normal oci-archive import.
func runPullDocker(o dockerOpts, out, errw io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(errw, "error:", err); return 1 }
	c, err := loadCfg(o.cfgPath)
	if err != nil {
		return fail(err)
	}
	if !o.experimental && !c.OCIEnabled() {
		return fail(errors.New("Docker/OCI images are experimental and disabled by default (use --experimental-oci, experimental.oci_images: true or OML_EXPERIMENTAL=oci)"))
	}
	if o.source == "" {
		o.source = "dockerhub"
	}
	if s, ok := c.Sources[o.source]; !ok || s.Type != "oci-registry" {
		return fail(fmt.Errorf("%q is not an oci-registry source", o.source))
	}
	if o.name == "" {
		o.name = dockerImageName(o.image)
	}
	if o.arch == "" {
		o.arch = "amd64"
	}
	dir := filepath.Join(c.Cache.Path, "oci")
	tar := filepath.Join(dir, dockerImageName(o.image)+"-"+o.arch+".tar")
	if o.offline || c.Cache.Offline == "on" || !c.Policy.AllowOnline {
		if _, err := os.Stat(tar); err != nil {
			return fail(fmt.Errorf("offline: %s is not in the cache (%s)", o.image, tar))
		}
		fmt.Fprintln(out, "pull: offline, using the cached archive", tar)
	} else {
		bin, err := exec.LookPath(craneBin)
		if err != nil {
			return fail(errors.New("`crane` not found: it is bundled in the loader ISO (/usr/local/bin/crane); on another machine install go-containerregistry's crane (https://github.com/google/go-containerregistry/releases)"))
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fail(err)
		}
		ctx, cancel := ctxTimeout()
		defer cancel()
		order, err := dockerOrder(ctx, c, o.source, o.mirror, errw)
		if err != nil {
			return fail(err)
		}
		var last error
		done := false
		for _, b := range order {
			ref := config.RewriteImageRef(o.image, b.URL)
			fmt.Fprintf(out, "pull: %s via %s (%s)\n", o.image, b.Name, ref)
			part := tar + ".part" // crane --format oci writes an OCI layout DIRECTORY; it is packed into an oci-archive tar below
			os.RemoveAll(part)
			cmd := exec.CommandContext(ctx, bin, "pull", "--format", "oci", "--platform", "linux/"+o.arch, ref, part)
			cmd.Stdout, cmd.Stderr = out, out
			if err := cmd.Run(); err != nil {
				last = fmt.Errorf("mirror %s: %v", b.Name, err)
				fmt.Fprintf(out, "pull: %v -- trying the next server\n", last)
				os.RemoveAll(part)
				if ctx.Err() != nil {
					break
				}
				continue
			}
			pk := exec.CommandContext(ctx, "tar", "-C", part, "-cf", tar+".new", ".")
			pk.Stderr = out
			if err := pk.Run(); err != nil {
				os.RemoveAll(part)
				os.Remove(tar + ".new")
				return fail(fmt.Errorf("pack oci layout: %v", err))
			}
			os.RemoveAll(part)
			if err := os.Rename(tar+".new", tar); err != nil {
				return fail(err)
			}
			done = true
			break
		}
		if !done {
			if last == nil {
				last = errors.New("no server to try")
			}
			return fail(fmt.Errorf("pull %s failed: %v", o.image, last))
		}
	}
	root := resolveImagesRoot(c, o.root)
	d, err := imgimport.Import(imgimport.Options{Kind: "oci-archive", Path: tar, Name: o.name, Out: root, TmpDir: o.tmp,
		Runner: &boot.ExecRunner{}, Warn: errw, Force: o.force, ExperimentalOCI: true})
	if err != nil {
		return fail(err)
	}
	fmt.Fprintln(out, "imported:", d)
	return 0
}
