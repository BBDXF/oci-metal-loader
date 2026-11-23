#!/bin/bash
# Run ON THE REMOTE. TUI e2e over the QEMU serial console (driver: test/tui-drive.py, pty + tiny VT emulator):
#  run1: loader boots into the TUI (no kernel flags) -> images list from the fixture disk -> detail/tree (base + live state; snapshot create refused on a never-booted image)
#        -> Pull tab: `source test` + filtered catalog + pull of a container from the local fake simplestreams
#        (10.0.2.2:PORT) -> boot it (dry-run preview, then continue current) -> container boots via switch-root, counter 1
#  run2: second loader boot, live state persisted; create snapshot s1 (note+tag) and revert to it from the TUI; Reboot from the TUI ends the VM
#  run3: Esc -> shell -> `exit` -> TUI again; boot current again (on top of s1): counter 2
# Env: SKIP_BUILD=1 reuse out/ and the disk built earlier by this script
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
export W EXPERIMENTAL_OCI=1
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
SRVPID=
trap 'pkill -P $$ qemu-system 2>/dev/null; pkill -u $USER -f "$W/run\." 2>/dev/null; [ -n "$SRVPID" ] && kill $SRVPID 2>/dev/null' EXIT
set -e
mkdir -p $W
D=$O/tui-usb.raw
PORT=${SRCPORT:-$((20000 + RANDOM % 20000))}; export SRCPORT=$PORT
if [ -z "${SKIP_BUILD:-}" ]; then
  $ROOT/build/build-iso.sh
  [ -f $O/oci-images/omloci/base.squashfs ] || $T/make-fixture-oci.sh
  [ -f $O/fixture.raw ] || $T/make-fixture.sh
  $T/make-pull-fixture.sh
fi
# data disk: the OCI fixture on VTOYDATA + omloader/boot.yaml (no default_image => no countdown) pointing at the local fake server
rm -rf $W/tui-extra; mkdir -p $W/tui-extra/omloader
cat > $W/tui-extra/omloader/boot.yaml <<EOT
network:
  retries: 1
  timeout_sec: 5
sources:
  lxc-images:
    type: simplestreams
    url: http://10.0.2.2:$PORT
cache:
  path: /mnt/snap/cache
EOT
VTOY_EXTRA=$W/tui-extra NO_VM=1 DISK_GB=${DISK_GB:-6} $T/make-disk.sh
cp --sparse=always $O/usb.raw $D; export DISK=$D
python3 -m http.server $PORT --bind 127.0.0.1 --directory $O/lxdsrv/www > $W/tui-http.log 2>&1 &
SRVPID=$!
sleep 1
curl -sf http://127.0.0.1:$PORT/streams/v1/index.json >/dev/null && pass "test simplestreams server up on 127.0.0.1:$PORT" || { fail "http server"; exit 1; }
set +e
for r in run1 run2 run3; do
  echo "=== $r"
  python3 $T/tui-drive.py $r; rc=$?
  echo "=== $r exit $rc"
  [ $rc = 0 ] || FAIL=1
done
rm -f $D
[ $FAIL = 0 ] && echo "E2E-TUI: ALL PASS" || echo "E2E-TUI: FAILED"
exit $FAIL
