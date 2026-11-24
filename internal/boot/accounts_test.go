package boot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omloader/internal/config"
)

func TestSha512CryptVectors(t *testing.T) {
	for _, c := range []struct{ pw, set, want string }{
		{"Hello world!", "$6$saltstring", "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"Hello world!", "$6$rounds=10000$saltstringsaltstring", "$6$rounds=10000$saltstringsaltst$OW1/O6BYHV6BcXZu8QVeXbDWra3Oeqh0sbHbbMCVNSnCM/UrjmM0Dp8vOuZeHBy/YTBmSK6H9qs/y3RnOaw5v."},
		{"pässwörd 1", "$6$abcdefgh$whatever", "$6$abcdefgh$JS0C7qF90oW5YhCPGBVaDdqSktD6ZKUasub5AG5TZOmGaIKq0Q2racNetPqyrc2eqzHnXWf.vJZah3LZyj4w9/"},
	} {
		got, err := Sha512Crypt(c.pw, c.set)
		if err != nil || got != c.want {
			t.Errorf("%q %q: got %q err %v\nwant %q", c.pw, c.set, got, err, c.want)
		}
	}
	h, err := HashPassword("x")
	if err != nil || !strings.HasPrefix(h, "$6$") || len(h) != 3+16+1+86 {
		t.Errorf("HashPassword %q %v", h, err)
	}
	if h2, _ := Sha512Crypt("x", h); h2 != h {
		t.Error("verify round trip")
	}
}

func accRoot(t *testing.T) string {
	r := t.TempDir()
	w := func(p, c string, m os.FileMode) {
		os.MkdirAll(filepath.Dir(filepath.Join(r, p)), 0o755)
		os.WriteFile(filepath.Join(r, p), []byte(c), m)
	}
	w("etc/passwd", "root:x:0:0:root:/root:/bin/bash\nnobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin\n", 0o644)
	w("etc/group", "root:x:0:\nsudo:x:27:\nshadow:x:42:\nnogroup:x:65534:\n", 0o644)
	w("etc/shadow", "root:*:19000:0:99999:7:::\n", 0o640)
	w("etc/gshadow", "root:*::\nsudo:*::\n", 0o640)
	w("bin/bash", "x", 0o755)
	w("etc/skel/.profile", "# p\n", 0o644)
	return r
}

func rf(r, p string) string { b, _ := os.ReadFile(filepath.Join(r, p)); return string(b) }

func TestApplyAccounts(t *testing.T) {
	r := accRoot(t)
	rt := config.Runtime{
		RootPasswordHash: "$6$s$roothash",
		Users: []config.User{
			{Name: "alice", Password: "secret", Groups: []string{"sudo", "video"}, Sudo: true, SSHKeys: []string{"ssh-ed25519 AAAA alice@x"}},
			{Name: "bob", PasswordHash: "$6$s$bobhash", Shell: "/bin/sh"},
		},
	}
	var w strings.Builder
	if err := ApplyAccounts(r, rt, &w); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "plain-text password") || strings.Contains(w.String(), "secret") {
		t.Errorf("warning: %q", w.String())
	}
	pw := rf(r, "etc/passwd")
	if !strings.Contains(pw, "alice:x:1000:1000:alice:/home/alice:/bin/bash\n") || !strings.Contains(pw, "bob:x:1001:1001:bob:/home/bob:/bin/sh\n") {
		t.Errorf("passwd:\n%s", pw)
	}
	sh := rf(r, "etc/shadow")
	if !strings.Contains(sh, "root:$6$s$roothash:") || !strings.Contains(sh, "bob:$6$s$bobhash:") {
		t.Errorf("shadow:\n%s", sh)
	}
	var hash string
	for _, l := range strings.Split(sh, "\n") {
		if strings.HasPrefix(l, "alice:") {
			hash = strings.Split(l, ":")[1]
		}
	}
	if v, err := Sha512Crypt("secret", hash); err != nil || v != hash {
		t.Errorf("alice hash does not verify: %q", hash)
	}
	gr := rf(r, "etc/group")
	if !strings.Contains(gr, "sudo:x:27:alice\n") || !strings.Contains(gr, "alice:x:1000:\n") || !strings.Contains(gr, "video:x:999:alice\n") {
		t.Errorf("group:\n%s", gr)
	}
	if !strings.Contains(rf(r, "etc/gshadow"), "sudo:*::alice\n") || !strings.Contains(rf(r, "etc/gshadow"), "alice:!::") {
		t.Errorf("gshadow:\n%s", rf(r, "etc/gshadow"))
	}
	if fi, err := os.Stat(filepath.Join(r, "etc/shadow")); err != nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("shadow mode %v", fi.Mode())
	}
	if fi, err := os.Stat(filepath.Join(r, "etc/sudoers.d/90-omloader-alice")); err != nil || fi.Mode().Perm() != 0o440 {
		t.Errorf("sudoers %v %v", fi, err)
	}
	if s := rf(r, "etc/sudoers.d/90-omloader-alice"); s != "alice ALL=(ALL:ALL) ALL\n" {
		t.Errorf("sudoers %q", s)
	}
	if fi, err := os.Stat(filepath.Join(r, "home/alice/.ssh/authorized_keys")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("authorized_keys %v %v", fi, err)
	}
	if fi, _ := os.Stat(filepath.Join(r, "home/alice/.ssh")); fi.Mode().Perm() != 0o700 {
		t.Error(".ssh mode")
	}
	if rf(r, "home/alice/.profile") != "# p\n" {
		t.Error("skel")
	}
	// idempotent: applying again changes neither accounts nor groups (and keeps ids)
	before := [4]string{rf(r, "etc/passwd"), rf(r, "etc/group"), rf(r, "etc/gshadow"), rf(r, "home/alice/.ssh/authorized_keys")}
	rt.Users[0].Password, rt.Users[0].PasswordHash = "", hash
	if err := ApplyAccounts(r, rt, &w); err != nil {
		t.Fatal(err)
	}
	if before != [4]string{rf(r, "etc/passwd"), rf(r, "etc/group"), rf(r, "etc/gshadow"), rf(r, "home/alice/.ssh/authorized_keys")} {
		t.Error("second apply changed the account database")
	}
	if nl(rf(r, "etc/passwd"), "alice") != 1 || nl(rf(r, "etc/shadow"), "alice") != 1 {
		t.Error("duplicate alice")
	}
	// dropping sudo removes the drop-in
	rt.Users[0].Sudo = false
	ApplyAccounts(r, rt, &w)
	if _, err := os.Stat(filepath.Join(r, "etc/sudoers.d/90-omloader-alice")); err == nil {
		t.Error("sudoers drop-in must go away")
	}
}

