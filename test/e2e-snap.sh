#!/bin/bash
# Run ON THE REMOTE. M2 snapshot management e2e. Guest boots go through the loader as usual; snapshot CLI operations
# (they need root: mounts, loop, dm) run inside the loader VM via the omloader.hook=1 stage-disk mechanism.
# Env: SKIP_BUILD=1 reuse artifacts (ISO, fixtures, disk)  SKIP_ISO=1 only skip the ISO rebuild  NO_VM=1 skip the VM-fixture part
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
export TIMEOUT=${TIMEOUT:-300}
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
eq() { [ "$1" = "$2" ] && pass "$3" || fail "$3 (got '$1', want '$2')"; }
trap 'pkill -P $$ qemu-system 2>/dev/null; pkill -u $USER -f "$W/run\." 2>/dev/null' EXIT
set -e
if [ -z "${SKIP_BUILD:-}" ]; then
  [ -n "${SKIP_ISO:-}" ] || $ROOT/build/build-iso.sh
  [ -n "${NO_VM:-}" ] || [ -f $O/fixture.raw ] || $T/make-fixture.sh
  $T/make-fixture-oci.sh
  P1_MB=${P1_MB:-2300} DISK_GB=4 NAME=omlfixture NO_VM=${NO_VM:-} $T/make-disk.sh
fi
set +e
cp --sparse=always $O/usb.raw $O/usb-run.raw; export DISK=$O/usb-run.raw

clean() { tr -d '\r' < $W/$1.log | sed 's/\x1b\[[0-9;]*[mK]//g' > $W/$1.clean; }
val() { grep -o "$2=[0-9a-z,.-]*" $W/$1.clean | tail -1 | cut -d= -f2; }
guest() { # log image snapshot [marker]
  NAME=$2 SNAP=$3 EXTRA="${4:+omloader.marker=$4}" LOG=$W/$1.log $T/run-kvm.sh > /dev/null; clean $1
  echo "--- $1 (image=$2 snapshot=$3) serial excerpt:"; grep -E 'OMLOADER_(COUNTER|MARKS|DATA_EXT4|GUEST_OK)|adapter applied|space guard|error:' $W/$1.clean | tail -8; }
sect() { sed -n "/TAG:$2:BEGIN/,/TAG:$2:END/p" $W/$1.clean; }
rc() { sect $1 $2 | grep -o 'RC=[0-9]*' | tail -1 | cut -d= -f2; }
hook() { # log  (hook body on stdin)
  rm -rf $W/hk; mkdir -p $W/hk
  printf 'snapshot:\n  retention:\n    keep_last: 1\n    keep_tagged: true\n' > $W/hk/omloader.yaml
  printf 'snapshot:\n  retention:\n    min_free_percent: 99\n' > $W/hk/guard.yaml
  { cat <<'HEAD'
S="--root /mnt/snap/snapshots --images-root /mnt/images/images"
t() { n=$1; shift; echo "TAG:$n:BEGIN"; "$@" 2>&1; echo "TAG:$n:RC=$?"; echo "TAG:$n:END"; }
omloader() { command omloader "$@"; }
HEAD
    cat; } > $W/hk/hook.sh
  rm -f $W/hk.img; truncate -s 32M $W/hk.img; mkfs.ext4 -q -L OMLSTAGE -d $W/hk $W/hk.img
  DISK2=$W/hk.img EXTRA=omloader.hook=1 NAME= SNAP= LOG=$W/$1.log $T/run-kvm.sh > /dev/null; clean $1
  echo "--- $1 (hook) excerpt:"; grep -E 'TAG:.*:RC=|HOOK_RC' $W/$1.clean | tr '\n' ' '; echo; }

# ================= OCI / overlayfs =================
guest g1 omloci new:s1 s1
eq "$(val g1 OMLOADER_MARKS)" "s1," "g1: first guest sees only its own marker"
eq "$(val g1 OMLOADER_DATA_EXT4)" "1" "g1: ext4-file data volume created, counter 1"
eq "$(val g1 OMLOADER_DATA_FSTYPE)" "ext4" "g1: data volume is a mounted ext4 loop file"
eq "$(val g1 OMLOADER_DATA_DIR)" "1" "g1: dir data volume counter 1"

