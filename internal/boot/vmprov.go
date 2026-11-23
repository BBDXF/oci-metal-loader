package boot

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"omloader/internal/config"
)

// VMProvisionMarker is written into the guest root (inside the live COW layer) once the default accounts are in place.
const VMProvisionMarker = "/var/lib/omloader/provisioned"

// ESPFstabMarker is written once the /boot/efi fstab fix has been applied in this layer.
const ESPFstabMarker = "/var/lib/omloader/esp-fstab-fixed"

// HostnameMarker records the hostname the loader set in this layer.
const HostnameMarker = "/var/lib/omloader/hostname"

// ProvisionVMGuest prepares the mounted root of an lxd-vm guest (mnt = root partition of the FINAL dm-snapshot device, so writes land in the
// live COW layer and the base is never touched):
//   - default accounts (users, root password, sudoers, getty autologin, sshd password login; see config.EffectiveRuntime), once per layer
//     (VMProvisionMarker);
//   - when host != "": the guest hostname (/etc/hostname + /etc/hosts, SetGuestHostname), once per layer and name (HostnameMarker holds the name);
//   - when fixESP: the guest's /boot/efi fstab entry becomes nofail (FixESPFstab), once per layer (ESPFstabMarker), independent of the accounts.
//
// Both are done once per live layer, so later changes in the guest survive and a fresh layer (revert, ephemeral boot) is handled again.
// Best effort: every failure is logged and the boot continues. mnt is left mounted read-write; the caller unmounts it.
func ProvisionVMGuest(r Runner, mnt string, rt config.Runtime, fixESP bool, host string, out, warn io.Writer) {
	if out == nil {
		out = io.Discard
	}
	if warn == nil {
		warn = io.Discard
	}
	wantAcc := len(rt.Users) > 0 || rt.RootPassword != "" || rt.RootPasswordHash != "" || rt.AutologinOn()
	doAcc := wantAcc && !guestExists(mnt, VMProvisionMarker)
	doESP := fixESP && !guestExists(mnt, ESPFstabMarker)
	doHost := false
	if host != "" {
		m, _ := readGuest(mnt, HostnameMarker)
		doHost = strings.TrimSpace(m) != host
	}
	if !doAcc && !doESP && !doHost {
		fmt.Fprintln(out, "OMLOADER: VM guest provisioning: nothing to do (already done in this layer, or nothing configured)")
		return
	}
	if _, err := r.Run("mount", "-o", "remount,rw", mnt); err != nil {
		fmt.Fprintf(warn, "warning: VM guest provisioning skipped: cannot mount the guest root read-write (%v); the guest boots unmodified\n", err)
		return
	}
	if doAcc {
		if err := provisionVMFiles(mnt, rt, out, warn); err != nil {
			fmt.Fprintf(warn, "warning: VM guest provisioning failed: %v (the guest boots anyway)\n", err)
		}
	}
	if doHost {
		if err := SetGuestHostname(mnt, host); err != nil {
			fmt.Fprintf(warn, "warning: VM guest hostname not set: %v (the guest boots anyway)\n", err)
		} else {
			guestWrite(mnt, HostnameMarker, []byte(host+"\n"), 0o644)
			fmt.Fprintf(out, "OMLOADER: VM guest hostname set to %s\n", host)
		}
	}
	if doESP {
		changed, err := FixESPFstab(mnt)
		if err != nil {
			fmt.Fprintf(warn, "warning: VM guest /boot/efi fstab fix failed: %v (the guest boots anyway)\n", err)
		} else {
			guestWrite(mnt, ESPFstabMarker, []byte("omloader esp fstab\n"), 0o644)
			fmt.Fprintf(out, "OMLOADER: VM guest /boot/efi fstab entry nofail (changed=%v)\n", changed)
		}
	}
	r.Run("sync")
}

var fstabField = regexp.MustCompile(`\S+`)

