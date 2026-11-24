#!/bin/bash
# Optional user firmware: <data>/omloader/firmware/ (same paths as /lib/firmware) is overlaid read-only on /lib/firmware,
# then drivers that logged "Direct firmware load for X failed" are reloaded once. Idempotent, never fatal, silent if absent.
# GPU drivers (amdgpu i915 nouveau xe radeon) are never reloaded: their firmware must be bundled in the ISO (needed at early boot).
D=${1:-/mnt/images/omloader/firmware}
[ -d "$D" ] || exit 0
N=$(find "$D" -type f 2>/dev/null | wc -l)
[ "$N" -gt 0 ] || exit 0
echo "OMLOADER_LOADER: user firmware: $N files from $D"
if ! grep -q ' /lib/firmware overlay ' /proc/mounts; then
  mount -t overlay overlay -o "lowerdir=$D:/lib/firmware" /lib/firmware 2>/dev/null ||
    { echo "OMLOADER_LOADER: firmware overlay failed, copying into /lib/firmware"; cp -rn "$D"/. /lib/firmware/ 2>&1 | head -2; echo -n "$D" > /sys/module/firmware_class/parameters/path 2>/dev/null; }
fi
# modules whose firmware load failed: "iwlwifi 0000:00:14.3: Direct firmware load for iwlwifi-... failed"
for m in $(dmesg 2>/dev/null | grep 'Direct firmware load for' | sed -n 's/^\[[^]]*\] \([A-Za-z0-9_-]*\)[ :].*/\1/p' | sort -u); do
  case "$m" in amdgpu|i915|nouveau|xe|radeon|nvidia*|simpledrm|efifb) continue;; esac
  lsmod | grep -q "^${m//-/_} " || continue
  echo "OMLOADER_LOADER: firmware: reloading $m"
  { modprobe -r "$m" && modprobe "$m"; } 2>&1 | head -2 || true
done
exit 0
