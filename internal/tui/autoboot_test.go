package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func autoModel(t *testing.T, timeout int) (*Model, *time.Time, *fakeSvc) {
	m, f := newModel(t, true)
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	m.Now = func() time.Time { return now }
	m.ArmAutoBoot(AutoBoot{Image: "alpine", Snapshot: "current", Timeout: timeout, Flags: []string{"poweroff_on_fail=1", "experimental=oci"}})
	return m, &now, f
}

func TestAutoBootCountdownBanner(t *testing.T) {
	m, now, _ := autoModel(t, 10)
	m.Tick()
	if m.AutoBootActive() {
		t.Fatal("must not start before the first frame hook")
	}
	m.StartAutoBoot()
	mustHave(t, m, "Booting alpine (current) in 10 s -- press any key to cancel")
	*now = now.Add(3500 * time.Millisecond)
	m.Tick()
	mustHave(t, m, "in 7 s")
	if m.Done() {
		t.Fatal("too early")
	}
	goldenFrame(t, "autoboot_banner", m)
}

func TestAutoBootMinimumAndExpiry(t *testing.T) {
	m, now, f := autoModel(t, 1) // below the minimum: 5 s
	m.StartAutoBoot()
	mustHave(t, m, "in 5 s")
	*now = now.Add(4900 * time.Millisecond)
	m.Tick()
	if m.Done() {
		t.Fatal("boot before the minimum time")
	}
	*now = now.Add(200 * time.Millisecond)
	m.Tick()
	r := m.Result()
	if !m.Done() || r.Kind != ResBoot || r.Boot.Image != "alpine" || r.Boot.Snapshot != "current" || r.Boot.Ephemeral {
		t.Fatalf("expiry: %+v done=%v", r, m.Done())
	}
	_ = f
	if r.Boot.Flags != "poweroff_on_fail=1 experimental=oci" {
		t.Fatalf("extra_cmdline_flags must reach the boot request: %+v", r.Boot.Flags)
	}
	if got := strings.Join(m.TakeMarks(), "\n"); !strings.Contains(got, "Booting 'alpine' (current) in 5 s") || !strings.Contains(got, "now") {
		t.Fatal(got)
	}
}

func TestAutoBootAnyKeyCancels(t *testing.T) {
	for _, k := range []Key{R('x'), N("esc"), N("enter"), N("down")} {
		m, now, _ := autoModel(t, 10)
		m.StartAutoBoot()
		m.TakeMarks()
		m.Update(k)
		if m.AutoBootActive() || m.Done() {
			t.Fatalf("%v: cancel failed", k)
		}
		if m.Screen != ScrImages || m.isel != 0 {
			t.Fatalf("%v: the key must be consumed", k)
		}
		mustHave(t, m, "Auto-boot cancelled.")
		if got := strings.Join(m.TakeMarks(), "\n"); got != "OMLOADER_LOADER: boot cancelled, opening the menu" {
			t.Fatal(got)
		}
		*now = now.Add(time.Minute)
		m.Tick()
		if m.Done() {
			t.Fatal("booted after cancel")
		}
		m.Update(R('j')) // normal TUI again
		if m.isel != 1 {
			t.Fatal("TUI not usable after cancel")
		}
	}
}

func TestAutoBootNotArmed(t *testing.T) {
	m, _ := newModel(t, true)
	m.StartAutoBoot()
	if m.AutoBootActive() {
		t.Fatal("nothing armed")
	}
}

type abSvc struct {
	*fakeSvc
	cancelled bool
}

func (a *abSvc) AutoBoot() *AutoBoot     { return nil }
func (a *abSvc) AutoBootCancelled() bool { return a.cancelled }
func (a *abSvc) AutoBootCancel()         { a.cancelled = true }

func TestAutoBootCancelledElsewhere(t *testing.T) {
	s := &abSvc{fakeSvc: newFake()}
	m := New(s, Theme{ASCII: true})
	now := time.Now()
	m.Now = func() time.Time { return now }
	m.Init()
	m.ArmAutoBoot(AutoBoot{Image: "alpine", Timeout: 10})
	m.StartAutoBoot()
	s.cancelled = true // key pressed on the other console (VNC vs serial)
	now = now.Add(time.Minute)
	m.Tick()
	if m.AutoBootActive() || m.Done() {
		t.Fatal("must cancel, not boot")
	}
	// local key cancel records the cancel for the other consoles
	s.cancelled = false
	m.ArmAutoBoot(AutoBoot{Image: "alpine", Timeout: 10})
	m.autoEnd = time.Time{}
	m.StartAutoBoot()
	m.Update(R('x'))
	if !s.cancelled {
		t.Fatal("cancel must be recorded")
	}
}

type dockerSvc struct {
	*fakeSvc
	pulled string
}

func (d *dockerSvc) Sources() ([]SourceInfo, error) {
	return []SourceInfo{{Name: "dockerhub", Type: "oci-registry", URL: "registry-1.docker.io", Usable: true, Mirror: "official", MirrorIDs: []string{"official", "auto", "1ms"}}}, nil
}
func (d *dockerSvc) PullDocker(ctx context.Context, source, image, name string, w io.Writer) error {
	d.pulled = source + "|" + image + "|" + name
	io.WriteString(w, "pull: ok\n")
	return nil
}

func TestPullDockerForm(t *testing.T) {
	s := &dockerSvc{fakeSvc: newFake()}
	m := New(s, Theme{ASCII: true})
	m.Init()
	m.Update(R('2'))
	m.Update(N("enter"))
	mustHave(t, m, "Pull Docker image (experimental)", "Image (e.g. nginx:alpine)")
	keys(m, "nginx:alpine\n\n")
	waitOp(t, m)
	if s.pulled != "dockerhub|nginx:alpine|" {
		t.Fatalf("pulled %q", s.pulled)
	}
}

func TestBootWithBusyboxShell(t *testing.T) {
	m, _ := newModel(t, true)
	m.Update(R(' ')) // image menu
	mustHave(t, m, "Boot with the busybox shell (this boot only)")
	for i := 0; i < 4; i++ {
		m.Update(N("down"))
	}
	m.Update(N("enter"))
	r := m.Result()
	if !m.Done() || r.Kind != ResBoot || r.Boot.Shell != "busybox" || r.Boot.Snapshot != "current" || r.Boot.Ephemeral {
		t.Fatalf("%+v done=%v", r, m.Done())
	}
}
