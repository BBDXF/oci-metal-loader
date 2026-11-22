#!/bin/bash
# omloader-mounts.service: find and mount the OMLOADER (ext4) and VTOYDATA partitions, write /run/omloader/env.
CMD=$(cat /proc/cmdline)
arg() { local a; for a in $CMD; do case "$a" in "$1="*) echo "${a#*=}"; return;; esac; done; }
echo "OMLOADER_LOADER: starting (kernel $(uname -r))"
mkdir -p /mnt/snap /mnt/images /run/omloader
# Only wait (<= 15 s) when the command line asks for an automatic action; otherwise a plain shell must not be delayed.
MAXTRY=1
for k in omloader.image omloader.hook omloader.selftest; do [ -n "$(arg $k)" ] && MAXTRY=30; done
SNAPDEV=; IMGDEV=; i=0
while :; do
  [ -z "$SNAPDEV" ] && SNAPDEV=$(blkid -L OMLOADER 2>/dev/null)
  [ -z "$IMGDEV" ] && IMGDEV=$(blkid -L VTOYDATA 2>/dev/null)
  [ -n "$SNAPDEV" ] && [ -n "$IMGDEV" ] && break
  i=$((i+1)); [ $i -ge $MAXTRY ] && break
  sleep 0.5
done
SNAPOK=0
if [ -n "$SNAPDEV" ]; then mount -t ext4 -o rw "$SNAPDEV" /mnt/snap && SNAPOK=1; fi
IMAGES_ROOT=/mnt/snap/images
if [ -n "$IMGDEV" ]; then
  T=$(blkid -o value -s TYPE "$IMGDEV")
  if mount -t "$T" -o rw "$IMGDEV" /mnt/images; then IMAGES_ROOT=/mnt/images/images; fi
fi
[ $SNAPOK = 1 ] && mkdir -p /mnt/snap/snapshots 2>/dev/null
echo "OMLOADER_LOADER: snap=${SNAPDEV:-none}(mounted=$SNAPOK) images=${IMGDEV:-none} images_root=$IMAGES_ROOT"
[ -d "$IMAGES_ROOT" ] && echo "OMLOADER_LOADER: images: $(ls $IMAGES_ROOT | tr '\n' ' ')"
cat > /run/omloader/env <<ENV
IMAGES_ROOT=$IMAGES_ROOT
SNAPDEV=$SNAPDEV
IMGDEV=$IMGDEV
SNAPOK=$SNAPOK
ENV
exit 0
