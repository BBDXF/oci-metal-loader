#!/bin/bash
# Run ON THE REMOTE. Snapshot model e2e (base + one live "current", labelled snapshots, revert, delete/merge, ephemeral boots, prune, flatten). Guest boots go through the loader as usual; snapshot CLI operations
# (they need root: mounts, loop, dm) run inside the loader VM via the omloader.hook=1 stage-disk mechanism.
# Env: SKIP_BUILD=1 reuse artifacts (ISO, fixtures, disk)  SKIP_ISO=1 only skip the ISO rebuild  NO_VM=1 skip the VM-fixture part
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
export EXPERIMENTAL_OCI=1   # the OCI fixture is an experimental feature (opt-in)
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
# boots of "current" continue the live layer (no new layer per boot)
guest g1 omloci "" s1
eq "$(val g1 OMLOADER_MARKS)" "s1," "g1: first guest sees only its own marker"
eq "$(val g1 OMLOADER_COUNTER)" "1" "g1: counter 1"
eq "$(val g1 OMLOADER_DATA_EXT4)" "1" "g1: ext4-file data volume created, counter 1"
eq "$(val g1 OMLOADER_DATA_FSTYPE)" "ext4" "g1: data volume is a mounted ext4 loop file"
eq "$(val g1 OMLOADER_DATA_DIR)" "1" "g1: dir data volume counter 1"
guest g1b omloci "" s1b
eq "$(val g1b OMLOADER_MARKS)" "s1,s1b," "g1b: second boot of current continues the same live state"
eq "$(val g1b OMLOADER_COUNTER)" "2" "g1b: counter 2 (same layer)"

hook h1 <<'BODY'
t layers sh -c 'ls /mnt/snap/snapshots/omloci | tr "\n" " "'
t create omloader snapshot create omloci S1 --note second --tag keep $S
t dup omloader snapshot create omloci S1 $S
t badname omloader snapshot create omloci base $S
t list omloader snapshot list omloci --tree --sizes $S
t verify omloader snapshot verify omloci $S
t df omloader snapshot df omloci $S
t nothing omloader snapshot create omloci S1b $S
BODY
sect h1 layers | grep -q ' n1 \| n1 *$' && ! sect h1 layers | grep -q n2 && pass "h1: two boots of current produced exactly one layer (n1)" || fail "h1: layers: $(sect h1 layers | tr '\n' ' ')"
eq "$(rc h1 create)" 0 "h1: snapshot create S1 (--note --tag)"
[ "$(rc h1 dup)" != 0 ] && [ "$(rc h1 badname)" != 0 ] && pass "h1: duplicate / reserved label refused" || fail "h1: dup/reserved"
sect h1 list | grep -q 'base (original image)' && pass "h1: list --tree shows base (original image) as root" || fail "h1: base root: $(sect h1 list)"
sect h1 list | grep -q 'S1.*tags=keep.*note="second"' && pass "h1: list shows label, tags and note" || fail "h1: list output"
sect h1 list | grep -q '● current (you are here)' && pass "h1: live marker (no changes yet: created at the next boot)" || fail "h1: current marker"
sect h1 list | grep -q 'allocated' && pass "h1: list --sizes shows apparent/allocated" || fail "h1: sizes"
eq "$(rc h1 verify)" 0 "h1: verify clean"
sect h1 df | grep -q 'snapshot partition' && pass "h1: snapshot df" || fail "h1: df"
[ "$(rc h1 nothing)" != 0 ] && sect h1 nothing | grep -q 'nothing to snapshot' && pass "h1: create without changes since S1 is refused" || fail "h1: nothing to snapshot"

guest g2 omloci "" s2
eq "$(val g2 OMLOADER_MARKS)" "s1,s1b,s2," "g2: new live layer on top of S1 sees S1's data plus its own"
eq "$(val g2 OMLOADER_COUNTER)" "3" "g2: counter 3"
hook h2 <<'BODY'
t create2 omloader snapshot create omloci S2 $S
BODY
eq "$(rc h2 create2)" 0 "h2: snapshot create S2"
guest g3 omloci "" s3
eq "$(val g3 OMLOADER_MARKS)" "s1,s1b,s2,s3," "g3: chain S1 -> S2 -> live"
eq "$(val g3 OMLOADER_COUNTER)" "4" "g3: counter 4"
eq "$(val g3 OMLOADER_DATA_EXT4)" "4" "g3: ext4 data volume counter 4 (not part of snapshots)"

