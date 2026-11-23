#!/bin/bash
# Run ON THE REMOTE. M4 online pull e2e: a tiny simplestreams tree is served by python3 http.server on the host
# (QEMU user-net guest reaches it as 10.0.2.2). The loader VM runs `omloader pull` (dead primary + working mirror)
# for a container and a VM image, both are booted; then the server is stopped and `omloader pull --offline`
# (+ cache.offline: on) builds second copies from the cache only, which boot again.
# Env: SKIP_BUILD=1 reuse out/ (ISO, fixtures, lxdsrv)
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
export TIMEOUT=${TIMEOUT:-300}
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
eq() { [ "$1" = "$2" ] && pass "$3" || fail "$3 (got '$1', want '$2')"; }
SRVPID=
trap 'pkill -P $$ qemu-system 2>/dev/null; pkill -u $USER -f "$W/run\." 2>/dev/null; [ -n "$SRVPID" ] && kill $SRVPID 2>/dev/null' EXIT
set -e
mkdir -p $W
if [ -z "${SKIP_BUILD:-}" ]; then
  $ROOT/build/build-iso.sh
  [ -f $O/fixture.raw ] || $T/make-fixture.sh
  [ -f $O/oci-images/omloci/base.squashfs ] || $T/make-fixture-oci.sh
  $T/make-pull-fixture.sh
