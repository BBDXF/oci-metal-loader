# Sourced (not executed) by /etc/profile.d/omloader-banner.sh in the autologin login shell on tty1 / ttyS* when
# /run/omloader/ui says "tui". Runs `omloader tui` in the foreground of this console; the shell (and its banner) follows when
# the TUI is left with "shell". Exiting that shell makes getty log in again, which shows the TUI again.
#   exit 0 + /run/omloader-switch  -> container boot prepared: switch-root.sh does `systemctl switch-root` (never returns)
#   VM boot                        -> `omloader boot` ends in kexec -e inside the TUI process (never returns)
#   exit 10                        -> user asked for the shell
omloader_tui_main() {
  local a
  . /run/omloader/env 2>/dev/null
  for a in $(cat /proc/cmdline); do case "$a" in omloader.experimental=*) export OML_EXPERIMENTAL="${a#*=}";; esac; done
  export DM_DISABLE_UDEV=1
  # tty1 / VNC / screen only (never serial): size-matched Terminus font + UTF-8 (the boot unit did it already; this re-applies
  # when omloader.font= / console.conf only became readable after the data partitions were mounted), then UTF-8 + 16/256 colours.
  case "$(tty 2>/dev/null)" in
    /dev/tty[0-9]*)
      /usr/local/lib/omloader/console-font.sh apply
      if [ "$(cat /run/omloader/font 2>/dev/null)" != default ] && [ -s /run/omloader/font ]; then export LC_CTYPE=C.UTF-8; fi;;
  esac
  local args=(tui --images-root "${IMAGES_ROOT:-/mnt/images/images}" --snap-root /mnt/snap/snapshots)
  [ -s /run/omloader/uicfg ] && args+=(-c "$(cat /run/omloader/uicfg)")
  omloader "${args[@]}"
  local rc=$?
  if [ "$rc" = 0 ] && [ -s /run/omloader-switch ]; then
    /usr/local/lib/omloader/switch-root.sh
    echo "OMLOADER_LOADER: container handoff from the TUI failed (rc=$?); shell follows"
  fi
}
omloader_tui_main
unset -f omloader_tui_main
