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
	ProvisionVMGuest(fr, mnt, defaultsRT(), false, "", &out, &warn)
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
	ProvisionVMGuest(fr2, mnt, defaultsRT(), false, "", &out, &warn)
	if len(fr2.cmds) != 0 || !strings.Contains(rf(mnt, "etc/passwd"), "later:") {
		t.Errorf("re-provisioned: %v", fr2.cmds)
	}
	// fresh layer after revert: marker gone -> provisioned again
	os.Remove(filepath.Join(mnt, VMProvisionMarker))
	fr3 := &recRunner{}
	ProvisionVMGuest(fr3, mnt, defaultsRT(), false, "", &out, &warn)
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
	ProvisionVMGuest(&recRunner{failMnt: true}, mnt, defaultsRT(), false, "", &out, &warn)
	if !strings.Contains(warn.String(), "skipped: cannot mount the guest root read-write") || strings.Contains(rf(mnt, "etc/passwd"), "oci:") {
		t.Errorf("rw mount failure: %q", warn.String())
	}
	// nothing configured (image disabled the defaults): no remount at all
	fr := &recRunner{}
	off := false
	ProvisionVMGuest(fr, mnt, config.EffectiveRuntime(config.GuestDefaults{Autologin: &off, Users: []config.User{{Name: "x"}}}, config.Runtime{Users: []config.User{}}), false, "", &out, &warn)
	if len(fr.cmds) != 0 {
		t.Errorf("disabled defaults still touched the guest: %v", fr.cmds)
	}
}

func TestFixESPFstabVariants(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"ubuntu label", "LABEL=cloudimg-rootfs\t/\text4\tdiscard,errors=remount-ro\t0 1\nLABEL=UEFI\t/boot/efi\tvfat\tumask=0077\t0 1\n",
			"LABEL=cloudimg-rootfs\t/\text4\tdiscard,errors=remount-ro\t0 1\nLABEL=UEFI\t/boot/efi\tvfat\tumask=0077,nofail,x-systemd.device-timeout=1s\t0 1\n"},
		{"debian uuid spaces", "# /etc/fstab\nUUID=ABCD-1234  /boot/efi  vfat  defaults  0  2\n",
			"# /etc/fstab\nUUID=ABCD-1234  /boot/efi  vfat  defaults,nofail,x-systemd.device-timeout=1s  0  2\n"},
		{"partlabel 4 fields", "PARTLABEL=esp /boot/efi/ vfat umask=0077\n", "PARTLABEL=esp /boot/efi/ vfat umask=0077,nofail,x-systemd.device-timeout=1s\n"},
		{"3 fields", "LABEL=UEFI /boot/efi vfat\n", "LABEL=UEFI /boot/efi vfat\tdefaults,nofail,x-systemd.device-timeout=1s\n"},
		{"nofail present, timeout added", "LABEL=UEFI /boot/efi vfat nofail 0 1\n", "LABEL=UEFI /boot/efi vfat nofail,x-systemd.device-timeout=1s 0 1\n"},
		{"already complete", "LABEL=UEFI /boot/efi vfat nofail,x-systemd.device-timeout=5s 0 1\n", "LABEL=UEFI /boot/efi vfat nofail,x-systemd.device-timeout=5s 0 1\n"},
		{"commented / other mount / not vfat / no trailing newline", "#LABEL=UEFI /boot/efi vfat umask=0077 0 1\nLABEL=X /boot ext4 defaults 0 1\n/dev/sda1 /boot/efi ext4 defaults 0 1\nLABEL=UEFI /boot/efi vfat defaults",
			"#LABEL=UEFI /boot/efi vfat umask=0077 0 1\nLABEL=X /boot ext4 defaults 0 1\n/dev/sda1 /boot/efi ext4 defaults 0 1\nLABEL=UEFI /boot/efi vfat defaults,nofail,x-systemd.device-timeout=1s"},
	}
	for _, c := range cases {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "etc"), 0o644|0o111)
		os.WriteFile(filepath.Join(root, "etc/fstab"), []byte(c.in), 0o644)
		ch, err := FixESPFstab(root)
		if err != nil || rf(root, "etc/fstab") != c.want {
			t.Errorf("%s: err=%v got %q want %q", c.name, err, rf(root, "etc/fstab"), c.want)
		}
		if ch != (c.in != c.want) {
			t.Errorf("%s: changed=%v", c.name, ch)
		}
		if ch && rf(root, "etc/fstab.omloader-orig") != c.in {
			t.Errorf("%s: backup %q", c.name, rf(root, "etc/fstab.omloader-orig"))
		}
		if ch2, _ := FixESPFstab(root); ch2 {
			t.Errorf("%s: not idempotent", c.name)
		}
	}
	if ch, err := FixESPFstab(t.TempDir()); ch || err != nil {
		t.Error("missing fstab must be a no-op")
	}
}

