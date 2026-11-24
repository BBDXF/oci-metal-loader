#!/bin/bash
# Container handoff (oci / lxd-container): `omloader boot` (also when started from the TUI) mounted /newroot and wrote
# /run/omloader-switch (line 1 = new root, then init + args, one per line). This script switches the system root with
# `systemctl switch-root`. Called by autoboot.sh (omloader.image=) and tui-launch.sh (boot from the TUI).
# Returns 1 when there is nothing to do and 127 when the switch did not happen.
export PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
. /run/omloader/env 2>/dev/null
[ -s /run/omloader-switch ] || exit 1
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
# systemd >= 255 (Debian 13) refuses switch-root outside an initrd, in both client and PID 1 (cached at boot): flag the
# live system as "initrd" and re-exec PID 1 so it re-reads it; the client honours $SYSTEMD_IN_INITRD.
: > /etc/initrd-release && systemctl daemon-reexec && sleep 1
if SYSTEMD_IN_INITRD=1 systemctl switch-root "$NR" "$SWINIT"; then
  sleep 30   # PID 1 now kills everything and execs the new init; reaching the next line means it did not happen
  echo "OMLOADER_LOADER: systemctl switch-root was accepted but the system did not switch"
else
  echo "OMLOADER_LOADER: systemctl switch-root failed"
fi
exit 127
