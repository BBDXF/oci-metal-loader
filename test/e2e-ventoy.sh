#!/bin/bash
# Run ON THE REMOTE. Boots omloader.iso THROUGH Ventoy (real Ventoy release EFI/GRUB files, rootless disk image built by
# make-ventoy-disk.sh) in QEMU/OVMF and checks that stage 1 finds the ISO file (iso-scan) in
#   1. Ventoy normal mode  -> live shell        2. Ventoy GRUB2 mode -> live shell
#   3. normal mode + data disk with VTOYDATA/omloader/boot.yaml (default_image) -> container fixture auto-boots (no kernel params)
#   4. GRUB2 mode + same data disk, and Enter during the countdown cancels the auto boot (shell)
#   FYI (not asserted): Secure Boot OVMF + Microsoft keys.
# Ventoy's menu is driven through the QEMU monitor (sendkey); ventoy.json uses long timeouts so the keys cannot race.
# Env: SKIP_BUILD=1 reuse out/omloader.iso, out/oci-images, out/usb.raw   SKIP_VDISK=1 reuse out/ventoy.raw   MODES="normal grub2 ..."
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
cleanup() { for p in $W/vt-*.pid; do [ -f "$p" ] && kill -9 "$(cat "$p")" 2>/dev/null; done; }
trap cleanup EXIT
set -e; mkdir -p $W
if [ -z "${SKIP_BUILD:-}" ]; then
  $ROOT/build/build-iso.sh; $T/make-fixture-oci.sh
  rm -rf $W/vtoy-extra; mkdir -p $W/vtoy-extra/omloader
  printf 'boot:\n  default_image: omloci\n  default_snapshot: new:run1\n  timeout: 4\n  extra_cmdline_flags: [poweroff_on_fail=1, experimental=oci]\n' > $W/vtoy-extra/omloader/boot.yaml
  VTOY_EXTRA=$W/vtoy-extra NO_VM=1 $T/make-disk.sh
fi
cat > $W/ventoy-test.json <<'JSON'
{
  "control": [
    { "VTOY_DEFAULT_IMAGE": "/omloader.iso" },
    { "VTOY_MENU_TIMEOUT": "60" },
    { "VTOY_SECONDARY_TIMEOUT": "60" }
  ],
  "menu_alias": [ { "image": "/omloader.iso", "alias": "OCI Metal Loader (omloader.iso)" } ]
}
JSON
if [ -z "${SKIP_VDISK:-}" ] || [ ! -f $O/ventoy.raw ]; then VJSON=$W/ventoy-test.json $T/make-ventoy-disk.sh; fi
# empty loader data disk (OMLOADER ext4 + VTOYDATA exfat, no images, no boot.yaml)
D=$W/vt-empty.raw; P1=$W/vt1.img; P2=$W/vt2.img; rm -f $D $P1 $P2; truncate -s 64M $P1; truncate -s 64M $P2
mkfs.exfat -L VTOYDATA $P1 >/dev/null; mkfs.ext4 -q -L OMLOADER $P2; truncate -s 140M $D
printf 'label: gpt\nstart=2048, size=64MiB, type=EBD0A0A2-B9E5-4433-87C0-68B6B72699C7, name=VTOYDATA\ntype=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=OMLOADER\n' | sfdisk -q $D
dd if=$P1 of=$D bs=1M seek=1 conv=notrunc status=none; dd if=$P2 of=$D bs=512 seek=$((2048+64*2048)) conv=notrunc status=none; rm -f $P1 $P2
set +e
clean() { tr -d '\r' < $W/$1.serial | sed 's/\x1b\[[0-9;=?]*[a-zA-Z]//g' > $W/$1.clean; }
waitfor() { # $1 tag $2 regex $3 seconds
  for i in $(seq 1 $3); do clean $1; grep -aqE "$2" $W/$1.clean && return 0; sleep 1; done; return 1; }
