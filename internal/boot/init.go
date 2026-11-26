package boot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omloader/internal/config"
)

// InitFile is the confirmed first-boot init of an image: <images root>/<image>/init.json. It is shared by every snapshot of the
// image (they all descend from `base`); reverting to base removes it so the next boot asks again.
const InitFile = "init.json"

// InitMarker in the guest root records which init state was applied (so a later boot of the same layer does not rewrite it).
const InitMarker = "/.omloader-init"

// Init field keys.
const (
	FTimezone = "timezone"
	FHostname = "hostname"
	FLocale   = "locale"
	FKeymap   = "keymap"
	FUser     = "user"
	FPassword = "password"
)

// Source labels shown next to each field of the init form.
const (
	SrcImage   = "image"
	SrcDefault = "default config"
)

// InitField is one row of the init form.
type InitField struct{ Key, Value, Source string }

// InitState is the confirmed init. Keep lists the fields the image provides itself and the user left unchanged: they are not written.
type InitState struct {
	Timezone string   `json:"timezone"`
	Hostname string   `json:"hostname"`
	Locale   string   `json:"locale"`
	Keymap   string   `json:"keymap"`
	User     string   `json:"user"`
	Password string   `json:"password"`
	Keep     []string `json:"keep,omitempty"`
	RTC      string   `json:"rtc"` // utc | localtime
	Time     string   `json:"time,omitempty"`
}

func (s InitState) keep(k string) bool {
	for _, x := range s.Keep {
		if x == k {
			return true
		}
	}
	return false
}

