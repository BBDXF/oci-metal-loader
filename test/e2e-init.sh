#!/bin/bash
# Run ON THE REMOTE. First-boot init dialog (internal/boot/init.go, internal/tui/initform.go), direct ISO boot, 3 short boots of the OCI fixture:
#   A  guest.init: confirm (default) + boot.default_image: no countdown, the TUI shows the init form ("not initialised"), nothing boots
#   B  guest.init: accept-defaults + guest.timezone Asia/Shanghai + guest.hostname: the guest boots with that hostname and /etc/localtime -> Asia/Shanghai
#   C  same disk, second boot: init.json is reused, the values persist
# The VM (kexec) fixture is not covered here (its guest has no console probe for these values).
# Env: SKIP_BUILD=1 reuse out/omloader.iso, out/oci-images and out/ab-base.raw
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
cleanup() { for p in $W/in-*.pid; do [ -f "$p" ] && kill -9 "$(cat "$p")" 2>/dev/null; done; pkill -u $USER -f "$W/run\." 2>/dev/null; }
trap cleanup EXIT
set -e; mkdir -p $W
if [ -z "${SKIP_BUILD:-}" ]; then $ROOT/build/build-iso.sh; $T/make-fixture-oci.sh; fi
if [ ! -f $O/in-base.raw ] || [ -z "${SKIP_BUILD:-}" ]; then OUT=$O/in-base.raw NO_VM=1 $T/make-disk.sh >/dev/null; fi
set +e
P2_OFF=$(( 2048 + 2048*2048 ))   # sector of the OMLOADER partition (make-disk.sh: P1_MB=2048)
mkdisk() { # $1 tag  $2 boot.yaml text  [$3 last-boot text]
  local d=$W/$1.raw; cp --sparse=always $O/in-base.raw $d
  local sz=$(sfdisk -d $d | awk '/name="OMLOADER"/{for(i=1;i<=NF;i++) if($i=="size=") print $(i+1)+0}' | tr -d ,)
  local st=$(sfdisk -d $d | awk '/name="OMLOADER"/{for(i=1;i<=NF;i++) if($i=="start=") print $(i+1)+0}' | tr -d ,)
  dd if=$d of=$W/$1.p2 bs=512 skip=$st count=$sz status=none
  printf '%s' "$2" > $W/$1.boot.yaml; [ -n "${3:-}" ] && printf '%s' "$3" > $W/$1.last
  { echo "mkdir omloader"; echo "write $W/$1.boot.yaml omloader/boot.yaml"; [ -n "${3:-}" ] && echo "write $W/$1.last omloader/last-boot"; } > $W/$1.dbg
  debugfs -w -f $W/$1.dbg $W/$1.p2 >/dev/null 2>&1
  dd if=$W/$1.p2 of=$d bs=512 seek=$st conv=notrunc,sparse status=none; rm -f $W/$1.p2
}
clean() { tr -d '\r' < $W/$1.log | sed 's/\x1b\[[0-9;=?]*[a-zA-Z]//g' > $W/$1.clean; }
waitfor() { for i in $(seq 1 $3); do clean $1; grep -aqE "$2" $W/$1.clean && return 0; sleep 1; done; return 1; }
vmstart() { # $1 tag $2 extra kernel args
  rm -f $W/$1.log; ( DISK=$W/$1.raw LOG=$W/$1.log SERIALSOCK=$W/$1.sock EXTRA="$2" TIMEOUT=${3:-240} $T/run-kvm.sh > $W/$1.run 2>&1 & ); sleep 3
  pgrep -u $USER -f "$W/run\.[0-9]*/boot.iso|$W/$1.raw" | head -1 > $W/$1.pid; }
vmstop() { pkill -u $USER -f "$W/$1.raw" 2>/dev/null; sleep 1; }
vmalive() { pgrep -u $USER -f "$W/$1.raw" >/dev/null; }
send() { $T/ventoy-ser.py $W/$1.sock "$2"; }
CFG() { printf 'guest:\n  init: %s\n  timezone: Asia/Shanghai\n  hostname: initbox\nboot:\n  default_image: omloci\n  timeout: 4\n  extra_cmdline_flags: [experimental=oci]\n' "$1"; }
# ---- A: confirm blocks the autoboot (INIT_CONFIRM=1 keeps the policy; run-kvm.sh would otherwise add omloader.init=accept)
mkdisk in-a "$(CFG confirm)"
export INIT_CONFIRM=1
vmstart in-a ""
if waitfor in-a 'OMLOADER_TUI_READY' 150; then
  sleep 12; clean in-a; C=$W/in-a.clean
  grep -aq "image 'omloci' is not initialised -- no countdown" $C && pass "A: un-initialised image gets no countdown" || fail "A: no 'not initialised' marker"
  ! grep -aq "Booting 'omloci'" $C && ! grep -aq OMLOADER_GUEST_OK $C && vmalive in-a && pass "A: nothing booted, the loader stays in the TUI" || fail "A: booted or died"
else fail "A: no TUI"; fi
vmstop in-a
unset INIT_CONFIRM
# ---- B: accept-defaults applies timezone + hostname; C: second boot of the same disk keeps them
mkdisk in-b "$(CFG accept-defaults)"
for n in 1 2; do
  vmstart in-b ""
  if waitfor in-b 'OMLOADER_COUNTER=[0-9]|OMLOADER_LOADER_FAILED' 220; then
    C=$W/in-b.clean; sleep 3; clean in-b
    grep -aq 'OMLOADER_GUEST_HOSTNAME=initbox' $C && pass "B$n: hostname from guest.hostname" || fail "B$n: hostname"
    grep -aq 'OMLOADER_GUEST_TZLINK=/usr/share/zoneinfo/Asia/Shanghai' $C && grep -aq 'OMLOADER_GUEST_TIMEZONE=Asia/Shanghai' $C && pass "B$n: /etc/localtime -> Asia/Shanghai" || fail "B$n: timezone"
  else fail "B$n: no boot result"; fi
  vmstop in-b; sleep 2
done
exit $FAIL
