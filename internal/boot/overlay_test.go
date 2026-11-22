package boot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omloader/internal/config"
	"omloader/internal/snapshot"
)

type ofx struct {
	t         *testing.T
	root, img string
	o         Options
	res       *config.Resolved
}

func osetup(t *testing.T, tree *snapshot.Tree, rtYAML string) *ofx {
	d := t.TempDir()
	f := &ofx{t: t, root: d}
	f.img = filepath.Join(d, "images", "c")
	os.MkdirAll(f.img, 0o755)
	os.WriteFile(filepath.Join(f.img, "base.squashfs"), []byte("sq"), 0o644)
	y := "name: c\ntype: oci\nformat: squashfs\nbase: base.squashfs\nsnapshots:\n  mode: overlayfs\n" + rtYAML
	os.WriteFile(filepath.Join(f.img, "image.yaml"), []byte(y), 0o644)
	snap := filepath.Join(d, "snaps")
	if tree != nil {
		os.MkdirAll(filepath.Join(snap, "c"), 0o755)
		tree.Save(filepath.Join(snap, "c", "tree.json"))
	}
	res, err := config.Load("", filepath.Join(f.img, "image.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	f.res = res
	nr := filepath.Join(d, "newroot")
	os.MkdirAll(nr, 0o755)
	f.o = Options{Image: "c", SnapRoot: snap, ImagesRoot: filepath.Join(d, "images"), NewRoot: nr,
		RunDir: filepath.Join(d, "run"), SwitchFile: filepath.Join(d, "run", "switch"), KernelRelease: "6.1.0-test",
		ModulesSrc: filepath.Join(d, "nomods"), Busybox: "/bin/true",
		SpaceFn: func(string) (uint64, uint64, error) { return 100, 90, nil }}
	return f
}

func systemdRoot(nr string) {
	for _, p := range []string{"lib/systemd/systemd", "lib/systemd/system/systemd-networkd.service", "bin/sh"} {
		os.MkdirAll(filepath.Dir(filepath.Join(nr, p)), 0o755)
		os.WriteFile(filepath.Join(nr, p), []byte("x"), 0o755)
	}
	os.MkdirAll(filepath.Join(nr, "sbin"), 0o755)
	os.Symlink("/lib/systemd/systemd", filepath.Join(nr, "sbin/init"))
	os.MkdirAll(filepath.Join(nr, "etc"), 0o755)
	os.Symlink("/proc/self/mounts", filepath.Join(nr, "etc/resolv.conf")) // must be replaced, not followed
}

func chainTree() *snapshot.Tree {
	t := snapshot.New("c", "")
	t.Create("a", "base")
	t.Create("b", "a")
	t.Create("c", "b")
	return t
}

func TestOverlayBootNewAndFrozen(t *testing.T) {
	f := osetup(t, chainTree(), "runtime:\n  hostname: hh\n  network: dhcp\n  autologin: true\n  env:\n    K: v\n")
	systemdRoot(f.o.NewRoot)
	f.o.Snapshot = "b"
	if _, err := BuildPlan(f.o, f.res, f.img); err == nil || !strings.Contains(err.Error(), "children") {
		t.Fatalf("non-leaf must be refused: %v", err)
	}
	f.o.Snapshot = "c"
	p, err := BuildPlan(f.o, f.res, f.img)
	if err != nil {
		t.Fatal(err)
	}
	r := &DryRunner{}
	if _, err := ExecuteOverlay(f.o, f.res, p, r); err == nil || !strings.Contains(err.Error(), "missing on disk") {
		t.Fatalf("missing ancestor layers must be refused: %v", err)
	}
	for _, n := range []string{"a", "b"} {
		os.MkdirAll(filepath.Join(f.o.SnapRoot, "c", n, "upper"), 0o755)
	}
	tiny := f.o
	tiny.SpaceFn = func(string) (uint64, uint64, error) { return 100, 5, nil }
	if _, err := ExecuteOverlay(tiny, f.res, p, r); err == nil || !strings.Contains(err.Error(), "space guard") {
		t.Fatalf("space guard: %v", err)
	}
	if len(r.Cmds) != 0 {
		t.Fatalf("nothing may run before the guards pass: %v", r.Cmds)
	}
	spec, err := ExecuteOverlay(f.o, f.res, p, r)
	if err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(r.Cmds, "\n")
	up := func(n string) string { return filepath.Join(f.o.SnapRoot, "c", n, "upper") }
	wantOpt := "lowerdir=" + up("b") + ":" + up("a") + ":" + filepath.Join(f.o.RunDir, "base") +
		",upperdir=" + up("c") + ",workdir=" + filepath.Join(f.o.SnapRoot, "c", "c", "work")
	if !strings.Contains(cmds, wantOpt) || !strings.Contains(cmds, "-t squashfs -o loop,ro") {
		t.Errorf("cmds:\n%s\nwant %s", cmds, wantOpt)
	}
	if spec.NewRoot != f.o.NewRoot || len(spec.Init) != 1 || spec.Init[0] != "/sbin/init" {
		t.Errorf("spec %+v", spec)
	}
	sw, _ := os.ReadFile(f.o.SwitchFile)
	if string(sw) != f.o.NewRoot+"\n/sbin/init\n" {
		t.Errorf("switch file %q", sw)
	}
	nr := f.o.NewRoot
	rd := func(p string) string { b, _ := os.ReadFile(filepath.Join(nr, p)); return string(b) }
	if rd("etc/hostname") != "hh\n" || !strings.Contains(rd("etc/fstab"), "omloader") || !strings.Contains(rd("etc/resolv.conf"), "nameserver") {
		t.Errorf("adapter files")
	}
	if fi, _ := os.Lstat(filepath.Join(nr, "etc/resolv.conf")); fi.Mode()&os.ModeSymlink != 0 {
		t.Error("resolv.conf symlink not replaced")
	}
	if !strings.Contains(rd("etc/systemd/network/80-omloader-dhcp.network"), "DHCP=ipv4") {
		t.Error("networkd file")
	}
	if l, err := os.Readlink(filepath.Join(nr, "etc/systemd/system/multi-user.target.wants/systemd-networkd.service")); err != nil || !strings.HasSuffix(l, "systemd-networkd.service") {
		t.Errorf("networkd not enabled: %v", err)
	}
	if !strings.Contains(rd("etc/systemd/system/serial-getty@ttyS0.service.d/omloader-autologin.conf"), "--autologin root") {
		t.Error("autologin")
	}
	if !strings.Contains(rd("etc/systemd/system.conf.d/omloader-env.conf"), `"K=v"`) {
		t.Error("env")
	}
	if rd(AdaptMarker) == "" {
		t.Fatal("no marker")
	}
	// second run: marker present => nothing reapplied
	os.WriteFile(filepath.Join(nr, "etc/hostname"), []byte("custom\n"), 0o644)
	if applied, err := Adapt(AdaptInput{Root: nr, Image: "c"}); err != nil || applied {
		t.Errorf("re-applied: %v %v", applied, err)
	}
	if rd("etc/hostname") != "custom\n" {
		t.Error("hostname overwritten")
	}
	if b, _ := os.ReadFile(f.o.SnapRoot + "/c/tree.json"); !strings.Contains(string(b), `"c"`) {
		t.Error("tree")
	}
}

func TestOverlayBaseEphemeralAndNew(t *testing.T) {
	f := osetup(t, nil, "")
	systemdRoot(f.o.NewRoot)
	f.o.Snapshot = "base"
	p, err := BuildPlan(f.o, f.res, f.img)
	if err != nil || !strings.HasPrefix(p.Target, "tmp-") || len(p.Layers) != 1 {
		t.Fatalf("%v %+v", err, p)
	}
	f.o.Snapshot = "new:run1"
	p, err = BuildPlan(f.o, f.res, f.img)
	if err != nil || p.Target != "run1" || p.Layers[0].Dir != filepath.Join(f.o.SnapRoot, "c", "run1") {
		t.Fatalf("%v %+v", err, p)
	}
}

func TestOverlayLimits(t *testing.T) {
	var ls []Layer
	for i := 0; i < 600; i++ {
		ls = append(ls, Layer{Dir: "/s/n" + strings.Repeat("x", 2)})
	}
	if _, err := OverlayOptions(ls, "/m"); err == nil || !strings.Contains(err.Error(), "flatten") {
		t.Errorf("want flatten hint: %v", err)
	}
	ls = nil
	for i := 0; i < 100; i++ {
		ls = append(ls, Layer{Dir: "/snapshots/" + strings.Repeat("n", 40)})
	}
	if _, err := OverlayOptions(ls, "/m"); err == nil || !strings.Contains(err.Error(), "flatten") {
		t.Errorf("want length error: %v", err)
	}
	if _, err := OverlayOptions([]Layer{{Dir: "/a,b"}}, "/m"); err == nil {
		t.Error("comma path must fail")
	}
}

func TestSelectInitWrapper(t *testing.T) {
	nr := t.TempDir()
	os.MkdirAll(filepath.Join(nr, "bin"), 0o755)
	os.WriteFile(filepath.Join(nr, "bin/sh"), []byte("x"), 0o755)
	os.WriteFile(filepath.Join(nr, "bin/app"), []byte("x"), 0o755)
	var w strings.Builder
	// no init, no entrypoint: /bin/sh via wrapper (+warning)
	if _, err := Adapt(AdaptInput{Root: nr, Image: "x", Busybox: "/bin/true", Runtime: config.Runtime{Network: "dhcp"}}); err != nil {
		t.Fatal(err)
	}
	cmd, err := SelectInit(nr, config.Runtime{}, &w)
	if err != nil || cmd[0] != "/.omloader/entrypoint" || cmd[1] != "/bin/sh" || !strings.Contains(w.String(), "/bin/sh") {
		t.Errorf("%v %v %q", cmd, err, w.String())
	}
	rt := config.Runtime{Entrypoint: []string{"/bin/app", "-x"}, Cmd: []string{"y"}, Env: map[string]string{"A": "it's"}, WorkingDir: "/srv"}
	cmd, err = SelectInit(nr, rt, nil)
	if err != nil || strings.Join(cmd, " ") != "/.omloader/entrypoint /bin/app -x y" {
		t.Errorf("%v %v", cmd, err)
	}
	env, _ := os.ReadFile(filepath.Join(nr, ".omloader/env"))
	if !strings.Contains(string(env), `export A='it'\''s'`) || !strings.Contains(string(env), "cd '/srv'") {
		t.Errorf("env %q", env)
	}
	for _, p := range []string{".omloader/net.sh", ".omloader/udhcpc.script", ".omloader/busybox"} {
		if _, err := os.Stat(filepath.Join(nr, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	if _, err := SelectInit(t.TempDir(), config.Runtime{}, nil); err == nil {
		t.Error("expected no-init error")
	}
}

func TestExportAndFlatten(t *testing.T) {
	f := osetup(t, chainTree(), "")
	snap := f.o.SnapRoot
	os.MkdirAll(filepath.Join(snap, "c", "b", "upper"), 0o755)
	r := &DryRunner{}
	out := filepath.Join(f.root, "b.squashfs")
	if err := ExportSquashfs(r, snap, "c", "b", out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.Cmds[0], "mksquashfs "+filepath.Join(snap, "c", "b", "upper")+" "+out) {
		t.Errorf("%v", r.Cmds)
	}
	if err := ExportSquashfs(r, snap, "c", "a", out); err == nil {
		t.Error("a has no upper")
	}
	// flatten: the fake creates the staged file when mksquashfs runs
	tmp := t.TempDir()
	r = &DryRunner{MntDir: tmp}
	r.Respond = func(line string) (string, bool) {
		if strings.HasPrefix(line, "mv ") {
			f := strings.Fields(line)
			os.MkdirAll(filepath.Dir(f[2]), 0o755)
			os.WriteFile(f[2], []byte("flat"), 0o644)
		}
		return "", false
	}
	d, err := FlattenOverlay(r, FlattenOptions{SnapRoot: snap, ImagesRoot: filepath.Join(f.root, "images"), Image: "c", Node: "b"})
	if err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(r.Cmds, "\n")
	up := func(n string) string { return filepath.Join(snap, "c", n, "upper") }
	if !strings.Contains(cmds, "lowerdir="+up("b")+":"+up("a")+":"+filepath.Join(tmp, "base")+" overlay") ||
		!strings.Contains(cmds, "mksquashfs "+filepath.Join(tmp, "merged")) {
		t.Errorf("cmds:\n%s", cmds)
	}
	res, err := config.Load("", filepath.Join(d, "image.yaml"))
	if err != nil || res.Image.Name != "c-flat-b" || res.Image.SHA256 == "" {
		t.Errorf("%v %+v", err, res)
	}
	// vm flatten is a TODO
	os.WriteFile(filepath.Join(f.img, "image.yaml"), []byte("name: c\ntype: vm\nformat: raw\nbase: b.raw\nsnapshots:\n  mode: dm-snapshot\n"), 0o644)
	if _, err := FlattenOverlay(&DryRunner{}, FlattenOptions{SnapRoot: snap, ImagesRoot: filepath.Join(f.root, "images"), Image: "c", Node: "b"}); err == nil || !strings.Contains(err.Error(), "TODO") {
		t.Errorf("%v", err)
	}
}
