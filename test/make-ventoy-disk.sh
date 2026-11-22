#!/bin/bash
# Run ON THE REMOTE. Builds out/ventoy.raw: a rootless imitation of a disk prepared by Ventoy2Disk (which needs root):
#   GPT (PTABLE=mbr for MBR style): p1 = exFAT label "Ventoy" (data: omloader.iso, ventoy/ventoy.json, ...),
#   p2 = 32 MiB VTOYEFI (FAT, ESP-typed) = ventoy.disk.img from the Ventoy release (EFI/BOOT/BOOTX64.EFI, grub/, ventoy/ ...).
# exFAT is populated rootlessly by booting the loader ISO itself in a helper VM (omloader.hook=1, like make-disk.sh).
# Env: VENTOY_DIR (~/ventoy-1.1.17)  ISO (out/omloader.iso)  VJSON (file copied to ventoy/ventoy.json, optional)
#      PAYLOAD (extra dir copied to the root of p1, optional)  PTABLE=gpt|mbr  OUT (out/ventoy.raw)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; mkdir -p $O $W
VENTOY_DIR=${VENTOY_DIR:-$HOME/ventoy-1.1.17}; ISO=${ISO:-$O/omloader.iso}; PTABLE=${PTABLE:-gpt}; OUT=${OUT:-$O/ventoy.raw}
[ -f "$VENTOY_DIR/ventoy/ventoy.disk.img.xz" ] || { echo "no Ventoy release at $VENTOY_DIR"; exit 1; }
ISO_MB=$(( ($(stat -c %s $ISO) + 1048575) / 1048576 ))
P1_MB=${P1_MB:-$((ISO_MB + 300))}
rm -rf $W/vstage; mkdir -p $W/vstage/payload/ventoy
cp $ISO $W/vstage/payload/omloader.iso
[ -n "${VJSON:-}" ] && cp "$VJSON" $W/vstage/payload/ventoy/ventoy.json
[ -n "${PAYLOAD:-}" ] && cp -r "$PAYLOAD"/. $W/vstage/payload/
echo 'mkdir -p /mnt/v && mount -t exfat "$(blkid -L Ventoy)" /mnt/v && cp -r /mnt/stage/payload/. /mnt/v/ && sync && ls -R /mnt/v | head -20 && umount /mnt/v && echo VENTOY_POPULATED' > $W/vstage/hook.sh
P1=$W/vp1.img; ST=$W/vstage.img; P2=$W/vp2.img
rm -f $P1 $ST $P2; truncate -s ${P1_MB}M $P1; mkfs.exfat -L Ventoy $P1 >/dev/null
truncate -s $((P1_MB + 100))M $ST; mkfs.ext4 -q -L OMLSTAGE -d $W/vstage $ST
if ! DISK=$P1 DISK2=$ST BUS=virtio EXTRA=omloader.hook=1 ISO=$ISO LOG=$W/vpopulate.log TIMEOUT=400 $ROOT/test/run-kvm.sh >/dev/null || ! grep -q VENTOY_POPULATED $W/vpopulate.log || ! fsck.exfat $P1 >/dev/null; then
  echo "exFAT population failed, see $W/vpopulate.log"; exit 1
fi
xz -dc $VENTOY_DIR/ventoy/ventoy.disk.img.xz > $P2
[ "$(stat -c %s $P2)" = 33554432 ] || { echo "unexpected ventoy.disk.img size"; exit 1; }
rm -f $OUT; truncate -s $((P1_MB + 32 + 2))M $OUT
if [ "$PTABLE" = gpt ]; then
  printf 'label: gpt\nstart=2048, size=%sMiB, type=EBD0A0A2-B9E5-4433-87C0-68B6B72699C7, name=Ventoy\nsize=32MiB, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B, name=VTOYEFI\n' $P1_MB | sfdisk -q $OUT
else
  printf 'label: dos\nstart=2048, size=%sMiB, type=7\nsize=32MiB, type=ef\n' $P1_MB | sfdisk -q $OUT
fi
dd if=$P1 of=$OUT bs=1M seek=1 conv=notrunc,sparse status=none
dd if=$P2 of=$OUT bs=512 seek=$((2048 + P1_MB * 2048)) conv=notrunc status=none
# what Ventoy2Disk/VentoyWorker.sh writes outside the partitions: boot.img, core.img (BIOS), the disk UUID that Ventoy's EFI
# build verifies ("MBR check failed" otherwise); steps copied from tool/VentoyWorker.sh (install path)
dd status=none conv=notrunc if=$VENTOY_DIR/boot/boot.img of=$OUT bs=1 count=$([ "$PTABLE" = gpt ] && echo 440 || echo 446)
if [ "$PTABLE" = gpt ]; then
  printf '\x22' | dd status=none conv=notrunc of=$OUT bs=1 count=1 seek=92
  xz -dc $VENTOY_DIR/boot/core.img.xz | dd status=none conv=notrunc of=$OUT bs=512 count=2014 seek=34
  printf '\x23' | dd status=none conv=notrunc of=$OUT bs=1 count=1 seek=17908
else
  xz -dc $VENTOY_DIR/boot/core.img.xz | dd status=none conv=notrunc of=$OUT bs=512 count=2047 seek=1
  head -c 4 /dev/urandom | dd status=none conv=notrunc of=$OUT bs=1 count=4 seek=440
fi
head -c 16 /dev/urandom | dd status=none conv=notrunc of=$OUT bs=1 count=16 seek=384
rm -f $P1 $P2 $ST; rm -rf $W/vstage
sfdisk -l $OUT | tail -4; echo "created $OUT ($PTABLE, p1=${P1_MB}MiB exFAT Ventoy, p2=32MiB VTOYEFI)"
