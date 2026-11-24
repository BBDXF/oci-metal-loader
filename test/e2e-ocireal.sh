#!/bin/bash
# Run ON THE REMOTE. REAL Docker/OCI images (experimental path): import oci-archives with the loader, boot them, probe the guest over the serial console.
# Input: work/ocireal/*.tar (oci-archive files fetched with podman/skopeo on a machine with registry access; see docs/sources.md "Docker Hub").
# Output: a report per image: import rc, boot reached (login/shell), probe lines (init, os-release, network, account), power-off, then one snapshot create/revert.
# Env: IMAGES="alpine_3.20 busybox ..." (file basenames)  WAIT=120 (seconds to wait for the guest console)  SKIP_BUILD=1 (reuse out/omloader.iso)
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test; R=$W/ocireal
WAIT=${WAIT:-120}; export EXPERIMENTAL_OCI=1
trap 'pkill -u $USER -f "$W/ocr-.*\.raw" 2>/dev/null' EXIT
set +e; mkdir -p $R
[ -f $O/ab-base.raw ] || { echo "need out/ab-base.raw (run test/e2e-autoboot.sh once)"; exit 1; }
clean() { tr -d '\r' < $1 | sed 's/\x1b\[[0-9;=?]*[a-zA-Z]//g'; }
hook() { # $1 tag $2 disk ; stdin = hook script ; stage dir $R/stage-$1 holds extra files
  mkdir -p $R/stage-$1; cat > $R/stage-$1/hook.sh
  rm -f $R/stage-$1.img; truncate -s $(( $(du -sm $R/stage-$1 | cut -f1) + 64 ))M $R/stage-$1.img
  mkfs.ext4 -q -L OMLSTAGE -d $R/stage-$1 $R/stage-$1.img
  DISK=$2 DISK2=$R/stage-$1.img BUS=nvme EXTRA=omloader.hook=1 LOG=$R/hook-$1.log TIMEOUT=300 $T/run-kvm.sh >/dev/null 2>&1
  clean $R/hook-$1.log > $R/hook-$1.clean
}
for n in ${IMAGES:-$(cd $R && ls *.tar | sed 's/\.tar$//')}; do
  echo "================ $n ($(stat -c %s $R/$n.tar) bytes tar)"
  D=$W/ocr-$n.raw; cp --sparse=always $O/ab-base.raw $D
  rm -rf $R/stage-imp; mkdir -p $R/stage-imp; ln $R/$n.tar $R/stage-imp/oci.tar 2>/dev/null || cp $R/$n.tar $R/stage-imp/oci.tar
  hook imp $D <<'BODY'
echo "##IMPORT"; omloader import oci-archive --experimental-oci /mnt/stage/oci.tar --name real --out /mnt/images/images --autologin --tmp /mnt/snap 2>&1; echo "##IMPORT_RC=$?"
echo "##YAML"; cat /mnt/images/images/real/image.yaml; ls -la /mnt/images/images/real | head -5
BODY
  sed -n '/##IMPORT/,/HOOK_RC/p' $R/hook-imp.clean | grep -av '^\[' | head -40
  grep -aq '##IMPORT_RC=0' $R/hook-imp.clean || { echo "RESULT $n: IMPORT FAILED"; rm -f $D; continue; }
  # boot with the serial console as a socket
  rm -f $R/boot-$n.log; ( NAME=real DISK=$D LOG=$R/boot-$n.log SERIALSOCK=$R/boot-$n.sock TIMEOUT=$((WAIT+200)) $T/run-kvm.sh >/dev/null 2>&1 & )
  up=0; for i in $(seq 1 $WAIT); do sleep 1; clean $R/boot-$n.log > $R/boot-$n.clean; if grep -aqE 'login:|[#$] *$|udhcpc: lease|OMLOADER_LOADER_FAILED|returned rc=' $R/boot-$n.clean; then up=1; break; fi; done
  echo "--- boot: reached console=$up after ${i}s"; grep -aE "OMLOADER_LOADER|switch-root|warning|error|login:" $R/boot-$n.clean | tail -8 | cut -c1-200
  if [ $up = 1 ]; then
    sleep 3; $T/ventoy-ser.py $R/boot-$n.sock '\n'; sleep 2
    $T/ventoy-ser.py $R/boot-$n.sock 'echo PROBE_BEGIN; id; cat /proc/1/comm; tr "\0" " " < /proc/1/cmdline; echo; head -2 /etc/os-release; uname -r; (ip -4 -o addr show 2>/dev/null || ifconfig 2>/dev/null) | head -3; hostname; (systemctl is-system-running 2>&1 | head -1); echo PROBE_END\n'
    sleep 6; clean $R/boot-$n.log > $R/boot-$n.clean
    sed -n '/PROBE_BEGIN/,/PROBE_END/p' $R/boot-$n.clean | tail -14 | cut -c1-200
    $T/ventoy-ser.py $R/boot-$n.sock 'poweroff\n'; sleep 12
  fi
  pgrep -u $USER -f "$R/boot-$n.log" >/dev/null && echo "--- power-off: VM still running after poweroff (killed)" || echo "--- power-off: VM exited"
  pkill -u $USER -f "$W/ocr-$n.raw" 2>/dev/null; sleep 1
  echo "--- tail of the serial log:"; clean $R/boot-$n.log | tail -6 | cut -c1-200
  # snapshot create / revert via the loader hook
  hook snap $D <<'BODY'
echo "##SNAP"; omloader snapshot create real s1 --root /mnt/snap/snapshots --images-root /mnt/images/images 2>&1 | tail -2; echo "##RC1=$?"
omloader snapshot revert real s1 --root /mnt/snap/snapshots --images-root /mnt/images/images 2>&1 | tail -2; echo "##RC2=$?"
omloader snapshot list real --tree --root /mnt/snap/snapshots --images-root /mnt/images/images 2>&1 | head -6
BODY
  sed -n '/##SNAP/,/HOOK_RC/p' $R/hook-snap.clean | grep -av '^\[' | head -12
  rm -f $D
done