hook h1 <<'BODY'
t create omloader snapshot create omloci s2 --from s1 --note second --tag keep $S
t list omloader snapshot list omloci --tree --sizes $S
t verify omloader snapshot verify omloci $S
t df omloader snapshot df omloci $S
BODY
eq "$(rc h1 create)" 0 "h1: snapshot create s2 (--from s1 --note --tag)"
sect h1 list | grep -q 's2.*tags=keep.*note="second"' && pass "h1: list --tree shows tags and note" || fail "h1: list output"
sect h1 list | grep -q 's1.*frozen' && pass "h1: s1 flagged frozen (has children)" || fail "h1: frozen flag"
sect h1 list | grep -q 'allocated' && pass "h1: list --sizes shows apparent/allocated" || fail "h1: sizes"
eq "$(rc h1 verify)" 0 "h1: verify clean"
sect h1 df | grep -q 'snapshot partition' && pass "h1: snapshot df" || fail "h1: df"

guest g2 omloci s2 s2
eq "$(val g2 OMLOADER_MARKS)" "s1,s2," "g2: s2 sees s1's data (stacked) plus its own"
eq "$(val g2 OMLOADER_COUNTER)" "2" "g2: counter 2"
guest g3 omloci new:s3 s3
eq "$(val g3 OMLOADER_MARKS)" "s1,s2,s3," "g3: chain s1->s2->s3"
eq "$(val g3 OMLOADER_DATA_EXT4)" "3" "g3: ext4 data volume counter 3"

hook h2 <<'BODY'
t del_s1 omloader snapshot delete omloci s1 $S
t del_s2 omloader snapshot delete omloci s2 $S
t discard omloader snapshot rollback omloci s1 --discard $S
t rollback omloader snapshot rollback omloci s1 $S
t list omloader snapshot list omloci --tree $S
t ondisk sh -c 'ls /mnt/snap/snapshots/omloci/s2/upper/var/lib/omloader-test/ /mnt/snap/snapshots/omloci/s3/upper/var/lib/omloader-test/'
t guard_create omloader snapshot create omloci zz -c /mnt/stage/guard.yaml $S
t guard_boot omloader boot --image omloci --snapshot new:zz2 -c /mnt/stage/guard.yaml --images-root /mnt/images/images --snap-root /mnt/snap/snapshots
t after_guard omloader snapshot list omloci $S
BODY
[ "$(rc h2 del_s1)" != 0 ] && sect h2 del_s1 | grep -q children && pass "h2: delete refuses non-leaf s1" || fail "h2: delete s1 not refused"
[ "$(rc h2 del_s2)" != 0 ] && pass "h2: delete refuses non-leaf s2" || fail "h2: delete s2 not refused"
[ "$(rc h2 discard)" != 0 ] && sect h2 discard | grep -q frozen && pass "h2: rollback --discard refuses frozen node (data untouched)" || fail "h2: discard on frozen"
eq "$(rc h2 rollback)" 0 "h2: rollback to s1 (metadata)"
sect h2 list | grep -E 's1' | grep -q '\*' && pass "h2: current marker moved to s1" || fail "h2: current marker"
sect h2 ondisk | grep -q mark-s2 && sect h2 ondisk | grep -q mark-s3 && pass "h2: s2/s3 data still on disk after rollback" || fail "h2: s2/s3 data gone"
[ "$(rc h2 guard_create)" != 0 ] && sect h2 guard_create | grep -q 'space guard' && pass "h2: space guard refuses snapshot create (min_free_percent=99)" || fail "h2: guard create"
[ "$(rc h2 guard_boot)" != 0 ] && sect h2 guard_boot | grep -q 'space guard' && pass "h2: space guard refuses boot" || fail "h2: guard boot"
sect h2 after_guard | grep -q 'zz' && fail "h2: guard left a node behind" || pass "h2: refused create/boot left no snapshot behind"

