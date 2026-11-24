#!/bin/bash
# Run ON THE REMOTE. Boots the loader ISO under KVM/OVMF with the simulated USB disk.
# Env: NAME (image; => omloader.image=NAME) SNAP (=> omloader.snapshot) EXTRA (more cmdline)
#      EXPERIMENTAL_OCI=1 (=> omloader.experimental=oci: opt in to the experimental OCI support)
#      SELFTEST=1 (mount check + poweroff) LOG=serial log file TIMEOUT=sec (default 240)
#      SERIALSOCK=unix-socket path (serial console as a socket for input; the log file still gets the output)
#      VGA=virtio (virtio-vga instead of the default std VGA, like the libvirt domain of test/manual-run.sh; RES=WxH sets its xres/yres)
#      MONITOR=unix-socket path (qemu monitor, e.g. for system_powerdown)
#      DISK=path DISK2=path(optional extra virtio disk) BUS=nvme|virtio|usb ISO=path INTERACTIVE=1 (serial on stdio)
# How cmdline args reach the kernel: the ISO's grub.cfg `source`s /boot/grub/extra.cfg if present.
# We copy the ISO per run and inject extra.cfg (setting omloader_args) with xorriso (no rebuild).
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; mkdir -p $W
ISO=${ISO:-$O/omloader.iso}; DISK=${DISK:-$O/usb.raw}; BUS=${BUS:-nvme}
LOG=${LOG:-$W/serial.log}; TIMEOUT=${TIMEOUT:-240}; MEM=${MEM:-2G}
ARGS=""
[ -n "${NAME:-}" ] && ARGS="omloader.image=$NAME omloader.poweroff_on_fail=1"
[ -n "${SNAP:-}" ] && ARGS="$ARGS omloader.snapshot=$SNAP"
[ -n "${SELFTEST:-}" ] && ARGS="$ARGS omloader.selftest=1"
[ -n "${EXPERIMENTAL_OCI:-}" ] && ARGS="$ARGS omloader.experimental=oci"
ARGS="$ARGS ${EXTRA:-}"
RUN=$W/run.$$; mkdir -p $RUN; trap 'rm -rf $RUN' EXIT
cp /usr/share/OVMF/OVMF_VARS_4M.fd $RUN/vars.fd
cp $ISO $RUN/boot.iso
echo "set omloader_args=\"$ARGS\"" > $RUN/extra.cfg
xorriso -dev $RUN/boot.iso -boot_image any keep -map $RUN/extra.cfg /boot/grub/extra.cfg -commit -end >/dev/null 2>&1 || { echo "xorriso inject failed"; exit 3; }
case $BUS in
  nvme)   DEV="-device nvme,drive=usb,serial=omloader0";;
  virtio) DEV="-device virtio-blk-pci,drive=usb";;
  usb)    DEV="-device qemu-xhci -device usb-storage,drive=usb";;
esac
[ -n "${DISK2:-}" ] && DEV="$DEV -drive if=none,id=d2,file=$DISK2,format=raw -device virtio-blk-pci,drive=d2"
MON=none; SERCH="-serial file:$LOG"; [ -n "${SERIALSOCK:-}" ] && { rm -f $SERIALSOCK; SERCH="-chardev socket,id=s0,path=$SERIALSOCK,server=on,wait=off,logfile=$LOG -serial chardev:s0"; }
[ -n "${MONITOR:-}" ] && MON="unix:$MONITOR,server,nowait"
if [ -n "${INTERACTIVE:-}" ]; then SER="-nographic -serial mon:stdio"; else SER="-display none -monitor $MON $SERCH"; : > $LOG; fi
VID=""; [ "${VGA:-}" = virtio ] && VID="-vga none -device virtio-vga${RES:+,xres=${RES%x*},yres=${RES#*x}}"
echo "run-kvm: args='$ARGS' bus=$BUS log=$LOG"
timeout ${INTERACTIVE:+--foreground} -s KILL $TIMEOUT qemu-system-x86_64 -enable-kvm -machine q35 -cpu host -m $MEM -smp 2 -no-reboot \
  -drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd \
  -drive if=pflash,format=raw,file=$RUN/vars.fd \
  -drive if=none,id=cd,file=$RUN/boot.iso,media=cdrom,readonly=on -device ide-cd,drive=cd,bootindex=1 \
  -drive if=none,id=usb,file=$DISK,format=raw $DEV \
  $VID $SER
rc=$?; echo "run-kvm: qemu exit=$rc (124/137=timeout)"; exit $rc
