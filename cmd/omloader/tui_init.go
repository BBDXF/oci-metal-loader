package main

import (
	"os"
	"path/filepath"

	"omloader/internal/boot"
	"omloader/internal/config"
	"omloader/internal/tui"
)

// initFields loads the config + image and builds the prefilled init rows.
func (s *realSvc) initFields(image string) (*config.Config, []boot.InitField, error) {
	res, err := config.Load(s.cfgPath, filepath.Join(s.imageDir(image), "image.yaml"))
	if err != nil {
		return nil, nil, err
	}
	return &res.Config, boot.InitPrefill(&res.Config, boot.ImageHints{Templates: res.Image.LXDTemplates}, image), nil
}

// InitNeeded implements tui.Initer: the image has no confirmed first-boot init (and the policy is not skip/accept-defaults).
func (s *realSvc) InitNeeded(image string) bool {
	if kernelArg("omloader.init") == "accept" {
		return false
	}
	c, err := s.cfg()
	if err != nil || !fileExists(filepath.Join(s.imageDir(image), "image.yaml")) {
		return false // unknown image: the boot itself reports it
	}
	return !boot.InitReady(c.Guest.InitMode(), s.imageDir(image))
}

func (s *realSvc) InitPrefill(image string) []tui.InitRow {
	_, f, err := s.initFields(image)
	if err != nil {
		return nil
	}
	rows := make([]tui.InitRow, len(f))
	for i, x := range f {
		rows[i] = tui.InitRow{Key: x.Key, Value: x.Value, Source: x.Source}
	}
	return rows
}

func (s *realSvc) InitSave(image string, values []string) error {
	c, f, err := s.initFields(image)
	if err != nil {
		return err
	}
	ed := make([]boot.InitField, len(f))
	for i, x := range f {
		ed[i] = boot.InitField{Key: x.Key, Value: x.Value, Source: x.Source}
		if i < len(values) {
			ed[i].Value = values[i]
		}
	}
	return boot.SaveInit(s.imageDir(image), boot.MergeInit(f, ed, c.Clock.EffRTC()))
}

// loaderClock makes the loader itself (menu, logs) use guest.timezone and clock.rtc. Best effort: it never blocks the boot.
func loaderClock(c *config.Config) {
	tz := c.Guest.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	os.Setenv("TZ", tz)
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return
	}
	r := &boot.ExecRunner{}
	r.Run("timedatectl", "set-timezone", tz)
	rtc := "0"
	if c.Clock.EffRTC() == "localtime" {
		rtc = "1"
	}
	r.Run("timedatectl", "set-local-rtc", rtc)
}
