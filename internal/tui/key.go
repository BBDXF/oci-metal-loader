package tui

import (
	"strings"
	"unicode/utf8"
)

// Key is one keystroke: a printable rune (Name == "") or a named key.
type Key struct {
	Name string // up down left right enter esc tab btab bs del pgup pgdn home end ctrl-c ctrl-l ctrl-d f1
	Rune rune
}

// R makes a printable key (tests).
func R(r rune) Key { return Key{Rune: r} }

// N makes a named key (tests).
func N(name string) Key { return Key{Name: name} }

func (k Key) String() string {
	if k.Name != "" {
		return k.Name
	}
	return string(k.Rune)
}

var csiFinal = map[byte]string{'A': "up", 'B': "down", 'C': "right", 'D': "left", 'H': "home", 'F': "end", 'Z': "btab"}
var csiTilde = map[string]string{"1": "home", "7": "home", "4": "end", "8": "end", "3": "del", "5": "pgup", "6": "pgdn", "11": "f1"}

// ParseKeys decodes terminal input. rest holds an incomplete escape sequence (a lone ESC is returned in rest: the caller
// turns it into Esc after a short timeout, see FlushEsc).
func ParseKeys(b []byte) (keys []Key, rest []byte) {
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == 0x1b:
			if len(b) == 1 {
				return keys, b
			}
			switch b[1] {
			case '[':
				// CSI: params then a final byte 0x40..0x7e
				i := 2
				if i < len(b) && b[i] == '[' { // Linux console F1..F5: ESC [ [ A
					if i+1 >= len(b) {
						return keys, b
					}
					if b[i+1] == 'A' {
						keys = append(keys, N("f1"))
					}
					b = b[i+2:]
					continue
				}
				for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
					i++
				}
				if i >= len(b) {
					return keys, b
				}
				params, fin := string(b[2:i]), b[i]
				if fin == '~' {
					if n, ok := csiTilde[strings.Split(params, ";")[0]]; ok {
						keys = append(keys, N(n))
					}
				} else if n, ok := csiFinal[fin]; ok {
					keys = append(keys, N(n))
				}
				b = b[i+1:]
			case 'O':
				if len(b) < 3 {
					return keys, b
				}
				if n, ok := csiFinal[b[2]]; ok {
					keys = append(keys, N(n))
				} else if b[2] == 'P' {
					keys = append(keys, N("f1"))
				}
				b = b[3:]
			default: // ESC + char (alt): treat as Esc then the char
				keys = append(keys, N("esc"))
				b = b[1:]
			}
		case c == '\r' || c == '\n':
			keys = append(keys, N("enter"))
			b = b[1:]
			if len(b) > 0 && c == '\r' && b[0] == '\n' {
				b = b[1:]
			}
		case c == '\t':
			keys = append(keys, N("tab"))
			b = b[1:]
		case c == 0x7f || c == 0x08:
			keys = append(keys, N("bs"))
			b = b[1:]
		case c == 0x03:
			keys = append(keys, N("ctrl-c"))
			b = b[1:]
		case c == 0x0c:
			keys = append(keys, N("ctrl-l"))
			b = b[1:]
		case c == 0x04:
			keys = append(keys, N("ctrl-d"))
			b = b[1:]
		case c < 0x20:
			b = b[1:]
		default:
			if !utf8.FullRune(b) {
				return keys, b
			}
			r, n := utf8.DecodeRune(b)
			if r != utf8.RuneError {
				keys = append(keys, R(r))
			}
			b = b[n:]
		}
	}
	return keys, nil
}

// Keymap maps navigation actions to key names/runes. It can be overridden from a "k=v" list (e.g. OMLOADER_TUI_KEYS).
type Keymap map[string][]string

// DefaultKeymap returns the default navigation bindings.
func DefaultKeymap() Keymap {
	return Keymap{
		"up": {"up", "k"}, "down": {"down", "j"}, "pgup": {"pgup", "ctrl-b"}, "pgdn": {"pgdn", "ctrl-f"},
		"home": {"home", "g"}, "end": {"end", "G"}, "select": {"enter"}, "back": {"esc"},
		"next": {"tab"}, "prev": {"btab"}, "help": {"?", "f1"}, "cancel": {"ctrl-c"}, "redraw": {"ctrl-l"},
	}
}

// Override parses "action=key,key;action=key" and replaces those bindings.
func (km Keymap) Override(spec string) {
	for _, part := range strings.FieldsFunc(spec, func(r rune) bool { return r == ';' || r == ' ' }) {
		a, v, ok := strings.Cut(part, "=")
		if !ok || a == "" {
			continue
		}
		if _, known := km[a]; known && v != "" {
			km[a] = strings.Split(v, ",")
		}
	}
}

// Action returns the navigation action bound to k ("" if none).
func (km Keymap) Action(k Key) string {
	for a, keys := range km {
		for _, s := range keys {
			if s == k.String() {
				return a
			}
		}
	}
	return ""
}
