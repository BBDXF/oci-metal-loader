#!/bin/bash
# Run ON THE REMOTE (never started by CI locally). Boots the ISO under KVM/OVMF with a virtio-vga of the requested size, lets the
# loader reach the TUI on tty1 and takes a qemu screendump (PPM, converted to PNG when ImageMagick/netpbm exist).
#   test/look-screenshot.sh [WxH=1920x1080] [extra kernel args, e.g. omloader.font=Uni3-TerminusBold24x12]
# Output: work/look-<WxH>.ppm/.png and the serial log work/look-<WxH>.log (look for "omloader: console font").
# The serial console is still used by the harness; the screenshot shows what tty1 / a VNC viewer shows.
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; mkdir -p $W
RES=${1:-1920x1080}; shift || true
ISO=${ISO:-$O/omloader.iso}; DISK=${DISK:-$O/usb.raw}
TAG=look-$RES; LOG=$W/$TAG.log; MON=$W/$TAG.mon; SHOT=$W/$TAG.ppm
RUN=$W/run.$$; mkdir -p $RUN; trap 'rm -rf $RUN $MON' EXIT
cp /usr/share/OVMF/OVMF_VARS_4M.fd $RUN/vars.fd; cp $ISO $RUN/boot.iso
echo "set omloader_args=\"omloader.gfx=$RES $*\"" > $RUN/extra.cfg
xorriso -dev $RUN/boot.iso -boot_image any keep -map $RUN/extra.cfg /boot/grub/extra.cfg -commit -end >/dev/null 2>&1 || { echo "xorriso inject failed"; exit 3; }
: > $LOG; rm -f $MON
timeout -s KILL ${TIMEOUT:-150} qemu-system-x86_64 -enable-kvm -machine q35 -cpu host -m 2G -smp 2 -no-reboot \
  -drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd -drive if=pflash,format=raw,file=$RUN/vars.fd \
  -drive if=none,id=cd,file=$RUN/boot.iso,media=cdrom,readonly=on -device ide-cd,drive=cd,bootindex=1 \
  -drive if=none,id=usb,file=$DISK,format=raw -device nvme,drive=usb,serial=omloader0 \
  -device virtio-vga,edid=on,xres=${RES%x*},yres=${RES#*x} -display none -serial file:$LOG -monitor unix:$MON,server,nowait &
Q=$!
for i in $(seq 1 120); do grep -q OMLOADER_TUI_READY $LOG 2>/dev/null && break; sleep 1; done
sleep 4
echo "screendump $SHOT" | socat - UNIX-CONNECT:$MON >/dev/null 2>&1 || echo "screendump failed (socat missing?)"
sleep 1; echo quit | socat - UNIX-CONNECT:$MON >/dev/null 2>&1; wait $Q 2>/dev/null
if command -v convert >/dev/null; then convert $SHOT $W/$TAG.png && echo "screenshot: $W/$TAG.png"; elif command -v pnmtopng >/dev/null; then pnmtopng $SHOT > $W/$TAG.png && echo "screenshot: $W/$TAG.png"; else echo "screenshot: $SHOT (PPM)"; fi
grep -a "console font\|OMLOADER_TUI_READY" $LOG | head -5