// FixESPFstab adds `nofail,x-systemd.device-timeout=1s` to every vfat /boot/efi entry of the guest's /etc/fstab (by LABEL=, UUID=, PARTLABEL=
// or a path alike). Idempotent: existing options are kept and never duplicated; comments, other lines and the original whitespace stay;
// the first change keeps a copy as /etc/fstab.omloader-orig. It reports whether the file changed.
func FixESPFstab(root string) (bool, error) {
	s, ok := readGuest(root, "/etc/fstab")
	if !ok {
		return false, nil
	}
	lines := strings.Split(s, "\n")
	changed := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		loc := fstabField.FindAllStringIndex(l, -1)
		if len(loc) < 3 {
			continue
		}
		f := func(k int) string { return l[loc[k][0]:loc[k][1]] }
		if m := strings.TrimRight(f(1), "/"); m != "/boot/efi" || f(2) != "vfat" {
			continue
		}
		var opts []string
		if len(loc) >= 4 {
			opts = strings.Split(f(3), ",")
		} else {
			opts = []string{"defaults"}
		}
		has := func(prefix string) bool {
			for _, o := range opts {
				if o == prefix || (strings.HasSuffix(prefix, "=") && strings.HasPrefix(o, prefix)) {
					return true
				}
			}
			return false
		}
		add := false
		if !has("nofail") {
			opts, add = append(opts, "nofail"), true
		}
		if !has("x-systemd.device-timeout=") {
			opts, add = append(opts, "x-systemd.device-timeout=1s"), true
		}
		if !add {
			continue
		}
		o := strings.Join(opts, ",")
		if len(loc) >= 4 {
			lines[i] = l[:loc[3][0]] + o + l[loc[3][1]:]
		} else {
			lines[i] = l[:loc[2][1]] + "\t" + o + l[loc[2][1]:]
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	if !guestExists(root, "/etc/fstab.omloader-orig") {
		if err := guestWriteKeep(root, "/etc/fstab.omloader-orig", s, 0o644, -1); err != nil {
			return false, err
		}
	}
	return true, guestWriteKeep(root, "/etc/fstab", strings.Join(lines, "\n"), 0o644, -1)
}

func provisionVMFiles(mnt string, rt config.Runtime, out, warn io.Writer) error {
	if err := ApplyAccounts(mnt, rt, warn); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	sysd := isSystemd(mnt)
	user := rt.AutologinUser
	if user == "" {
		user = "root"
	}
	if rt.AutologinOn() {
		if err := applySystemdAutologin(mnt, rt, user, sysd, warn); err != nil {
			return fmt.Errorf("autologin: %w", err)
		}
	}
	ssh := "no openssh-server"
	if guestExists(mnt, "/usr/sbin/sshd") {
		ssh = "sshd password login not configured (no sshd_config.d)"
		if guestDirExists(mnt, "/etc/ssh/sshd_config.d") {
			if cfg, _ := readGuest(mnt, "/etc/ssh/sshd_config"); strings.Contains(cfg, "sshd_config.d") {
				// 00- sorts first, sshd keeps the first value (cloud images ship 60-cloudimg-settings.conf with PasswordAuthentication no)
				if err := guestWrite(mnt, "/etc/ssh/sshd_config.d/00-omloader.conf", []byte("PasswordAuthentication yes\n"), 0o644); err != nil {
					return fmt.Errorf("sshd: %w", err)
				}
				ssh = "sshd PasswordAuthentication yes"
			}
		}
	}
	if err := guestWrite(mnt, VMProvisionMarker, []byte("omloader provisioned\n"), 0o644); err != nil {
		return fmt.Errorf("marker: %w", err)
	}
	names := make([]string, 0, len(rt.Users))
	for _, u := range rt.Users {
		names = append(names, u.Name)
	}
	fmt.Fprintf(out, "OMLOADER: VM guest provisioned (users=%s root_password=%v autologin=%v systemd=%v; %s)\n",
		strings.Join(names, ","), rt.RootPassword != "" || rt.RootPasswordHash != "", rt.AutologinOn(), sysd, ssh)
	return nil
}
