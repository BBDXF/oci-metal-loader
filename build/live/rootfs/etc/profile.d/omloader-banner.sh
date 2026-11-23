# omloader live shell banner (interactive login shells only)
case $- in *i*) ;; *) return 0 2>/dev/null || exit 0;; esac
export PS1='omloader# '
# console TUI (boot.ui: tui, see docs/tui.md): on the first login of tty1 / serial consoles, before the shell banner.
# autoboot.sh writes the decision to /run/omloader/ui (omloader.ui= kernel flag > boot.yaml > default tui; image=/hook/selftest -> shell).
if [ -z "$OMLOADER_TUI_STARTED" ] && [ -z "$SSH_TTY" ] && [ "$(cat /run/omloader/ui 2>/dev/null)" = tui ]; then
  case "$(tty 2>/dev/null)" in
    /dev/tty1|/dev/ttyS[0-9]|/dev/hvc0) export OMLOADER_TUI_STARTED=1; . /usr/local/lib/omloader/tui-launch.sh;;
  esac
fi
if [ -z "$OMLOADER_BANNER_SHOWN" ]; then
  export OMLOADER_BANNER_SHOWN=1
  . /run/omloader/env 2>/dev/null
  cat <<BANNER

=== OCI Metal Loader (omloader) live shell ===
  omloader tui   (interactive UI; exit the shell to get it back)    boot.ui: shell / omloader.ui=shell disables it
  omloader boot --image <name> [--snapshot <n>] --images-root ${IMAGES_ROOT:-/mnt/images/images} --snap-root /mnt/snap/snapshots [--dry-run]
  omloader snapshot list|create|revert|delete|gc|prune|verify|flatten ...   (omloader snapshot for all operations)
  mounts: /mnt/snap (OMLOADER) /mnt/images (VTOYDATA)    network: networkctl / resolvectl     poweroff: poweroff -f
===============================================
BANNER
  echo "OMLOADER_SHELL_READY uptime=$(cut -d' ' -f1 /proc/uptime)"
fi
