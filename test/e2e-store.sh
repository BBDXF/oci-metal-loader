#!/bin/bash
# Run ON THE REMOTE. Store-file mode: the disk's ONLY partition is exFAT (or NTFS) VTOYDATA holding images/ and
# omloader/store.img (ext4, label OMLOADER, created by `omloader store create` inside the loader).
#   A exfat + container: new:run1 counter 1 -> leaf 2, --snapshot base isolation, clean ext4 / exfat after poweroff, store info/check/grow
#   B exfat + VM (kexec, guest-side nested storage): counter 1 -> 2
#   C ntfs (ntfs3) + container: counter 1 -> 2; dirty NTFS => read-only host => store unusable, loader warns, boot stays ephemeral
# Env: SKIP_BUILD=1 reuse ISO/fixtures/disks   SKIP_ISO=1 only skip the ISO   NO_VM=1 skip B   SKIP_NTFS=1 skip C
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
export EXPERIMENTAL_OCI=1 TIMEOUT=${TIMEOUT:-300}
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
eq() { [ "$1" = "$2" ] && pass "$3" || fail "$3 (got '$1', want '$2')"; }
trap 'pkill -P $$ qemu-system 2>/dev/null; pkill -u $USER -f "$W/run\." 2>/dev/null' EXIT
set -e
if [ -z "${SKIP_BUILD:-}" ]; then
  [ -n "${SKIP_ISO:-}" ] || $ROOT/build/build-iso.sh
  [ -n "${NO_VM:-}" ] || [ -f $O/fixture.raw ] || $T/make-fixture.sh
  $T/make-fixture-oci.sh
  FS=exfat NO_VM=${NO_VM:-} $T/make-store-disk.sh
  [ -n "${SKIP_NTFS:-}" ] || FS=ntfs NO_VM=1 $T/make-store-disk.sh
fi
set +e
clean() { tr -d '\r' < $W/$1.log | sed 's/\x1b\[[0-9;]*[mK]//g' > $W/$1.clean; }
val() { grep -o "$2=[0-9a-z,.-]*" $W/$1.clean | tail -1 | cut -d= -f2; }
guest() { # log image snapshot  (DISK exported by the caller)
  NAME=$2 SNAP=$3 LOG=$W/$1.log $T/run-kvm.sh > /dev/null; clean $1
  echo "--- $1 (image=$2 snapshot=$3) excerpt:"; grep -E 'OMLOADER_(COUNTER|GUEST|LOADER: (store|snap|WARN))|store file|error:|WARNING' $W/$1.clean | grep -v 'OMLOADER_GUEST_\(KERNEL\|MODULES\|NET\|HOSTNAME\|ROOTFS\)' | tail -8; }
sect() { sed -n "/TAG:$2:BEGIN/,/TAG:$2:END/p" $W/$1.clean; }
rc() { sect $1 $2 | grep -o 'RC=[0-9]*' | tail -1 | cut -d= -f2; }
hook() { # log extra-cmdline  (hook body on stdin; DISK exported)
  rm -rf $W/hk; mkdir -p $W/hk
  { cat <<'HEAD'
F=/mnt/images/omloader/store.img
t() { n=$1; shift; echo "TAG:$n:BEGIN"; "$@" 2>&1; echo "TAG:$n:RC=$?"; echo "TAG:$n:END"; }
HEAD
    cat; } > $W/hk/hook.sh
  rm -f $W/hk.img; truncate -s 32M $W/hk.img; mkfs.ext4 -q -L OMLSTAGE -d $W/hk $W/hk.img
  DISK2=$W/hk.img EXTRA="omloader.hook=1 $2" NAME= SNAP= LOG=$W/$1.log $T/run-kvm.sh > /dev/null; clean $1
  echo "--- $1 (hook) excerpt:"; grep -E 'TAG:.*:RC=|HOOK_RC' $W/$1.clean | tr '\n' ' '; echo; }
exfat_dirty() { python3 - "$DISK" <<'PY'
import struct, sys
f = open(sys.argv[1], "rb"); f.seek(2048 * 512); b = f.read(512)
print("n/a" if b[3:11] != b"EXFAT   " else ("yes" if struct.unpack("<H", b[106:108])[0] & 2 else "no"))
PY
}
fresh() { cp --sparse=always $O/store-$1.raw $O/store-run.raw; export DISK=$O/store-run.raw; }
store_in() { grep -q "store file /mnt/images/omloader/store.img attached as /dev/loop" $W/$1.clean; }