// Hash fingerprints the state (without its timestamp).
func (s InitState) Hash() string {
	s.Time = ""
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// ImageHints says which of the init fields the image itself defines (from its LXD metadata.yaml templates, see imgimport).
type ImageHints struct {
	Templates []string // guest paths templated by the image: /etc/hostname, /var/lib/cloud/seed/..., ...
}

func (h ImageHints) has(prefix string) bool {
	for _, t := range h.Templates {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

// CloudInitHint reports whether the image templates cloud-init data.
func (h ImageHints) CloudInitHint() bool { return h.has("/var/lib/cloud/") || h.has("/etc/cloud/") }

// InitPrefill builds the form rows: the image's own settings (hints) first, then the guest.* / guest_defaults values.
func InitPrefill(c *config.Config, hints ImageHints, image string) []InitField {
	g := c.Guest
	or := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	host := or(g.Hostname, "{image}")
	if host == "{image}" || host == "image" {
		host = config.SanitizeHostname(image)
	}
	user, pw := "oci", "Oci123"
	if len(c.GuestDefaults.Users) > 0 {
		user, pw = c.GuestDefaults.Users[0].Name, c.GuestDefaults.Users[0].Password
	}
	src := func(own bool) string {
		if own {
			return SrcImage
		}
		return SrcDefault
	}
	return []InitField{
		{FTimezone, or(g.Timezone, "Asia/Shanghai"), src(hints.has("/etc/timezone") || hints.has("/etc/localtime"))},
		{FHostname, host, src(hints.has("/etc/hostname"))},
		{FLocale, or(g.Locale, "en_US.UTF-8"), src(hints.has("/etc/locale.conf") || hints.has("/etc/default/locale"))},
		{FKeymap, or(g.Keymap, "us"), src(hints.has("/etc/vconsole.conf") || hints.has("/etc/default/keyboard"))},
		{FUser, user, SrcDefault},
		{FPassword, pw, SrcDefault},
	}
}

// MergeInit turns the (possibly edited) form rows into the state: an unchanged row whose source is the image goes to Keep.
func MergeInit(prefill, edited []InitField, rtc string) InitState {
	st := InitState{RTC: rtc, Time: time.Now().UTC().Format(time.RFC3339)}
	for i, f := range prefill {
		v := f.Value
		if i < len(edited) {
			v = edited[i].Value
		}
		if f.Source == SrcImage && v == f.Value {
			st.Keep = append(st.Keep, f.Key)
		}
		switch f.Key {
		case FTimezone:
			st.Timezone = v
		case FHostname:
			st.Hostname = v
		case FLocale:
			st.Locale = v
		case FKeymap:
			st.Keymap = v
		case FUser:
			st.User = v
		case FPassword:
			st.Password = v
		}
	}
	return st
}

// InitFromFields is MergeInit without an edit: the prefilled values are accepted as they are.
func InitFromFields(f []InitField, rtc string) InitState { return MergeInit(f, nil, rtc) }

// LoadInit reads <imageDir>/init.json (nil, nil when the image is not initialised).
func LoadInit(imageDir string) (*InitState, error) {
	b, err := os.ReadFile(filepath.Join(imageDir, InitFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st InitState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(imageDir, InitFile), err)
	}
	return &st, nil
}

// SaveInit stores the confirmed init.
func SaveInit(imageDir string, st InitState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(filepath.Join(imageDir, InitFile), append(b, '\n'), 0o600)
}

// ClearInit forgets the init (reset to base); a missing file is fine.
func ClearInit(imageDir string) error {
	if err := os.Remove(filepath.Join(imageDir, InitFile)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// InitReady reports whether the image may boot unattended: it is initialised, or the policy is skip / accept-defaults.
func InitReady(mode, imageDir string) bool {
	if mode == "skip" || mode == "accept-defaults" {
		return true
	}
	st, _ := LoadInit(imageDir)
	return st != nil
}

// ApplyRuntime overrides the default accounts (not an image's own) with the user/password of the init.
func (s InitState) ApplyRuntime(rt config.Runtime) config.Runtime {
	if !rt.FromDefaults || s.User == "" {
		return rt
	}
	rt.Users = []config.User{{Name: s.User, Password: s.Password, Sudo: true, Groups: []string{"sudo"}}}
	if s.User != "root" {
		rt.RootPassword, rt.RootPasswordHash = s.Password, ""
	}
	if rt.AutologinUser != "" {
		rt.AutologinUser = s.User
	}
	return rt
}

// GuestCaps is what the guest root supports.
type GuestCaps struct{ CloudInit, Systemd bool }

// DetectGuest looks at the guest root.
func DetectGuest(root string, hints ImageHints) GuestCaps {
	ci := hints.CloudInitHint() || guestExists(root, "/etc/cloud/cloud.cfg") || guestExists(root, "/usr/bin/cloud-init")
	return GuestCaps{CloudInit: ci, Systemd: isSystemd(root)}
}

// NoCloudSeed renders the NoCloud user-data and meta-data for the state.
func NoCloudSeed(s InitState) (userData, metaData string) {
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	if !s.keep(FHostname) {
		fmt.Fprintf(&b, "hostname: %s\nmanage_etc_hosts: true\n", s.Hostname)
	}
	if !s.keep(FTimezone) {
		fmt.Fprintf(&b, "timezone: %s\n", s.Timezone)
	}
	if !s.keep(FLocale) {
		fmt.Fprintf(&b, "locale: %s\n", s.Locale)
	}
	if !s.keep(FKeymap) {
		fmt.Fprintf(&b, "keyboard:\n  layout: %s\n", s.Keymap)
	}
	return b.String(), fmt.Sprintf("instance-id: omloader-%s\nlocal-hostname: %s\n", s.Hash(), s.Hostname)
}

// ApplyInit writes the confirmed init into the guest root (idempotent: the marker holds the state hash). The hostname and the
// account are handled by the adapter / provisioner; this writes time zone, locale, keymap, the RTC mode and the cloud-init seed.
func ApplyInit(root string, s InitState, caps GuestCaps, warn io.Writer) error {
	if m, _ := readGuest(root, InitMarker); strings.TrimSpace(m) == s.Hash() {
		return nil
	}
	W := func(rel, data string) error { return guestWrite(root, rel, []byte(data), 0o644) }
	var errs []error
	do := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if !s.keep(FTimezone) && s.Timezone != "" {
		do(guestSymlink(root, "/etc/localtime", "/usr/share/zoneinfo/"+s.Timezone))
		do(W("/etc/timezone", s.Timezone+"\n"))
		if !caps.Systemd {
			do(W("/etc/TZ", s.Timezone+"\n"))
		}
	}
	if caps.Systemd {
		if !s.keep(FLocale) && s.Locale != "" {
			do(W("/etc/locale.conf", "LANG="+s.Locale+"\n"))
			do(W("/etc/default/locale", "LANG="+s.Locale+"\n"))
		}
		if !s.keep(FKeymap) && s.Keymap != "" {
			do(W("/etc/vconsole.conf", "KEYMAP="+s.Keymap+"\n"))
		}
	}
	mode := "UTC"
	if s.RTC == "localtime" {
		mode = "LOCAL"
	}
	do(W("/etc/adjtime", "0.0 0 0\n0\n"+mode+"\n"))
	if caps.CloudInit {
		ud, md := NoCloudSeed(s)
		do(W("/var/lib/cloud/seed/nocloud/user-data", ud))
		do(W("/var/lib/cloud/seed/nocloud/meta-data", md))
	}
	if len(errs) > 0 {
		return fmt.Errorf("first-boot init: %v", errs[0])
	}
	return guestWrite(root, InitMarker, []byte(s.Hash()+"\n"), 0o644)
}

// ErrNotInitialised is returned for an image without a confirmed first-boot init.
type ErrNotInitialised struct{ Image string }

func (e ErrNotInitialised) Error() string {
	return fmt.Sprintf("image %q is not initialised: confirm its first-boot settings in the TUI (omloader tui), or pass --accept-defaults / set guest.init: accept-defaults", e.Image)
}

// ResolveInit decides the init for a boot: skip -> (nil, true); a stored init.json; accept (flag, omloader.init=accept or
// guest.init: accept-defaults) -> the prefilled values, stored unless dry; else ErrNotInitialised.
func ResolveInit(c *config.Config, hints ImageHints, image, imageDir string, accept, dry bool) (*InitState, bool, error) {
	mode := c.Guest.InitMode()
	if mode == "skip" {
		return nil, true, nil
	}
	if st, err := LoadInit(imageDir); err != nil || st != nil {
		return st, false, err
	}
	if !accept && mode != "accept-defaults" {
		return nil, false, ErrNotInitialised{image}
	}
	st := InitFromFields(InitPrefill(c, hints, image), c.Clock.EffRTC())
	if !dry {
		if err := SaveInit(imageDir, st); err != nil {
			return &st, false, nil // read-only images root: apply for this boot only
		}
	}
	return &st, false, nil
}