fi
# ---- empty GPT disk: p1 exfat VTOYDATA 3 GiB, p2 ext4 OMLOADER 2.5 GiB (snapshots + download cache) ----
D=$O/pull-usb.raw; P1=$W/pl1.img; P2=$W/pl2.img
rm -f $D $P1 $P2; truncate -s 3G $P1; truncate -s 2560M $P2
mkfs.exfat -L VTOYDATA $P1 >/dev/null; mkfs.ext4 -q -L OMLOADER $P2
truncate -s $((3*1024+2560+8))M $D
printf 'label: gpt\nstart=2048, size=3GiB, type=EBD0A0A2-B9E5-4433-87C0-68B6B72699C7, name=VTOYDATA\ntype=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=OMLOADER\n' | sfdisk -q $D
dd if=$P1 of=$D bs=1M seek=1 conv=notrunc,sparse status=none
dd if=$P2 of=$D bs=512 seek=$((2048+3*1024*2048)) conv=notrunc,sparse status=none
rm -f $P1 $P2
export DISK=$D
# ---- publish: http.server on 127.0.0.1 (= 10.0.2.2 from the guest); a second port stays closed (dead primary) ----
PORT=$((20000 + RANDOM % 20000)); DEAD=$((PORT + 1))
python3 -m http.server $PORT --bind 127.0.0.1 --directory $O/lxdsrv/www > $W/pull-http.log 2>&1 &
SRVPID=$!
sleep 1
curl -sf http://127.0.0.1:$PORT/streams/v1/index.json >/dev/null && pass "test simplestreams server up on 127.0.0.1:$PORT (guest: 10.0.2.2:$PORT)" || { fail "http server"; exit 1; }
set +e
clean() { tr -d '\r' < $W/$1.log | sed 's/\x1b\[[0-9;]*[mK]//g' > $W/$1.clean; }
sect() { sed -n "/TAG:$2:BEGIN/,/TAG:$2:END/p" $W/$1.clean; }
rc() { sect $1 $2 | grep -o 'RC=[0-9]*' | tail -1 | cut -d= -f2; }
hook() { # log extra-config-yaml ; hook body on stdin
  rm -rf $W/hk; mkdir -p $W/hk
  cat > $W/hk/omloader.yaml <<EOT
network:
  retries: 1
  timeout_sec: 5
sources:
  lxc-images:
    type: simplestreams
    url: http://10.0.2.2:$DEAD
    mirrors: [http://10.0.2.2:$PORT]
cache:
  path: /mnt/snap/cache
$2
EOT
  { cat <<'HEAD'
export DM_DISABLE_UDEV=1
C="-c /mnt/stage/omloader.yaml"
t() { n=$1; shift; echo "TAG:$n:BEGIN"; "$@" 2>&1; echo "TAG:$n:RC=$?"; echo "TAG:$n:END"; }
for i in $(seq 1 30); do ip route | grep -q '^default' && break; sleep 1; done
HEAD
    cat; } > $W/hk/hook.sh
  rm -f $W/hk.img; truncate -s 32M $W/hk.img; mkfs.ext4 -q -L OMLSTAGE -d $W/hk $W/hk.img
  DISK2=$W/hk.img EXTRA=omloader.hook=1 NAME= SNAP= LOG=$W/$1.log $T/run-kvm.sh > /dev/null; clean $1
  echo "--- $1 (hook) excerpt:"; grep -E 'TAG:.*:RC=|HOOK_RC|warning|error' $W/$1.clean | cut -c1-200 | head -30; }
guest() { # log image snapshot
  NAME=$2 SNAP=$3 LOG=$W/$1.log $T/run-kvm.sh > /dev/null; clean $1
  GOT=$(grep -o 'OMLOADER_COUNTER=[0-9]*' $W/$1.clean | tail -1 | cut -d= -f2)
  echo "--- $1 (image=$2 snapshot=$3) serial excerpt:"; grep -E 'OMLOADER_(COUNTER|GUEST_OK|GUEST_ROOTFS)|error:' $W/$1.clean | tail -6; }

# ================= online pull: dead primary, working mirror =================
hook p1 "" <<'BODY'
t netstat omloader net status $C
t srctest omloader source test $C
t list omloader images list $C
t pullct omloader pull lxc:omltest/ct --name pullct $C
t pullvm omloader pull lxc:omltest/vm --vm --name pullvm $C
t ctyaml cat /mnt/images/images/pullct/image.yaml
t vmyaml cat /mnt/images/images/pullvm/image.yaml
t cachels omloader cache ls $C
BODY
sect p1 srctest | grep -q 'FAIL.*:'$DEAD && sect p1 srctest | grep -q "OK .*:$PORT" && pass "source test: dead primary FAIL, mirror OK (with timing)" || fail "source test output: $(sect p1 srctest | tr '\n' '|')"
sect p1 netstat | grep -q 'default route: default via' && sect p1 netstat | grep -q "OK .*:$PORT" && pass "net status: default route + per-mirror reachability" || fail "net status: $(sect p1 netstat | tr '\n' '|')"
sect p1 list | grep -q 'lxc:omltest/ct/default *container' && sect p1 list | grep -q 'lxc:omltest/vm/default *vm' && pass "images list shows the container and the vm image" || fail "images list: $(sect p1 list | tr '\n' '|')"
eq "$(rc p1 pullct)" 0 "pull container via mirror fallback (rc)"
eq "$(rc p1 pullvm)" 0 "pull VM via mirror fallback (rc)"
sect p1 pullct | grep -q 'sha256 verified' && pass "container download digest-verified" || fail "no verification line"
sect p1 ctyaml | grep -q 'type: lxd-container' && pass "pullct/image.yaml type lxd-container" || fail "ct image.yaml"
sect p1 vmyaml | grep -q 'type: lxd-vm' && sect p1 vmyaml | grep -q 'root_partition: 1' && sect p1 vmyaml | grep -q 'path: /boot/vmlinuz' && sect p1 vmyaml | grep -q 'initrd: /boot/initrd.img' \
  && pass "VM probe filled kernel.path/initrd/root_partition: $(sect p1 vmyaml | grep -E 'path:|initrd:|root_partition:' | tr -s ' \n' ' ')" || fail "vm image.yaml: $(sect p1 vmyaml | tr '\n' '|')"
sect p1 cachels | grep -q '^blob ' && pass "cache ls lists content-addressed blobs" || fail "cache ls"

guest g1 pullct ""
grep -q OMLOADER_GUEST_OK $W/g1.clean && eq "$GOT" 1 "pulled container boots (current), counter" || fail "pulled container did not boot"
guest g2 pullvm ""
grep -q OMLOADER_GUEST_OK $W/g2.clean && eq "$GOT" 1 "pulled VM boots via kexec + guest reassembly (current), counter" || fail "pulled VM did not boot"

# ================= offline: server stopped, builds come from the cache only =================
kill $SRVPID; wait $SRVPID 2>/dev/null; SRVPID=
curl -sf -m 3 http://127.0.0.1:$PORT/streams/v1/index.json >/dev/null && fail "server still up" || pass "test server stopped"
hook p2 "  offline: on" <<'BODY'
t pullct2 omloader pull lxc:omltest/ct --name pullct2 --offline $C
t pullvm2 omloader pull lxc:omltest/vm --vm --name pullvm2 $C
t miss omloader pull lxc:omltest/nonexistent --offline $C
t srctest omloader source test $C
BODY
eq "$(rc p2 pullct2)" 0 "offline pull of the container from the cache (--offline), server down"
eq "$(rc p2 pullvm2)" 0 "offline pull of the VM from the cache (cache.offline: on), server down"
[ "$(rc p2 miss)" != 0 ] && [ -n "$(rc p2 miss)" ] && pass "offline pull of an unknown image fails" || fail "offline miss rc=$(rc p2 miss)"
sect p2 srctest | grep -q 'skipped: offline' && pass "source test skips the network in offline mode" || fail "source test offline"
guest g3 pullct2 ""
grep -q OMLOADER_GUEST_OK $W/g3.clean && eq "$GOT" 1 "offline-pulled container boots" || fail "offline container did not boot"
guest g4 pullvm2 ""
grep -q OMLOADER_GUEST_OK $W/g4.clean && eq "$GOT" 1 "offline-pulled VM boots" || fail "offline VM did not boot"
rm -f $D
[ $FAIL = 0 ] && echo "E2E-PULL: ALL PASS" || echo "E2E-PULL: FAILED"
exit $FAIL
