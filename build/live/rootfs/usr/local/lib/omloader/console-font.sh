#!/bin/bash
# Linux console (tty1 / VNC / physical screen) font + UTF-8 mode. Never touches serial consoles.
#   console-font.sh apply [--force]   pick a Terminus font for the framebuffer size and load it on tty1..tty6 (idempotent)
#   console-font.sh pick W H          print the font name chosen for a W x H pixel framebuffer
# Override (first match wins):  kernel arg omloader.font=NAME|auto|default  >  <data>/omloader/console.conf (font=NAME)  >  auto
#   NAME = a file in /usr/share/consolefonts (without .psf.gz), `default` = keep the kernel's built-in font.
# Auto choice: the largest Uni3-Terminus font that still gives >= 100 columns x 30 rows, else >= 80x24, else the 8x16 font.
CF=${OMLOADER_CONSOLEFONTS:-/usr/share/consolefonts}
STATE=${OMLOADER_FONTSTATE:-/run/omloader/font}

# name width height (largest first)
FONTS="Uni3-TerminusBold32x16:16:32 Uni3-TerminusBold28x14:14:28 Uni3-TerminusBold24x12:12:24 Uni3-TerminusBold20x10:10:20 Uni3-TerminusBold16:8:16"

pick_font() { # W H -> font name (prints nothing when the size is unknown)
  local w=$1 h=$2 f n fw fh
  [ "${w:-0}" -gt 0 ] 2>/dev/null && [ "${h:-0}" -gt 0 ] 2>/dev/null || return 0
  for min in "100 30" "80 24"; do
    set -- $min
    for f in $FONTS; do
      IFS=: read -r n fw fh <<<"$f"
      [ -f "$CF/$n.psf.gz" ] || continue
      if [ $((w / fw)) -ge "$1" ] && [ $((h / fh)) -ge "$2" ]; then echo "$n"; return 0; fi
    done
  done
  echo Uni3-TerminusBold16
}

fb_size() { # "W H" of fb0, empty without a framebuffer
  local v
  v=$(cat /sys/class/graphics/fb0/virtual_size 2>/dev/null) || return 0
  echo "${v%,*} ${v#*,}"
}

cmdline_arg() { local a; for a in $(cat /proc/cmdline 2>/dev/null); do case "$a" in "$1="*) echo "${a#*=}"; return;; esac; done; }

conf_font() { # font=NAME from the boot-config directory of the data partitions
  local f v
  for f in /mnt/images/omloader/console.conf /mnt/snap/omloader/console.conf "${MEDIUM_HOST_MNT:-/nonexistent}/omloader/console.conf"; do
    [ -f "$f" ] || continue
    v=$(sed -n 's/^[[:space:]]*font[[:space:]]*=[[:space:]]*\([A-Za-z0-9_.-]*\).*/\1/p' "$f" | head -1)
    [ -n "$v" ] && { echo "$v"; return; }
  done
}

apply_font() {
  local force=$1 want size name t
  . /run/omloader/env 2>/dev/null
  want=$(cmdline_arg omloader.font); [ -n "$want" ] || want=$(conf_font); [ -n "$want" ] || want=auto
  [ "$want" = default ] && { echo default > "$STATE" 2>/dev/null; return 0; }
  size=$(fb_size)
  [ -n "$size" ] || return 0                       # no framebuffer (text mode / serial-only VM): nothing to do
  if [ "$want" = auto ]; then name=$(pick_font $size); else name=$want; fi
  [ -n "$name" ] || return 0
  if [ -f "$CF/$name.psf.gz" ] || [ -f "$CF/$name.psf" ]; then :; else echo "omloader: console font '$name' not found in $CF" >&2; return 1; fi
  [ "$force" = 1 ] || [ "$(cat "$STATE" 2>/dev/null)" != "$name" ] || return 0
  for t in 1 2 3 4 5 6; do
    [ -c /dev/tty$t ] || continue
    setfont -C /dev/tty$t "$name" 2>/dev/null
    printf '\033%%G' > /dev/tty$t 2>/dev/null     # UTF-8 mode (unicode_start without the keymap part)
    kbd_mode -u -C /dev/tty$t 2>/dev/null
  done
  mkdir -p "$(dirname "$STATE")" 2>/dev/null
  echo "$name" > "$STATE" 2>/dev/null
  echo "omloader: console font $name (framebuffer ${size/ /x})"
}

main() {
  case "$1" in
    pick) shift; pick_font "$@";;
    apply) apply_font "$([ "$2" = --force ] && echo 1 || echo 0)";;
    *) echo "usage: $0 apply [--force] | pick W H" >&2; return 2;;
  esac
}
[ "${BASH_SOURCE[0]}" = "$0" ] && main "$@"
