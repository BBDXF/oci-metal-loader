package boot

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omloader/internal/config"
)

type recRunner struct {
	cmds    []string
	failMnt bool
}

func (f *recRunner) Run(name string, args ...string) (string, error) {
	f.cmds = append(f.cmds, quote(name, args))
	if f.failMnt && name == "mount" {
		return "", errors.New("wrong fs type")
	}
	return "", nil
}

func defaultsRT() config.Runtime {
	on := true
	return config.EffectiveRuntime(config.GuestDefaults{Autologin: &on, AutologinUser: "oci", RootPassword: "Oci123",
		Users: []config.User{{Name: "oci", Password: "Oci123", Sudo: true, Groups: []string{"sudo"}}}}, config.Runtime{})
}

func TestProvisionVMGuest(t *testing.T) {
	mnt := accRoot(t)
	systemdRoot(mnt)
	os.MkdirAll(filepath.Join(mnt, "usr/sbin"), 0o755)
	os.WriteFile(filepath.Join(mnt, "usr/sbin/sshd"), []byte("x"), 0o755)
	os.MkdirAll(filepath.Join(mnt, "etc/ssh/sshd_config.d"), 0o755)
	os.WriteFile(filepath.Join(mnt, "etc/ssh/sshd_config"), []byte("Include /etc/ssh/sshd_config.d/*.conf\n"), 0o644)
	// the "base": a file outside the mounted root that must never change
	store := t.TempDir()
	base := filepath.Join(store, "base.raw")
	os.WriteFile(base, []byte("BASE-IMAGE-BYTES"), 0o644)
	before := sha256.Sum256([]byte("BASE-IMAGE-BYTES"))

	fr := &recRunner{}
	var out, warn bytes.Buffer
	ProvisionVMGuest(fr, mnt, defaultsRT(), &out, &warn)
	if strings.Join(fr.cmds, "|") != "mount -o remount,rw "+mnt+"|sync" {
		t.Errorf("commands: %v", fr.cmds)
	}
	if !strings.Contains(rf(mnt, "etc/passwd"), "oci:x:1000:") || !strings.Contains(rf(mnt, "etc/shadow"), "oci:$6$") || !strings.Contains(rf(mnt, "etc/shadow"), "root:$6$") {
		t.Errorf("accounts: %q %q", rf(mnt, "etc/passwd"), rf(mnt, "etc/shadow"))
	}
	if !strings.Contains(rf(mnt, "etc/group"), "sudo:x:27:oci") {
		t.Errorf("sudo group: %q", rf(mnt, "etc/group"))
	}
	if rf(mnt, "etc/sudoers.d/90-omloader-oci") != "oci ALL=(ALL:ALL) ALL\n" {
		t.Errorf("sudoers: %q", rf(mnt, "etc/sudoers.d/90-omloader-oci"))
	}
	if fi, _ := os.Stat(filepath.Join(mnt, "etc/sudoers.d/90-omloader-oci")); fi.Mode().Perm() != 0o440 {
		t.Errorf("sudoers mode %v", fi.Mode())
	}
	for _, d := range []string{"getty@tty1", "serial-getty@ttyS0"} {
		if !strings.Contains(rf(mnt, "etc/systemd/system/"+d+".service.d/omloader-autologin.conf"), "--autologin oci") {
			t.Errorf("%s drop-in missing", d)
		}
	}
	if rf(mnt, "etc/ssh/sshd_config.d/00-omloader.conf") != "PasswordAuthentication yes\n" {
		t.Error("sshd drop-in")
	}
	if rf(mnt, VMProvisionMarker) == "" || !strings.Contains(out.String(), "VM guest provisioned") {
		t.Errorf("marker/log: %q", out.String())
	}
	if strings.Contains(out.String()+warn.String(), "Oci123") || strings.Contains(warn.String(), "plain-text") {
		t.Errorf("password leaked / default-password warning: %q %q", out.String(), warn.String())
	}
	// the store (base) is untouched: only the mounted root was written
	if after := sha256.Sum256(mustRead(t, base)); after != before {
		t.Error("base changed")
	}
	if ents, _ := os.ReadDir(store); len(ents) != 1 {
		t.Errorf("unexpected files next to the base: %v", ents)
	}
	// second boot of the same layer: marker -> no remount, no rewrite, user changes survive
	os.WriteFile(filepath.Join(mnt, "etc/passwd"), []byte(rf(mnt, "etc/passwd")+"later:x:1001:1001::/home/later:/bin/sh\n"), 0o644)
	fr2 := &recRunner{}
	ProvisionVMGuest(fr2, mnt, defaultsRT(), &out, &warn)
	if len(fr2.cmds) != 0 || !strings.Contains(rf(mnt, "etc/passwd"), "later:") {
		t.Errorf("re-provisioned: %v", fr2.cmds)
	}
	// fresh layer after revert: marker gone -> provisioned again
	os.Remove(filepath.Join(mnt, VMProvisionMarker))
	fr3 := &recRunner{}
	ProvisionVMGuest(fr3, mnt, defaultsRT(), &out, &warn)
	if len(fr3.cmds) != 2 {
		t.Errorf("fresh layer not provisioned: %v", fr3.cmds)
	}
}

func mustRead(t *testing.T, p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProvisionVMGuestBestEffort(t *testing.T) {
	mnt := accRoot(t)
	var out, warn bytes.Buffer
	ProvisionVMGuest(&recRunner{failMnt: true}, mnt, defaultsRT(), &out, &warn)
	if !strings.Contains(warn.String(), "skipped: cannot mount the guest root read-write") || strings.Contains(rf(mnt, "etc/passwd"), "oci:") {
		t.Errorf("rw mount failure: %q", warn.String())
	}
	// nothing configured (image disabled the defaults): no remount at all
	fr := &recRunner{}
	off := false
	ProvisionVMGuest(fr, mnt, config.EffectiveRuntime(config.GuestDefaults{Autologin: &off, Users: []config.User{{Name: "x"}}}, config.Runtime{Users: []config.User{}}), &out, &warn)
	if len(fr.cmds) != 0 {
		t.Errorf("disabled defaults still touched the guest: %v", fr.cmds)
	}
}
