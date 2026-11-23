package boot

import (
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"

	"omloader/internal/config"
)

// Loader partitions are bind-mounted by the loader at these places inside the guest
// (see autoboot.sh); /.omloader/devs lists "<name> <device> <fstype>".
const (
	GuestSnapMount   = guestHelperDir + "/snap"
	GuestImagesMount = guestHelperDir + "/images"
	syncScriptPath   = guestHelperDir + "/sync.sh"
)

// syncScript runs at the very end of the guest's shutdown: flush and remount the
// loader partitions read-only, which marks the ext4 journal clean (and clears the exfat dirty flag).
// systemd-shutdown may already have detached the bind mounts; the superblock lives on (the overlay
// holds it), so the script mounts the device again (same superblock) and remounts that read-only.
// Store-file mode: the "snap" entry is the loop device of the store file (listed before "images"), so the store ext4
// is remounted read-only first and the host partition afterwards; the host remount is refused while the loop
// device still has the file open, but the final sync clears the exfat dirty flag anyway.
const syncScript = `#!%s
# omloader: flush + mark clean the loader partitions (OMLOADER ext4 / VTOYDATA) at shutdown.
B=/.omloader/busybox; [ -x $B ] || B=
export PATH=/sbin:/bin:/usr/sbin:/usr/bin
[ -r /.omloader/devs ] || exit 0
$B sync
while read -r name dev fs; do
  d=/.omloader/$name
  if ! $B grep -q " $d " /proc/mounts 2>/dev/null; then
    d=/run/omloader-sync-$name
    $B mkdir -p $d 2>/dev/null
    $B mount -t "$fs" "$dev" $d 2>/dev/null || continue
  fi
  $B sync
  if $B mount -o remount,ro $d 2>/dev/null; then echo "omloader: $name flushed and remounted read-only" > /dev/console 2>/dev/null
  else $B sync; echo "omloader: $name flushed (remount read-only refused: busy, e.g. the OMLOADER store file on it is still attached)" > /dev/console 2>/dev/null; fi
done < /.omloader/devs
$B sync
# NTFS host via ntfs-3g (FUSE daemon, argv[0] "@ntfs-3g" so it survives the root switch): everything is flushed and the
# store ext4 is read-only now, so let the daemon unmount cleanly (SIGTERM) to clear the NTFS dirty flag.
for d in /proc/[0-9]*; do
  [ "$($B cat $d/comm 2>/dev/null)" = ntfs-3g ] && $B kill -TERM "${d#/proc/}" 2>/dev/null
done
exit 0
`

const syncUnit = `[Unit]
Description=omloader: flush the loader partitions and mark them clean at shutdown
DefaultDependencies=no
After=local-fs.target
Before=sysinit.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/true
ExecStop=/.omloader/sync.sh
TimeoutStopSec=20

[Install]
WantedBy=sysinit.target
`

const sysvSyncScript = `#!/bin/sh
### BEGIN INIT INFO
# Provides: omloader-sync
# Default-Stop: 0 6
### END INIT INFO
case "$1" in stop) /.omloader/sync.sh ;; esac
exit 0
`

const debugShellUnit = `[Unit]
Description=omloader debug shell (busybox) on the console
After=systemd-user-sessions.service plymouth-quit-wait.service
ConditionPathExists=/.omloader/busybox

[Service]
ExecStart=/.omloader/dbgshell
StandardInput=tty-force
StandardOutput=inherit
StandardError=inherit
TTYPath=/dev/console
TTYReset=yes
Restart=always
RestartSec=1

[Install]
WantedBy=multi-user.target
`

const debugLauncher = `#!/.omloader/busybox sh
ENV=/.omloader/shrc
export ENV
exec /.omloader/busybox sh
`

const debugShrc = `# omloader debug shell: this image has no usable getty/login
echo "OMLOADER_DEBUG_SHELL tty=$(/.omloader/busybox tty 2>/dev/null) user=$(/.omloader/busybox id -un 2>/dev/null) (busybox; no getty/login in this image, runtime.debug_shell)"
export PATH=/.omloader:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
alias ls='/.omloader/busybox ls'
`

func firstExisting(root string, c ...string) string {
	for _, p := range c {
		if guestExists(root, p) {
			return p
		}
	}
	return ""
}

// consoleUsable reports whether the guest ships a getty and a login program
// (guest busybox counts for both).
func consoleUsable(root string) bool {
	bb := firstExisting(root, "/bin/busybox", "/usr/bin/busybox", "/sbin/busybox") != ""
	getty := bb || firstExisting(root, "/sbin/agetty", "/usr/sbin/agetty", "/sbin/getty", "/usr/sbin/getty") != ""
	login := bb || firstExisting(root, "/bin/login", "/usr/bin/login", "/sbin/login") != ""
	return getty && login
}

func initFlavour(root string, sysd bool) string {
	if sysd {
		return "systemd"
	}
	if !guestExists(root, "/sbin/init") {
		return "none"
	}
	if hp, err := ResolveInRoot(root, "/sbin/init"); err == nil && strings.HasSuffix(hp, "busybox") {
		return "busybox"
	}
	if s, ok := readGuest(root, "/etc/inittab"); ok {
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "::") {
				return "busybox"
			}
		}
	}
	return "sysv"
}