func TestApplyAccountsPathSafety(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "passwd"), []byte("victim\n"), 0o644)
	r := accRoot(t)
	// /etc/passwd -> absolute symlink out of the root, /home -> ../../outside
	os.Remove(filepath.Join(r, "etc/passwd"))
	os.Symlink(filepath.Join(outside, "passwd"), filepath.Join(r, "etc/passwd"))
	os.Symlink("../../../../"+strings.TrimPrefix(outside, "/"), filepath.Join(r, "home"))
	os.Symlink(outside, filepath.Join(r, "etc/sudoers.d"))
	err := ApplyAccounts(r, config.Runtime{Users: []config.User{{Name: "eve", Sudo: true, SSHKeys: []string{"k"}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rf(outside, "passwd") != "victim\n" {
		t.Error("wrote through a symlink out of the root")
	}
	ents, _ := os.ReadDir(outside)
	if len(ents) != 1 {
		t.Errorf("files leaked outside the root: %v", ents)
	}
	if !strings.Contains(rf(r, "etc/passwd"), "eve:x:") {
		t.Error("passwd not written inside the root")
	}
}

func TestAdaptUIIdempotentAndReapply(t *testing.T) {
	r := accRoot(t)
	systemdRoot(r)
	in := AdaptInput{Root: r, Image: "c", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none", Autologin: boolp(true), AutologinUser: "alice",
		Users: []config.User{{Name: "alice", PasswordHash: "$6$a$b"}}}}
	if ok, err := Adapt(in); !ok || err != nil {
		t.Fatalf("first: %v %v", ok, err)
	}
	for _, p := range []string{"etc/systemd/system/getty@tty1.service.d/omloader-autologin.conf", "etc/systemd/system/serial-getty@ttyS0.service.d/omloader-autologin.conf"} {
		if !strings.Contains(rf(r, p), "--autologin alice") {
			t.Errorf("%s: %q", p, rf(r, p))
		}
	}
	for _, p := range []string{".omloader/sync.sh", "usr/lib/systemd/system-shutdown/omloader-sync.sh"} {
		if fi, err := os.Stat(filepath.Join(r, p)); err != nil || fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s: %v", p, err)
		}
	}
	if !strings.Contains(rf(r, "etc/systemd/system/omloader-sync.service"), "ExecStop=/.omloader/sync.sh") {
		t.Error("sync unit")
	}
	if !strings.Contains(rf(r, AdaptMarker), "ui-config=") {
		t.Error("marker has no config hash")
	}
	if ok, err := Adapt(in); ok || err != nil {
		t.Fatalf("second identical run must be a no-op: %v %v", ok, err)
	}
	if nl(rf(r, "etc/passwd"), "alice") != 1 {
		t.Error("duplicate")
	}
	in.Runtime.Users = append(in.Runtime.Users, config.User{Name: "carol"})
	in.Runtime.Autologin = boolp(false)
	if ok, err := Adapt(in); !ok || err != nil {
		t.Fatalf("changed config must re-apply: %v %v", ok, err)
	}
	if nl(rf(r, "etc/passwd"), "alice") != 1 || !strings.Contains(rf(r, "etc/passwd"), "carol:") {
		t.Error("accounts after re-apply")
	}
	if _, err := os.Lstat(filepath.Join(r, "etc/systemd/system/getty@tty1.service.d/omloader-autologin.conf")); err == nil {
		t.Error("autologin drop-in must be removed")
	}
	f := false
	in.Runtime.ShutdownSync = &f
	Adapt(in)
	if _, err := os.Lstat(filepath.Join(r, "etc/systemd/system/omloader-sync.service")); err == nil {
		t.Error("shutdown_sync=false must remove the hook")
	}
}

func TestDebugShellFallback(t *testing.T) {
	// systemd guest without getty/login: unit
	r := t.TempDir()
	for _, p := range []string{"lib/systemd/systemd"} {
		os.MkdirAll(filepath.Dir(filepath.Join(r, p)), 0o755)
		os.WriteFile(filepath.Join(r, p), []byte("x"), 0o755)
	}
	var w strings.Builder
	if _, err := Adapt(AdaptInput{Root: r, Image: "c", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none"}, Warn: &w}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rf(r, "etc/systemd/system/omloader-debug-shell.service"), "/.omloader/dbgshell") {
		t.Errorf("no debug shell unit; warnings: %s", w.String())
	}
	// debug_shell: never
	r2 := t.TempDir()
	os.MkdirAll(filepath.Join(r2, "lib/systemd"), 0o755)
	os.WriteFile(filepath.Join(r2, "lib/systemd/systemd"), []byte("x"), 0o755)
	Adapt(AdaptInput{Root: r2, Image: "c", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none", DebugShell: "never"}})
	if _, err := os.Lstat(filepath.Join(r2, "etc/systemd/system/omloader-debug-shell.service")); err == nil {
		t.Error("never must not install the unit")
	}
	// busybox-style init: inittab line
	r3 := t.TempDir()
	for _, p := range []string{"sbin/init", "etc/inittab"} {
		os.MkdirAll(filepath.Dir(filepath.Join(r3, p)), 0o755)
		os.WriteFile(filepath.Join(r3, p), []byte("::sysinit:/etc/rcS\n"), 0o755)
	}
	Adapt(AdaptInput{Root: r3, Image: "c", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none"}})
	it := rf(r3, "etc/inittab")
	if !strings.Contains(it, "::respawn:-/.omloader/dbgshell\n") || !strings.Contains(it, "::shutdown:/.omloader/sync.sh\n") {
		t.Errorf("inittab:\n%s", it)
	}
	// a guest with busybox + login keeps its own getty: no debug shell
	r4 := t.TempDir()
	os.MkdirAll(filepath.Join(r4, "lib/systemd"), 0o755)
	os.WriteFile(filepath.Join(r4, "lib/systemd/systemd"), []byte("x"), 0o755)
	for _, p := range []string{"sbin/agetty", "bin/login"} {
		os.MkdirAll(filepath.Dir(filepath.Join(r4, p)), 0o755)
		os.WriteFile(filepath.Join(r4, p), []byte("x"), 0o755)
	}
	Adapt(AdaptInput{Root: r4, Image: "c", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none"}})
	if _, err := os.Lstat(filepath.Join(r4, "etc/systemd/system/omloader-debug-shell.service")); err == nil {
		t.Error("auto must not add a shell when getty+login exist")
	}
}

func nl(data, name string) int {
	n := 0
	for _, l := range strings.Split(data, "\n") {
		if strings.HasPrefix(l, name+":") {
			n++
		}
	}
	return n
}

func boolp(b bool) *bool { return &b }

func TestAdaptBusyboxAutologin(t *testing.T) {
	r := accRoot(t)
	mk := func(rel, data string) {
		if err := guestWrite(r, rel, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("/bin/busybox", "x")
	mk("/sbin/init", "x")
	mk("/etc/inittab", "::sysinit:/sbin/openrc sysinit\ntty1::respawn:/sbin/getty 38400 tty1\nttyS0::respawn:/sbin/getty -L 0 ttyS0 vt100\n::respawn:/sbin/getty 38400 console\n# tty1 getty\n")
	in := AdaptInput{Root: r, Image: "c", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none", Autologin: boolp(true), AutologinUser: "oci",
		Users: []config.User{{Name: "oci", Password: "Oci123", Sudo: true}}}}
	if _, err := Adapt(in); err != nil {
		t.Fatal(err)
	}
	it, _ := readGuest(r, "/etc/inittab")
	if strings.Count(it, "getty -n -l /.omloader/autologin") != 3 || !strings.Contains(it, "# tty1 getty\n") {
		t.Errorf("inittab: %q", it)
	}
	if al, _ := readGuest(r, "/.omloader/autologin"); !strings.Contains(al, "login -f oci") {
		t.Errorf("autologin script: %q", al)
	}
	in.Runtime.Autologin = boolp(false)
	if _, err := Adapt(in); err != nil {
		t.Fatal(err)
	}
	if it, _ = readGuest(r, "/etc/inittab"); strings.Contains(it, "autologin") {
		t.Errorf("autologin not reverted: %q", it)
	}
}

func TestConsoleShellPolicy(t *testing.T) {
	mk := func(files ...string) string {
		r := t.TempDir()
		for _, p := range files {
			os.MkdirAll(filepath.Dir(filepath.Join(r, p)), 0o755)
			os.WriteFile(filepath.Join(r, p), []byte("x"), 0o755)
		}
		return r
	}
	launcher := func(r string) string { return rf(r, ".omloader/dbgshell") }
	// entrypoint-only image WITHOUT a shell (distroless style): auto injects busybox, image has nothing to offer
	r := mk("docker-entrypoint.sh")
	Adapt(AdaptInput{Root: r, Image: "c", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none"}})
	if !strings.Contains(launcher(r), "exec /.omloader/busybox sh") {
		t.Errorf("auto, no image shell: busybox expected:\n%s", launcher(r))
	}
	if st, err := os.Stat(filepath.Join(r, ".omloader/busybox")); err != nil || st.Size() != 0 {
		t.Error("only an empty placeholder may land in the layer (the binary is bind-mounted at boot)")
	}
	// the same with shell: image -> no shell, warning
	r = mk("docker-entrypoint.sh")
	var w strings.Builder
	Adapt(AdaptInput{Root: r, Image: "c", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none", Shell: "image"}, Warn: &w})
	if launcher(r) != "" || !strings.Contains(w.String(), "no console shell available") {
		t.Errorf("shell: image without an image shell: %q %s", launcher(r), w.String())
	}
	// entrypoint-only image WITH a shell (nginx:alpine): auto uses the image's own shell, busybox forces busybox
	r = mk("bin/sh", "docker-entrypoint.sh")
	Adapt(AdaptInput{Root: r, Image: "c", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none"}})
	if !strings.Contains(launcher(r), "exec /bin/sh\n") {
		t.Errorf("auto with an image shell:\n%s", launcher(r))
	}
	r = mk("bin/sh", "docker-entrypoint.sh")
	Adapt(AdaptInput{Root: r, Image: "c", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none", Shell: "busybox"}})
	if !strings.Contains(launcher(r), "exec /.omloader/busybox sh") {
		t.Errorf("shell: busybox must force busybox:\n%s", launcher(r))
	}
	// the wrapper starts the shell on /dev/console and tty1
	if err := WriteWrapperEnv(r, config.Runtime{}); err != nil {
		t.Fatal(err)
	}
	env := rf(r, ".omloader/env")
	if !strings.Contains(env, "/.omloader/dbgshell </dev/console") || !strings.Contains(env, "/dev/tty1") {
		t.Errorf("wrapper env:\n%s", env)
	}
	// legacy debug_shell: always == shell: busybox
	if (config.Runtime{DebugShell: "always"}).ShellMode() != "busybox" || (config.Runtime{DebugShell: "never", Shell: "auto"}).ShellMode() != "auto" {
		t.Error("ShellMode mapping")
	}
}

func TestSerialGettyWithoutUdev(t *testing.T) {
	r := t.TempDir()
	for _, p := range []string{"lib/systemd/systemd", "lib/systemd/system/getty@.service", "lib/systemd/system/getty.target", "sbin/agetty", "bin/login"} {
		os.MkdirAll(filepath.Dir(filepath.Join(r, p)), 0o755)
		os.WriteFile(filepath.Join(r, p), []byte("x"), 0o755)
	}
	Adapt(AdaptInput{Root: r, Image: "c", Busybox: "/bin/true", Runtime: config.Runtime{Network: "none", Autologin: boolp(true)}})
	u := rf(r, "etc/systemd/system/omloader-serial-getty.service")
	if !strings.Contains(u, "--autologin root") || !strings.Contains(u, "ttyS0") || strings.Contains(u, "dev-") {
		t.Errorf("serial getty unit for udev-less images:\n%s", u)
	}
	if l, _ := os.Readlink(filepath.Join(r, "etc/systemd/system/serial-getty@ttyS0.service")); l != "/dev/null" {
		t.Error("the device-bound serial-getty@ttyS0 must be masked")
	}
	if _, err := os.Lstat(filepath.Join(r, "etc/systemd/system/multi-user.target.wants/getty.target")); err != nil {
		t.Error("getty.target must be pulled in (empty multi-user.target.wants in minimal images)")
	}
}
