package tui

import "testing"

type initSvc struct {
	*fakeSvc
	needed bool
	saved  []string
}

func (s *initSvc) InitNeeded(string) bool { return s.needed }
func (s *initSvc) InitPrefill(string) []InitRow {
	return []InitRow{{"timezone", "Asia/Shanghai", "default config"}, {"hostname", "alpine", "image"}}
}
func (s *initSvc) InitSave(_ string, v []string) error { s.saved = v; s.needed = false; return nil }

func TestInitFormBlocksBootAndAutoBoot(t *testing.T) {
	s := &initSvc{fakeSvc: newFake(), needed: true}
	m := New(s, Theme{})
	m.Init()
	m.ArmAutoBoot(AutoBoot{Image: "alpine", Timeout: 5})
	m.StartAutoBoot()
	if m.AutoBootActive() || m.dlg == nil || m.dlg.Kind != dkForm {
		t.Fatalf("expected the init form instead of a countdown (dlg=%v)", m.dlg)
	}
	if m.dlg.Labels[1] != "hostname [image]" || m.Done() {
		t.Errorf("labels %v", m.dlg.Labels)
	}
	keys(m, "\n\n") // confirm both rows
	if !m.Done() || m.Result().Kind != ResBoot || m.Result().Boot.Image != "alpine" {
		t.Fatalf("no boot after confirm: %+v", m.Result())
	}
	if len(s.saved) != 2 || s.saved[0] != "Asia/Shanghai" {
		t.Errorf("saved %v", s.saved)
	}
	// initialised image: countdown runs
	m2 := New(s, Theme{})
	m2.Init()
	m2.ArmAutoBoot(AutoBoot{Image: "alpine", Timeout: 5})
	m2.StartAutoBoot()
	if !m2.AutoBootActive() {
		t.Error("countdown must run for an initialised image")
	}
}
