package imgimport

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ImageConfig is the subset of the OCI/docker image config omloader uses.
type ImageConfig struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Config       struct {
		Entrypoint []string            `json:"Entrypoint"`
		Cmd        []string            `json:"Cmd"`
		Env        []string            `json:"Env"`
		WorkingDir string              `json:"WorkingDir"`
		Volumes    map[string]struct{} `json:"Volumes"`
	} `json:"config"`
}

// Source is a parsed image: ordered layers + config.
type Source struct {
	Layers  []LayerOpener
	Config  ImageConfig
	Cleanup func()
}

// extractArchive unpacks a (possibly gzip) tar of an OCI layout / docker save into dst.
// Only regular files and directories are materialised (blobs); everything else is ignored.
func extractArchive(archive, dst string) error {
	rc, err := FileOpener(archive)()
	if err != nil {
		return err
	}
	defer rc.Close()
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := cleanName(h.Name)
		if name == "" {
			continue
		}
		p := filepath.Join(dst, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			f, err := os.Create(p)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	return nil
}

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Platform  *struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"platform"`
}

func blobPath(dir, digest string) (string, error) {
	alg, hex, ok := strings.Cut(digest, ":")
	if !ok || strings.ContainsAny(digest, "/\\") || strings.Contains(digest, "..") {
		return "", fmt.Errorf("bad digest %q", digest)
	}
	return filepath.Join(dir, "blobs", alg, hex), nil
}

// OpenOCIDir reads an OCI image layout directory.
func OpenOCIDir(dir string) (*Source, error) {
	var idx struct {
		Manifests []descriptor `json:"manifests"`
	}
	if err := readJSON(filepath.Join(dir, "index.json"), &idx); err != nil {
		return nil, fmt.Errorf("not an OCI layout (index.json): %w", err)
	}
	pick := func(ds []descriptor) *descriptor {
		for i := range ds {
			d := &ds[i]
			if d.Platform == nil || (d.Platform.OS == "linux" && d.Platform.Architecture == "amd64") {
				return d
			}
		}
		return nil
	}
	d := pick(idx.Manifests)
	if d == nil {
		return nil, fmt.Errorf("index.json: no linux/amd64 manifest")
	}
	for depth := 0; depth < 3; depth++ {
		bp, err := blobPath(dir, d.Digest)
		if err != nil {
			return nil, err
		}
		var m struct {
			Manifests []descriptor `json:"manifests"`
			Config    descriptor   `json:"config"`
			Layers    []descriptor `json:"layers"`
		}
		if err := readJSON(bp, &m); err != nil {
			return nil, err
		}
		if len(m.Manifests) > 0 { // nested index
			if d = pick(m.Manifests); d == nil {
				return nil, fmt.Errorf("manifest list: no linux/amd64 entry")
			}
			continue
		}
		s := &Source{}
		cp, err := blobPath(dir, m.Config.Digest)
		if err != nil {
			return nil, err
		}
		if err := readJSON(cp, &s.Config); err != nil {
			return nil, err
		}
		for _, l := range m.Layers {
			lp, err := blobPath(dir, l.Digest)
			if err != nil {
				return nil, err
			}
			s.Layers = append(s.Layers, FileOpener(lp))
		}
		if len(s.Layers) == 0 {
			return nil, fmt.Errorf("image has no layers")
		}
		return s, nil
	}
	return nil, fmt.Errorf("manifest nesting too deep")
}

// OpenDockerDir reads an extracted `docker save` / podman docker-archive directory.
func OpenDockerDir(dir string) (*Source, error) {
	var ms []struct {
		Config string
		Layers []string
	}
	if err := readJSON(filepath.Join(dir, "manifest.json"), &ms); err != nil {
		return nil, fmt.Errorf("not a docker archive (manifest.json): %w", err)
	}
	if len(ms) == 0 {
		return nil, fmt.Errorf("manifest.json is empty")
	}
	s := &Source{}
	if err := readJSON(filepath.Join(dir, filepath.Clean("/"+ms[0].Config)), &s.Config); err != nil {
		return nil, err
	}
	for _, l := range ms[0].Layers {
		s.Layers = append(s.Layers, FileOpener(filepath.Join(dir, filepath.Clean("/"+l))))
	}
	if len(s.Layers) == 0 {
		return nil, fmt.Errorf("image has no layers")
	}
	return s, nil
}

// Open opens kind = oci-dir | oci-archive | docker-archive.
func Open(kind, p, tmp string) (*Source, error) {
	switch kind {
	case "oci-dir":
		return OpenOCIDir(p)
	case "oci-archive", "docker-archive":
		x, err := os.MkdirTemp(tmp, "omloader-import-")
		if err != nil {
			return nil, err
		}
		if err := extractArchive(p, x); err != nil {
			os.RemoveAll(x)
			return nil, err
		}
		var s *Source
		if kind == "oci-archive" {
			s, err = OpenOCIDir(x)
		} else {
			s, err = OpenDockerDir(x)
		}
		if err != nil {
			os.RemoveAll(x)
			return nil, err
		}
		s.Cleanup = func() { os.RemoveAll(x) }
		return s, nil
	}
	return nil, fmt.Errorf("unknown import kind %q", kind)
}