hook h3 <<'BODY'
t guard_create omloader snapshot create omloci zz -c /mnt/stage/guard.yaml $S
t guard_boot omloader boot --image omloci --snapshot S2 -c /mnt/stage/guard.yaml --images-root /mnt/images/images --snap-root /mnt/snap/snapshots
t after_guard omloader snapshot list omloci $S
t del_base omloader snapshot delete omloci base $S
t del_missing omloader snapshot delete omloci nope $S
t del_s1 omloader snapshot delete omloci S1 $S
t list_merged omloader snapshot list omloci --tree $S
t merged_ondisk sh -c 'ls /mnt/snap/snapshots/omloci/n2/upper/var/lib/omloader-test/ | tr "\n" " "; echo; ls /mnt/snap/snapshots/omloci | tr "\n" " "'
t verify_merged omloader snapshot verify omloci $S
t revert omloader snapshot revert omloci S2 $S
t after_revert sh -c 'ls /mnt/snap/snapshots/omloci | tr "\n" " "'
t revert_base_keep_nolive omloader snapshot revert omloci nope $S
BODY
[ "$(rc h3 guard_create)" != 0 ] && sect h3 guard_create | grep -q 'space guard' && pass "h3: space guard refuses snapshot create (min_free_percent=99)" || fail "h3: guard create"
[ "$(rc h3 guard_boot)" != 0 ] && sect h3 guard_boot | grep -q 'space guard' && pass "h3: space guard refuses boot (snapshot boot)" || fail "h3: guard boot"
sect h3 after_guard | grep -q 'zz' && fail "h3: guard left a node behind" || pass "h3: refused create/boot left no snapshot behind"
[ "$(rc h3 del_base)" != 0 ] && [ "$(rc h3 del_missing)" != 0 ] && pass "h3: deleting base / an unknown label is refused" || fail "h3: del base/missing"
eq "$(rc h3 del_s1)" 0 "h3: delete S1 (middle snapshot, overlay) merges it into S2"
sect h3 del_s1 | grep -q 'merge S1 into S2' && pass "h3: merge message" || fail "h3: merge message: $(sect h3 del_s1)"
! sect h3 list_merged | grep -q ' S1' && sect h3 list_merged | grep -q 'S2' && pass "h3: S1 is gone from the tree, S2 remains" || fail "h3: tree after merge"
sect h3 merged_ondisk | sed -n 2p | grep -q 'mark-s1 ' && sect h3 merged_ondisk | sed -n 2p | grep -q 'mark-s1b' && sect h3 merged_ondisk | sed -n 2p | grep -q 'mark-s2' && pass "h3: S1's files were merged into S2's upper" || fail "h3: merged ondisk: $(sect h3 merged_ondisk)"
sect h3 merged_ondisk | sed -n 3p | grep -qw 'n1' && fail "h3: S1's layer dir still exists" || pass "h3: S1's layer dir removed"
eq "$(rc h3 verify_merged)" 0 "h3: verify clean after the merge"
eq "$(rc h3 revert)" 0 "h3: revert to S2 (discards the live layer)"
sect h3 after_revert | grep -q 'n3' && fail "h3: live layer n3 not deleted by revert" || pass "h3: revert deleted the discarded live layer"
[ "$(rc h3 revert_base_keep_nolive)" != 0 ] && pass "h3: revert to an unknown label refused" || fail "h3: revert unknown"

guest g4 omloci "" s2b
eq "$(val g4 OMLOADER_MARKS)" "s1,s1b,s2,s2b," "g4: after revert S2 the discarded live data (s3) is gone, S1+S2 data (merged) remain"
eq "$(val g4 OMLOADER_COUNTER)" "4" "g4: counter continues from S2's state (3 -> 4)"
eq "$(val g4 OMLOADER_DATA_EXT4)" "5" "g4: snapshot:false data volume survived revert (4 -> 5)"
eq "$(val g4 OMLOADER_DATA_DIR)" "5" "g4: dir data volume survived revert"
guest g5 omloci base eph
eq "$(val g5 OMLOADER_MARKS)" "eph," "g5: --snapshot base is an ephemeral boot of the original image"
eq "$(val g5 OMLOADER_COUNTER)" "1" "g5: base counter 1"

hook h4 <<'BODY'
t list_eph omloader snapshot list omloci --tree $S
t verify_eph omloader snapshot verify omloci $S
t gc_dry omloader snapshot gc omloci --ephemeral --dry-run $S
t gc omloader snapshot gc omloci --ephemeral $S
t dirs sh -c 'ls /mnt/snap/snapshots/omloci | tr "\n" " "'
BODY
sect h4 list_eph | grep -q '(ephemeral boot)' && pass "h4: ephemeral node of the base boot is listed" || fail "h4: no ephemeral node"
eq "$(rc h4 verify_eph)" 0 "h4: ephemeral leftover is only a note for verify"
sect h4 gc_dry | grep -q 'would delete 1 snapshot(s)' && pass "h4: gc --dry-run plans the ephemeral node" || fail "h4: gc dry"
sect h4 gc | grep -q 'deleted 1 snapshot(s)' && pass "h4: gc removed the ephemeral node and its layer" || fail "h4: gc"

