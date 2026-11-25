#!/bin/bash
# Run ON THE REMOTE. Host disks in guests (default ON, mode rw): exFAT VTOYDATA store disk (make-store-disk.sh).
#   A container (omloci): /mnt/disks/<disk>/VTOYDATA is listed, writable, holds omloader/, README.txt exists
#   B VM (omlfixture, kexec): whole-disk dm oml-<disk> + partition dm oml-<disk>p1 exist, fstab block written, guest log maps the host disk
# Env: SKIP_BUILD=1 reuse ISO/fixtures/disks
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
export EXPERIMENTAL_OCI=1 TIMEOUT=${TIMEOUT:-300}
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
trap 'pkill -P $$ qemu-system 2>/dev/null' EXIT
set -e
if [ -z "${SKIP_BUILD:-}" ]; then
  $ROOT/build/build-iso.sh
  [ -f $O/fixture.raw ] || $T/make-fixture.sh
  $T/make-fixture-oci.sh
  FS=exfat $T/make-store-disk.sh
fi
set +e
clean() { tr -d '\r' < $W/$1.log | sed 's/\x1b\[[0-9;]*[mK]//g' > $W/$1.clean; }
cp --sparse=always $O/store-exfat.raw $O/store-hd.raw; export DISK=$O/store-hd.raw
NAME=omloci LOG=$W/hd-a.log $T/run-kvm.sh >/dev/null; clean hd-a
grep -a 'OMLOADER_HOSTDISKS' $W/hd-a.clean | tail -5
grep -aq 'OMLOADER_HOSTDISKS=.*/mnt/disks/[a-z0-9]*/VTOYDATA' $W/hd-a.clean && pass "A: container sees the data partition under /mnt/disks/<disk>/VTOYDATA" || fail "A: no host disk listing"
grep -aq 'OMLOADER_HOSTDISKS_RW=yes' $W/hd-a.clean && pass "A: data partition writable in the container (Ventoy-like default)" || fail "A: not writable"
grep -aq 'OMLOADER_HOSTDISKS_OMLDIR=yes' $W/hd-a.clean && pass "A: omloader/ visible on it" || fail "A: omloader/ missing"
grep -aq 'OMLOADER_HOSTDISKS_README=yes' $W/hd-a.clean && pass "A: /mnt/disks/README.txt present" || fail "A: no README"
cp --sparse=always $O/store-exfat.raw $O/store-hd.raw
NAME=omlfixture LOG=$W/hd-b.log $T/run-kvm.sh >/dev/null; clean hd-b
grep -a 'OMLOADER_HOSTDISKS\|host disk' $W/hd-b.clean | tail -4
grep -aq 'OMLOADER_GUEST: host disk [a-z0-9]* mapped as oml-' $W/hd-b.clean && pass "B: guest preinit mapped the host disk (whole-disk dm + partitions)" || fail "B: host disk not mapped"
grep -aq 'OMLOADER_HOSTDISKS_FSTAB=1' $W/hd-b.clean && pass "B: fstab block written into the VM guest" || fail "B: no fstab block"
grep -aq 'OMLOADER_HOSTDISKS_DM=.*oml-[a-z0-9]*p1' $W/hd-b.clean && pass "B: oml-<disk>p1 present in the guest" || fail "B: no partition dm in guest"
# C: VM, VTOYDATA (exfat) and the OMLOADER ext4 partition (preinit-mounted store) on the SAME disk (usb.raw): one claim via the partition dm
[ -f $O/usb.raw ] || $T/make-disk.sh
cp --sparse=always $O/usb.raw $O/usb-hd.raw; export DISK=$O/usb-hd.raw
for n in 1 2; do
  NAME=omlfixture LOG=$W/hd-c$n.log $T/run-kvm.sh >/dev/null; clean hd-c$n
  grep -a 'OMLOADER_HOSTDISKS\|host disk\|host_disks\|OMLOADER_COUNTER\|host disk claim' $W/hd-c$n.clean | tail -6
done
grep -aq 'OMLOADER_GUEST: host disk [a-z0-9]* mapped as oml-' $W/hd-c1.clean && pass "C: same-disk OMLOADER partition: disk mapped" || fail "C: disk not mapped"
grep -aq 'using /dev/mapper/oml-[a-z0-9]*p2 instead of /dev/' $W/hd-c1.clean && pass "C: guest reached the OMLOADER partition via its partition dm" || fail "C: raw partition mounted"
grep -aq 'OMLOADER_HOSTDISKS_MNTS=.*/mnt/disks/[a-z0-9]*/VTOYDATA' $W/hd-c1.clean && pass "C: fstab has /mnt/disks/<disk>/VTOYDATA" || fail "C: VTOYDATA missing from fstab"
grep -aq 'OMLOADER_HOSTDISKS_DM=.*oml-[a-z0-9]*p1' $W/hd-c1.clean && pass "C: oml-<disk>p1 present" || fail "C: no p1 dm"
grep -aq 'OMLOADER_COUNTER=1' $W/hd-c1.clean && grep -aq 'OMLOADER_COUNTER=2' $W/hd-c2.clean && pass "C: persistence across reboot (counter 1 -> 2)" || fail "C: no persistence"
rm -f $O/usb-hd.raw
rm -f $O/store-hd.raw
[ $FAIL = 0 ] && echo "E2E-HOSTDISKS: ALL PASS" || echo "E2E-HOSTDISKS: FAILURES"
exit $FAIL
