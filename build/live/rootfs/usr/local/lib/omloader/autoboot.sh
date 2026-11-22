#!/bin/bash
# omloader-autoboot.service: hook / selftest / automatic `omloader boot` (VM: kexec; container: systemctl switch-root).
export PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export DM_DISABLE_UDEV=1
CMD=$(cat /proc/cmdline)
arg() { local a; for a in $CMD; do case "$a" in "$1="*) echo "${a#*=}"; return;; esac; done; }
. /run/omloader/env 2>/dev/null
# omloader.experimental=oci -> opt in to the experimental Docker/OCI image support (disabled by default)
EXP=$(arg omloader.experimental); [ -n "$EXP" ] && export OML_EXPERIMENTAL="$EXP"
echo 1 > /proc/sys/kernel/sysrq
find_label() { blkid -L "$1" 2>/dev/null; }
poweroff_now() { sync; echo o > /proc/sysrq-trigger; sleep 5; }

if [ "$(arg omloader.hook)" = 1 ]; then   # test helper: run /mnt/stage/hook.sh from an OMLSTAGE-labelled fs, then power off
  SD=$(find_label OMLSTAGE); mkdir -p /mnt/stage
  if [ -n "$SD" ] && mount -t ext4 "$SD" /mnt/stage; then sh /mnt/stage/hook.sh; echo "OMLOADER_HOOK_RC=$?"; else echo "OMLOADER_HOOK_RC=nostage"; fi
  sync; umount /mnt/images 2>/dev/null; umount /mnt/snap 2>/dev/null; poweroff_now
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