guest g5b omloci "" s2c
eq "$(val g5b OMLOADER_MARKS)" "s1,s1b,s2,s2b,s2c," "g5b: current after an ephemeral boot continues the live state (nothing of g5 persisted)"
eq "$(val g5b OMLOADER_COUNTER)" "5" "g5b: counter 5"
hook h5 <<'BODY'
t create_p1 omloader snapshot create omloci P1 $S
BODY
eq "$(rc h5 create_p1)" 0 "h5: snapshot create P1"
guest g6 omloci "" p2
eq "$(val g6 OMLOADER_COUNTER)" "6" "g6: counter 6 (live layer on top of P1)"
# a stale ephemeral node (boot of a frozen snapshot, read-only) must be cleaned by the next operation
guest g6b omloci P1 eph2
eq "$(val g6b OMLOADER_MARKS)" "s1,s1b,s2,s2b,s2c,eph2," "g6b: booting snapshot P1 is ephemeral: sees P1's state only plus its own marker"
hook h6 <<'BODY'
t keep omloader snapshot revert omloci S2 --keep P2 $S
t list_keep omloader snapshot list omloci --tree $S
BODY
eq "$(rc h6 keep)" 0 "h6: revert S2 --keep P2 (old live state kept as P2; the stale ephemeral node was gc'ed first)"
sect h6 list_keep | grep -q 'P2' && ! sect h6 list_keep | grep -q 'ephemeral boot' && pass "h6: P2 listed, no ephemeral leftovers" || fail "h6: list_keep: $(sect h6 list_keep)"
guest g7 omloci "" p3
eq "$(val g7 OMLOADER_MARKS)" "s1,s1b,s2,p3," "g7: after revert S2 --keep the live state starts from S2 again"
eq "$(val g7 OMLOADER_COUNTER)" "4" "g7: counter 4 (S2 state 3 + 1)"

hook h7 <<'BODY'
t create_p3 omloader snapshot create omloci P3 $S
t prune_dry omloader snapshot prune omloci --dry-run -c /mnt/stage/omloader.yaml $S
t still_there sh -c 'ls /mnt/snap/snapshots/omloci | tr "\n" " "'
t prune omloader snapshot prune omloci -c /mnt/stage/omloader.yaml $S
t after_prune omloader snapshot list omloci $S
t flatten omloader snapshot flatten omloci S2 --name omloci-flat $S
t flat_dir sh -c 'ls /mnt/images/images/omloci-flat'
t export omloader snapshot export omloci S2 --squashfs /mnt/snap/s2.sqfs $S
t export_ls sh -c 'ls -l /mnt/snap/s2.sqfs; rm -f /mnt/snap/s2.sqfs'
t verify omloader snapshot verify omloci $S
mkdir /mnt/snap/snapshots/omloci/orphan
t verify_bad omloader snapshot verify omloci $S
rmdir /mnt/snap/snapshots/omloci/orphan
t merge_cmd omloader snapshot merge omloci S2 $S
BODY
sect h7 prune_dry | grep -q 'would delete 2 snapshot(s): P2 P1' && pass "h7: prune --dry-run (keep_last=1): deletes the older branch P2 and its ancestor P1; S2 (tagged) and P3 kept" || fail "h7: prune dry: $(sect h7 prune_dry | tr '\n' ' ')"
sect h7 still_there | grep -q ' n4 ' && pass "h7: dry-run changed nothing" || fail "h7: dry-run deleted: $(sect h7 still_there)"
sect h7 prune | grep -q 'deleted 2 snapshot(s): P2 P1' && ! sect h7 after_prune | grep -q ' P1 \| P2 ' && sect h7 after_prune | grep -q ' S2 ' && sect h7 after_prune | grep -q ' P3 ' && pass "h7: prune removed P2+P1 only" || fail "h7: prune: $(sect h7 after_prune)"
eq "$(rc h7 flatten)" 0 "h7: overlay flatten of S2 -> omloci-flat"
sect h7 flat_dir | grep -q base.squashfs && sect h7 flat_dir | grep -q image.yaml && pass "h7: flattened image has base.squashfs + image.yaml" || fail "h7: flat dir"
eq "$(rc h7 export)" 0 "h7: overlay export --squashfs"
sect h7 export_ls | grep -q '^-.*/mnt/snap/s2.sqfs' && pass "h7: exported squashfs exists" || fail "h7: export file"
eq "$(rc h7 verify)" 0 "h7: verify OK after prune"
[ "$(rc h7 verify_bad)" = 1 ] && sect h7 verify_bad | grep -q orphan && pass "h7: verify reports an orphan layer (exit 1)" || fail "h7: verify_bad"
[ "$(rc h7 merge_cmd)" != 0 ] && pass "h7: the old merge command is gone" || fail "h7: merge cmd still exists"

