#!/bin/bash
# Run ON THE REMOTE. M3 flow: ISO -> OCI fixture (podman, rootless) -> disk -> 3 loader boots (no kexec) -> assertions.
# SKIP_BUILD=1 reuses artifacts in out/.  The guest must run on the LOADER's own kernel (compared via uname).
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
export NAME=${NAME:-omloci} TIMEOUT=${TIMEOUT:-300}
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
trap 'pkill -P $$ qemu-system 2>/dev/null; pkill -u $USER -f "$W/run\." 2>/dev/null' EXIT
set -e
if [ -z "${SKIP_BUILD:-}" ]; then $ROOT/build/build-iso.sh; $T/make-fixture-oci.sh; NO_VM=1 $T/make-disk.sh; fi
set +e
cp --sparse=always $O/usb.raw $O/usb-run.raw; export DISK=$O/usb-run.raw
boot() { # $1=snapshot arg  $2=logname
  SNAP="$1" LOG=$W/$2.log $T/run-kvm.sh; tr -d '\r' < $W/$2.log > $W/$2.clean
  GOT=$(grep -o 'OMLOADER_COUNTER=[0-9]*' $W/$2.clean | tail -1 | cut -d= -f2)
  echo "--- $2 (snapshot=$1) serial excerpt:"; grep -E 'OMLOADER|Linux version' $W/$2.clean | tail -25; }
check_common() { # $1 = log
  grep -q OMLOADER_GUEST_OK $W/$1.clean && pass "$1: guest booted (OMLOADER_GUEST_OK)" || fail "$1: no OMLOADER_GUEST_OK"
  LK=$(grep -o 'OMLOADER_LOADER: starting (kernel [^)]*' $W/$1.clean | head -1 | sed 's/.*kernel //')
  GK=$(grep -o 'OMLOADER_GUEST_KERNEL=[^ ]*' $W/$1.clean | tail -1 | cut -d= -f2)
  [ -n "$LK" ] && [ "$LK" = "$GK" ] && pass "$1: guest kernel == loader kernel ($LK), no kexec" || fail "$1: kernel loader='$LK' guest='$GK'"
  grep -q 'OMLOADER_GUEST_ROOTFS=overlay' $W/$1.clean && pass "$1: guest / is overlayfs" || fail "$1: root is not overlay"
  grep -q 'OMLOADER_GUEST_MODULES=yes' $W/$1.clean && pass "$1: loader modules visible in guest" || fail "$1: no /lib/modules/<kver> in guest"
  grep -q 'OMLOADER_GUEST_NET=lease' $W/$1.clean && pass "$1: systemd-networkd got a DHCP lease" || fail "$1: no DHCP lease"
  grep -q 'OMLOADER_GUEST_HOSTNAME=omloci' $W/$1.clean && pass "$1: hostname from runtime.hostname" || fail "$1: hostname"
}
boot new:run1 oci1
check_common oci1
grep -q 'firstboot adapter applied' $W/oci1.clean && pass "(a) firstboot adapter applied on new:run1" || fail "(a) adapter message missing"
[ "$GOT" = 1 ] && pass "(b) new:run1 counter==1" || fail "(b) counter='$GOT' expected 1"
boot base oci2
check_common oci2
[ "$GOT" = 1 ] && pass "(c) --snapshot base (ephemeral tmp-* child) counter==1 (base untouched)" || fail "(c) base counter='$GOT' expected 1"
boot run1 oci3
check_common oci3
grep -q 'firstboot adapter applied' $W/oci3.clean && fail "(d) adapter re-applied on run1" || pass "(d) adapter marker honoured on run1"
[ "$GOT" = 2 ] && pass "(e) run1 (leaf) counter==2 (upper layer persisted)" || fail "(e) run1 counter='$GOT' expected 2"
[ $FAIL = 0 ] && echo "E2E-OCI: ALL PASS" || echo "E2E-OCI: FAILED"
exit $FAIL
