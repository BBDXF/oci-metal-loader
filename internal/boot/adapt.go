package boot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"omloader/internal/config"
)

// AdaptMarker is created in the guest root once the firstboot adapter ran.
const AdaptMarker = "/.omloader-adapted"

// GuestDir inside the guest root holds omloader helper files (static busybox, wrapper).
const guestHelperDir = "/.omloader"

// AdaptInput configures the firstboot adapter.
type AdaptInput struct {
	Root       string // merged root (overlay mount, writes land in the upper)
	Image      string
	Runtime    config.Runtime
	Busybox    string // host path of a static busybox ("" = none)
	ResolvConf string // source resolv.conf ("" = built-in default)
	Warn       io.Writer
	// KeepHostname leaves the guest's own hostname alone (guest_defaults.hostname / runtime.hostname = keep); otherwise Runtime.Hostname
	// (resolved by config.ResolveHostname; empty = the sanitised image name) is written to /etc/hostname and /etc/hosts.
	KeepHostname bool
}

// walkInRoot resolves p inside root like ResolveInRoot but creates missing
// directories when mk is set and tolerates a missing tail otherwise.
// Absolute symlinks never escape root.
func walkInRoot(root, p string, mk bool) (string, error) {
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
		if os.IsNotExist(err) {
			if mk {
				if err := os.Mkdir(cand, 0o755); err != nil {
					return "", err
				}
			}
			stack = append(stack, c)
			continue
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if links++; links > maxSymlinks {
				return "", fmt.Errorf("resolve %s in guest: too many symlinks", p)
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

// guestWrite writes rel (absolute in the guest) safely: a symlink at the final
// component is replaced, intermediate symlinks are resolved inside root.
func guestWrite(root, rel string, data []byte, mode os.FileMode) error {
	dir, err := walkInRoot(root, path.Dir(rel), true)
	if err != nil {
		return err
	}
	full := filepath.Join(dir, path.Base(rel))
	if fi, err := os.Lstat(full); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		os.Remove(full)
	} else if err == nil && fi.Mode().IsRegular() {
		os.Chmod(full, fi.Mode().Perm()|0o200) // e.g. 0440 sudoers drop-ins when not root
	}
	if err := os.WriteFile(full, data, mode); err != nil {
		return err
	}
	return os.Chmod(full, mode)
}

func guestSymlink(root, rel, target string) error {
	dir, err := walkInRoot(root, path.Dir(rel), true)
	if err != nil {
		return err
	}
	full := filepath.Join(dir, path.Base(rel))
	os.Remove(full)
	return os.Symlink(target, full)
}

// guestExists reports whether abs path (guest view) resolves to an existing file.
func guestExists(root, p string) bool {
	hp, err := ResolveInRoot(root, p)
	if err != nil {
		return false
	}
	fi, err := os.Stat(hp)
	return err == nil && !fi.IsDir()
}

func isSystemd(root string) bool {
	return guestExists(root, "/lib/systemd/systemd") || guestExists(root, "/usr/lib/systemd/systemd")
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	os.Remove(dst)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

const wrapperScript = `#!/.omloader/busybox sh
# omloader entrypoint wrapper: minimal init environment for images without an init.
B=/.omloader/busybox
$B mountpoint -q /proc || $B mount -t proc proc /proc
$B mountpoint -q /sys || $B mount -t sysfs sysfs /sys
$B mountpoint -q /dev || $B mount -t devtmpfs dev /dev
$B mkdir -p /dev/pts /run /tmp
$B mountpoint -q /dev/pts || $B mount -t devpts devpts /dev/pts
$B mountpoint -q /run || $B mount -t tmpfs tmpfs /run
[ -n "$PATH" ] || export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH HOME=${HOME:-/root} TERM=${TERM:-linux}
[ -r /.omloader/env ] && . /.omloader/env
[ -r /etc/hostname ] && $B hostname -F /etc/hostname
[ -x /.omloader/net.sh ] && /.omloader/net.sh &
exec "$@"
`

const netScript = `#!/.omloader/busybox sh
# omloader DHCP helper (busybox udhcpc)
B=/.omloader/busybox
$B ip link set lo up
for d in /sys/class/net/*; do
  i=${d##*/}; [ "$i" = lo ] && continue
  $B ip link set "$i" up
  $B udhcpc -b -q -t 5 -i "$i" -s /.omloader/udhcpc.script
done
`

const udhcpcScript = `#!/.omloader/busybox sh
B=/.omloader/busybox
case "$1" in
bound|renew)
  $B ip addr flush dev "$interface" 2>/dev/null
  $B ip addr add "$ip/${mask:-24}" dev "$interface"
  for r in $router; do $B ip route add default via "$r" dev "$interface"; break; done
  if [ -n "$dns" ]; then : > /etc/resolv.conf; for d in $dns; do echo "nameserver $d" >> /etc/resolv.conf; done; fi
  ;;
esac
exit 0
`

const sysvNetScript = `#!/bin/sh
### BEGIN INIT INFO
# Provides: omloader-net
# Default-Start: 2 3 4 5
### END INIT INFO
case "$1" in start|"") /.omloader/net.sh ;; esac
exit 0
`

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Adapt applies the firstboot adapter into in.Root. The base part (hostname, resolv.conf, DHCP,
// helper files, env) runs once (AdaptMarker). The "UI" part (console autologin, users/passwords,
// debug shell, shutdown sync hooks) is re-applied whenever the hash of its config differs from the
// one stored in the marker, so editing runtime.users takes effect on the next boot without
// duplicating accounts. It returns whether anything was applied.
func Adapt(in AdaptInput) (bool, error) {
	warn := in.Warn
	if warn == nil {
		warn = io.Discard
	}
	first := !guestExists(in.Root, AdaptMarker)
	want := uiConfigHash(in.Runtime, in.KeepHostname)
	if !first && markerUIHash(in.Root) == want {
		return false, nil
	}
	sysd := isSystemd(in.Root)
	var hasBB bool
	if first {
		var err error
		if hasBB, err = adaptBase(in, warn, sysd); err != nil {
			return false, err
		}
	} else {
		hasBB = guestExists(in.Root, guestHelperDir+"/busybox")
	}
	if err := adaptUI(in, warn, sysd, hasBB); err != nil {
		return false, fmt.Errorf("console/users adapter: %w", err)
	}
	if err := guestWrite(in.Root, AdaptMarker, []byte("omloader adapter v2 "+time.Now().UTC().Format(time.RFC3339)+"\nui-config="+want+"\n"), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// uiConfigHash fingerprints the runtime keys handled by adaptUI.
func uiConfigHash(rt config.Runtime, keepHost bool) string {
	host := rt.Hostname
	if keepHost {
		host = "\x00keep"
	}
	v := struct {
		V  int
		RT config.Runtime
	}{4, config.Runtime{Hostname: host, Autologin: rt.Autologin, AutologinUser: rt.AutologinUser, Users: rt.Users, RootPasswordHash: rt.RootPasswordHash,
		RootPassword: rt.RootPassword, DebugShell: rt.DebugShell, ShutdownSync: rt.ShutdownSync}}
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func markerUIHash(root string) string {
	s, _ := readGuest(root, AdaptMarker)
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "ui-config=") {
			return strings.TrimPrefix(l, "ui-config=")
		}
	}
	return ""
}

func adaptBase(in AdaptInput, warn io.Writer, sysd bool) (bool, error) {
	rt := in.Runtime
	W := func(rel, data string, mode os.FileMode) error { return guestWrite(in.Root, rel, []byte(data), mode) }
	if err := W("/etc/fstab", "# omloader: root is an overlayfs; no entries\n", 0o644); err != nil {
		return false, err
	}
	if rt.Network != "none" {
		rc := "nameserver 1.1.1.1\nnameserver 8.8.8.8\n"
		if in.ResolvConf != "" {
			if b, err := os.ReadFile(in.ResolvConf); err == nil && strings.Contains(string(b), "nameserver") {
				rc = string(b)
			}
		}
		if err := W("/etc/resolv.conf", rc, 0o644); err != nil {
			return false, err
		}
	}
	// helper files: static busybox + wrapper + DHCP script
	hasBB := false
	if in.Busybox != "" {
		dir, err := walkInRoot(in.Root, guestHelperDir, true)
		if err != nil {
			return false, err
		}
		if err := copyFile(in.Busybox, filepath.Join(dir, "busybox"), 0o755); err != nil {
			fmt.Fprintf(warn, "warning: cannot install busybox into guest: %v\n", err)
		} else {
			hasBB = true
		}
	} else {
		fmt.Fprintln(warn, "warning: no static busybox available; entrypoint wrapper and busybox DHCP are disabled")
	}
	if hasBB {
		for n, c := range map[string]string{"entrypoint": wrapperScript, "udhcpc.script": udhcpcScript} {
			if err := W(guestHelperDir+"/"+n, c, 0o755); err != nil {
				return false, err
			}
		}
	}
	if rt.Network == "dhcp" {
		unit := false
		if sysd {
			for _, d := range []string{"/lib/systemd/system", "/usr/lib/systemd/system"} {
				if guestExists(in.Root, d+"/systemd-networkd.service") {
					unit = true
					if err := W("/etc/systemd/network/80-omloader-dhcp.network", "[Match]\nType=ether\n\n[Network]\nDHCP=ipv4\n\n[DHCPv4]\nUseDNS=true\n", 0o644); err != nil {
						return false, err
					}
					if err := guestSymlink(in.Root, "/etc/systemd/system/multi-user.target.wants/systemd-networkd.service", d+"/systemd-networkd.service"); err != nil {
						return false, err
					}
					if guestExists(in.Root, d+"/systemd-networkd.socket") {
						guestSymlink(in.Root, "/etc/systemd/system/sockets.target.wants/systemd-networkd.socket", d+"/systemd-networkd.socket")
					}
					break
				}
			}
			if !unit {
				fmt.Fprintln(warn, "warning: systemd guest has no systemd-networkd unit; falling back to busybox DHCP script")
			}
		}
		if !unit {
			if !hasBB {
				fmt.Fprintln(warn, "warning: runtime.network=dhcp but no DHCP client could be installed")
			} else {
				if err := W(guestHelperDir+"/net.sh", netScript, 0o755); err != nil {
					return false, err
				}
				if !sysd {
					if err := W("/etc/init.d/omloader-net", sysvNetScript, 0o755); err != nil {
						return false, err
					}
					for _, d := range []string{"/etc/rcS.d", "/etc/rc2.d", "/etc/rc3.d"} {
						if hp, err := walkInRoot(in.Root, d, false); err == nil {
							if st, err := os.Stat(hp); err == nil && st.IsDir() {
								guestSymlink(in.Root, d+"/S99omloader-net", "../init.d/omloader-net")
							}
						}
					}
				}
			}
		}
	}
	if sysd && len(rt.Env) > 0 {
		var kv []string
		for _, k := range sortedKeys(rt.Env) {
			kv = append(kv, `"`+k+"="+strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(rt.Env[k])+`"`)
		}
		if err := W("/etc/systemd/system.conf.d/omloader-env.conf", "[Manager]\nDefaultEnvironment="+strings.Join(kv, " ")+"\n", 0o644); err != nil {
			return false, err
		}
	}
	return hasBB, nil
}

func sortedKeys(m map[string]string) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// WriteWrapperEnv refreshes the per-boot env/workdir for the entrypoint wrapper.
func WriteWrapperEnv(root string, rt config.Runtime) error {
	var b strings.Builder
	for _, k := range sortedKeys(rt.Env) {
		fmt.Fprintf(&b, "export %s=%s\n", k, shQuote(rt.Env[k]))
	}
	if rt.WorkingDir != "" {
		fmt.Fprintf(&b, "cd %s 2>/dev/null || true\n", shQuote(rt.WorkingDir))
	}
	if rt.DebugShell == "always" {
		b.WriteString("[ -x /.omloader/dbgshell ] && ( while :; do /.omloader/dbgshell </dev/console >/dev/console 2>&1; $B sleep 1; done ) &\n")
	}
	return guestWrite(root, guestHelperDir+"/env", []byte(b.String()), 0o644)
}

// SelectInit picks the command run by switch_root. Order: runtime.entrypoint,
// /sbin/init, runtime.cmd, /bin/sh (warning). Commands that are not an init
// (basename init/systemd) are wrapped by /.omloader/entrypoint.
func SelectInit(root string, rt config.Runtime, warn io.Writer) ([]string, error) {
	if warn == nil {
		warn = io.Discard
	}
	var cmd []string
	switch {
	case len(rt.Entrypoint) > 0:
		cmd = append(append([]string{}, rt.Entrypoint...), rt.Cmd...)
	case guestExists(root, "/sbin/init"):
		cmd = []string{"/sbin/init"}
	case len(rt.Cmd) > 0:
		cmd = append([]string{}, rt.Cmd...)
	case guestExists(root, "/bin/sh"):
		fmt.Fprintln(warn, "warning: image has no entrypoint and no /sbin/init; falling back to /bin/sh")
		cmd = []string{"/bin/sh"}
	default:
		return nil, fmt.Errorf("no init found: set runtime.entrypoint, or provide /sbin/init or /bin/sh in the image")
	}
	if isInitCmd(root, cmd[0]) {
		if !guestExists(root, cmd[0]) {
			return nil, fmt.Errorf("init %s not found in image", cmd[0])
		}
		return cmd, nil
	}
	if !guestExists(root, guestHelperDir+"/entrypoint") {
		return nil, fmt.Errorf("entrypoint %q needs the wrapper but %s/entrypoint is missing (no busybox in loader?)", cmd[0], guestHelperDir)
	}
	if err := WriteWrapperEnv(root, rt); err != nil {
		return nil, err
	}
	return append([]string{guestHelperDir + "/entrypoint"}, cmd...), nil
}

func isInitCmd(root, c string) bool {
	b := path.Base(c)
	if b == "init" || b == "systemd" {
		return true
	}
	if hp, err := ResolveInRoot(root, c); err == nil {
		rb := filepath.Base(hp)
		return rb == "systemd" || rb == "init"
	}
	return false
}
