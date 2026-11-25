#!/bin/bash
# Run ON THE REMOTE. NTFS data partition holding omloader.iso, label VHDX (not VTOYDATA): a Ventoy-like layout without Ventoy.
#   disk: GPT, ONE NTFS partition (label VHDX) with /omloader/omloader.iso and /omloader/images/omloci (OCI fixture), no CD-ROM:
#   the loader's kernel+initrd are booted directly (-kernel/-initrd) so stage 1 can only find the ISO by iso-scan.
#   A  stage 1 finds the ISO on NTFS (ntfs-3g in the initramfs), the data partition is picked by the fallback (no VTOYDATA label),
#      images from omloader/images are visible, the host is rw, the volume is clean again after the poweroff
#   B  same disk again: read-write mount works (= it was cleaned by the previous poweroff)
#   C  machine killed (no clean shutdown);  D: next boot still finds the ISO and the images (ntfs-3g recovers an unclean volume; the
#      read-only fallback for a hibernated volume is not provoked here)
# Env: SKIP_BUILD=1 reuse out/omloader.iso and out/oci-images
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
trap 'pkill -u $USER -f "ntfs-run-" 2>/dev/null' EXIT
set -e; mkdir -p $W
if [ -z "${SKIP_BUILD:-}" ]; then $ROOT/build/build-iso.sh; $T/make-fixture-oci.sh; fi
ISO=$O/omloader.iso
# ---- populate: the loader itself formats the partition (mkntfs) and copies the files through ntfs-3g ----
rm -rf $W/ntfs-stage; mkdir -p $W/ntfs-stage/omloader/images
cp $ISO $W/ntfs-stage/omloader/omloader.iso
cp -r $O/oci-images/omloci $W/ntfs-stage/omloader/images/
cat > $W/ntfs-stage/hook.sh <<'HOOK'
set -e
modprobe fuse
mkntfs -F -Q -L VHDX /dev/vda >/dev/null 2>&1
mount -t ntfs-3g /dev/vda /mnt/images
cp -r /mnt/stage/omloader /mnt/images/
sync; umount /mnt/images
echo NTFS_POPULATED
HOOK
ISO_MB=$(( ($(stat -c %s $ISO) + 1048575) / 1048576 )); IMG_MB=$(du -sm --apparent-size $O/oci-images | cut -f1)
P1_MB=$((ISO_MB + IMG_MB + 3200)); P1=$W/ntfs-p1.img; ST=$W/ntfs-stage.img; rm -f $P1 $ST  # ~3 GiB free: room for the 1G store + 1G margin
truncate -s ${P1_MB}M $P1; truncate -s $(( (ISO_MB + IMG_MB) * 13 / 10 + 100 ))M $ST; mkfs.ext4 -q -L OMLSTAGE -d $W/ntfs-stage $ST
DISK=$P1 DISK2=$ST BUS=virtio EXTRA=omloader.hook=1 LOG=$W/ntfs-populate.log TIMEOUT=400 $T/run-kvm.sh >/dev/null || true
grep -q NTFS_POPULATED $W/ntfs-populate.log || { echo "populate failed, see $W/ntfs-populate.log"; exit 1; }
D=$W/ntfs-disk.raw; rm -f $D; truncate -s $((P1_MB + 4))M $D
printf 'label: gpt\nstart=2048, size=%sMiB, type=EBD0A0A2-B9E5-4433-87C0-68B6B72699C7, name=VHDX\n' $P1_MB | sfdisk -q $D
dd if=$P1 of=$D bs=1M seek=1 conv=notrunc,sparse status=none
rm -f $P1 $ST; rm -rf $W/ntfs-stage
# ---- kernel + initrd straight from the ISO (no CD-ROM in the VM) ----
mkdir -p $W/ntfs-k; rm -f $W/ntfs-k/*
xorriso -osirrox on -indev $ISO -extract /boot/vmlinuz $W/ntfs-k/vmlinuz -extract /boot/initrd.img $W/ntfs-k/initrd.img >/dev/null 2>&1
# ---- hook disk: reports the loader state ----
rm -rf $W/ntfs-hk; mkdir -p $W/ntfs-hk
cat > $W/ntfs-hk/hook.sh <<'HOOK'
echo "TAG:medium:BEGIN"; cat /run/omloader/medium.env; echo "TAG:medium:END"
. /run/omloader/env
echo "TAG:env:BEGIN"; cat /run/omloader/env; echo "TAG:env:END"
echo "TAG:ls:BEGIN"; ls $IMAGES_ROOT; echo "TAG:ls:END"
echo "TAG:mnt:BEGIN"; grep -E " /mnt/images | /run/medium-host " /proc/mounts; echo "TAG:mnt:END"
echo "TAG:argv:BEGIN"; for p in /proc/[0-9]*; do case "$(cat $p/comm 2>/dev/null)" in *ntfs-3g) tr '\0' ' ' < $p/cmdline; echo;; esac; done; echo "TAG:argv:END"
echo "TAG:snap:BEGIN"; omloader snapshot list omloci --root /tmp/s --images-root $IMAGES_ROOT 2>&1; echo "RC=$?"; echo "TAG:snap:END"
echo "TAG:rw:BEGIN"; touch /mnt/images/omloader/rwtest 2>&1 && echo RW_OK && rm -f /mnt/images/omloader/rwtest; echo "TAG:rw:END"
HOOK
ST=$W/ntfs-hook.img; rm -f $ST; truncate -s 16M $ST; mkfs.ext4 -q -L OMLSTAGE -d $W/ntfs-hk $ST
set +e
run() { # $1 tag $2 extra cmdline $3 timeout s  -> $W/$1.clean
  local tag=$1; : > $W/$tag.serial
  timeout -s KILL $3 qemu-system-x86_64 -enable-kvm -machine q35 -cpu host -m 2G -smp 2 -no-reboot -display none -name ntfs-run-$tag \
    -kernel $W/ntfs-k/vmlinuz -initrd $W/ntfs-k/initrd.img -append "console=ttyS0 $2" -serial file:$W/$tag.serial \
    -drive if=none,id=d0,file=$D,format=raw -device virtio-blk-pci,drive=d0 \
    -drive if=none,id=d1,file=$ST,format=raw -device virtio-blk-pci,drive=d1 > $W/$tag.qemu.log 2>&1 &
  echo $! > $W/$tag.pid
}
clean() { tr -d '\r' < $W/$1.serial | sed 's/\x1b\[[0-9;=?]*[a-zA-Z]//g' > $W/$1.clean; }
sect() { sed -n "/TAG:$2:BEGIN/,/TAG:$2:END/p" $W/$1.clean; }
waitfor() { for i in $(seq 1 $3); do clean $1; grep -aqE "$2" $W/$1.clean && return 0; sleep 1; done; return 1; }
common() { # $1 tag, $2 rw|ro
  local t=$1
  # grep the whole console (kernel printk (loop1: detected capacity change) can land in the MIDDLE of the stage-1 line on the serial console, so only the line prefix + medium.env are checked) (not just the first "live medium" line: serial/console interleaving can split it); dump stage 1 on failure
  L=$(grep -a 'OMLOADER_STAGE1: live medium' $W/$t.clean | head -1); echo "--- $t: $L"
  grep -aqE 'iso-scan: dev=/dev/vda1 fs=ntfs file=' $W/$t.clean && sect $t medium | grep -q 'omloader/omloader.iso' && pass "$t: stage 1 found the ISO on the NTFS partition (in-kernel ntfs3)" || { fail "$t: stage 1 line: $L"; grep -a 'STAGE1' $W/$t.clean | head -20; }
  sect $t env | grep -q 'IMGDEV=/dev/vda1' && pass "$t: data partition = the ISO's partition (label VHDX, no VTOYDATA: fallback)" || fail "$t: env: $(sect $t env | tr '\n' ' ')"
  sect $t env | grep -q 'IMAGES_ROOT=/mnt/images/omloader/images' && sect $t ls | grep -qx omloci && pass "$t: images from omloader/images visible (omloci)" || fail "$t: images: $(sect $t ls | tr '\n' ' ')"
  sect $t snap | grep -q 'RC=0' && pass "$t: image omloci resolved by the CLI list (image.yaml read from NTFS)" || fail "$t: snapshot list: $(sect $t snap | tr '\n' ' ')"
  sect $t medium | grep -q 'MEDIUM_HOST_DRV=ntfs3' && ! sect $t argv | grep -q ntfs-3g && pass "$t: host NTFS on the in-kernel ntfs3 driver (no FUSE daemon)" || fail "$t: driver: $(sect $t medium | tr '\n' ' ') $(sect $t argv | tr '\n' ' ')"
  if [ $2 = any ]; then   # ntfs-3g recovers an unclean (non-hibernated) volume and mounts it rw; only hibernation (Fast Startup) forces ro, which cannot be provoked here
    echo "INFO: $t: after the hard kill the volume was mounted $(sect $t medium | grep -q 'MEDIUM_HOST_RW=1' && echo read-write '(ntfs-3g recovered it)' || echo READ-ONLY)"
  elif [ $2 = rw ]; then
    sect $t medium | grep -q 'MEDIUM_HOST_RW=1' && sect $t rw | grep -q RW_OK && pass "$t: NTFS data partition mounted read-write" || fail "$t: rw: $(sect $t rw | tr '\n' ' ')"
  else
    sect $t medium | grep -q 'MEDIUM_HOST_RW=0' && ! sect $t rw | grep -q RW_OK && pass "$t: dirty NTFS mounted READ-ONLY (images usable)" || fail "$t: expected ro: $(sect $t medium | tr '\n' ' ') $(sect $t rw | tr '\n' ' ')"
    grep -aq 'not mountable read-write' $W/$t.clean && pass "$t: stage 1 warned about the read-only fallback" || fail "$t: no stage 1 ro warning"
  fi
}
for r in A B; do
  run ntfs-$r "omloader.hook=1" 240
  if waitfor ntfs-$r 'OMLOADER_HOOK_RC=' 200; then sleep 3; clean ntfs-$r; common ntfs-$r rw; else fail "$r: no hook result (see $W/ntfs-$r.clean)"; grep -a 'STAGE1\|FATAL' $W/ntfs-$r.clean | tail -5; fi
  waitfor ntfs-$r 'reboot: Power down|System halted|Power down' 60; pkill -9 -f "ntfs-run-ntfs-$r" 2>/dev/null; sleep 1
done
# C: rw mount, then the machine dies without unmounting -> dirty flag stays
run ntfs-C "" 200
if waitfor ntfs-C 'OMLOADER_TUI_READY|OMLOADER_SHELL_READY' 150; then sleep 2; pass "C: booted on the NTFS data partition (TUI/shell ready); killing the VM"; else fail "C: no console"; fi
pkill -9 -f "ntfs-run-ntfs-C" 2>/dev/null; sleep 2
run ntfs-D "omloader.hook=1" 240
if waitfor ntfs-D 'OMLOADER_HOOK_RC=' 200; then sleep 3; clean ntfs-D; common ntfs-D any; else fail "D: no hook result (see $W/ntfs-D.clean)"; grep -a 'STAGE1\|FATAL' $W/ntfs-D.clean | tail -5; fi
pkill -9 -f "ntfs-run-ntfs-D" 2>/dev/null

# ---- store-file cases on the NTFS data partition (ntfs-3g): auto-create, reuse, pre-placed foo.dat, vtoycow ignored ----
hookcase() { # $1 tag, $2 hook script body; boots the same disk, waits for the poweroff
  local t=$1; rm -rf $W/ntfs-hk2; mkdir -p $W/ntfs-hk2; printf '%s\n' "$2" > $W/ntfs-hk2/hook.sh
  ST=$W/ntfs-hook2.img; rm -f $ST; truncate -s 16M $ST; mkfs.ext4 -q -L OMLSTAGE -d $W/ntfs-hk2 $ST
  run $t "omloader.hook=1" 600
  waitfor $t 'OMLOADER_HOOK_RC=' 500 || fail "$t: no hook result (see $W/$t.clean)"
  sleep 2; clean $t; waitfor $t 'Power down|System halted' 60; pkill -9 -f "ntfs-run-$t" 2>/dev/null; sleep 1
}
REPORT='. /run/omloader/env; echo "TAG:r:BEGIN"; cat /run/omloader/env; echo SNAPDIR=$(ls /mnt/snap/snapshots 2>&1 | tr "\n" " "); ls -l /mnt/images/omloader /mnt/images/ventoy 2>&1; for f in /mnt/images/omloader/*.img /mnt/images/omloader/*.dat; do [ -f $f ] && echo "LABEL $f $(e2label $f)"; done; cat /run/omloader/store.log; echo "TAG:r:END"'
hookcase E0 "set -e; printf 'storage:\n  snapshots:\n    store_file:\n      size: 1G\n' > /mnt/images/omloader/boot.yaml; echo prepared"
hookcase E1 "for i in \$(seq 1 300); do grep -q ^ready /run/omloader/store-create.status && break; sleep 1; done; sleep 2; $REPORT; mkdir -p /mnt/snap/snapshots/snapA; echo hi > /mnt/snap/snapshots/snapA/m; sync"
R=$(sect E1 r)
echo "$R" | grep -q 'OMLOADER_LOADER: creating store .*1 GiB' && pass "E1: auto-create marker line (1 GiB, NTFS/ntfs3)" || fail "E1: no creation marker: $(grep -a 'OMLOADER_LOADER: .*store' $W/E1.clean | head -5)"
echo "$R" | grep -q 'store allocated via fallocate' && pass "E1: store allocated with fallocate (mode 0, full size)" || fail "E1: alloc method: $(echo "$R" | grep -a allocated)"
echo "$R" | grep -q 'LABEL /mnt/images/omloader/store.img OMLOADER' && pass "E1: omloader/store.img exists, label OMLOADER" || fail "E1: label: $R"
echo "$R" | grep -q 'SNAPOK=0' && pass "E1: background creation: this session was RAM-only (store used from the next boot)" || fail "E1: env: $R"
hookcase E2 "$REPORT; sleep 1"
R=$(sect E2 r)
! grep -aq 'OMLOADER_LOADER: creating store' $W/E2.clean && echo "$R" | grep -q 'STOREFILE=/mnt/images/omloader/store.img' && echo "$R" | grep -q 'SNAPOK=1' && pass "E2: store reused from the next boot (SNAPOK=1), no second creation" || fail "E2: $R"
# ---- H: power-off safety of a store auto-created on ntfs3: synced data survives a HARD kill; clean shutdown leaves a clean store ----
CHK='. /run/omloader/env; echo "TAG:r:BEGIN"; echo STOREFILE=$STOREFILE SNAPOK=$SNAPOK; ls /mnt/snap/snapshots 2>&1 | tr "\n" " "; echo; echo "SYNCED=$(cat /mnt/snap/snapshots/synced/m 2>&1)"; echo "BIG=$(cat /mnt/snap/snapshots/clean/m 2>&1)"; echo "BIGSUM=$(cat /mnt/snap/snapshots/synced/sum 2>&1)/$(sha256sum < /mnt/snap/snapshots/synced/big 2>&1 | cut -c1-16)"; dmesg | grep -ciE "EXT4-fs error|corrupt" | sed "s/^/EXT4ERR=/"; dmesg | grep -ci "recovering journal\|orphan" | sed "s/^/REPLAY=/"; umount /mnt/snap; L=$(losetup -j $STOREFILE -O NAME --noheadings); e2fsck -fn $STOREFILE >/tmp/fsck.out 2>&1; echo FSCK_RC=$?; tail -2 /tmp/fsck.out; echo "TAG:r:END"'
hookcase H0 "set -e; mkdir -p /mnt/snap/snapshots/synced; echo base > /mnt/snap/snapshots/synced/m; dd if=/dev/urandom of=/mnt/snap/snapshots/synced/big bs=1M count=64 status=none; sha256sum < /mnt/snap/snapshots/synced/big | cut -c1-16 > /mnt/snap/snapshots/synced/sum; sync; echo prepared"
# H1: unsynced writes in flight, then the VM dies
rm -rf $W/ntfs-hk2; mkdir -p $W/ntfs-hk2
printf '%s\n' 'mkdir -p /mnt/snap/snapshots/inflight; echo HOOK_WROTE; dd if=/dev/urandom of=/mnt/snap/snapshots/inflight/x bs=1M count=300 status=none & sleep 1; for i in 1 2 3 4 5 6; do echo $i > /mnt/snap/snapshots/inflight/f$i; done; echo HOOK_INFLIGHT; sleep 600' > $W/ntfs-hk2/hook.sh
ST=$W/ntfs-hook2.img; rm -f $ST; truncate -s 16M $ST; mkfs.ext4 -q -L OMLSTAGE -d $W/ntfs-hk2 $ST
run H1 "omloader.hook=1" 300
if waitfor H1 'HOOK_INFLIGHT' 200; then sleep 1; pass "H1: store mounted, data in flight; killing the VM (no shutdown)"; else fail "H1: no in-flight marker (see $W/H1.clean)"; fi
pkill -9 -f "ntfs-run-H1"; sleep 2
hookcase H2 "$CHK"
R=$(sect H2 r)
echo "$R" | grep -q 'SNAPOK=1' && echo "$R" | grep -q 'SYNCED=base' && pass "H2: after hard power-off the store mounts and the synced data is present" || fail "H2: $R"
S1=$(echo "$R" | sed -n 's/^BIGSUM=\(.*\)\/.*/\1/p'); S2=$(echo "$R" | sed -n 's/^BIGSUM=.*\///p'); [ -n "$S1" ] && [ "$S1" = "$S2" ] && pass "H2: synced 64 MiB file intact (checksum)" || fail "H2: checksum $S1 vs $S2"
echo "$R" | grep -q 'EXT4ERR=0' && pass "H2: no ext4 errors in dmesg (journal replay: $(echo "$R" | grep REPLAY))" || fail "H2: ext4 errors: $R"
echo "$R" | grep -q 'FSCK_RC=[01]$' && pass "H2: e2fsck -fn clean after journal replay" || fail "H2: fsck: $R"
# H3: clean shutdown -> nothing to replay, fsck clean
hookcase H3 "mkdir -p /mnt/snap/snapshots/clean; echo done > /mnt/snap/snapshots/clean/m; echo prepared"
hookcase H4 "$CHK"
R=$(sect H4 r)
echo "$R" | grep -q 'BIG=done' && echo "$R" | grep -q 'REPLAY=0' && echo "$R" | grep -q 'FSCK_RC=0$' && pass "H4: clean shutdown: data present, no journal replay, fsck clean" || fail "H4: $R"
# ---- Z: forced zero-write path (omloader.alloc=zero): progress file grows, store usable next boot ----
hookcase Z0 "rm -f /mnt/images/omloader/store.img; echo prepared"
rm -rf $W/ntfs-hk2; mkdir -p $W/ntfs-hk2
printf '%s\n' 'for i in $(seq 1 300); do cat /run/omloader/store-create.status >> /tmp/st.log; grep -q ^ready /run/omloader/store-create.status && break; sleep 1; done; echo "TAG:r:BEGIN"; sort -u /tmp/st.log | head -20; cat /run/omloader/store.log; echo "TAG:r:END"' > $W/ntfs-hk2/hook.sh
ST=$W/ntfs-hook2.img; rm -f $ST; truncate -s 16M $ST; mkfs.ext4 -q -L OMLSTAGE -d $W/ntfs-hk2 $ST
run Z1 "omloader.hook=1 omloader.alloc=zero" 600
waitfor Z1 'OMLOADER_HOOK_RC=' 500 || fail "Z1: no hook result"; sleep 2; clean Z1; pkill -9 -f "ntfs-run-Z1"
R=$(sect Z1 r)
echo "$R" | grep -q 'store allocated via zero-write' && [ $(echo "$R" | grep -c '^creating') -ge 2 ] && echo "$R" | grep -q '^ready' && pass "Z1: zero-write path: progress status updated (0 -> total) ($(echo "$R" | grep -c '^creating') distinct samples), ready" || fail "Z1: $R"
hookcase F0 "set -e; rm -f /mnt/images/omloader/store.img /mnt/images/omloader/boot.yaml; truncate -s 64M /mnt/images/omloader/foo.dat; mkfs.ext4 -q -L OMLOADER /mnt/images/omloader/foo.dat; mkdir -p /mnt/images/ventoy; truncate -s 64M /mnt/images/ventoy/x.dat; mkfs.ext4 -q -L vtoycow /mnt/images/ventoy/x.dat; echo prepared"
hookcase F1 "$REPORT"
R=$(sect F1 r)
echo "$R" | grep -q 'STOREFILE=/mnt/images/omloader/foo.dat' && pass "F1: pre-placed omloader/foo.dat (label OMLOADER) chosen" || fail "F1: $R"
! grep -aq 'OMLOADER_LOADER: creating store' $W/F1.clean && ! echo "$R" | grep -q 'ventoy/x.dat chosen' && pass "F1: no creation; vtoycow file ignored" || fail "F1: $R"
hookcase G0 "set -e; rm -f /mnt/images/omloader/foo.dat; echo prepared"
run G "" 200
if waitfor G 'OMLOADER_TUI_READY|OMLOADER_SHELL_READY' 60 && grep -aq 'creating omloader/store.img in the background' $W/G.clean; then pass "G: TUI/loader ready within 60 s while auto-create runs in the background"; else fail "G: loader not ready in 60 s with auto-create: $(grep -a 'OMLOADER_LOADER: .*store' $W/G.clean | head -3)"; fi
pkill -9 -f "ntfs-run-G" 2>/dev/null
[ $FAIL = 0 ] && echo "E2E-NTFS: ALL PASS" || echo "E2E-NTFS: FAILED"
exit $FAIL