func appendInittab(root, line string, create bool) error {
	s, ok := readGuest(root, "/etc/inittab")
	if !ok && !create {
		return nil
	}
	for _, l := range strings.Split(s, "\n") {
		if l == line {
			return nil
		}
	}
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return guestWriteKeep(root, "/etc/inittab", s+line+"\n", 0o644, -1)
}

func dropInittab(root, line string) {
	s, ok := readGuest(root, "/etc/inittab")
	if !ok || !strings.Contains(s, line) {
		return
	}
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != line {
			out = append(out, l)
		}
	}
	guestWriteKeep(root, "/etc/inittab", strings.Join(out, "\n"), 0o644, -1)
}

const (
	inittabDebugBB   = "::respawn:-/.omloader/dbgshell"
	inittabDebugSysv = "oml0:2345:respawn:/.omloader/dbgshell </dev/console >/dev/console 2>&1"
	inittabSyncBB    = "::shutdown:/.omloader/sync.sh"
)

// adaptUI applies console autologin, accounts, the debug-shell fallback and the shutdown hooks.
func adaptUI(in AdaptInput, warn io.Writer, sysd, hasBB bool) error {
	root, rt := in.Root, in.Runtime
	W := func(rel, data string, mode os.FileMode) error { return guestWrite(root, rel, []byte(data), mode) }
	flavour := initFlavour(root, sysd)

	// accounts
	if err := ApplyAccounts(root, rt, warn); err != nil {
		return err
	}

	// autologin (systemd guests): getty@tty1 + serial-getty@ttyS0
	user := rt.AutologinUser
	if user == "" {
		user = "root"
	}
	if flavour != "busybox" { // busybox-init guests (Alpine) are handled below
		if err := applySystemdAutologin(root, rt, user, sysd, warn); err != nil {
			return err
		}
	}

	// autologin for busybox-init guests (Alpine): the inittab getty lines for tty1/ttyS0/console get `-n -l /.omloader/autologin`
	if flavour == "busybox" {
		if err := adaptBusyboxAutologin(root, rt, user, warn); err != nil {
			return err
		}
	}

	// debug shell fallback
	usable := consoleUsable(root)
	mode := rt.DebugShell
	if mode == "" {
		mode = "auto"
	}
	debug := hasBB && (mode == "always" || (mode == "auto" && !usable))
	if !hasBB && mode != "never" && (mode == "always" || !usable) {
		fmt.Fprintln(warn, "warning: guest has no usable getty/login and the loader has no static busybox: no debug shell")
	}
	const unit = "/etc/systemd/system/omloader-debug-shell.service"
	const want = "/etc/systemd/system/multi-user.target.wants/omloader-debug-shell.service"
	guestRemove(root, unit)
	guestRemove(root, want)
	guestRemove(root, guestHelperDir+"/shrc")
	guestRemove(root, guestHelperDir+"/dbgshell")
	dropInittab(root, inittabDebugBB)
	dropInittab(root, inittabDebugSysv)
	if debug {
		if !usable {
			fmt.Fprintln(warn, "warning: guest has no usable getty/login: starting a busybox debug shell on /dev/console")
		}
		if err := W(guestHelperDir+"/shrc", debugShrc, 0o644); err != nil {
			return err
		}
		if err := W(guestHelperDir+"/dbgshell", debugLauncher, 0o755); err != nil {
			return err
		}
		switch flavour {
		case "systemd":
			if err := W(unit, debugShellUnit, 0o644); err != nil {
				return err
			}
			if err := guestSymlink(root, want, unit); err != nil {
				return err
			}
		case "busybox":
			if err := appendInittab(root, inittabDebugBB, true); err != nil {
				return err
			}
		case "sysv":
			if err := appendInittab(root, inittabDebugSysv, false); err != nil {
				return err
			}
		default: // no init: the entrypoint wrapper starts it (debug_shell: always), see WriteWrapperEnv
		}
	}

	// shutdown sync hooks
	shebang := "/bin/sh"
	if hasBB {
		shebang = guestHelperDir + "/busybox sh"
	}
	syncUnitPath, syncWants := "/etc/systemd/system/omloader-sync.service", "/etc/systemd/system/sysinit.target.wants/omloader-sync.service"
	hooks := []string{"/usr/lib/systemd/system-shutdown/omloader-sync.sh", "/lib/systemd/system-shutdown/omloader-sync.sh"}
	rm := func() {
		guestRemove(root, syncScriptPath)
		guestRemove(root, syncUnitPath)
		guestRemove(root, syncWants)
		for _, h := range hooks {
			guestRemove(root, h)
		}
		for _, l := range []string{"/etc/init.d/omloader-sync", "/etc/rc0.d/K99omloader-sync", "/etc/rc6.d/K99omloader-sync"} {
			guestRemove(root, l)
		}
		dropInittab(root, inittabSyncBB)
	}
	rm()
	if rt.SyncOnShutdown() {
		if err := W(syncScriptPath, fmt.Sprintf(syncScript, shebang), 0o755); err != nil {
			return err
		}
		switch flavour {
		case "systemd":
			for _, h := range hooks {
				if err := W(h, fmt.Sprintf("#!/bin/sh\nexec %s\n", syncScriptPath), 0o755); err != nil {
					return err
				}
			}
			if err := W(syncUnitPath, syncUnit, 0o644); err != nil {
				return err
			}
			if err := guestSymlink(root, syncWants, syncUnitPath); err != nil {
				return err
			}
		case "busybox":
			if err := appendInittab(root, inittabSyncBB, false); err != nil {
				return err
			}
		case "sysv":
			if err := W("/etc/init.d/omloader-sync", sysvSyncScript, 0o755); err != nil {
				return err
			}
			for _, d := range []string{"/etc/rc0.d", "/etc/rc6.d"} {
				if guestDirExists(root, d) {
					guestSymlink(root, path.Join(d, "K99omloader-sync"), "../init.d/omloader-sync")
				}
			}
		}
	}
	return nil
}

