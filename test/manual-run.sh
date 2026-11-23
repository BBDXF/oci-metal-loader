#!/bin/bash
# Run ON THE REMOTE. A persistent QEMU/OVMF VM for trying omloader by hand.
#   test/manual-run.sh start [image] [snapshot]   boot the loader ISO (no args: loader menu/TUI; image => boots it directly)
#   test/manual-run.sh console                    attach to the serial console (Ctrl-] detaches, the VM keeps running)
#   test/manual-run.sh monitor                    QEMU monitor (e.g. system_powerdown)
#   test/manual-run.sh status | stop              stop = ACPI powerdown, then kill after 30 s
#   test/manual-run.sh reset                      drop the store and recreate it from out/real-usb.raw (the e2e-real store)
# Persistent: out/manual-usb.raw (VTOYDATA = images, OMLOADER = snapshots/cache) and work/manual/vars.fd survive stop/start.
# Network: QEMU user-net; guest ssh is forwarded to 127.0.0.1:${SSH_PORT:-2222}. VNC (VGA, 127.0.0.1 only): display :${VNC_DISPLAY:-1} = port 590x.
# Env: OML_ARGS (extra kernel args, e.g. omloader.ui=shell), ISO, MEM (default 4G), SMP (default 2), BUS (nvme|virtio|usb)
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; M=$ROOT/work/manual; mkdir -p $M
ISO=${ISO:-$O/omloader.iso}; DISK=$O/manual-usb.raw; BUS=${BUS:-nvme}
VNCD=${VNC_DISPLAY:-1}; SSHP=${SSH_PORT:-2222}
alive() { [ -f $M/pid ] && kill -0 "$(cat $M/pid)" 2>/dev/null; }
case "${1:-}" in
start)
  alive && { echo "already running (pid $(cat $M/pid))"; exit 0; }
  [ -f $DISK ] || { [ -f $O/real-usb.raw ] || { echo "no $O/real-usb.raw: run test/e2e-real.sh with REAL_KEEP=1 first"; exit 1; }
    cp --sparse=always $O/real-usb.raw $DISK; }
  [ -f $M/vars.fd ] || cp /usr/share/OVMF/OVMF_VARS_4M.fd $M/vars.fd
  ARGS="${OML_ARGS:-}"; [ -n "${2:-}" ] && ARGS="$ARGS omloader.image=$2"; [ -n "${3:-}" ] && ARGS="$ARGS omloader.snapshot=$3"
  cp $ISO $M/boot.iso; echo "set omloader_args=\"$ARGS\"" > $M/extra.cfg
  xorriso -dev $M/boot.iso -boot_image any keep -map $M/extra.cfg /boot/grub/extra.cfg -commit -end >/dev/null 2>&1 || { echo "xorriso failed"; exit 3; }
  case $BUS in nvme) DEV="-device nvme,drive=usb,serial=omloader0";; virtio) DEV="-device virtio-blk-pci,drive=usb";; usb) DEV="-device qemu-xhci -device usb-storage,drive=usb";; esac
  rm -f $M/serial.sock $M/mon.sock
  qemu-system-x86_64 -enable-kvm -machine q35 -cpu host -m ${MEM:-4G} -smp ${SMP:-2} -name omloader-manual \
    -drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd -drive if=pflash,format=raw,file=$M/vars.fd \
    -drive if=none,id=cd,file=$M/boot.iso,media=cdrom,readonly=on -device ide-cd,drive=cd,bootindex=1 \
    -drive if=none,id=usb,file=$DISK,format=raw $DEV \
    -netdev user,id=n0,hostfwd=tcp:127.0.0.1:$SSHP-:22 -device virtio-net-pci,netdev=n0 \
    -vnc 127.0.0.1:$VNCD -device VGA \
    -serial unix:$M/serial.sock,server,nowait -monitor unix:$M/mon.sock,server,nowait \
    -pidfile $M/pid -daemonize && echo "started (pid $(cat $M/pid)); serial: $0 console; VNC 127.0.0.1:$((5900+VNCD)); guest ssh 127.0.0.1:$SSHP";;
console) [ -S $M/serial.sock ] || { echo "not running"; exit 1; }; echo "(Ctrl-] detaches)"; exec socat -,raw,echo=0,escape=0x1d UNIX-CONNECT:$M/serial.sock;;
monitor) exec socat -,raw,echo=0,escape=0x1d UNIX-CONNECT:$M/mon.sock;;
status) alive && echo "running pid $(cat $M/pid)" || echo "stopped";;
stop)
  alive || { echo "not running"; exit 0; }
  P=$(cat $M/pid); echo system_powerdown | socat - UNIX-CONNECT:$M/mon.sock >/dev/null 2>&1
  for i in $(seq 1 30); do kill -0 $P 2>/dev/null || break; sleep 1; done
  kill -0 $P 2>/dev/null && { echo "forcing"; kill $P; }; echo stopped;;
reset) alive && { echo "stop first"; exit 1; }; rm -f $DISK $M/vars.fd; echo "store removed; next start re-copies out/real-usb.raw";;
*) sed -n 2,12p $0; exit 1;;
esac
