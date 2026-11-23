package config

import "strings"

// RegistryHost strips a scheme and trailing slashes from a registry server (host or https://host[/path]).
func RegistryHost(s string) string {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	return strings.TrimRight(s, "/")
}

var hubNames = map[string]bool{"docker.io": true, "index.docker.io": true, "registry-1.docker.io": true, "registry.hub.docker.com": true}

// RewriteImageRef points a Docker Hub image reference at another registry host (a Docker Hub proxy). "alpine:3" ->
// "<host>/library/alpine:3", "docker.io/user/app@sha256:.." -> "<host>/user/app@sha256:..". References that name another
// registry (ghcr.io/x/y, localhost:5000/z) are returned unchanged. host "" or a Hub name only normalizes the reference.
func RewriteImageRef(ref, host string) string {
	host = RegistryHost(host)
	first, rest, hasSlash := strings.Cut(ref, "/")
	switch {
	case hasSlash && hubNames[first]:
		ref = rest
	case hasSlash && (strings.ContainsAny(first, ".:") || first == "localhost"):
		return ref // another registry
	}
	if !strings.Contains(ref, "/") {
		ref = "library/" + ref
	}
	if host == "" {
		host = "docker.io"
	}
	return host + "/" + ref
}