const inittabBackup = "/etc/inittab.omloader-orig"

var gettyRe = regexp.MustCompile(`(getty)(\s)`)

// adaptBusyboxAutologin rewrites the getty lines of a busybox /etc/inittab (original kept in inittabBackup, restored first so
// the result only depends on the current config).
func adaptBusyboxAutologin(root string, rt config.Runtime, user string, warn io.Writer) error {
	if orig, ok := readGuest(root, inittabBackup); ok {
		if err := guestWriteKeep(root, "/etc/inittab", orig, 0o644, -1); err != nil {
			return err
		}
	}
	if !rt.AutologinOn() {
		return nil
	}
	s, ok := readGuest(root, "/etc/inittab")
	if !ok {
		return nil
	}
	if findLine(LoadAccounts(root).Passwd, user) < 0 {
		fmt.Fprintf(warn, "warning: runtime.autologin_user %q does not exist in the guest\n", user)
	}
	var out []string
	changed := false
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "#") && strings.Contains(t, "getty") && (strings.Contains(t, "tty1") || strings.Contains(t, "ttyS0") || strings.HasSuffix(t, " console")) && !strings.Contains(t, ".omloader/autologin") {
			l = gettyRe.ReplaceAllString(l, "${1} -n -l /.omloader/autologin${2}")
			changed = true
		}
		out = append(out, l)
	}
	if !changed {
		fmt.Fprintln(warn, "warning: runtime.autologin: no tty1/ttyS0/console getty line found in /etc/inittab; autologin skipped")
		return nil
	}
	if !guestExists(root, inittabBackup) {
		if err := guestWrite(root, inittabBackup, []byte(s), 0o644); err != nil {
			return err
		}
	}
	if err := guestWrite(root, guestHelperDir+"/autologin", []byte("#!/bin/sh\nexec /bin/login -f "+user+"\n"), 0o755); err != nil {
		return err
	}
	return guestWriteKeep(root, "/etc/inittab", strings.Join(out, "\n"), 0o644, -1)
}

// applySystemdAutologin installs (or removes) the getty@tty1 / serial-getty@ttyS0 autologin drop-ins of a systemd guest.
func applySystemdAutologin(root string, rt config.Runtime, user string, sysd bool, warn io.Writer) error {
	W := func(rel, data string, mode os.FileMode) error { return guestWrite(root, rel, []byte(data), mode) }
	dropins := []string{"/etc/systemd/system/getty@tty1.service.d/omloader-autologin.conf", "/etc/systemd/system/serial-getty@ttyS0.service.d/omloader-autologin.conf"}
	if rt.AutologinOn() {
		if !sysd {
			fmt.Fprintln(warn, "warning: runtime.autologin is only implemented for systemd guests (use runtime.debug_shell for others)")
		} else if agetty := firstExisting(root, "/sbin/agetty", "/usr/sbin/agetty"); agetty == "" {
			fmt.Fprintln(warn, "warning: runtime.autologin: guest has no agetty; autologin skipped")
		} else {
			if findLine(LoadAccounts(root).Passwd, user) < 0 {
				fmt.Fprintf(warn, "warning: runtime.autologin_user %q does not exist in the guest\n", user)
			}
			if u := firstExisting(root, "/lib/systemd/system/getty@.service", "/usr/lib/systemd/system/getty@.service"); u != "" {
				// make sure tty1 has a getty even when the guest does not enable getty@tty1 itself
				if err := guestSymlink(root, "/etc/systemd/system/getty.target.wants/getty@tty1.service", u); err != nil {
					return err
				}
			}
			for _, d := range dropins {
				if err := W(d, "[Service]\nExecStart=\nExecStart=-"+agetty+" --autologin "+user+" --noclear %I $TERM\n", 0o644); err != nil {
					return err
				}
			}
		}
	} else {
		for _, d := range dropins {
			guestRemove(root, d)
		}
	}
	return nil
}
