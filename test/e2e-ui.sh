#!/bin/bash
# Run ON THE REMOTE. Usability e2e: container console login (autologin tty1 + ttyS0, users, passwords, sudo), the
# busybox debug-shell fallback for an image without getty/login, idempotency over a second boot, and a CLEAN
# OMLOADER (ext4) / VTOYDATA (exfat) filesystem after guest poweroff (superblock read directly from the disk image).
# Also reproduces the pre-change behaviour (runtime.shutdown_sync: false) to show the difference.
# Env: SKIP_BUILD=1 reuse artifacts in out/
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
export EXPERIMENTAL_OCI=1 NAME= TIMEOUT=${TIMEOUT:-300}
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
eq() { [ "$1" = "$2" ] && pass "$3" || fail "$3 (got '$1', want '$2')"; }
trap 'pkill -P $$ qemu-system 2>/dev/null; pkill -u $USER -f "$W/run\." 2>/dev/null' EXIT
set -e
if [ -z "${SKIP_BUILD:-}" ]; then
  $ROOT/build/build-iso.sh
  mkdir -p $W
  cat > $W/rt-ui.yaml <<'EOT'
autologin_user: alice
root_password: rootpw
users:
  - name: alice
    password: alicepw
    groups: [users]
    sudo: true
    ssh_authorized_keys:
      - ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMLTESTKEY alice@test
  - name: bob
    password_hash: "$6$omlbobsalt$Hys0l/UlHCGs4hmuxoUYs6RtdxV6lNZpiu/8FvlfzrkMXpZ/.pLp/X8goPOIYmk2ATZznns9EkCkkLRkPSSYO."
    shell: /bin/sh
  - name: carol
    sudo: true
EOT
  OCI_NAME=omlui OCI_RUNTIME_EXTRA=$W/rt-ui.yaml $T/make-fixture-oci.sh
  $T/make-fixture-bb.sh
  NO_VM=1 $T/make-disk.sh
fi
set +e
cp --sparse=always $O/usb.raw $O/usb-run.raw; export DISK=$O/usb-run.raw

clean() { tr -d '\r' < $W/$1.log | sed 's/\x1b\[[0-9;]*[mK]//g' > $W/$1.clean; }
val() { grep -o "$2=[^ ]*" $W/$1.clean | tail -1 | cut -d= -f2-; }
guest() { # log image snapshot
  NAME=$2 SNAP=$3 LOG=$W/$1.log $T/run-kvm.sh > /dev/null; clean $1
  echo "--- $1 (image=$2 snapshot=$3) excerpt:"; grep -E 'OMLOADER_(LOGIN|UI|BB|DEBUG)|adapter applied|warning|error' $W/$1.clean | tail -30; }
# filesystem state straight from the disk image: ext4 s_state/needs_recovery, exfat VolumeDirty
fsstate() { python3 - "$DISK" <<'PY'
import json, struct, subprocess, sys
d = sys.argv[1]
parts = json.loads(subprocess.check_output(["sfdisk", "-J", d]))["partitiontable"]["partitions"]
f = open(d, "rb")
def rd(off, n): f.seek(off); return f.read(n)
p1, p2 = parts[0]["start"] * 512, parts[1]["start"] * 512
state = struct.unpack("<H", rd(p2 + 1024 + 0x3A, 2))[0]
incompat = struct.unpack("<I", rd(p2 + 1024 + 0x60, 4))[0]
vf = struct.unpack("<H", rd(p1 + 106, 2))[0] if rd(p1 + 3, 8) == b"EXFAT   " else -1
print("ext4_valid=%s needs_recovery=%s exfat_dirty=%s" % ("yes" if state & 1 else "no", "yes" if incompat & 4 else "no", ("yes" if vf & 2 else "no") if vf >= 0 else "n/a"))
PY
}
fsv() { fsstate | tr ' ' '\n' | sed -n "s/^$1=//p"; }

