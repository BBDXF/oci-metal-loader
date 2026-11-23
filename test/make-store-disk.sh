#!/bin/bash
# Run ON THE REMOTE. Builds out/store-$FS.raw (FS=exfat|ntfs): GPT with ONE partition, label VTOYDATA, holding
# images/ (OCI fixture from out/oci-images, VM fixture out/fixture.raw unless NO_VM=1) and omloader/store.img, an ext4
# image (label OMLOADER) created by `omloader store create` INSIDE the loader (omloader.hook=1 stage disk).
# NTFS: the loader formats the partition itself (mkntfs from ntfs-3g) and writes through ntfs-3g (Debian's 6.1 kernel has no ntfs3).
# Env: FS (exfat) STORE_MB (1024) NO_VM=1 NAME (VM image name, omlfixture)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; mkdir -p $O $W
FS=${FS:-exfat}; STORE_MB=${STORE_MB:-1024}; NAME=${NAME:-omlfixture}
HAVE_VM=1; [ -f $O/fixture.raw ] && [ -z "${NO_VM:-}" ] || HAVE_VM=0
[ -d $O/oci-images ] && [ -n "$(ls -A $O/oci-images)" ] || { echo "run make-fixture-oci.sh first"; exit 1; }
rm -rf $W/stage; mkdir -p $W/stage/images
cp -r $O/oci-images/* $W/stage/images/
IMG_MB=$(du -sm --apparent-size $O/oci-images | cut -f1); VM_MB=0
if [ $HAVE_VM = 1 ]; then
  S=$W/stage/images/$NAME; mkdir -p $S; cp --sparse=always $O/fixture.raw $S/base.raw
  VM_MB=$(du -m --apparent-size $S/base.raw | cut -f1)
  cat > $S/image.yaml <<EOT
name: $NAME
type: lxd-vm
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
P1_MB=$(( (IMG_MB+VM_MB)*12/10 + STORE_MB + 300 ))
P1=$W/p1-$FS.img; rm -f $P1; truncate -s ${P1_MB}M $P1
case $FS in
  exfat) mkfs.exfat -L VTOYDATA $P1 >/dev/null
         PREP=':' ;;
  ntfs)  PREP='modprobe fuse; mkntfs -F -Q -L VTOYDATA /dev/vda >/dev/null 2>&1 && (grep -qw ntfs3 /proc/filesystems && mount -t ntfs3 /dev/vda /mnt/images || mount -t ntfs-3g /dev/vda /mnt/images)' ;;
  *) echo "FS must be exfat|ntfs"; exit 1;;
esac
cat > $W/stage/hook.sh <<EOT
set -e
$PREP
cp -r /mnt/stage/images /mnt/images/
mkdir -p /mnt/images/omloader
omloader store create --path /mnt/images/omloader/store.img --size ${STORE_MB}M
omloader store info --path /mnt/images/omloader/store.img
sync
echo OMLOADER_HOOK_COPIED
EOT
ST=$W/stage-$FS.img; rm -f $ST
truncate -s $((300+(IMG_MB+VM_MB)*13/10+(VM_MB>0?800:0)))M $ST
mkfs.ext4 -q -L OMLSTAGE -d $W/stage $ST
DISK=$P1 DISK2=$ST BUS=virtio EXTRA=omloader.hook=1 LOG=$W/populate-$FS.log TIMEOUT=900 $ROOT/test/run-kvm.sh >/dev/null || true
tr -d '\r' < $W/populate-$FS.log | grep -E 'OMLOADER_HOOK|created|state:|host_fs|error' || true
grep -q OMLOADER_HOOK_COPIED $W/populate-$FS.log || { echo "population failed, see $W/populate-$FS.log"; exit 1; }
[ $FS = exfat ] && { fsck.exfat -n $P1 >/dev/null && echo "fsck.exfat: clean" || { echo "fsck.exfat reports problems"; exit 1; }; }
D=$O/store-$FS.raw; rm -f $D; truncate -s $((P1_MB+4))M $D
printf 'label: gpt\nstart=2048, size=%sMiB, type=EBD0A0A2-B9E5-4433-87C0-68B6B72699C7, name=VTOYDATA\n' $P1_MB | sfdisk -q $D
dd if=$P1 of=$D bs=1M seek=1 conv=notrunc,sparse status=none
rm -f $P1 $ST; rm -rf $W/stage
sfdisk -l $D | tail -3; echo "created $D (VTOYDATA=$FS, store.img ${STORE_MB}M)"