func TestProvisionVMGuestESPIndependentOfAccounts(t *testing.T) {
	mnt := accRoot(t)
	os.WriteFile(filepath.Join(mnt, "etc/fstab"), []byte("LABEL=UEFI /boot/efi vfat umask=0077 0 1\n"), 0o644)
	store := t.TempDir()
	os.WriteFile(filepath.Join(store, "base.raw"), []byte("BASE"), 0o644)
	off := false
	noAcc := config.EffectiveRuntime(config.GuestDefaults{Autologin: &off}, config.Runtime{Users: []config.User{}, RootPasswordHash: "!"}) // accounts disabled by the image
	noAcc.RootPasswordHash = ""
	var out, warn bytes.Buffer
	fr := &recRunner{}
	ProvisionVMGuest(fr, mnt, noAcc, true, "", &out, &warn)
	if !strings.Contains(rf(mnt, "etc/fstab"), "nofail,x-systemd.device-timeout=1s") || rf(mnt, ESPFstabMarker) == "" {
		t.Errorf("fstab not fixed: %q", rf(mnt, "etc/fstab"))
	}
	if strings.Contains(rf(mnt, "etc/passwd"), "oci:") || guestExists(mnt, VMProvisionMarker) {
		t.Error("accounts were provisioned although disabled")
	}
	if strings.Join(fr.cmds, "|") != "mount -o remount,rw "+mnt+"|sync" {
		t.Errorf("commands: %v", fr.cmds)
	}
	// once per layer: second boot does nothing; fresh layer (marker gone) works again
	fr2 := &recRunner{}
	ProvisionVMGuest(fr2, mnt, noAcc, true, "", &out, &warn)
	if len(fr2.cmds) != 0 {
		t.Errorf("second boot touched the guest: %v", fr2.cmds)
	}
	// switch off: no remount at all on a fresh layer
	os.Remove(filepath.Join(mnt, ESPFstabMarker))
	os.WriteFile(filepath.Join(mnt, "etc/fstab"), []byte("LABEL=UEFI /boot/efi vfat umask=0077 0 1\n"), 0o644)
	fr3 := &recRunner{}
	ProvisionVMGuest(fr3, mnt, noAcc, false, "", &out, &warn)
	if len(fr3.cmds) != 0 || strings.Contains(rf(mnt, "etc/fstab"), "nofail") {
		t.Errorf("vm_fix_esp_fstab=false still modified: %v", fr3.cmds)
	}
	if string(mustRead(t, filepath.Join(store, "base.raw"))) != "BASE" {
		t.Error("base changed")
	}
}