# ---------- 1. first boot of omlui: login, users, sudo, clean shutdown ----------
echo "fs before any guest boot: $(fsstate)"
guest ui1 omlui new:run1
grep -q OMLOADER_GUEST_OK $W/ui1.clean && pass "ui1: guest booted" || fail "ui1: no OMLOADER_GUEST_OK"
grep -q 'OMLOADER_LOGIN_OK user=alice tty=/dev/tty1' $W/ui1.clean && pass "ui1: autologin on tty1 as runtime.autologin_user (alice)" || fail "ui1: no tty1 login marker"
grep -q 'OMLOADER_LOGIN_OK user=alice tty=/dev/ttyS0' $W/ui1.clean && pass "ui1: autologin on ttyS0 as alice" || fail "ui1: no ttyS0 login marker"
eq "$(val ui1 OMLOADER_UI_USERS)" 3 "ui1: alice, bob, carol exist exactly once"
eq "$(val ui1 OMLOADER_UI_SHADOW_ENTRIES)" 4 "ui1: shadow has root+3 users"
eq "$(val ui1 OMLOADER_UI_ALICE_HOME)" alice:alice:755 "ui1: home created, owned via numeric ids"
eq "$(val ui1 OMLOADER_UI_ALICE_KEYS)" alice:600 "ui1: authorized_keys 0600 owned by alice"
eq "$(val ui1 OMLOADER_UI_SUDOERS)" 440 "ui1: sudoers drop-in 0440"
eq "$(val ui1 OMLOADER_UI_PW_ALICE)" ok "ui1: alice plain password stored as working SHA-512 crypt"
eq "$(val ui1 OMLOADER_UI_PW_BOB)" ok "ui1: bob password_hash stored verbatim and verifies"
eq "$(val ui1 OMLOADER_UI_PW_ROOT)" ok "ui1: root_password applied"
eq "$(val ui1 OMLOADER_UI_PW_WRONG)" bad "ui1: wrong password rejected"
eq "$(val ui1 OMLOADER_UI_SUDO_ALICE)" 0 "ui1: sudo with password (PAM, via stdin) works for alice"
eq "$(val ui1 OMLOADER_UI_SUDO_CAROL)" 0 "ui1: passwordless sudo for carol (no password configured)"
eq "$(val ui1 OMLOADER_UI_SUDO_BOB)" denied "ui1: bob (sudo: false) is denied"
eq "$(val ui1 OMLOADER_UI_SNAPMNT)" 1 "ui1: OMLOADER partition reachable at /.omloader/snap in the guest"
grep -q 'plain-text password' $W/ui1.clean && pass "ui1: loader warned about plain-text passwords" || fail "ui1: no plain-password warning"
grep -qE 'alicepw|rootpw' $W/ui1.clean && fail "ui1: a password leaked into the serial log" || pass "ui1: no password in the serial log"
grep -q 'omloader: snap flushed and remounted read-only' $W/ui1.clean && pass "ui1: shutdown hook remounted the OMLOADER partition read-only" || fail "ui1: shutdown hook message missing"
S1=$(fsstate); echo "fs after ui1: $S1"
eq "$(fsv needs_recovery)" no "ui1: OMLOADER ext4 has no needs_recovery flag after guest poweroff"
eq "$(fsv ext4_valid)" yes "ui1: OMLOADER ext4 superblock state is clean"
[ "$(fsv exfat_dirty)" = no ] && pass "ui1: VTOYDATA exfat not marked dirty" || fail "ui1: VTOYDATA exfat dirty flag: $(fsv exfat_dirty)"