guest g4 omloci new:s1b s1b
eq "$(val g4 OMLOADER_MARKS)" "s1,s1b," "g4: after rollback to s1 the new branch does not see s2/s3 data"
eq "$(val g4 OMLOADER_COUNTER)" "2" "g4: counter continues from s1's state (1 -> 2)"
eq "$(val g4 OMLOADER_DATA_EXT4)" "4" "g4: snapshot:false data volume survived rollback (3 -> 4)"
eq "$(val g4 OMLOADER_DATA_DIR)" "4" "g4: dir data volume survived rollback"
guest g5 omloci base eph
eq "$(val g5 OMLOADER_MARKS)" "eph," "g5: ephemeral base child sees only base"

hook h3 <<'BODY'
t list_tmp omloader snapshot list omloci $S
t gc_dry omloader snapshot gc omloci --tmp --older-than 0s --dry-run $S
t gc omloader snapshot gc omloci --tmp --older-than 0s $S
t dirs sh -c 'ls /mnt/snap/snapshots/omloci | tr "\n" " "'
t prune_dry omloader snapshot prune omloci --dry-run -c /mnt/stage/omloader.yaml $S
t still_there sh -c 'ls /mnt/snap/snapshots/omloci | tr "\n" " "'
t prune omloader snapshot prune omloci -c /mnt/stage/omloader.yaml $S
t after_prune sh -c 'ls /mnt/snap/snapshots/omloci | tr "\n" " "'
t flatten omloader snapshot flatten omloci s2 --name omloci-flat $S
t flat_dir sh -c 'ls /mnt/images/images/omloci-flat'
t export omloader snapshot export omloci s2 --squashfs /mnt/snap/s2.sqfs $S
t export_ls sh -c 'ls -l /mnt/snap/s2.sqfs; rm -f /mnt/snap/s2.sqfs'
t verify omloader snapshot verify omloci $S
mkdir /mnt/snap/snapshots/omloci/orphan
t verify_bad omloader snapshot verify omloci $S
rmdir /mnt/snap/snapshots/omloci/orphan
t merge omloader snapshot merge omloci s1b $S
BODY
sect h3 list_tmp | grep -q 'tmp-' && pass "h3: ephemeral tmp-* node listed" || fail "h3: no tmp node"
sect h3 gc_dry | grep -q 'would delete 1 snapshot(s): tmp-' && pass "h3: gc --dry-run plans the tmp node" || fail "h3: gc dry"
sect h3 gc | grep -q 'deleted 1 snapshot(s): tmp-' && ! sect h3 dirs | grep -q tmp- && pass "h3: gc removed the tmp node and its upper dir" || fail "h3: gc"
sect h3 prune_dry | grep -q 'would delete 1 snapshot(s): s3' && pass "h3: prune --dry-run (keep_last=1, tagged s2 kept) plans s3" || fail "h3: prune dry: $(sect h3 prune_dry | tr '\n' ' ')"
sect h3 still_there | grep -q ' s3 ' && pass "h3: dry-run changed nothing" || fail "h3: dry-run deleted"
sect h3 prune | grep -q 'deleted 1 snapshot(s): s3' && ! sect h3 after_prune | grep -q ' s3 ' && sect h3 after_prune | grep -q ' s2 ' && pass "h3: prune removed s3 only (s2 tagged, s1 ancestor, s1b current)" || fail "h3: prune"
eq "$(rc h3 flatten)" 0 "h3: overlay flatten of s2 -> omloci-flat"
sect h3 flat_dir | grep -q base.squashfs && sect h3 flat_dir | grep -q image.yaml && pass "h3: flattened image has base.squashfs + image.yaml" || fail "h3: flat dir"
eq "$(rc h3 export)" 0 "h3: overlay export --squashfs"
sect h3 export_ls | grep -q '^-.*/mnt/snap/s2.sqfs' && pass "h3: exported squashfs exists" || fail "h3: export file"
eq "$(rc h3 verify)" 0 "h3: verify OK after gc/prune"
[ "$(rc h3 verify_bad)" = 1 ] && sect h3 verify_bad | grep -q orphan && pass "h3: verify reports an orphan layer (exit 1)" || fail "h3: verify_bad"
[ "$(rc h3 merge)" != 0 ] && sect h3 merge | grep -qE 'cannot merge|not supported' && pass "h3: merge is rejected (v1)" || fail "h3: merge"

