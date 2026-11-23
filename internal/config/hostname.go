package config

import (
	"regexp"
	"strings"
)

var hostLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// DefaultHostname is used when an image name has no usable character.
const DefaultHostname = "omloader"

// SanitizeHostname turns an image name into an RFC 1123 label: lowercase, only [a-z0-9-] (everything else becomes '-', runs collapse),
// no leading/trailing '-', at most 63 characters; nothing usable -> DefaultHostname.
func SanitizeHostname(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	if s == "" {
		return DefaultHostname
	}
	return s
}

// ResolveHostname returns the guest hostname for an image (all image types) and whether to leave the guest's own alone.
// Order: image.yaml runtime.hostname (literal; "keep" = leave alone) > guest_defaults.hostname ("" / "image" = sanitised image name,
// "keep", or a literal name).
func ResolveHostname(g GuestDefaults, rt Runtime, image string) (name string, keep bool) {
	v := rt.Hostname
	if v == "" {
		v = g.Hostname
	}
	switch v {
	case "keep":
		return "", true
	case "", "image":
		return SanitizeHostname(image), false
	}
	return v, false
}