# ---------- 2. second boot: idempotent ----------
guest ui2 omlui run1
grep -q OMLOADER_GUEST_OK $W/ui2.clean && pass "ui2: guest booted" || fail "ui2: no OMLOADER_GUEST_OK"
grep -q 'firstboot adapter applied' $W/ui2.clean && fail "ui2: adapter re-applied on unchanged config" || pass "ui2: adapter not re-applied (config hash in marker)"
eq "$(val ui2 OMLOADER_UI_USERS)" 3 "ui2: users not duplicated"
eq "$(val ui2 OMLOADER_UI_SHADOW_ENTRIES)" 4 "ui2: shadow entries not duplicated"
grep -q 'OMLOADER_LOGIN_OK user=alice tty=/dev/ttyS0' $W/ui2.clean && pass "ui2: autologin still works" || fail "ui2: autologin lost"
eq "$(val ui2 OMLOADER_UI_PW_ALICE)" ok "ui2: alice password unchanged-valid"
eq "$(fsv needs_recovery)" no "ui2: OMLOADER ext4 clean after second poweroff"

# ---------- 3. busybox-only image (no getty/login): debug shell + sync via inittab ----------
guest bb1 omlbb new:r1
grep -q OMLOADER_BB_GUEST_OK $W/bb1.clean && pass "bb1: busybox-init guest booted" || fail "bb1: no OMLOADER_BB_GUEST_OK"
grep -q 'no usable getty/login' $W/bb1.clean && pass "bb1: loader detected missing getty/login" || fail "bb1: no detection warning"
grep -q 'OMLOADER_DEBUG_SHELL' $W/bb1.clean && pass "bb1: debug shell spawned on the console from the omloader busybox" || fail "bb1: no debug shell banner"
grep -q 'omloader: snap flushed and remounted read-only' $W/bb1.clean && pass "bb1: inittab ::shutdown hook ran" || fail "bb1: no shutdown hook message"
echo "fs after bb1: $(fsstate)"
eq "$(fsv needs_recovery)" no "bb1: OMLOADER ext4 clean after busybox-init poweroff"

# ---------- 4. baseline: shutdown_sync=false reproduces the old behaviour ----------
IND=$(awk '/^runtime:/{f=1;next} f{match($0,/^ */);print RLENGTH;exit}' $O/oci-images/omlui/image.yaml); PAD=$(printf '%*s' ${IND:-2} '')
rm -rf $W/hk; mkdir -p $W/hk
echo "sed -i 's/^runtime:/runtime:\\n${PAD}shutdown_sync: false/' /mnt/images/images/omlui/image.yaml; grep -A2 '^runtime:' /mnt/images/images/omlui/image.yaml; sync" > $W/hk/hook.sh
rm -f $W/hk.img; truncate -s 16M $W/hk.img; mkfs.ext4 -q -L OMLSTAGE -d $W/hk $W/hk.img
DISK2=$W/hk.img EXTRA=omloader.hook=1 NAME= SNAP= LOG=$W/hook.log $T/run-kvm.sh > /dev/null; clean hook
grep -q 'OMLOADER_HOOK_RC=0' $W/hook.clean && pass "hook: image.yaml patched (shutdown_sync: false)" || fail "hook: patch failed"
eq "$(fsv needs_recovery)" no "hook: loader-side hook poweroff path (sync + remount ro) leaves OMLOADER clean"
guest nosync omlui new:nosync
grep -q OMLOADER_GUEST_OK $W/nosync.clean && pass "nosync: guest booted" || fail "nosync: no OMLOADER_GUEST_OK"
grep -q 'flushed and remounted' $W/nosync.clean && fail "nosync: hook still active" || pass "nosync: hooks removed on config change (re-applied through the marker hash)"
echo "fs after nosync (old behaviour): $(fsstate)"
[ "$(fsv needs_recovery)" = yes ] && pass "nosync: without the hook the OMLOADER ext4 is left needing journal recovery (the pre-change behaviour)" || fail "nosync: expected needs_recovery=yes, got $(fsv needs_recovery)"

[ $FAIL = 0 ] && echo "E2E-UI: ALL PASS" || echo "E2E-UI: FAILED"
exit $FAIL