vboot() { # $1 tag  $2 normal|grub2  $3 data disk  $4 wait-regex  $5 timeout s  [SB=1 in env]
  local tag=$1 mode=$2
  TAG=$tag DATA=$3 TIMEOUT=$(( $5 + 120 )) $T/ventoy-qemu.sh > /dev/null
  sleep 28   # OVMF + Ventoy menu (ventoy.json timeouts are 60 s: no race)
  $T/ventoy-mon.py $W/$tag.mon 'sendkey ret'; sleep 4            # menu -> secondary (boot mode) menu
  [ $mode = grub2 ] && { $T/ventoy-mon.py $W/$tag.mon 'sendkey down'; sleep 1; }
  $T/ventoy-mon.py $W/$tag.mon 'sendkey ret'
  waitfor $tag "$4" $5; local rc=$?
  $T/ventoy-mon.py $W/$tag.mon "screendump $W/$tag.ppm" 2>/dev/null
  return $rc
}
stop() { [ -f $W/$1.pid ] && kill -9 "$(cat $W/$1.pid)" 2>/dev/null; sleep 1; }
stage1line() { grep -a 'OMLOADER_STAGE1: live medium' $W/$1.clean | head -1; }
for mode in ${MODES:-normal grub2}; do
  tag=vt-$mode
  if vboot $tag $mode $D 'OMLOADER_SHELL_READY' 150; then
    L=$(stage1line $tag)
    echo "--- $mode: $L"; grep -a 'OMLOADER_LOADER: snap=' $W/$tag.clean | head -1
    pass "Ventoy $mode mode: loader reached the live shell"
    echo "$L" | grep -q 'iso-scan: dev=/dev/nvme0n1p1 fs=exfat file=/omloader.iso' && pass "Ventoy $mode mode: stage 1 iso-scan found /omloader.iso on the exFAT Ventoy partition" || fail "Ventoy $mode: stage 1 line: $L"
    grep -aq 'OMLOADER_LOADER: snap=/dev/vda2(mounted=1) images=/dev/vda1' $W/$tag.clean && pass "Ventoy $mode mode: OMLOADER/VTOYDATA data disk mounted by omloader-mounts" || fail "Ventoy $mode: data partitions not mounted"
    ! grep -aq 'auto boot image' $W/$tag.clean && pass "Ventoy $mode mode: no boot.yaml -> plain shell (no auto boot)" || fail "Ventoy $mode: unexpected auto boot"
  else fail "Ventoy $mode mode: no live shell (see $W/$tag.clean)"; grep -a 'STAGE1\|FATAL' $W/$tag.clean | tail -5; fi
  stop $tag
done
# boot.yaml default image (no kernel parameters possible under Ventoy)
for mode in ${MODES:-normal grub2}; do
  tag=vt-auto-$mode
  if vboot $tag $mode $O/usb.raw 'OMLOADER_COUNTER=[0-9]|OMLOADER_LOADER: .boot. returned|OMLOADER_LOADER_FAILED' 240; then
    echo "--- $mode auto: $(stage1line $tag)"; grep -a 'boot config\|booting .omloci\|auto boot image' $W/$tag.clean | head -4
    grep -aq 'OMLOADER_LOADER: boot config /mnt/images/omloader/boot.yaml: default_image=omloci' $W/$tag.clean && pass "Ventoy $mode mode: boot.yaml on VTOYDATA found, countdown printed" || fail "Ventoy $mode: boot config not announced"
    grep -aq 'OMLOADER_GUEST_OK' $W/$tag.clean && pass "Ventoy $mode mode: container fixture auto-booted from boot.yaml (OMLOADER_GUEST_OK)" || fail "Ventoy $mode: fixture did not boot"
    [ "$(grep -ao 'OMLOADER_COUNTER=[0-9]*' $W/$tag.clean | tail -1)" = OMLOADER_COUNTER=1 ] && pass "Ventoy $mode mode: default_snapshot new:run1 -> counter==1" || fail "Ventoy $mode: counter"
  else fail "Ventoy $mode mode auto-boot: no marker (see $W/$tag.clean)"; grep -a 'STAGE1\|FATAL\|boot config\|OMLOADER_LOADER' $W/$tag.clean | tail -8; fi
  stop $tag
done
# Enter during the countdown cancels the auto boot
tag=vt-cancel
if vboot $tag normal $O/usb.raw 'booting .omloci. in' 150; then
  $T/ventoy-ser.py $W/$tag.ser '\n'
  waitfor $tag 'boot cancelled|OMLOADER_SHELL_READY' 20
  grep -aq 'boot cancelled' $W/$tag.clean && ! grep -aq 'OMLOADER_GUEST_OK' $W/$tag.clean && pass "Enter on the serial console cancels the boot.yaml countdown" || fail "countdown cancel"
else fail "cancel test: no countdown seen"; fi
stop $tag
# FYI: Secure Boot (not a goal; just report)
tag=vt-secureboot
SB=1 vboot $tag normal $D 'OMLOADER_STAGE1: starting|Access Denied|Verification failed|Security Violation' 90
echo "--- FYI Secure Boot (OVMF secboot + MS keys), normal mode: $(grep -aoE 'OMLOADER_STAGE1: starting|Access Denied|Verification failed|Security Violation' $W/$tag.clean | head -1 || true) (screenshot $W/$tag.ppm)"
stop $tag
[ $FAIL = 0 ] && echo "E2E-VENTOY: ALL PASS" || echo "E2E-VENTOY: FAILED"
exit $FAIL
