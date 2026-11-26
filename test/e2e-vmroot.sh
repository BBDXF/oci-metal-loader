#!/bin/bash
# Run ON THE REMOTE. vm_root_size: VM fixture (1 GiB root) grown to 3G (persists over a reboot), the 20G default, and "off" (unchanged).
# Env: SKIP_BUILD=1 reuse ISO/fixture
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
export NAME=${NAME:-omlfixture} TIMEOUT=${TIMEOUT:-300}
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
trap 'pkill -P $$ qemu-system 2>/dev/null; pkill -u $USER -f "$W/run\." 2>/dev/null' EXIT
set -e
if [ -z "${SKIP_BUILD:-}" ]; then $ROOT/build/build-iso.sh; [ -f $O/fixture.raw ] || $T/make-fixture.sh; fi
set +e
mkdisk() { # $1=vm_root_size yaml value ("" = defaults)  -> $O/usb-vr.raw
  rm -rf $W/vx; mkdir -p $W/vx/omloader
  [ -z "$1" ] || printf 'guest_defaults:\n  vm_root_size: %s\n' "$1" > $W/vx/omloader/boot.yaml
  NO_VM= OUT=$O/usb-vr.raw VTOY_EXTRA=$W/vx $T/make-disk.sh >/dev/null || { fail "make-disk"; exit 1; }
  export DISK=$O/usb-vr.raw
}
boot() { SNAP="" LOG=$W/$1.log $T/run-kvm.sh >/dev/null; tr -d '\r' < $W/$1.log | sed 's/\x1b\[[0-9;]*[mK]//g' > $W/$1.clean
  echo "--- $1:"; grep -E 'OMLOADER_(COUNTER|ROOTSIZE|DEVSIZE)|vm_root_size|resize2fs|error:' $W/$1.clean | tail -6; }
val() { grep -o "$2=[0-9]*" $W/$1.clean | tail -1 | cut -d= -f2; }
G=$((1<<30))
# root device = the grown partition: within 4 MiB below the disk size (GPT headers + alignment)
near() { [ -n "$1" ] && [ "$1" -le "$2" ] && [ "$1" -gt $(($2-4*1024*1024)) ]; }
mkdisk 3G
boot g1
D=$(val g1 OMLOADER_DEVSIZE); R=$(val g1 OMLOADER_ROOTSIZE)
near "$D" $((3*G)) && pass "g1: root device is 3G" || fail "g1: devsize '$D'"
[ -n "$R" ] && [ "$R" -gt $((2*G)) ] && pass "g1: df size $R > 2G (fs grown)" || fail "g1: df size '$R'"
boot g2
[ "$(val g2 OMLOADER_COUNTER)" = 2 ] && near "$(val g2 OMLOADER_DEVSIZE)" $((3*G)) && [ "$(val g2 OMLOADER_ROOTSIZE)" -gt $((2*G)) ] && pass "g2: reboot persists (counter 2, still 3G)" || fail "g2: reboot"
mkdisk ""
boot d1
near "$(val d1 OMLOADER_DEVSIZE)" $((20*G)) && pass "d1: default is 20G" || fail "d1: default devsize '$(val d1 OMLOADER_DEVSIZE)'"
mkdisk off
boot o1
[ "$(val o1 OMLOADER_DEVSIZE)" -lt $((2*G)) ] && [ -n "$(val o1 OMLOADER_DEVSIZE)" ] && pass "o1: off leaves the root unchanged ($(val o1 OMLOADER_DEVSIZE))" || fail "o1: off devsize '$(val o1 OMLOADER_DEVSIZE)'"
rm -f $O/usb-vr.raw
[ $FAIL = 0 ] && echo "E2E-VMROOT: ALL PASS" || echo "E2E-VMROOT: FAILED"
exit $FAIL
