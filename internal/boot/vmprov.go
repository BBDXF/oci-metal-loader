package boot

import (
	"fmt"
	"io"
	"strings"

	"omloader/internal/config"
)

// VMProvisionMarker is written into the guest root (inside the live COW layer) once the default accounts are in place.
const VMProvisionMarker = "/var/lib/omloader/provisioned"

// ProvisionVMGuest creates the configured accounts (guest_defaults + image runtime, see config.EffectiveRuntime) in the mounted root of an
// lxd-vm guest: users, root password, sudoers drop-ins, getty autologin drop-ins and (best effort) sshd password login. mnt must be the
// root partition of the FINAL dm-snapshot device (writes land in the live COW layer, the base is never touched). It is done once per live
// layer (VMProvisionMarker inside the guest), so later changes made in the guest are not overwritten and a fresh layer (after a revert or
// an ephemeral boot) is provisioned again. Best effort: every failure is logged and the boot continues. mnt is left mounted read-write;
// the caller unmounts it.
func ProvisionVMGuest(r Runner, mnt string, rt config.Runtime, out, warn io.Writer) {
	if out == nil {
		out = io.Discard
	}
	if warn == nil {
		warn = io.Discard
	}
	if len(rt.Users) == 0 && rt.RootPassword == "" && rt.RootPasswordHash == "" && !rt.AutologinOn() {
		fmt.Fprintln(out, "OMLOADER: VM guest provisioning: nothing configured (guest_defaults / runtime), skipped")
		return
	}
	if guestExists(mnt, VMProvisionMarker) {
		fmt.Fprintln(out, "OMLOADER: VM guest already provisioned in this layer (skipped)")
		return
	}
	if _, err := r.Run("mount", "-o", "remount,rw", mnt); err != nil {
		fmt.Fprintf(warn, "warning: VM guest provisioning skipped: cannot mount the guest root read-write (%v); the guest boots without the default accounts\n", err)
		return
	}
	if err := provisionVMFiles(mnt, rt, out, warn); err != nil {
		fmt.Fprintf(warn, "warning: VM guest provisioning failed: %v (the guest boots anyway)\n", err)
	}
	r.Run("sync")
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
