#!/bin/bash
# Run ON THE REMOTE. Auto-boot (boot.default_image + boot.timeout) owned by the TUI, direct ISO boot (no Ventoy), 5 short boots:
#   A  ui=tui:   banner marker `Booting 'omloci' (current) in 5 s` (timeout 4 is raised to the 5 s minimum), expiry boots ONCE,
#                boot.extra_cmdline_flags apply (experimental=oci is needed for the OCI image)
#   B  cancel:   a key on the serial console cancels (`boot cancelled`); no boot afterwards; leave to the shell and back to the TUI -> no second countdown
#   C  ui=shell: plain shell countdown (`press any key within 4s`), still boots, the TUI never starts
#   D  default_image: last (<data>/omloader/last-boot) boots the recorded image
#   E  poweroff_on_fail=1: unknown default image -> OMLOADER_LOADER_FAILED and the VM powers off
# boot.yaml / last-boot are written into the OMLOADER ext4 partition of a copy of the data disk with debugfs (no root needed).
# Env: SKIP_BUILD=1 reuse out/omloader.iso + out/oci-images and out/ab-base.raw
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
cleanup() { for p in $W/ab-*.pid; do [ -f "$p" ] && kill -9 "$(cat "$p")" 2>/dev/null; done; pkill -u $USER -f "$W/run\." 2>/dev/null; }
trap cleanup EXIT
set -e; mkdir -p $W
if [ -z "${SKIP_BUILD:-}" ]; then $ROOT/build/build-iso.sh; $T/make-fixture-oci.sh; fi
if [ ! -f $O/ab-base.raw ] || [ -z "${SKIP_BUILD:-}" ]; then OUT=$O/ab-base.raw NO_VM=1 $T/make-disk.sh >/dev/null; fi
set +e
P2_OFF=$(( 2048 + 2048*2048 ))   # sector of the OMLOADER partition (make-disk.sh: P1_MB=2048)
mkdisk() { # $1 tag  $2 boot.yaml text  [$3 last-boot text]
  local d=$W/$1.raw; cp --sparse=always $O/ab-base.raw $d
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
FLAGS='  extra_cmdline_flags: [poweroff_on_fail=1, experimental=oci]\n'
# ---- A: ui=tui, expiry boots once
mkdisk ab-a "$(printf 'boot:\n  default_image: omloci\n  default_snapshot: current\n  timeout: 4\n  extra_cmdline_flags: [poweroff_on_fail=1, experimental=oci]\n')"
vmstart ab-a ""
if waitfor ab-a 'OMLOADER_COUNTER=[0-9]|OMLOADER_LOADER: .boot. returned|OMLOADER_LOADER_FAILED' 200; then
  C=$W/ab-a.clean
  grep -aq 'OMLOADER_LOADER: boot config .*default_image=omloci snapshot=current timeout=4s -- the TUI shows the countdown' $C && pass "A: autoboot.sh hands the countdown to the TUI (boot config marker)" || fail "A: no handover marker"
  grep -aq "OMLOADER_LOADER: Booting 'omloci' (current) in 5 s" $C && pass "A: TUI countdown banner marker, timeout 4 raised to the 5 s minimum" || fail "A: no banner marker"
  grep -aq 'OMLOADER_TUI_READY' $C && ! grep -aq 'press any key within' $C && pass "A: TUI started, no shell countdown" || fail "A: TUI/shell countdown mix"
  [ "$(grep -ac "OMLOADER_LOADER: Booting 'omloci' (current) now" $C)" = 1 ] && [ "$(grep -ac 'OMLOADER_GUEST_OK' $C)" -ge 1 ] && [ "$(grep -ao 'OMLOADER_COUNTER=[0-9]*' $C | tail -1)" = OMLOADER_COUNTER=1 ] && pass "A: expiry booted the image exactly once (extra_cmdline_flags experimental=oci applied, counter 1)" || fail "A: expiry boot"
else fail "A: no boot result"; grep -a 'OMLOADER_LOADER' $W/ab-a.clean | tail -5; fi
vmstop ab-a
# ---- B: cancel, shell, back to the TUI: no second countdown
mkdisk ab-b "$(printf 'boot:\n  default_image: omloci\n  timeout: 6\n  extra_cmdline_flags: [experimental=oci]\n')"
vmstart ab-b ""
if waitfor ab-b "OMLOADER_LOADER: Booting 'omloci' \(current\) in 6 s" 120; then
  send ab-b 'x'
  waitfor ab-b 'boot cancelled' 15 && pass "B: a key on the serial console cancels the TUI countdown (marker: boot cancelled)" || fail "B: no cancel marker"
  sleep 12; clean ab-b
  ! grep -aq 'OMLOADER_GUEST_OK\|Booting .omloci. (current) now' $W/ab-b.clean && pass "B: no boot after the cancel" || fail "B: booted after cancel"
  send ab-b 'S'; sleep 2; send ab-b '\r'; waitfor ab-b 'OMLOADER_SHELL_READY' 20 && { send ab-b 'exit\n'; sleep 3; }
  n0=$(grep -ac OMLOADER_TUI_READY $W/ab-b.clean); waitfor ab-b '.' 1; sleep 15; clean ab-b
  [ "$(grep -ac OMLOADER_TUI_READY $W/ab-b.clean)" -ge 2 ] && pass "B: TUI restarted after leaving the shell" || fail "B: TUI did not restart"
  [ "$(grep -ac "OMLOADER_LOADER: Booting 'omloci' (current) in" $W/ab-b.clean)" = 1 ] && ! grep -aq 'OMLOADER_GUEST_OK' $W/ab-b.clean && pass "B: no second countdown after leaving the TUI (one banner marker, no boot)" || fail "B: second countdown or boot"
else fail "B: no countdown banner"; fi
vmstop ab-b
# ---- C: ui=shell keeps the plain countdown
mkdisk ab-c "$(printf 'boot:\n  default_image: omloci\n  timeout: 4\n  extra_cmdline_flags: [experimental=oci]\n')"
vmstart ab-c "omloader.ui=shell"
if waitfor ab-c 'OMLOADER_COUNTER=[0-9]|OMLOADER_LOADER: .boot. returned|OMLOADER_LOADER_FAILED' 200; then
  C=$W/ab-c.clean
  grep -aq 'press any key within 4s for the menu' $C && grep -aq "OMLOADER_LOADER: Booting 'omloci' (current) in [0-9] s -- press any key for the menu" $C && pass "C: ui=shell prints the plain countdown lines" || fail "C: no plain countdown"
  ! grep -aq 'OMLOADER_TUI_READY' $C && grep -aq OMLOADER_GUEST_OK $C && pass "C: no TUI, image booted from the shell path" || fail "C: TUI started or no boot"
else fail "C: no boot result"; fi
vmstop ab-c
# ---- D: default_image: last
mkdisk ab-d "$(printf 'boot:\n  default_image: last\n  timeout: 4\n  extra_cmdline_flags: [experimental=oci]\n')" "$(printf 'omloci\ncurrent\n')"
vmstart ab-d ""
if waitfor ab-d 'OMLOADER_COUNTER=[0-9]|OMLOADER_LOADER: .boot. returned|OMLOADER_LOADER_FAILED' 200; then
  grep -aq 'default_image=omloci snapshot=current' $W/ab-d.clean && grep -aq OMLOADER_GUEST_OK $W/ab-d.clean && pass "D: default_image: last resolved to the recorded image and booted" || fail "D: last"
else fail "D: no boot result"; fi
vmstop ab-d
# ---- E: poweroff_on_fail=1 after a failed TUI auto-boot
mkdisk ab-e "$(printf 'boot:\n  default_image: nosuchimage\n  timeout: 4\n  extra_cmdline_flags: [poweroff_on_fail=1, experimental=oci]\n')"
vmstart ab-e ""
if waitfor ab-e 'OMLOADER_LOADER_FAILED' 150; then
  pass "E: failed TUI auto-boot printed OMLOADER_LOADER_FAILED"
  for i in $(seq 1 40); do vmalive ab-e || break; sleep 1; done
  vmalive ab-e && fail "E: VM still running after OMLOADER_LOADER_FAILED" || pass "E: poweroff_on_fail=1 powered the VM off"
else fail "E: no FAILED marker"; grep -a 'OMLOADER_LOADER\|error' $W/ab-e.clean | tail -5; fi
vmstop ab-e
[ $FAIL = 0 ] && echo "E2E-AUTOBOOT: ALL PASS" || echo "E2E-AUTOBOOT: FAILED"
exit $FAIL
