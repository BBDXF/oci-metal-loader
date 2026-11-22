#!/bin/bash
# omloader-mounts.service: find and mount the snapshot store (OMLOADER) and VTOYDATA, write /run/omloader/env.
#   start (default)  store precedence: 1. a real ext4/xfs/btrfs partition labelled OMLOADER, 2. an ext4 store FILE
#                    (label OMLOADER, `omloader store find`) on the host partition (VTOYDATA: exfat/ntfs3/vfat/ext4...),
#                    3. nothing (SNAPOK=0: /mnt/snap stays on the loader's tmpfs, i.e. ephemeral).
#   stop             clean shutdown order: store ext4 (umount) -> loop device -> host fs (umount, else remount ro).
export PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
if [ "$1" = stop ]; then
  . /run/omloader/env 2>/dev/null
  sync
  mountpoint -q /mnt/snap && umount /mnt/snap
  if [ -n "$STOREFILE" ]; then
    for l in $(losetup -j "$STOREFILE" --noheadings -O NAME 2>/dev/null); do losetup -d "$l" 2>/dev/null; done
  fi
  sync
  if mountpoint -q /mnt/images; then umount /mnt/images 2>/dev/null || mount -o remount,ro /mnt/images 2>/dev/null; fi
  sync
  exit 0
fi
CMD=$(cat /proc/cmdline)
arg() { local a; for a in $CMD; do case "$a" in "$1="*) echo "${a#*=}"; return;; esac; done; }
echo "OMLOADER_LOADER: starting (kernel $(uname -r))"
mkdir -p /mnt/snap /mnt/images /run/omloader
# Only wait (<= 15 s) when the command line asks for an automatic action; otherwise a plain shell must not be delayed.
MAXTRY=1
for k in omloader.image omloader.hook omloader.selftest; do [ -n "$(arg $k)" ] && MAXTRY=30; done
SNAPDEV=; IMGDEV=; i=0; j=
while :; do
  if [ -z "$SNAPDEV" ]; then
    d=$(blkid -L OMLOADER 2>/dev/null)
    # only a real Linux filesystem counts (an exfat/ntfs volume that happens to carry the label is not a store)
    case "$(blkid -o value -s TYPE "$d" 2>/dev/null)" in ext4|xfs|btrfs) SNAPDEV=$d;; esac
  fi
  [ -z "$IMGDEV" ] && IMGDEV=$(blkid -L VTOYDATA 2>/dev/null)
  [ -n "$SNAPDEV" ] && [ -n "$IMGDEV" ] && break
  i=$((i+1)); [ $i -ge $MAXTRY ] && break
  # store-file mode has no OMLOADER partition: once the host partition is there, give a (slow) partition only ~3 s more
  [ -n "$IMGDEV" ] && [ -z "$j" ] && j=$i
  [ -n "$j" ] && [ $((i-j)) -ge 6 ] && break
  sleep 0.5
done
SNAPOK=0; STOREFILE=; STOREERR=; IMGRW=0
# 1. real partition
if [ -n "$SNAPDEV" ]; then
  ST=$(blkid -o value -s TYPE "$SNAPDEV"); mount -t "$ST" -o rw "$SNAPDEV" /mnt/snap && SNAPOK=1
