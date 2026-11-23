package boot

import (
	"strings"
)

// SetGuestHostname writes name to /etc/hostname and keeps /etc/hosts consistent: the old hostname is removed from alias lists, every
// 127.0.1.1 entry is replaced by one `127.0.1.1<TAB>name` line (added when absent), all other lines (localhost, ::1, comments) stay.
// A missing /etc/hosts is created with the usual loopback lines.
func SetGuestHostname(root, name string) error {
	old := ""
	if s, ok := readGuest(root, "/etc/hostname"); ok {
		old = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	}
	if err := guestWrite(root, "/etc/hostname", []byte(name+"\n"), 0o644); err != nil {
		return err
	}
	hosts, ok := readGuest(root, "/etc/hosts")
	if !ok {
		hosts = "127.0.0.1\tlocalhost\n::1\tlocalhost ip6-localhost\n"
	}
	return guestWriteKeep(root, "/etc/hosts", RewriteHosts(hosts, old, name), 0o644, -1)
}

// RewriteHosts is the pure /etc/hosts edit of SetGuestHostname.
func RewriteHosts(hosts, old, name string) string {
	var out []string
	placed := false
	for _, l := range strings.Split(strings.TrimRight(hosts, "\n"), "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			out = append(out, l)
			continue
		}
		f := strings.Fields(l)
		if f[0] == "127.0.1.1" {
			if !placed {
				out = append(out, "127.0.1.1\t"+name)
				placed = true
			}
			continue
		}
		if old != "" && old != name {
			kept := f[:1]
			for _, h := range f[1:] {
				if h != old {
					kept = append(kept, h)
				}
			}
			if len(kept) < 2 { // the line only named the old hostname
				continue
			}
			if len(kept) != len(f) {
				l = strings.Join(kept, " ")
			}
		}
		out = append(out, l)
	}
	if !placed {
		out = append(out, "127.0.1.1\t"+name)
	}
	return strings.Join(out, "\n") + "\n"
}
