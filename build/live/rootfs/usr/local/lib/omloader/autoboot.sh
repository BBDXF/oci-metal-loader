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
# OMLOADER or the partition holding the ISO. If boot.default_image is set: countdown boot.timeout s (Enter = shell), then
# behave exactly as if omloader.image=<default_image> [omloader.snapshot=<default_snapshot>] [+ extra_cmdline_flags] was given.
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
    TO=$(omloader config get -c "$f" boot.timeout 2>/dev/null); TO=${TO:-5}
    SN=$(omloader config get -c "$f" boot.default_snapshot 2>/dev/null)
    FL=$(omloader config get -c "$f" boot.extra_cmdline_flags 2>/dev/null)
    say "OMLOADER_LOADER: boot config $f: default_image=$DI snapshot=${SN:-current} timeout=${TO}s -- press Enter within ${TO}s to get a shell instead"
    ( : </dev/ttyS0 ) 2>/dev/null && exec 3</dev/ttyS0 || exec 3</dev/null
    ( : </dev/tty1 ) 2>/dev/null && exec 4</dev/tty1 || exec 4</dev/null
    local end=$((SECONDS + TO)) last=
    while [ $SECONDS -lt $end ]; do
      [ "$last" != "$((end - SECONDS))" ] && { last=$((end - SECONDS)); say "OMLOADER_LOADER: booting '$DI' in ${last}s (Enter = shell)"; }
      if read -r -t 0.3 -u 3 ans 2>/dev/null || read -r -t 0.2 -u 4 ans 2>/dev/null; then
        say "OMLOADER_LOADER: boot cancelled, starting the shell"; exit 0
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
if [ -z "$(arg omloader.image)" ] && [ "$(arg omloader.hook)" != 1 ] && [ "$(arg omloader.selftest)" != 1 ]; then bootcfg_apply; fi
# omloader.experimental=oci -> opt in to the experimental Docker/OCI image support (disabled by default)
EXP=$(arg omloader.experimental); [ -n "$EXP" ] && export OML_EXPERIMENTAL="$EXP"
echo 1 > /proc/sys/kernel/sysrq
find_label() { blkid -L "$1" 2>/dev/null; }
# clean shutdown of the loader's own partitions: flush, then remount read-only (marks the ext4 journal clean
# / clears the exfat dirty flag), then power off. systemd's normal poweroff does this by itself; these paths bypass it.
sync_ro() { sync; /usr/local/lib/omloader/mounts.sh stop >/dev/null 2>&1; for m in /mnt/snap /mnt/images; do mountpoint -q $m && mount -o remount,ro $m 2>/dev/null; done; sync; }
poweroff_now() { sync_ro; echo o > /proc/sysrq-trigger; sleep 5; }

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
  # no udev activity while loop/dm/kpartx devices are assembled and torn down (kexec path needs them closed)
  udevadm control --stop-exec-queue 2>/dev/null
  omloader "$@"
  RC=$?
  # container (oci / lxd-container) handoff: omloader mounted /newroot and wrote /run/omloader-switch
  # (line 1 = new root, then init + args, one per line).
  if [ "$RC" = 0 ] && [ -s /run/omloader-switch ]; then
    mapfile -t SW < /run/omloader-switch
    NR=${SW[0]}; INIT=("${SW[@]:1}")
    rm -f /run/omloader-switch
    # systemd switch-root execs a *wrapper* so the new init starts fresh (no --deserialize of the live system's state)
    # and arbitrary init arguments / env are possible; the wrapper interpreter is the guest's own static busybox.
    if [ ! -x "$NR/.omloader/busybox" ] && BB=$(command -v busybox); then
      mkdir -p "$NR/.omloader" 2>/dev/null && cp "$BB" "$NR/.omloader/busybox" 2>/dev/null && chmod 755 "$NR/.omloader/busybox"
    fi
    {
      echo '#!/.omloader/busybox sh'
      printf 'exec'
      for a in "${INIT[@]}"; do printf " '%s'" "${a//\'/\'\\\'\'}"; done
      echo
    } > "$NR/.omloader/switch-init" 2>/dev/null && chmod 755 "$NR/.omloader/switch-init" && SWINIT=/.omloader/switch-init || SWINIT=${INIT[0]}
    # keep the loader partitions reachable (and cleanly shut-downable) inside the guest: the overlay upper/work live on
    # OMLOADER so it can never be unmounted while the root is mounted; the guest's shutdown hook (/.omloader/sync.sh)
    # remounts these read-only at poweroff. /.omloader/devs lists "<name> <device> <fstype>" for the hook.
    : > "$NR/.omloader/devs" 2>/dev/null
    for pair in "snap:$SNAPDEV:/mnt/snap" "images:$IMGDEV:/mnt/images"; do
      n=${pair%%:*}; r=${pair#*:}; dev=${r%%:*}; mp=${r#*:}
      if [ -n "$dev" ] && mountpoint -q "$mp"; then
        mkdir -p "$NR/.omloader/$n" && mount --bind "$mp" "$NR/.omloader/$n" \
          && echo "$n $dev $(blkid -o value -s TYPE "$dev")" >> "$NR/.omloader/devs"
      fi
    done
    mountpoint -q "$NR/run" || mount -t tmpfs -o mode=755 tmpfs "$NR/run"   # fresh /run for the new init (systemd moves /proc /sys /dev itself)
    echo "OMLOADER_LOADER: switch-root $NR ${INIT[*]} (loader kernel $(uname -r))"
    sync
    if systemctl switch-root "$NR" "$SWINIT"; then
      sleep 30   # PID 1 now kills everything and execs the new init; reaching the next line means it did not happen
      echo "OMLOADER_LOADER: systemctl switch-root was accepted but the system did not switch"
    else
      echo "OMLOADER_LOADER: systemctl switch-root failed"
    fi
    RC=127
  fi
  udevadm control --start-exec-queue 2>/dev/null
  echo "OMLOADER_LOADER: 'omloader boot' returned rc=$RC (exec failed or no kexec)"
  if [ "$(arg omloader.poweroff_on_fail)" = 1 ]; then echo OMLOADER_LOADER_FAILED; poweroff_now; fi
fi
exit 0