guest g8 omloci-flat "" f1
eq "$(val g8 OMLOADER_MARKS)" "s1,s1b,s2,f1," "g8: flattened image = merged state of S1+S2 (no p3/s3/eph), plus f1"
eq "$(val g8 OMLOADER_COUNTER)" "4" "g8: counter inherited from S2's state (3 -> 4)"

# ================= VM / dm-snapshot =================
if [ -z "${NO_VM:-}" ]; then
guest v1 omlfixture "" 
eq "$(val v1 OMLOADER_COUNTER)" "1" "v1: VM first boot counter 1"
guest v1b omlfixture ""
eq "$(val v1b OMLOADER_COUNTER)" "2" "v1b: VM booting current again continues the same COW (counter 2)"
hook hv1 <<'BODY'
t create omloader snapshot create omlfixture V1 --note vm-first $S
t list omloader snapshot list omlfixture --tree --sizes $S
t verify omloader snapshot verify omlfixture $S
t df omloader snapshot df omlfixture $S
t cows sh -c 'ls -ls /mnt/snap/snapshots/omlfixture/'
BODY
eq "$(rc hv1 create)" 0 "hv1: VM snapshot create V1 (no new COW file yet: created at the next boot)"
sect hv1 list | grep -q 'base (original image)' && sect hv1 list | grep -q 'V1.*frozen' && sect hv1 list | grep -q 'allocated' && pass "hv1: VM list --tree --sizes" || fail "hv1: list"
eq "$(rc hv1 verify)" 0 "hv1: VM verify (COW headers valid / fresh)"
guest v2 omlfixture ""
eq "$(val v2 OMLOADER_COUNTER)" "3" "v2: VM live layer on top of V1: counter 3"
hook hv2 <<'BODY'
t del_v1 omloader snapshot delete omlfixture V1 $S
t del_base omloader snapshot delete omlfixture base $S
t revert omloader snapshot revert omlfixture V1 $S
t verify omloader snapshot verify omlfixture $S
t qcow omloader snapshot export omlfixture V1 --qcow2 /mnt/snap/x.qcow2 $S
t qcow_info sh -c 'qemu-img info /mnt/snap/x.qcow2 | head -3; rm -f /mnt/snap/x.qcow2'
t flatten omloader snapshot flatten omlfixture V1 --name omlfixture-flat $S
t flat_ls sh -c 'ls -ls /mnt/images/images/omlfixture-flat'
BODY
[ "$(rc hv2 del_v1)" != 0 ] && sect hv2 del_v1 | grep -q 'not supported' && sect hv2 del_v1 | grep -q flatten && pass "hv2: VM delete of a snapshot with a child (would merge) is refused with the flatten hint" || fail "hv2: del_v1: $(sect hv2 del_v1)"
[ "$(rc hv2 del_base)" != 0 ] && pass "hv2: base cannot be deleted" || fail "hv2: del base"
eq "$(rc hv2 revert)" 0 "hv2: VM revert to V1 (live COW discarded)"
eq "$(rc hv2 verify)" 0 "hv2: VM verify clean"
[ "$(rc hv2 qcow)" = 0 ] && sect hv2 qcow_info | grep -q 'file format: qcow2' && pass "hv2: dm export --qcow2 (qemu-img in the Debian loader) produced a valid qcow2" || fail "hv2: qcow: $(sect hv2 qcow | tr '\n' ' ')"
eq "$(rc hv2 flatten)" 0 "hv2: VM flatten V1 -> omlfixture-flat (dmsetup stack + dd)"
sect hv2 flat_ls | grep -q base.raw && pass "hv2: flattened base.raw exists" || fail "hv2: flat_ls"
guest v3 omlfixture ""
eq "$(val v3 OMLOADER_COUNTER)" "3" "v3: after revert V1 the counter is back to V1's state + 1 (2 -> 3)"
guest v4 omlfixture base
eq "$(val v4 OMLOADER_COUNTER)" "1" "v4: --snapshot base is an ephemeral boot of the original image (counter 1, nothing persisted)"
hook hv3 <<'BODY'
t revert_base omloader snapshot revert omlfixture base $S
t list omloader snapshot list omlfixture --tree $S
BODY
eq "$(rc hv3 revert_base)" 0 "hv3: VM revert base"
guest v5 omlfixture ""
eq "$(val v5 OMLOADER_COUNTER)" "1" "v5: after revert base the VM starts from the original image (counter 1)"
guest v6 omlfixture-flat ""
eq "$(val v6 OMLOADER_COUNTER)" "3" "v6: flattened VM boots with V1's merged state (2 -> 3)"
fi
[ $FAIL = 0 ] && echo "E2E-SNAP: ALL PASS" || echo "E2E-SNAP: FAILED"
exit $FAIL
