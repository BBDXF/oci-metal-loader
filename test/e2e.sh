#!/bin/bash
# Run ON THE REMOTE. Full flow: ISO -> fixture -> disk -> 3 boots -> assertions.
# SKIP_BUILD=1 reuses existing artifacts in out/.
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
export NAME=${NAME:-omlfixture} TIMEOUT=${TIMEOUT:-240}
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
trap 'pkill -P $$ qemu-system 2>/dev/null; pkill -u $USER -f "$W/run\." 2>/dev/null' EXIT
set -e
if [ -z "${SKIP_BUILD:-}" ]; then $ROOT/build/build-iso.sh; $T/make-fixture.sh; $T/make-disk.sh; fi
set +e
cp --sparse=always $O/usb.raw $O/usb-run.raw; export DISK=$O/usb-run.raw
boot() { # $1=snapshot arg  $2=logname
  SNAP="$1" LOG=$W/$2.log $T/run-kvm.sh; tr -d '\r' < $W/$2.log > $W/$2.clean
  GOT=$(grep -o 'OMLOADER_COUNTER=[0-9]*' $W/$2.clean | tail -1 | cut -d= -f2)
  echo "--- $2 (snapshot=$1) serial tail:"; tail -12 $W/$2.clean; }
boot new:run1 boot1   # new child of base; guest rebuilds dm stack via appended initrd (absolute /boot/vmlinuz symlink in fixture)
grep -q OMLOADER_GUEST_OK $W/boot1.clean && pass "(a) guest booted via omloader (OMLOADER_GUEST_OK)" || fail "(a) no OMLOADER_GUEST_OK"
[ "$GOT" = 1 ] && pass "(b) first boot counter==1" || fail "(b) counter='$GOT' expected 1"
boot base boot2
[ "$GOT" = 1 ] && pass "(c) --snapshot base (ephemeral tmp-* child) counter==1 (base untouched)" || fail "(c) base counter='$GOT' expected 1"
boot run1 boot3
[ "$GOT" = 2 ] && pass "(d) snapshot run1 (leaf) counter==2 (COW persisted)" || fail "(d) run1 counter='$GOT' expected 2"
[ $FAIL = 0 ] && echo "E2E: ALL PASS" || echo "E2E: FAILED"
exit $FAIL
