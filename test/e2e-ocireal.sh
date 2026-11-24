#!/bin/bash
# Run ON THE REMOTE. REAL Docker/OCI images (experimental path): get them (import a local oci-archive, or `omloader pull docker:REF` through the Hub
# mirrors with the loader's bundled crane), boot them under the loader, probe the guest over the serial console, then snapshot create/revert once.
# ENTRIES (default below): "label|tar:BASENAME|extra-kernel-args" (work/ocireal/BASENAME.tar) or "label|docker:REF|extra-kernel-args" (pull inside the loader VM).
#   extra-kernel-args e.g. omloader.shell=busybox (force the busybox console shell for that boot).
# Env: ENTRIES="..." (newline separated)  WAIT=120  PULL_MIRROR=auto|official|ID (default auto)  PULL_TIMEOUT=600
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test; R=$W/ocireal
WAIT=${WAIT:-120}; export EXPERIMENTAL_OCI=1
DEFAULT_ENTRIES='busybox|docker:busybox|
nginx|docker:nginx:alpine|
alpine-bb|docker:alpine:3.20|omloader.shell=busybox
jrei|docker:jrei/systemd-debian:12|'
trap 'pkill -u $USER -f "$W/ocr-.*\.raw" 2>/dev/null' EXIT
set +e; mkdir -p $R
[ -f $O/ab-base.raw ] || { echo "need out/ab-base.raw (run test/e2e-autoboot.sh once)"; exit 1; }
clean() { tr -d '\r' < $1 | sed 's/\x1b\[[0-9;=?]*[a-zA-Z]//g'; }
hook() { # $1 tag $2 disk ; stdin = hook script ; stage dir $R/stage-$1 holds extra files
  mkdir -p $R/stage-$1; cat > $R/stage-$1/hook.sh
  rm -f $R/stage-$1.img; truncate -s $(( $(du -sm $R/stage-$1 | cut -f1) + 64 ))M $R/stage-$1.img
  mkfs.ext4 -q -L OMLSTAGE -d $R/stage-$1 $R/stage-$1.img
  DISK=$2 DISK2=$R/stage-$1.img BUS=nvme EXTRA=omloader.hook=1 LOG=$R/hook-$1.log TIMEOUT=${HOOK_TIMEOUT:-300} $T/run-kvm.sh >/dev/null 2>&1
  clean $R/hook-$1.log > $R/hook-$1.clean
}
printf '%s\n' "${ENTRIES:-$DEFAULT_ENTRIES}" | while IFS='|' read -r n src extra; do
  [ -n "$n" ] || continue
  echo "================ $n  ($src) ${extra:+[kernel: $extra]}"
  D=$W/ocr-$n.raw; cp --sparse=always $O/ab-base.raw $D
  rm -rf $R/stage-imp; mkdir -p $R/stage-imp
  case $src in
    tar:*) cp $R/${src#tar:}.tar $R/stage-imp/oci.tar
      IMP='omloader import oci-archive --experimental-oci /mnt/stage/oci.tar --name real --out /mnt/images/images --autologin --tmp /mnt/snap';;
    docker:*) IMP="omloader pull ${src} --mirror ${PULL_MIRROR:-auto} --experimental-oci --name real --out /mnt/images/images --tmp /mnt/snap";;
  esac
  T0=$(date +%s)
  HOOK_TIMEOUT=${PULL_TIMEOUT:-600} hook imp $D <<BODY
for i in \$(seq 1 40); do ip route 2>/dev/null | grep -q default && break; sleep 1; done; echo "##NET \$i s: \$(ip -4 -o addr show | tail -1)"
echo "##IMPORT"; $IMP > /tmp/imp.out 2>&1; rc=\$?; tail -25 /tmp/imp.out; echo "##IMPORT_RC=\$rc"
echo "##YAML"; cat /mnt/images/images/real/image.yaml; ls -la /mnt/images/images/real | head -5
BODY
  echo "--- import/pull: $(( $(date +%s)-T0 )) s"
  sed -n '/##IMPORT/,/HOOK_RC/p' $R/hook-imp.clean | grep -av '^\[' | grep -a "pull:\|imported\|error\|IMPORT_RC\|denied\|trying\|shell:\|autologin" | head -14 | cut -c1-200
  grep -aq '##IMPORT_RC=0' $R/hook-imp.clean || { echo "RESULT $n: IMPORT/PULL FAILED"; rm -f $D; continue; }
  # boot with the serial console as a socket
  rm -f $R/boot-$n.log; ( NAME=real EXTRA="$extra" DISK=$D LOG=$R/boot-$n.log SERIALSOCK=$R/boot-$n.sock TIMEOUT=$((WAIT+200)) $T/run-kvm.sh >/dev/null 2>&1 & )
  up=0; for i in $(seq 1 $WAIT); do sleep 1; clean $R/boot-$n.log > $R/boot-$n.clean; if grep -aqE 'login:|[#$] *$|udhcpc: lease|OMLOADER_LOADER_FAILED|returned rc=' $R/boot-$n.clean; then up=1; break; fi; done
  echo "--- boot: console=$up after ${i}s"; grep -aE "OMLOADER_LOADER: (auto|switch)|warning|OMLOADER_DEBUG_SHELL|login:|Started getty|Reached target getty|Startup finished" $R/boot-$n.clean | tail -8 | cut -c1-200
  if [ $up = 1 ]; then
    sleep 4; $T/ventoy-ser.py $R/boot-$n.sock '\n'; sleep 2
    $T/ventoy-ser.py $R/boot-$n.sock 'echo PROBE_BEGIN; id; cat /proc/1/comm; ps | head -6; ps | grep -c [n]ginx; ls /proc | head -3; (ip -4 -o addr show 2>/dev/null || ifconfig 2>/dev/null) | head -2; echo PROBE_END\n'
    sleep 8; clean $R/boot-$n.log > $R/boot-$n.clean
    sed -n '/PROBE_BEGIN$/,/PROBE_END$/p' $R/boot-$n.clean | tail -22 | cut -c1-160
    $T/ventoy-ser.py $R/boot-$n.sock 'poweroff\n'; sleep 12
  fi
  pgrep -u $USER -f "$R/boot-$n.log" >/dev/null && echo "--- power-off: VM still running (killed)" || echo "--- power-off: VM exited"
  pkill -u $USER -f "$W/ocr-$n.raw" 2>/dev/null; sleep 1
  echo "--- tail of the serial log:"; clean $R/boot-$n.log | tail -4 | cut -c1-200
  hook snap $D <<'BODY'
echo "##SNAP"; omloader snapshot create real s1 --root /mnt/snap/snapshots --images-root /mnt/images/images 2>&1 | tail -1; echo "##RC1=$?"
omloader snapshot revert real s1 --root /mnt/snap/snapshots --images-root /mnt/images/images 2>&1 | tail -1; echo "##RC2=$?"
BODY
  sed -n '/##SNAP/,/HOOK_RC/p' $R/hook-snap.clean | grep -av '^\[' | head -6
  rm -f $D
done
