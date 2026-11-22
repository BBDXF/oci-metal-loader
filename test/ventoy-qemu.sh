#!/bin/bash
# Run ON THE REMOTE. Starts (in the background) QEMU/KVM + OVMF booting out/ventoy.raw (the Ventoy disk) as an NVMe disk,
# plus an optional data disk (VTOYDATA/OMLOADER) on virtio. No CD-ROM: the loader ISO is only a file on the Ventoy partition.
# Env: VDISK (out/ventoy.raw)  DATA (data disk, optional)  TAG (run name: work/<TAG>.{serial,mon,pid,vars})  SB=1 (Secure Boot OVMF + MS keys)
#      TIMEOUT (kill after N s, default 300)   MEM (2G)
# Output: serial log work/$TAG.serial (input socket work/$TAG.ser, see ventoy-ser.py), QEMU monitor unix socket work/$TAG.mon (sendkey / screendump), pidfile work/$TAG.pid
# Disks are opened with snapshot=on: nothing is ever written to the images.
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; mkdir -p $W
TAG=${TAG:-vt}; VDISK=${VDISK:-$O/ventoy.raw}; TIMEOUT=${TIMEOUT:-300}; MEM=${MEM:-2G}
if [ "${SB:-0}" = 1 ]; then CODE=/usr/share/OVMF/OVMF_CODE_4M.secboot.fd; VARS=/usr/share/OVMF/OVMF_VARS_4M.ms.fd; MACH="-machine q35,smm=on -global driver=cfi.pflash01,property=secure,value=on"
else CODE=/usr/share/OVMF/OVMF_CODE_4M.fd; VARS=/usr/share/OVMF/OVMF_VARS_4M.fd; MACH="-machine q35"; fi
cp $VARS $W/$TAG.vars; rm -f $W/$TAG.mon $W/$TAG.ser; : > $W/$TAG.serial
DEV="-drive if=none,id=vt,file=$VDISK,format=raw,snapshot=on -device nvme,drive=vt,serial=ventoy0,bootindex=1"
[ -n "${DATA:-}" ] && DEV="$DEV -drive if=none,id=dd,file=$DATA,format=raw,snapshot=on -device virtio-blk-pci,drive=dd"
timeout -s KILL $TIMEOUT qemu-system-x86_64 -enable-kvm $MACH -cpu host -m $MEM -smp 2 -no-reboot \
  -drive if=pflash,format=raw,readonly=on,file=$CODE -drive if=pflash,format=raw,file=$W/$TAG.vars \
  $DEV -vga std -display none -chardev socket,id=ser,path=$W/$TAG.ser,server=on,wait=off,logfile=$W/$TAG.serial -serial chardev:ser -monitor unix:$W/$TAG.mon,server,nowait \
  -pidfile $W/$TAG.pid > $W/$TAG.qemu.log 2>&1 &
sleep 1; echo "started $TAG pid=$(cat $W/$TAG.pid 2>/dev/null)"
