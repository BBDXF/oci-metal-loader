#!/bin/bash
# Run ON THE REMOTE. Builds out/usb.raw (sparse, DISK_GB default 4): GPT,
#  p1 exfat VTOYDATA: images/$NAME/{image.yaml,base.raw}   p2 ext4 OMLOADER: snapshots/
# Also copies out/oci-images/* (from make-fixture-oci.sh) into images/ when present; the VM fixture is
# optional if only OCI images exist (NO_VM=1 forces that).
# exfat populated rootless via exfat-fuse inside rootless podman; falls back to ext4 VTOYDATA.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; mkdir -p $O $W
NAME=${NAME:-omlfixture}; DISK_GB=${DISK_GB:-4}; P1_MB=${P1_MB:-2048}
HAVE_VM=1; [ -f $O/fixture.raw ] && [ -z "${NO_VM:-}" ] || HAVE_VM=0
HAVE_OCI=0; [ -d $O/oci-images ] && [ -n "$(ls -A $O/oci-images)" ] && HAVE_OCI=1
[ $HAVE_VM = 1 ] || [ $HAVE_OCI = 1 ] || { echo "run make-fixture.sh and/or make-fixture-oci.sh first"; exit 1; }
rm -rf $W/stage; mkdir -p $W/stage/images $W/stage2/snapshots
[ -z "${VTOY_EXTRA:-}" ] || cp -r "$VTOY_EXTRA"/. $W/stage/   # optional extra files at the VTOYDATA root (e.g. omloader/boot.yaml)
EXTRA_MB=0
if [ $HAVE_OCI = 1 ]; then cp -r $O/oci-images/* $W/stage/images/; EXTRA_MB=$(du -sm --apparent-size $O/oci-images | cut -f1); fi
S=$W/stage/images/$NAME
if [ $HAVE_VM = 1 ]; then
mkdir -p $S
cp --sparse=always $O/fixture.raw $S/base.raw
cat > $S/image.yaml <<EOT
name: $NAME
type: vm
format: raw
base: base.raw
kernel:
  path: /boot/vmlinuz
  initrd: /boot/initrd.img
  cmdline: root=LABEL=OMLROOT rw console=tty0 console=ttyS0,115200 panic=-1
snapshots:
  mode: dm-snapshot
EOT
fi
P1=$W/p1.img; P2=$W/p2.img; rm -f $P1 $P2
TOTAL_MB=$((DISK_GB*1024)); P2_MB=$((TOTAL_MB-P1_MB-4))
FS=exfat
# Rootless exfat population is not possible on the host (no FUSE in userns, kernel mount needs root), so we
# boot the loader ISO itself (its kernel has exfat) with omloader.hook=1: it mounts the blank exfat disk (label
# VTOYDATA) plus an ext4 stage disk (label OMLSTAGE) and copies images/ across. This also exercises loader exfat rw.
truncate -s ${P1_MB}M $P1
mkfs.exfat -L VTOYDATA $P1 >/dev/null 2>&1 || mkfs.exfat -L VTOYDATA $P1 >/dev/null
echo 'cp -r /mnt/stage/images /mnt/images/ && { [ ! -d /mnt/stage/omloader ] || cp -r /mnt/stage/omloader /mnt/images/; } && echo OMLOADER_HOOK_COPIED' > $W/stage/hook.sh
ST=$W/stage.img; rm -f $ST; VM_MB=0; [ $HAVE_VM = 1 ] && VM_MB=$(du -m --apparent-size $S/base.raw | cut -f1)
truncate -s $((300+EXTRA_MB*13/10+VM_MB+(VM_MB>0?800:0)))M $ST
mkfs.ext4 -q -L OMLSTAGE -d $W/stage $ST
if DISK=$P1 DISK2=$ST BUS=virtio EXTRA=omloader.hook=1 LOG=$W/populate.log TIMEOUT=300 $ROOT/test/run-kvm.sh >/dev/null && grep -q OMLOADER_HOOK_COPIED $W/populate.log && fsck.exfat $P1 >/dev/null; then
  echo "VTOYDATA filesystem: exfat (populated via loader VM)"
else
  FS=ext4; echo "WARNING: exfat population failed (see $W/populate.log); falling back to ext4 labelled VTOYDATA"
  rm -f $P1; truncate -s ${P1_MB}M $P1; mkfs.ext4 -q -L VTOYDATA -d $W/stage $P1
fi
rm -f $ST
truncate -s ${P2_MB}M $P2; mkfs.ext4 -q -L OMLOADER -d $W/stage2 $P2
D=$O/usb.raw; rm -f $D; truncate -s ${DISK_GB}G $D
printf 'label: gpt\nstart=2048, size=%sMiB, type=EBD0A0A2-B9E5-4433-87C0-68B6B72699C7, name=VTOYDATA\ntype=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=OMLOADER\n' $P1_MB | sfdisk -q $D
dd if=$P1 of=$D bs=1M seek=1 conv=notrunc,sparse status=none
dd if=$P2 of=$D bs=512 seek=$((2048+P1_MB*2048)) conv=notrunc,sparse status=none
rm -f $P1 $P2; rm -rf $W/stage $W/stage2
echo "$FS" > $O/usb.vtoydata-fs
sfdisk -l $D | tail -4; echo "created $D (VTOYDATA=$FS)"