fi
# host partition (images; also the store file's host). NTFS: the in-kernel ntfs3 if the kernel has it (Debian bookworm's
# 6.1 does not), else ntfs-3g (FUSE). A dirty/hibernated (Windows fast startup) volume refuses rw: it is then mounted
# read-only (images stay usable, a store file on it is not).
IMAGES_ROOT=/mnt/snap/images
if [ -n "$IMGDEV" ]; then
  T=$(blkid -o value -s TYPE "$IMGDEV"); MT=$T
  if [ "$T" = ntfs ]; then
    if grep -qw ntfs3 /proc/filesystems || modprobe ntfs3 2>/dev/null; then MT=ntfs3; else MT=ntfs-3g; modprobe fuse 2>/dev/null; fi
  fi
  : > /run/omloader/hostmount.err
  # ntfs-3g is a FUSE daemon that must outlive `systemctl switch-root` (container handoff) and the guest's shutdown
  # killall: systemd spares processes whose argv[0] starts with '@' ("root storage daemon" convention).
  hmount() { if [ "$MT" = ntfs-3g ]; then bash -c 'exec -a @ntfs-3g /usr/bin/ntfs-3g "$@"' ntfs-3g "$IMGDEV" /mnt/images -o "$1"; else mount -t "$MT" -o "$1" "$IMGDEV" /mnt/images; fi; }
  if hmount rw,noatime 2>>/run/omloader/hostmount.err; then
    IMAGES_ROOT=/mnt/images/images; IMGRW=1
  elif [ "$T" = ntfs ] && hmount ro,noatime 2>>/run/omloader/hostmount.err; then
    IMAGES_ROOT=/mnt/images/images; STOREERR=host-readonly
    echo "OMLOADER_LOADER: WARNING: $IMGDEV (ntfs, $MT) could not be mounted read-write (dirty / hibernated by Windows? fast startup?): mounted READ-ONLY. $(tail -1 /run/omloader/hostmount.err)"
  else
    echo "OMLOADER_LOADER: WARNING: mounting $IMGDEV ($T) failed: $(tail -1 /run/omloader/hostmount.err)"
  fi
fi
# 2. store file on the host partition
# omloader.store=off (kernel cmdline): do not attach a store file (maintenance: check/grow it offline)
if [ $SNAPOK = 0 ] && [ -n "$IMGDEV" ] && [ "$(arg omloader.store)" != off ] && mountpoint -q /mnt/images; then
  CFG=; [ -r /mnt/images/omloader.yaml ] && CFG="-c /mnt/images/omloader.yaml"
  SF=$(omloader store find --host-root /mnt/images --host-dev "$IMGDEV" $CFG 2>/run/omloader/store-find.err)
  if [ -n "$SF" ]; then
    if [ $IMGRW != 1 ]; then
      STOREERR=host-readonly
      echo "OMLOADER_LOADER: WARNING: store file $SF is on a read-only host partition: not usable (snapshots stay in RAM, ephemeral). Fix the host fs (chkdsk / disable Windows fast startup), then reboot."
    elif L=$(losetup -f --show "$SF") && mount -t ext4 -o rw "$L" /mnt/snap; then
      SNAPOK=1; SNAPDEV=$L; STOREFILE=$SF
      echo "OMLOADER_LOADER: store file $SF attached as $L and mounted on /mnt/snap"
    else
      [ -n "$L" ] && losetup -d "$L" 2>/dev/null
      STOREERR=mount-failed
      echo "OMLOADER_LOADER: WARNING: store file $SF could not be mounted (try: omloader store check --path $SF)"
    fi
  elif grep -q 'does not match' /run/omloader/store-find.err 2>/dev/null; then
    echo "OMLOADER_LOADER: store file search skipped: $(cat /run/omloader/store-find.err)"
  fi
fi
[ $SNAPOK = 1 ] && mkdir -p /mnt/snap/snapshots 2>/dev/null
echo "OMLOADER_LOADER: snap=${SNAPDEV:-none}(mounted=$SNAPOK${STOREFILE:+ store-file=$STOREFILE}${STOREERR:+ store-error=$STOREERR}) images=${IMGDEV:-none} images_root=$IMAGES_ROOT"
[ -d "$IMAGES_ROOT" ] && echo "OMLOADER_LOADER: images: $(ls $IMAGES_ROOT | tr '\n' ' ')"
cat > /run/omloader/env <<ENV
IMAGES_ROOT=$IMAGES_ROOT
SNAPDEV=$SNAPDEV
IMGDEV=$IMGDEV
SNAPOK=$SNAPOK
STOREFILE=$STOREFILE
STOREERR=$STOREERR
IMGRW=$IMGRW
ENV
exit 0
