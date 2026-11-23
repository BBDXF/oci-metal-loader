#!/bin/bash
# Run ON THE REMOTE. Real public images: `omloader pull` over the network INSIDE the loader (QEMU user-net NAT),
# then each image is booted under KVM/OVMF and must reach userspace/login on the serial console; clean ACPI shutdown
# via the QEMU monitor; snapshot create / boot / revert on one container and one VM.
# Env: REAL_IMAGES="ct:ubuntu/noble ct:debian/bookworm ct:alpine/3.21 vm:ubuntu/noble vm:debian/bookworm"  (kind:os/release[/variant])
#      REAL_SNAP="ct:ubuntu/noble vm:debian/bookworm" (which of them get the snapshot round trip)
#      REAL_SOURCE_URL (simplestreams base URL, default https://images.linuxcontainers.org; a mirror works too)
#      REAL_KEEP=1 keep out/real-usb.raw (store with the pulled images; used by test/manual-run.sh)
#      REAL_REUSE=1 reuse out/real-usb.raw (already pulled), skip disk creation and pulls
#      REAL_VM_CMDLINE (default 'systemd.mask=boot-efi.mount') appended to kernel.cmdline of the pulled VM images: WORKAROUND for
#        the missing /dev/disk/by-label/UEFI in the guest (dm partitions get no udev symlinks; see docs/DEVELOPMENT.md). Set to '-' to test without it.
#      REAL_OCI=host/path:tag (EXPERIMENTAL, optional) e.g. docker.1ms.run/jrei/systemd-debian:12 - fetched with skopeo on this host (omloader has no
#        registry pull), imported inside the loader and booted with experimental.oci. Skipped when empty or skopeo/registry fail.
#      SKIP_BUILD=1 reuse out/omloader.iso   BOOT_TIMEOUT=sec (default 420)  PULL_TIMEOUT=sec (default 2400)
# No network => the suite is SKIPPED (exit 0) with a clear message.
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
REAL_IMAGES=${REAL_IMAGES:-"ct:ubuntu/noble ct:debian/bookworm ct:alpine/3.21 vm:ubuntu/noble vm:debian/bookworm"}
REAL_SNAP=${REAL_SNAP:-"ct:ubuntu/noble vm:debian/bookworm"}
URL=${REAL_SOURCE_URL:-https://images.linuxcontainers.org}
BOOT_TIMEOUT=${BOOT_TIMEOUT:-420}; PULL_TIMEOUT=${PULL_TIMEOUT:-2400}
FAIL=0; SUMMARY=(); PULLED=()
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
mkdir -p $W
curl -sf -m 20 -o /dev/null $URL/streams/v1/index.json || { echo "SKIP: e2e-real: $URL not reachable from this host (set REAL_SOURCE_URL to a mirror); nothing tested"; exit 0; }
trap 'pkill -u $USER -f "$W/run\." 2>/dev/null' EXIT
[ -n "${SKIP_BUILD:-}" ] || $ROOT/build/build-iso.sh >/dev/null || { echo "ISO build failed"; exit 1; }
[ -n "${REAL_REUSE:-}" ] && [ -f $O/real-usb.raw ] || { REUSE=
# ---- empty GPT disk: p1 exfat VTOYDATA ext4 10 GiB (images; exFAT would store the sparse base.raw fully allocated), p2 ext4 OMLOADER 12 GiB (snapshots, cache); both sparse ----
D=$O/real-usb.raw; P1=$W/rl1.img; P2=$W/rl2.img
rm -f $D $P1 $P2; truncate -s 10G $P1; truncate -s 12G $P2
mkfs.ext4 -q -L VTOYDATA $P1; mkfs.ext4 -q -L OMLOADER $P2
truncate -s $((10*1024+12*1024+8))M $D
printf 'label: gpt\nstart=2048, size=10GiB, type=EBD0A0A2-B9E5-4433-87C0-68B6B72699C7, name=VTOYDATA\ntype=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=OMLOADER\n' | sfdisk -q $D
dd if=$P1 of=$D bs=1M seek=1 conv=notrunc,sparse status=none
dd if=$P2 of=$D bs=512 seek=$((2048+10*1024*2048)) conv=notrunc,sparse status=none
rm -f $P1 $P2
}
D=$O/real-usb.raw
export DISK=$D
clean() { tr -d '\r' < $W/$1.log | sed 's/\x1b\[[0-9;]*[mK]//g' > $W/$1.clean; }
sect() { sed -n "/TAG:$2:BEGIN/,/TAG:$2:END/p" $W/$1.clean; }
rc() { sect $1 $2 | grep -o 'RC=[0-9]*' | tail -1 | cut -d= -f2; }
nm() { echo "${1}" | tr '/.:' '---'; }   # kind:os/rel[/variant] -> kind-os-rel[-variant]
pullspec() { local k=${1%%:*} s=${1#*:}; echo "lxc:$s $([ $k = vm ] && echo --vm)"; }
hook() { # log ; body on stdin
  rm -rf $W/hk; mkdir -p $W/hk; [ -n "${HK_EXTRA:-}" ] && cp $HK_EXTRA/* $W/hk/
  printf 'network:\n  retries: 3\n  timeout_sec: 60\nsources:\n  lxc-images:\n    type: simplestreams\n    url: %s\ncache:\n  path: /mnt/snap/cache\n' "$URL" > $W/hk/omloader.yaml
  { cat <<'HEAD'
export DM_DISABLE_UDEV=1
C="-c /mnt/stage/omloader.yaml"
S="--root /mnt/snap/snapshots --images-root /mnt/images/images"
t() { n=$1; shift; echo "TAG:$n:BEGIN"; s=$(date +%s); "$@" 2>&1; echo "TAG:$n:RC=$?"; echo "TAG:$n:SECS=$(( $(date +%s)-s ))"; echo "TAG:$n:END"; }
for i in $(seq 1 60); do ip route | grep -q '^default' && break; sleep 1; done
HEAD
    cat; } > $W/hk/hook.sh
  rm -f $W/hk.img; truncate -s ${HOOKSZ:-32M} $W/hk.img; mkfs.ext4 -q -L OMLSTAGE -d $W/hk $W/hk.img
  TIMEOUT=${HOOK_TIMEOUT:-600} DISK2=$W/hk.img EXTRA=omloader.hook=1 NAME= SNAP= LOG=$W/$1.log $T/run-kvm.sh > /dev/null; clean $1; }
# boot an image, wait for a serial marker, power it off via the monitor. Sets BOOT_OK, EVID, BOOT_SECS
bootimg() { # log image snapshot kind
  local L=$1 MON=$W/mon.$$ t0=$(date +%s) pid i
  rm -f $MON; : > $W/$L.log
  TIMEOUT=$BOOT_TIMEOUT MONITOR=$MON SERIALSOCK=$W/ser.$$ NAME=$2 SNAP=$3 LOG=$W/$L.log $T/run-kvm.sh > /dev/null 2>&1 & pid=$!
  BOOT_OK=0; EVID=
  while kill -0 $pid 2>/dev/null; do
    sleep 3; clean $L
    if grep -qE '^[^ ]+ login: ?$|login: *$|Reached target.*(Multi-User|Graphical)|Startup finished in|OpenRC.*default|Welcome to Alpine|[a-z]+@[^ ]+:.*[#$] ' $W/$L.clean; then BOOT_OK=1; break; fi
    grep -qE 'omloader: error|kexec.*fail|Kernel panic|^error:|You are in emergency mode|Cannot open access to console' $W/$L.clean && break
  done
  BOOT_SECS=$(( $(date +%s)-t0 ))
  EVID=$(grep -E 'login: *$|Reached target.*(Multi-User|Graphical)|Startup finished in|Welcome to Alpine|[a-z]+@[^ ]+:.*[#$] |Kernel panic|error:|emergency mode' $W/$L.clean | head -2 | tr '\n' '|' | cut -c1-150)
  PROOF_RES=
  if [ $BOOT_OK = 1 ] && [ -n "${PROOF:-}" ]; then loginproof $L $W/ser.$$; fi
  if [ $BOOT_OK = 1 ]; then
    sleep 8; python3 - $MON <<'PY'
import socket,sys,time
s=socket.socket(socket.AF_UNIX); s.settimeout(5)
try:
    s.connect(sys.argv[1]); time.sleep(.5); s.send(b"system_powerdown\n"); time.sleep(1)
except Exception as e: print("monitor:", e)
PY
    for i in $(seq 1 60); do kill -0 $pid 2>/dev/null || break; sleep 1; done
  fi
  if kill -0 $pid 2>/dev/null; then POWER=forced; pkill -u $USER -f "$W/run\.$pid/" ; else POWER=clean; fi
  pkill -u $USER -f "$W/run\.$pid/" 2>/dev/null; wait $pid 2>/dev/null; clean $L; rm -f $MON; }
# default-account proof over the serial console (autologin shell as oci; sudo with the password; su to root with the password)
loginproof() { # log sock
  sleep 6; python3 - $2 <<'PY'
import socket,sys,time
s=socket.socket(socket.AF_UNIX); s.connect(sys.argv[1]); s.settimeout(1)
def snd(t,w=2.5):
    s.send(t.encode()); time.sleep(w)
    try: s.recv(65536)
    except Exception: pass
snd("\r"); snd("\r")
snd("echo OMLPROOF_ID=$(id -un)\r")
snd("echo Oci123 | sudo -S -p '' id -un 2>&1 | sed 's/^/OMLPROOF_SUDO=/'\r", 4)
snd("su - root -c 'echo OMLPROOF_SU=$(id -un)'\r", 2.5)
snd("Oci123\r", 4)
snd("grep -c '^root:\\$' /etc/shadow | sed 's/^/OMLPROOF_ROOTHASH=/'\r")
PY
  clean $1
  PROOF_RES=$(grep -ao 'OMLPROOF_[A-Z]*=[a-z0-9]*' $W/$1.clean | tr '\n' ' ')
}
report() { SUMMARY+=("$(printf '%-34s pull=%-6s size=%-10s boot=%-4s (%ss) power=%-6s %s' "$1" "$2" "$3" "$4" "$5" "$6" "$7")"); }

VMCL=${REAL_VM_CMDLINE-systemd.mask=boot-efi.mount}; [ "$VMCL" = - ] && VMCL=
if [ -n "${REAL_REUSE:-}" ] && [ -z "${REUSE+x}" ]; then
  echo "== reusing the store $D (no pull)"
  { for i in $REAL_IMAGES; do n=$(nm $i); echo "t size-$n du -sh --apparent-size /mnt/images/images/$n"; done; } > $W/pull.body; for i in $REAL_IMAGES; do n=$(nm $i); echo "TAG:pull-$n:BEGIN"; echo "TAG:pull-$n:RC=0"; echo "TAG:pull-$n:SECS=0"; done > /dev/null
  HOOK_TIMEOUT=300 hook rp < $W/pull.body
  for i in $REAL_IMAGES; do n=$(nm $i); printf 'TAG:pull-%s:BEGIN\nTAG:pull-%s:RC=0\nTAG:pull-%s:SECS=0\nTAG:pull-%s:END\n' $n $n $n $n >> $W/rp.clean; done
else
echo "== pulling from $URL: $REAL_IMAGES"
{ for i in $REAL_IMAGES; do n=$(nm $i); echo "t pull-$n omloader pull $(pullspec $i) --name $n \$C"; echo "t size-$n du -sh --apparent-size /mnt/images/images/$n; t ls-$n ls -la /mnt/images/images/$n; t yaml-$n cat /mnt/images/images/$n/image.yaml"; done; echo "t df df -h /mnt/snap /mnt/images"; } > $W/pull.body
HOOK_TIMEOUT=$PULL_TIMEOUT hook rp < $W/pull.body
fi
grep -E 'TAG:pull-.*:(RC|SECS)|warning|error' $W/rp.clean | cut -c1-200 | head -40
for i in $REAL_IMAGES; do
  n=$(nm $i); r=$(rc rp pull-$n); secs=$(sect rp pull-$n | grep -o 'SECS=[0-9]*' | cut -d= -f2)
  sz=$(sect rp size-$n | head -2 | tail -1 | cut -f1)
  if [ "$r" = 0 ]; then pass "pull $i (${secs}s, $sz)"; PULLED[${#PULLED[@]}]=$i
  else fail "pull $i rc=$r: $(sect rp pull-$n | tail -4 | tr '\n' '|' | cut -c1-300)"; report "$i" "FAIL" - - - - -; fi
done
if [ -n "$VMCL" ]; then   # WORKAROUND for the by-label UEFI problem (VM images only)
  { for i in $REAL_IMAGES; do case $i in vm:*) n=$(nm $i); echo "grep -q -- '$VMCL' /mnt/images/images/$n/image.yaml || sed -i 's|^\\( *cmdline: .*\\)\$|\\1 $VMCL|' /mnt/images/images/$n/image.yaml; grep cmdline /mnt/images/images/$n/image.yaml";; esac; done; } > $W/patch.body
  hook rpatch < $W/patch.body; grep -a 'cmdline' $W/rpatch.clean | head -4
fi
VMS=$(for i in $REAL_IMAGES; do case $i in vm:*) nm $i;; esac; done)
if [ -n "$VMS" ] && [ -z "${REAL_NOBASECHECK:-}" ]; then   # the default-account provisioning must only write to the COW layer, never to base.raw
  { for n in $VMS; do echo "t sha-$n sha256sum /mnt/images/images/$n/base.raw"; done; } > $W/sha.body
  HOOK_TIMEOUT=900 hook rsha1 < $W/sha.body
fi
for i in "${PULLED[@]}"; do
  n=$(nm $i); secs=$(sect rp pull-$n | grep -o 'SECS=[0-9]*' | cut -d= -f2); sz=$(sect rp size-$n | head -2 | tail -1 | cut -f1)
  PROOF=1 bootimg b-$n $n ""
  if [ $BOOT_OK = 1 ]; then pass "boot $i reached userspace in ${BOOT_SECS}s (power-off: $POWER)"; else fail "boot $i (see $W/b-$n.clean): $EVID"; fi
  echo "PROOF $i: $PROOF_RES"
  case "$PROOF_RES" in *OMLPROOF_ID=oci*) pass "$i: autologin shell as oci";; *) fail "$i: no autologin shell as oci";; esac
  case "$PROOF_RES" in *OMLPROOF_SUDO=root*) pass "$i: sudo with the password works";; *) echo "NOTE: $i: sudo did not work (sudo not installed?)";; esac
  case "$PROOF_RES" in *OMLPROOF_SU=root*) pass "$i: su - root with Oci123 works";; *) fail "$i: su - root with the default password failed";; esac
  report "$i" "${secs}s" "$sz" "$([ $BOOT_OK = 1 ] && echo ok || echo FAIL)" "$BOOT_SECS" "$POWER" "$EVID"
done
for i in $REAL_SNAP; do
  case " ${PULLED[*]} " in *" $i "*) ;; *) continue;; esac
  n=$(nm $i)
  hook rs1 <<BODY
t create omloader snapshot create $n S1 --note real-e2e \$S
t list omloader snapshot list $n --tree \$S
BODY
  [ "$(rc rs1 create)" = 0 ] && pass "snapshot create $n S1" || { fail "snapshot create $n: $(sect rs1 create | tr '\n' '|' | cut -c1-300)"; continue; }
  bootimg s-$n $n S1; [ $BOOT_OK = 1 ] && pass "boot $n on snapshot S1 (${BOOT_SECS}s)" || fail "boot $n S1: $EVID"
  hook rs2 <<BODY
t revert omloader snapshot revert $n S1 \$S
t verify omloader snapshot verify $n \$S
t list omloader snapshot list $n --tree \$S
BODY
  [ "$(rc rs2 revert)" = 0 ] && pass "snapshot revert $n S1" || fail "snapshot revert $n: $(sect rs2 revert | tr '\n' '|' | cut -c1-300)"
  bootimg r-$n $n ""; [ $BOOT_OK = 1 ] && pass "boot $n after revert (${BOOT_SECS}s)" || fail "boot $n after revert: $EVID"
done
if [ -n "${REAL_OCI:-}" ]; then
  echo "== experimental OCI: $REAL_OCI"
  rm -f $W/hk-oci.tar
  if command -v skopeo >/dev/null && skopeo copy --override-os linux --override-arch amd64 docker://$REAL_OCI oci-archive:$W/hk-oci.tar:img >/dev/null 2>$W/skopeo.err; then
    rm -rf $W/hkx; mkdir -p $W/hkx; mv $W/hk-oci.tar $W/hkx/oci.tar
    HK_EXTRA=$W/hkx HOOKSZ=$(( $(stat -c %s $W/hkx/oci.tar)/1048576 + 64 ))M hook ro <<'BODY'
t imp omloader import oci-archive --experimental-oci /mnt/stage/oci.tar --name oci-real --out /mnt/images/images --autologin --tmp /mnt/snap
t yaml cat /mnt/images/images/oci-real/image.yaml
BODY
    if [ "$(rc ro imp)" = 0 ]; then
      EXPERIMENTAL_OCI=1 bootimg b-oci oci-real ""
      [ $BOOT_OK = 1 ] && pass "EXPERIMENTAL oci $REAL_OCI boots (${BOOT_SECS}s, power-off: $POWER)" || fail "EXPERIMENTAL oci $REAL_OCI: $EVID"
      report "oci:$REAL_OCI" "-" "-" "$([ $BOOT_OK = 1 ] && echo ok || echo FAIL)" "$BOOT_SECS" "$POWER" "$EVID"
    else fail "oci import: $(sect ro imp | tail -3 | tr '\n' '|')"; fi
  else echo "SKIP: OCI: skopeo/registry unavailable ($(tail -1 $W/skopeo.err 2>/dev/null))"; fi
fi
if [ -n "$VMS" ] && [ -z "${REAL_NOBASECHECK:-}" ]; then
  HOOK_TIMEOUT=900 hook rsha2 < $W/sha.body
  for n in $VMS; do
    a=$(sect rsha1 sha-$n | grep -o '^[0-9a-f]\{64\}'); b=$(sect rsha2 sha-$n | grep -o '^[0-9a-f]\{64\}')
    [ -n "$a" ] && [ "$a" = "$b" ] && pass "$n: base.raw unchanged after provisioning/boots/snapshots (sha256 ${a:0:12}...)" || fail "$n: base.raw sha256 before='$a' after='$b'"
  done
fi
echo; echo "== summary"; printf '%s\n' "${SUMMARY[@]}"
[ -n "${REAL_KEEP:-}" ] || rm -f $D
[ $FAIL = 0 ] && echo "E2E-REAL: ALL PASS" || echo "E2E-REAL: FAILED"
exit $FAIL
