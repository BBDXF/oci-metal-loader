#!/bin/bash
# Run ON THE REMOTE. Stage-1 ISO search order, kernel+initrd booted directly (no CD-ROM), 3-partition disk (ext4):
#   A  p1 decoy (decoy/x.iso + omloader/omloader.iso), p2 label ISOS with ventoy/ventoy.json VTOY_DEFAULT_SEARCH_ROOT=/ISO2 + /ISO2/omloader-test.iso,
#      p3 empty  -> p2 chosen via ventoy-search-root, skipped-candidates line lists p1
#   B  p1 empty, p2 (no ventoy.json) omloader/omloader-x.iso, p3 empty -> omloader-dir
# Env: SKIP_BUILD=1 reuse out/omloader.iso
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
FAIL=0; pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
trap 'pkill -u $USER -f "isoscan-run-" 2>/dev/null' EXIT
set -e; mkdir -p $W
[ -n "${SKIP_BUILD:-}" ] || $ROOT/build/build-iso.sh
ISO=$O/omloader.iso
mkdir -p $W/is-k; xorriso -osirrox on -indev $ISO -extract /boot/vmlinuz $W/is-k/vmlinuz -extract /boot/initrd.img $W/is-k/initrd.img >/dev/null 2>&1
rm -rf $W/is-hk; mkdir -p $W/is-hk
echo 'echo TAG:medium:BEGIN; cat /run/omloader/medium.env; echo TAG:medium:END' > $W/is-hk/hook.sh
HK=$W/is-hook.img; rm -f $HK; truncate -s 16M $HK; mkfs.ext4 -q -L OMLSTAGE -d $W/is-hk $HK
MB=$(( ($(stat -c %s $ISO) + 1048575) / 1048576 + 64 ))
mkpart() { # $1 name $2 label $3 stage-dir -> $W/is-$1.img
  rm -f $W/is-$1.img; truncate -s ${MB}M $W/is-$1.img; mkfs.ext4 -q -L $2 -d $3 $W/is-$1.img; }
mkdisk() { # $1 case
  local D=$W/is-$1.raw; rm -f $D; truncate -s $((3 * MB + 8))M $D
  printf 'label: gpt\nstart=2048, size=%sMiB, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4\nsize=%sMiB, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4\nsize=%sMiB, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4\n' $MB $MB $MB | sfdisk -q $D
  local i=0; for p in p1 p2 p3; do dd if=$W/is-$p.img of=$D bs=1M seek=$((1 + i * MB)) conv=notrunc,sparse status=none; i=$((i+1)); done
}
runcase() { # $1 tag -> $W/$1.clean
  local t=$1; : > $W/$t.serial
  timeout -s KILL 240 qemu-system-x86_64 -enable-kvm -machine q35 -cpu host -m 2G -smp 2 -no-reboot -display none -name isoscan-run-$t \
    -kernel $W/is-k/vmlinuz -initrd $W/is-k/initrd.img -append "console=ttyS0 omloader.hook=1" -serial file:$W/$t.serial \
    -drive if=none,id=d0,file=$W/is-$t.raw,format=raw -device virtio-blk-pci,drive=d0 \
    -drive if=none,id=d1,file=$HK,format=raw -device virtio-blk-pci,drive=d1 > $W/$t.qemu.log 2>&1 &
  local pid=$!
  for i in $(seq 1 200); do tr -d '\r' < $W/$t.serial | sed 's/\x1b\[[0-9;=?]*[a-zA-Z]//g' > $W/$t.clean; grep -aq 'TAG:medium:END' $W/$t.clean && break; sleep 1; done
  kill -9 $pid 2>/dev/null; wait $pid 2>/dev/null || true
}
set +e
# ---- case A ----
rm -rf $W/is-s; mkdir -p $W/is-s/a $W/is-s/b/ventoy $W/is-s/b/ISO2 $W/is-s/c
mkdir -p $W/is-s/a/decoy $W/is-s/a/omloader; echo x > $W/is-s/a/decoy/x.iso; cp $ISO $W/is-s/a/omloader/omloader.iso
printf '\xef\xbb\xbf{\r\n "control": [ {"VTOY_DEFAULT_SEARCH_ROOT" : "/ISO2"} ]\r\n}\r\n' > $W/is-s/b/ventoy/ventoy.json
cp $ISO $W/is-s/b/ISO2/omloader-test.iso; touch $W/is-s/c/empty
mkpart p1 DECOY $W/is-s/a; mkpart p2 ISOS $W/is-s/b; mkpart p3 PLAIN $W/is-s/c; mkdisk A
runcase A
grep -aq 'OMLOADER_STAGE1: iso: /dev/vda2 (ext4, label ISOS) /ISO2/omloader-test.iso via ventoy-search-root' $W/A.clean && pass "A ventoy-search-root on p2" || fail "A step line"
grep -aq 'skipped other candidates: /dev/vda1' $W/A.clean && pass "A skipped line" || fail "A skipped line"
grep -aq 'MEDIUM_STEP=ventoy-search-root' $W/A.clean && pass "A medium.env step" || fail "A medium.env"
# ---- case B ----
rm -rf $W/is-s; mkdir -p $W/is-s/a $W/is-s/b/omloader $W/is-s/c; touch $W/is-s/a/e $W/is-s/c/e
cp $ISO $W/is-s/b/omloader/omloader-x.iso
mkpart p1 PLAIN1 $W/is-s/a; mkpart p2 DATA $W/is-s/b; mkpart p3 PLAIN3 $W/is-s/c; mkdisk B
runcase B
grep -aq 'OMLOADER_STAGE1: iso: /dev/vda2 (ext4, label DATA) /omloader/omloader-x.iso via omloader-dir' $W/B.clean && pass "B omloader-dir" || fail "B step line"
grep -aq 'MEDIUM_STEP=omloader-dir' $W/B.clean && pass "B medium.env step" || fail "B medium.env"
rm -rf $W/is-s $W/is-p?.img $W/is-*.raw
exit $FAIL