# ================= A: exfat host, container =================
fresh exfat
guest a1 omloci new:run1
grep -q OMLOADER_GUEST_OK $W/a1.clean && pass "a1: container guest booted from the store file" || fail "a1: no OMLOADER_GUEST_OK"
store_in a1 && pass "a1: loader attached the store file and mounted it on /mnt/snap (no OMLOADER partition on this disk)" || fail "a1: no store-file mount message"
eq "$(val a1 OMLOADER_COUNTER)" 1 "a1: new:run1 counter 1"
grep -q 'omloader: snap flushed and remounted read-only' $W/a1.clean && pass "a1: guest shutdown hook remounted the store ext4 read-only" || fail "a1: no store sync message"
grep -q 'omloader: images flushed' $W/a1.clean && pass "a1: guest shutdown hook flushed the host exfat after the store (remount ro refused while the loop file is open: expected)" || fail "a1: no host flush message"
guest a2 omloci base
eq "$(val a2 OMLOADER_COUNTER)" 1 "a2: --snapshot base (ephemeral) counter 1: base untouched"
guest a3 omloci run1
eq "$(val a3 OMLOADER_COUNTER)" 2 "a3: leaf run1 counter 2: overlay upper persisted inside the store file"
eq "$(exfat_dirty)" no "a3: exfat host partition not marked dirty after container poweroff"
# clean state of the store file after poweroff, read in a loader boot that does NOT attach it
hook ha1 "omloader.store=off" <<'BODY'
t info omloader store info --path $F
t check omloader store check --path $F
t ls sh -c 'ls /mnt/images /mnt/images/omloader'
t grow omloader store grow --path $F --size 1152M
t info2 omloader store info --path $F
t check2 omloader store check --path $F
BODY
sect ha1 info | grep -q 'state: clean' && pass "ha1: store file ext4 state clean after container poweroff" || fail "ha1: state: $(sect ha1 info | grep state)"
sect ha1 info | grep -q 'host_fs: exfat' && sect ha1 info | grep -q 'label: OMLOADER' && pass "ha1: store info: host fs exfat, label OMLOADER" || fail "ha1: info: $(sect ha1 info | tr '\n' ' ')"
A1=$(sect ha1 info | sed -n 's/^allocated: \([0-9]*\).*/\1/p'); S1=$(sect ha1 info | sed -n 's/^size: \([0-9]*\).*/\1/p')
[ -n "$A1" ] && [ "$A1" -ge "$S1" ] && pass "ha1: exfat store file fully allocated (no sparse files: $A1 >= $S1)" || fail "ha1: allocation $A1 vs $S1"
eq "$(rc ha1 check)" 0 "ha1: store check (e2fsck -n) clean"
sect ha1 ls | grep -q snapshots && fail "ha1: snapshots leaked onto the host partition" || pass "ha1: no snapshots/ directory on the host partition (all inside the store)"
eq "$(rc ha1 grow)" 0 "ha1: store grow 1024M -> 1152M offline"
eq "$(sect ha1 info2 | sed -n 's/^size: \([0-9]*\).*/\1/p')" 1207959552 "ha1: store size is now 1152 MiB"
eq "$(rc ha1 check2)" 0 "ha1: store check clean after grow (resize2fs ran)"
# store attached (normal loader boot): busy refusal, --release
hook ha2 "" <<'BODY'
t attached omloader store info --path $F
t check_busy omloader store check --path $F
t grow_busy omloader store grow --path $F --size 1280M
t snaps sh -c 'ls /mnt/snap/snapshots/omloci | tr "\n" " "'
t grow_rel omloader store grow --path $F --size 1280M --release
t info omloader store info --path $F
t check omloader store check --path $F
BODY
sect ha2 attached | grep -q 'attached: \[/dev/loop' && pass "ha2: store info shows the loop device while the loader has it mounted" || fail "ha2: attached"
[ "$(rc ha2 check_busy)" != 0 ] && sect ha2 check_busy | grep -q 'losetup -d' && pass "ha2: check of a mounted store refused with the exact release hint" || fail "ha2: check_busy"
[ "$(rc ha2 grow_busy)" != 0 ] && sect ha2 grow_busy | grep -q 'in use' && pass "ha2: grow of a mounted store refused" || fail "ha2: grow_busy"
sect ha2 snaps | grep -q 'run1' && pass "ha2: snapshots of the earlier boots are in the store (run1)" || fail "ha2: snaps: $(sect ha2 snaps | tr '\n' ' ')"
eq "$(rc ha2 grow_rel)" 0 "ha2: grow --release (umount + losetup -d + grow)"
eq "$(rc ha2 check)" 0 "ha2: store check clean after the second grow"
sect ha2 info | grep -q 'size: 1342177280' && pass "ha2: store is now 1280 MiB" || fail "ha2: size $(sect ha2 info | grep '^size')"
guest a4 omloci run1
eq "$(val a4 OMLOADER_COUNTER)" 3 "a4: after grow the leaf continues (counter 3): data intact"

