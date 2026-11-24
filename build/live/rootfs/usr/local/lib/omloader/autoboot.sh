#!/bin/bash
# omloader-autoboot.service: hook / selftest / automatic `omloader boot` (VM: kexec; container: systemctl switch-root).
export PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export DM_DISABLE_UDEV=1
CMD=$(cat /proc/cmdline)
arg() { local a; for a in $CMD; do case "$a" in "$1="*) echo "${a#*=}"; return;; esac; done; }
. /run/omloader/env 2>/dev/null
. /run/omloader/medium.env 2>/dev/null   # written by stage 1 when the live medium is an ISO file found by iso-scan (Ventoy etc.)
say() { echo "$@"; echo "$@" > /dev/tty1 2>/dev/null; }

# ---- boot config (no kernel parameters, e.g. under Ventoy): <data>/omloader/boot.yaml with boot.default_image ----
# Without omloader.image=/hook/selftest on the kernel command line, look for boot.yaml (omloader.yaml format) on VTOYDATA,
# OMLOADER or the partition holding the ISO. If boot.default_image is set: countdown boot.timeout s (ui=shell: here, any key = menu; ui=tui: the TUI shows the countdown, see /run/omloader/autoboot), then
# behave exactly as if omloader.image=<default_image> [omloader.snapshot=<default_snapshot>] [+ extra_cmdline_flags] was given.
# image recorded by the TUI at its last boot (first line of <data>/omloader/last-boot); empty when absent
last_boot_image() {
  local f
  for f in /mnt/images/omloader/last-boot /mnt/snap/omloader/last-boot "${MEDIUM_HOST_MNT:-/nonexistent}/omloader/last-boot"; do
    [ -s "$f" ] && { head -1 "$f"; return; }
  done
}
bootcfg_apply() {
  local f DI TO SN FL ans
  for f in /mnt/images/omloader/boot.yaml /mnt/images/omloader.yaml /mnt/snap/omloader/boot.yaml /mnt/snap/omloader.yaml \
           "${MEDIUM_HOST_MNT:-/nonexistent}/omloader/boot.yaml"; do
    [ -f "$f" ] || continue
    DI=$(omloader config get -c "$f" boot.default_image 2>/dev/null)
    if [ -z "$DI" ]; then
      omloader config validate -c "$f" >/dev/null 2>&1 || say "OMLOADER_LOADER: ignoring invalid boot config $f ($(omloader config validate -c "$f" 2>&1 | head -1))"
      continue
    fi
    TO=$(omloader config get -c "$f" boot.timeout 2>/dev/null); TO=${TO:-10}
    SN=$(omloader config get -c "$f" boot.default_snapshot 2>/dev/null)
    if [ "$DI" = last ]; then   # boot.default_image: last = what the TUI recorded at its last boot (<data>/omloader/last-boot: image, snapshot, ephemeral flag)
      DI=$(last_boot_image)
      if [ -z "$DI" ]; then say "OMLOADER_LOADER: boot config $f: default_image=last but nothing was booted yet (no last-boot file) -- menu"; continue; fi
      [ -z "$SN" ] && SN=current
    fi
    FL=$(omloader config get -c "$f" boot.extra_cmdline_flags 2>/dev/null | tr "\n" " ")
    if [ "$(cat /run/omloader/ui 2>/dev/null)" = tui ]; then
      # TUI mode: the TUI owns the countdown (banner, any key cancels; omloader tui reads /run/omloader/autoboot and creates
      # /run/omloader/autoboot.claimed). Nothing blocks here (no getty delay, no tty reads). Watchdog: if the TUI never claims it,
      # the shell path below boots after the countdown plus a grace period (autoboot.sh --fallback).
      printf 'IMAGE="%s"\nSNAPSHOT="%s"\nTIMEOUT="%s"\nFLAGS="%s"\nCONFIG="%s"\n' "$DI" "${SN:-current}" "$TO" "$FL" "$f" > /run/omloader/autoboot
      say "OMLOADER_LOADER: boot config $f: default_image=$DI snapshot=${SN:-current} timeout=${TO}s -- the TUI shows the countdown (any key there cancels)"
      setsid -f /usr/local/lib/omloader/autoboot.sh --fallback </dev/null >/dev/null 2>&1
      return 1
    fi
    say "OMLOADER_LOADER: boot config $f: default_image=$DI snapshot=${SN:-current} timeout=${TO}s -- press any key within ${TO}s for the menu instead"
    ( : </dev/ttyS0 ) 2>/dev/null && exec 3</dev/ttyS0 || exec 3</dev/null
    ( : </dev/tty1 ) 2>/dev/null && exec 4</dev/tty1 || exec 4</dev/null
    local end=$((SECONDS + TO)) last=
    while [ $SECONDS -lt $end ]; do
      [ "$last" != "$((end - SECONDS))" ] && { last=$((end - SECONDS)); say "OMLOADER_LOADER: Booting '$DI' (${SN:-current}) in ${last} s -- press any key for the menu"; }
      if read -r -n1 -t 0.3 -u 3 ans 2>/dev/null || read -r -n1 -t 0.2 -u 4 ans 2>/dev/null; then
        say "OMLOADER_LOADER: boot cancelled, opening the menu"; exit 0
      fi
      sleep 0.1
    done
    exec 3<&- 4<&-
    CMD="$CMD omloader.image=$DI"
    [ -n "$SN" ] && [ "$SN" != current ] && CMD="$CMD omloader.snapshot=$SN"
    for ans in $FL; do CMD="$CMD omloader.$ans"; done
    BOOTCFG=$f
    return 0
  done
  return 1
}
# ---- console UI: omloader.ui=tui|shell (kernel flag) > boot.ui of the boot config file > default tui. The TUI never starts when
# something else drives the console (omloader.image= / hook / selftest). The result is read by /etc/profile.d/omloader-banner.sh
# (-> tui-launch.sh) on tty1/ttyS*. /run/omloader/uicfg names the config file to pass to `omloader tui -c`.
decide_ui() {
  local ui f v
  ui=$(arg omloader.ui)
  for f in /mnt/images/omloader/boot.yaml /mnt/images/omloader.yaml /mnt/snap/omloader/boot.yaml /mnt/snap/omloader.yaml \
           "${MEDIUM_HOST_MNT:-/nonexistent}/omloader/boot.yaml"; do
    [ -f "$f" ] || continue
    omloader config validate -c "$f" >/dev/null 2>&1 || continue
    echo "$f" > /run/omloader/uicfg
    [ -z "$ui" ] && ui=$(omloader config get -c "$f" boot.ui 2>/dev/null)
    break
  done
  case "$ui" in tui|shell) ;; *) ui=tui;; esac
  if [ -n "$(arg omloader.image)" ] || [ "$(arg omloader.hook)" = 1 ] || [ "$(arg omloader.selftest)" = 1 ]; then ui=shell; fi
  echo "$ui" > /run/omloader/ui
}
find_label() { blkid -L "$1" 2>/dev/null; }
# clean shutdown of the loader's own partitions: flush, then remount read-only (marks the ext4 journal clean
# / clears the exfat dirty flag), then power off. systemd's normal poweroff does this by itself; these paths bypass it.
sync_ro() { sync; /usr/local/lib/omloader/mounts.sh stop >/dev/null 2>&1; for m in /mnt/snap /mnt/images; do mountpoint -q $m && mount -o remount,ro $m 2>/dev/null; done; sync; }
poweroff_now() { sync_ro; echo o > /proc/sysrq-trigger; sleep 5; }
mkdir -p /run/omloader
if [ "${1:-}" = --poweroff ]; then echo 1 > /proc/sys/kernel/sysrq; sync_ro; echo o > /proc/sysrq-trigger; sleep 5; exit 0; fi   # used by the TUI auto-boot (poweroff_on_fail=1)
if [ "${1:-}" = --fallback ]; then
  # watchdog started by bootcfg_apply in TUI mode: the TUI claims the auto-boot when it starts; if it never does, boot here
  say() { echo "$@"; }
  . /run/omloader/autoboot 2>/dev/null || exit 0
  eff=${TIMEOUT:-10}; case "$eff" in *[!0-9]*) eff=10;; esac; [ "$eff" -lt 5 ] && eff=5
  sleep $((eff + 30))
  [ -e /run/omloader/autoboot.claimed ] && exit 0
  say "OMLOADER_LOADER: the TUI did not start; falling back to the shell auto-boot of '$IMAGE' (${SNAPSHOT})"
  CMD="$CMD omloader.image=$IMAGE"
  [ -n "$SNAPSHOT" ] && [ "$SNAPSHOT" != current ] && CMD="$CMD omloader.snapshot=$SNAPSHOT"
  for ans in $FLAGS; do CMD="$CMD omloader.$ans"; done
  BOOTCFG=$CONFIG