guest g6 omloci-flat new:f1 f1
eq "$(val g6 OMLOADER_MARKS)" "f1,s1,s2," "g6: flattened image = merged state of s1+s2 (no s1b/s3/eph), plus f1"
eq "$(val g6 OMLOADER_COUNTER)" "3" "g6: counter inherited from merged s2 state (2 -> 3)"

# ================= VM / dm-snapshot =================
if [ -z "${NO_VM:-}" ]; then
guest v1 omlfixture new:v1
eq "$(val v1 OMLOADER_COUNTER)" "1" "v1: VM first boot counter 1"
hook hv1 <<'BODY'
t create omloader snapshot create omlfixture v2 --from v1 --note vm-second $S
t list omloader snapshot list omlfixture --tree --sizes $S
t verify omloader snapshot verify omlfixture $S
t df omloader snapshot df omlfixture $S
t cows sh -c 'ls -ls /mnt/snap/snapshots/omlfixture/'
BODY
eq "$(rc hv1 create)" 0 "hv1: VM snapshot create v2 (sparse COW)"
sect hv1 list | grep -q 'v1.*frozen' && sect hv1 list | grep -q 'allocated' && pass "hv1: VM list --tree --sizes" || fail "hv1: list"
eq "$(rc hv1 verify)" 0 "hv1: VM verify (COW headers valid / fresh)"
guest v2 omlfixture v2
eq "$(val v2 OMLOADER_COUNTER)" "2" "v2: VM snapshot v2 on top of v1 counter 2"
hook hv2 <<'BODY'
t del_v1 omloader snapshot delete omlfixture v1 $S
t discard omloader snapshot rollback omlfixture v1 --discard $S
t create3 omloader snapshot create omlfixture v3 --from v1 $S
t discard3 omloader snapshot rollback omlfixture v3 --discard $S
t prune_dry omloader snapshot prune omlfixture --dry-run -c /mnt/stage/omloader.yaml $S
t gc_dry omloader snapshot gc omlfixture --dry-run $S
t verify omloader snapshot verify omlfixture $S
t qcow omloader snapshot export omlfixture v2 --qcow2 /mnt/snap/x.qcow2 $S
t flatten omloader snapshot flatten omlfixture v2 --name omlfixture-flat $S
t flat_ls sh -c 'ls -ls /mnt/images/images/omlfixture-flat'
BODY
[ "$(rc hv2 del_v1)" != 0 ] && pass "hv2: VM delete refuses non-leaf" || fail "hv2: del_v1"
[ "$(rc hv2 discard)" != 0 ] && pass "hv2: VM rollback --discard refuses frozen v1" || fail "hv2: discard"
eq "$(rc hv2 discard3)" 0 "hv2: VM rollback --discard on leaf v3 (fresh COW)"
sect hv2 prune_dry | grep -q 'would delete 1 snapshot(s): v2' && pass "hv2: VM prune --dry-run plans v2" || fail "hv2: prune: $(sect hv2 prune_dry | tr '\n' ' ')"
eq "$(rc hv2 verify)" 0 "hv2: VM verify clean"
[ "$(rc hv2 qcow)" != 0 ] && sect hv2 qcow | grep -q qemu-img && pass "hv2: qcow2 export gives qemu-img hint (not in loader)" || fail "hv2: qcow"
eq "$(rc hv2 flatten)" 0 "hv2: VM flatten v2 -> omlfixture-flat (dmsetup stack + dd)"
sect hv2 flat_ls | grep -q base.raw && pass "hv2: flattened base.raw exists" || fail "hv2: flat_ls"
guest v3 omlfixture-flat new:f1
eq "$(val v3 OMLOADER_COUNTER)" "3" "v3: flattened VM boots with merged state (2 -> 3)"
fi
[ $FAIL = 0 ] && echo "E2E-SNAP: ALL PASS" || echo "E2E-SNAP: FAILED"
exit $FAIL