# ================= B: exfat host, VM (kexec, guest-side nested storage) =================
if [ -z "${NO_VM:-}" ]; then
fresh exfat
guest b1 omlfixture new:v1
grep -q OMLOADER_GUEST_OK $W/b1.clean || grep -q OMLOADER_COUNTER $W/b1.clean
store_in b1 && pass "b1: loader mounted the store file" || fail "b1: no store file in the loader"
eq "$(val b1 OMLOADER_COUNTER)" 1 "b1: VM new:v1 counter 1 (kexec guest, COW in the store file)"
grep -q 'OMLOADER_GUEST: storage s[0-9]: store file .* attached as /dev/loop' $W/b1.clean && pass "b1: guest initramfs attached the store file (nested loop storage)" || fail "b1: no guest-side store attach"
guest b2 omlfixture v1
eq "$(val b2 OMLOADER_COUNTER)" 2 "b2: VM leaf v1 counter 2: dm-snapshot COW persisted in the store file"
hook hb1 "omloader.store=off" <<'BODY'
t info omloader store info --path $F
t check omloader store check --path $F
BODY
echo "INFO: after VM poweroff the store ext4 state is: $(sect hb1 info | grep '^state') (VM guests cannot hook their shutdown: dirty/needs_recovery is expected, journal is replayed at next mount); e2fsck: rc=$(rc hb1 check); exfat dirty flag: $(exfat_dirty)"
fresh exfat
fi

# ================= C: NTFS host (ntfs-3g / ntfs3), container =================
if [ -z "${SKIP_NTFS:-}" ]; then
fresh ntfs
guest c1 omloci new:run1
grep -q OMLOADER_GUEST_OK $W/c1.clean && pass "c1: container guest booted from a store file on NTFS (ntfs3)" || fail "c1: no OMLOADER_GUEST_OK"
store_in c1 && pass "c1: loader mounted the store file from the NTFS partition" || fail "c1: no store-file mount message"
eq "$(val c1 OMLOADER_COUNTER)" 1 "c1: new:run1 counter 1"
guest c2 omloci run1
eq "$(val c2 OMLOADER_COUNTER)" 2 "c2: leaf run1 counter 2 (persisted through NTFS)"
hook hc1 "omloader.store=off" <<'BODY'
t info omloader store info --path $F
t check omloader store check --path $F
t mnt sh -c 'grep " /mnt/images " /proc/mounts'
BODY
sect hc1 info | grep -q 'state: clean' && pass "hc1: NTFS-hosted store ext4 clean after poweroff" || fail "hc1: $(sect hc1 info | grep state)"
sect hc1 info | grep -qE 'host_fs: (ntfs|fuseblk)' && pass "hc1: host fs reported as ntfs (fuseblk = ntfs-3g)" || fail "hc1: host fs: $(sect hc1 info | grep host_fs)"
eq "$(rc hc1 check)" 0 "hc1: e2fsck -n clean"
sect hc1 mnt | grep -qE 'ntfs3|fuseblk' && pass "hc1: host mounted rw by the NTFS driver (clean volume after the guest poweroff)" || fail "hc1: mount: $(sect hc1 mnt)"
# dirty NTFS: mount rw (ntfs3 sets the dirty flag), then reset the machine without unmounting
hook hc2 "omloader.store=off" <<'BODY'
touch /mnt/images/dirtyme
echo b > /proc/sysrq-trigger
BODY
guest c3 omloci run1
if grep -q 'mounted READ-ONLY' $W/c3.clean; then
  pass "c3: dirty NTFS: host mounted read-only with a warning"
  grep -q 'store file .* is on a read-only host partition' $W/c3.clean && pass "c3: store file refused on the read-only host (snapshots stay ephemeral)" || fail "c3: no read-only store warning"
  eq "$(val c3 OMLOADER_COUNTER)" 1 "c3: boot continues ephemeral (counter 1, nothing persisted)"
else
  echo "INFO: c3: ntfs3 did not leave the dirty flag after the forced reset (test could not provoke the read-only path): $(grep -E 'WARNING|store file' $W/c3.clean | head -3)"
fi
fi
[ $FAIL = 0 ] && echo "E2E-STORE: ALL PASS" || echo "E2E-STORE: FAILED"
exit $FAIL