else
  rm -f /run/omloader/uicfg /run/omloader/autoboot /run/omloader/autoboot.claimed /run/omloader/autoboot.cancelled
  decide_ui
  if [ -z "$(arg omloader.image)" ] && [ "$(arg omloader.hook)" != 1 ] && [ "$(arg omloader.selftest)" != 1 ]; then bootcfg_apply; fi
fi
# omloader.experimental=off -> switch the EXPERIMENTAL Docker/OCI image support off (it is on by default; =oci forces it on)
EXP=$(arg omloader.experimental); [ -n "$EXP" ] && export OML_EXPERIMENTAL="$EXP"
echo 1 > /proc/sys/kernel/sysrq

if [ "$(arg omloader.hook)" = 1 ]; then   # test helper: run /mnt/stage/hook.sh from an OMLSTAGE-labelled fs, then power off
  SD=$(find_label OMLSTAGE); mkdir -p /mnt/stage
  if [ -n "$SD" ] && mount -t ext4 "$SD" /mnt/stage; then sh /mnt/stage/hook.sh; echo "OMLOADER_HOOK_RC=$?"; else echo "OMLOADER_HOOK_RC=nostage"; fi
  sync; poweroff_now   # poweroff_now -> sync_ro -> mounts.sh stop (ordered umount: store ext4, loop, host fs)