func TestRewriteHosts(t *testing.T) {
	cases := []struct{ name, in, old, want string }{
		{"debian style", "127.0.0.1\tlocalhost\n127.0.1.1\tdistrobuilder-abc\n::1\tlocalhost ip6-localhost\n", "distrobuilder-abc",
			"127.0.0.1\tlocalhost\n127.0.1.1\tvm-x\n::1\tlocalhost ip6-localhost\n"},
		{"no 127.0.1.1: appended, old alias dropped", "127.0.0.1 localhost oldhost\n::1 ip6-localhost\n", "oldhost",
			"127.0.0.1 localhost\n::1 ip6-localhost\n127.0.1.1\tvm-x\n"},
		{"duplicate 127.0.1.1 collapse, comments kept", "# c\n127.0.1.1 a\n127.0.1.1 b\n127.0.0.1 localhost\n", "a", "# c\n127.0.1.1\tvm-x\n127.0.0.1 localhost\n"},
		{"line only naming the old host is dropped", "127.0.0.1 localhost\n10.0.0.5 oldhost\n", "oldhost", "127.0.0.1 localhost\n127.0.1.1\tvm-x\n"},
		{"other hosts untouched, spaces kept", "192.168.1.2   nas  oldhost-nas\n127.0.0.1 localhost\n", "oldhost", "192.168.1.2   nas  oldhost-nas\n127.0.0.1 localhost\n127.0.1.1\tvm-x\n"},
		{"already correct", "127.0.0.1 localhost\n127.0.1.1\tvm-x\n", "vm-x", "127.0.0.1 localhost\n127.0.1.1\tvm-x\n"},
	}
	for _, c := range cases {
		if got := RewriteHosts(c.in, c.old, "vm-x"); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestProvisionVMGuestHostname(t *testing.T) {
	mnt := accRoot(t)
	os.WriteFile(filepath.Join(mnt, "etc/hostname"), []byte("distrobuilder-1234\n"), 0o644)
	os.WriteFile(filepath.Join(mnt, "etc/hosts"), []byte("127.0.0.1 localhost\n127.0.1.1 distrobuilder-1234\n"), 0o644)
	var out, warn bytes.Buffer
	fr := &recRunner{}
	ProvisionVMGuest(fr, mnt, config.Runtime{}, false, "vm-ubuntu-noble", &out, &warn)
	if rf(mnt, "etc/hostname") != "vm-ubuntu-noble\n" || rf(mnt, "etc/hosts") != "127.0.0.1 localhost\n127.0.1.1\tvm-ubuntu-noble\n" || strings.TrimSpace(rf(mnt, HostnameMarker)) != "vm-ubuntu-noble" {
		t.Errorf("hostname: %q %q", rf(mnt, "etc/hostname"), rf(mnt, "etc/hosts"))
	}
	if strings.Join(fr.cmds, "|") != "mount -o remount,rw "+mnt+"|sync" {
		t.Errorf("commands %v", fr.cmds)
	}
	fr2 := &recRunner{}
	ProvisionVMGuest(fr2, mnt, config.Runtime{}, false, "vm-ubuntu-noble", &out, &warn)
	if len(fr2.cmds) != 0 {
		t.Errorf("same name again touched the guest: %v", fr2.cmds)
	}
	ProvisionVMGuest(fr2, mnt, config.Runtime{}, false, "other", &out, &warn) // config changed -> applied again
	if rf(mnt, "etc/hostname") != "other\n" {
		t.Error("changed name not applied")
	}
	fr3 := &recRunner{}
	ProvisionVMGuest(fr3, mnt, config.Runtime{}, false, "", &out, &warn) // keep
	if len(fr3.cmds) != 0 || rf(mnt, "etc/hostname") != "other\n" {
		t.Error("keep touched the guest")
	}
}

func TestAdaptHostname(t *testing.T) {
	r := accRoot(t)
	systemdRoot(r)
	in := AdaptInput{Root: r, Image: "ct-Alpine.3.21", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none"}}
	if _, err := Adapt(in); err != nil {
		t.Fatal(err)
	}
	if rf(r, "etc/hostname") != "ct-alpine-3-21\n" || !strings.Contains(rf(r, "etc/hosts"), "127.0.1.1\tct-alpine-3-21") {
		t.Errorf("default hostname: %q %q", rf(r, "etc/hostname"), rf(r, "etc/hosts"))
	}
	in.Runtime.Hostname = "box"
	if _, err := Adapt(in); err != nil || rf(r, "etc/hostname") != "box\n" {
		t.Errorf("changed hostname not re-applied: %v %q", err, rf(r, "etc/hostname"))
	}
	in.KeepHostname = true
	os.WriteFile(filepath.Join(r, "etc/hostname"), []byte("guestown\n"), 0o644)
	if _, err := Adapt(in); err != nil || rf(r, "etc/hostname") != "guestown\n" {
		t.Errorf("keep: %v %q", err, rf(r, "etc/hostname"))
	}
}