fi

if [ "$(arg omloader.selftest)" = 1 ]; then
  grep -E ' /mnt/' /proc/mounts
  omloader 2>&1 | head -5
  echo OMLOADER_LOADER_SELFTEST_DONE
  poweroff_now
fi

IMG=$(arg omloader.image); SNAP=$(arg omloader.snapshot)
if [ -n "$IMG" ]; then
  echo "OMLOADER_LOADER: auto boot image=$IMG snapshot=${SNAP:-default}"
  set -- boot --image "$IMG" --images-root "${IMAGES_ROOT:-/mnt/images/images}" --snap-root /mnt/snap/snapshots
  [ -n "$SNAP" ] && set -- "$@" --snapshot "$SNAP"
  # the boot config file found by decide_ui (VTOYDATA/OMLOADER omloader/boot.yaml or omloader.yaml) is the global config: guest_defaults etc.
  [ -s /run/omloader/uicfg ] && set -- "$@" -c "$(cat /run/omloader/uicfg)"
  # no udev activity while loop/dm/kpartx devices are assembled and torn down (kexec path needs them closed)
  udevadm control --stop-exec-queue 2>/dev/null
  omloader "$@"
  RC=$?
  # container (oci / lxd-container) handoff: omloader mounted /newroot and wrote /run/omloader-switch -> switch-root.sh
  if [ "$RC" = 0 ] && [ -s /run/omloader-switch ]; then
    /usr/local/lib/omloader/switch-root.sh
    RC=$?
  fi
  udevadm control --start-exec-queue 2>/dev/null
  echo "OMLOADER_LOADER: 'omloader boot' returned rc=$RC (exec failed or no kexec)"
  if [ "$(arg omloader.poweroff_on_fail)" = 1 ]; then echo OMLOADER_LOADER_FAILED; poweroff_now; fi
fi
exit 0
