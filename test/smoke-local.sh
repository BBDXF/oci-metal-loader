#!/bin/bash
# Local/CI smoke test (no remote host, no disk fixtures): boots out/omloader.iso under QEMU + OVMF with
# `omloader.ui=shell` and waits for OMLOADER_SHELL_READY on the serial console. Uses KVM when /dev/kvm is usable, else TCG (slow).
# Needs: qemu-system-x86_64, ovmf, xorriso.   Env: ISO (default out/omloader.iso) TIMEOUT (default 300, 900 without KVM) MEM (2G) LOG
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; W=$ROOT/work; mkdir -p "$W"
ISO=${ISO:-$ROOT/out/omloader.iso}; LOG=${LOG:-$W/smoke-serial.log}; MEM=${MEM:-2G}
[ -s "$ISO" ] || { echo "missing $ISO (run make iso)"; exit 2; }
CODE=; VARS=
for d in /usr/share/OVMF /usr/share/edk2/ovmf /usr/share/ovmf; do
  for s in _4M ""; do [ -f $d/OVMF_CODE$s.fd ] && [ -f $d/OVMF_VARS$s.fd ] && { CODE=$d/OVMF_CODE$s.fd; VARS=$d/OVMF_VARS$s.fd; break 2; }; done
done
[ -n "$CODE" ] || { echo "OVMF firmware not found (apt install ovmf)"; exit 2; }
if [ -w /dev/kvm ]; then ACCEL="-enable-kvm -cpu host"; TIMEOUT=${TIMEOUT:-300}; else ACCEL="-accel tcg -cpu max"; TIMEOUT=${TIMEOUT:-900}; echo "smoke: no /dev/kvm, using TCG (slow)"; fi
RUN=$(mktemp -d "$W/smoke.XXXXXX"); trap 'rm -rf "$RUN"' EXIT
cp "$VARS" "$RUN/vars.fd"; cp "$ISO" "$RUN/boot.iso"
echo 'set omloader_args="omloader.ui=shell"' > "$RUN/extra.cfg"   # grub.cfg sources /boot/grub/extra.cfg
xorriso -dev "$RUN/boot.iso" -boot_image any keep -map "$RUN/extra.cfg" /boot/grub/extra.cfg -commit -end >/dev/null 2>&1 || { echo "xorriso inject failed"; exit 3; }
: > "$LOG"
timeout -s KILL "$TIMEOUT" qemu-system-x86_64 $ACCEL -machine q35 -m "$MEM" -smp 2 -no-reboot -display none -monitor none -serial "file:$LOG" \
  -drive if=pflash,format=raw,readonly=on,file="$CODE" -drive if=pflash,format=raw,file="$RUN/vars.fd" \
  -drive if=none,id=cd,file="$RUN/boot.iso",media=cdrom,readonly=on -device ide-cd,drive=cd,bootindex=1 &
QP=$!
ok=0
for ((i = 0; i < TIMEOUT; i++)); do
  grep -q OMLOADER_SHELL_READY "$LOG" 2>/dev/null && { ok=1; break; }
  kill -0 $QP 2>/dev/null || break
  sleep 1
done
pkill -KILL -P $$ qemu-system-x86 2>/dev/null; kill -KILL $QP 2>/dev/null; wait $QP 2>/dev/null
if [ $ok = 1 ]; then grep -a OMLOADER_SHELL_READY "$LOG" | tail -1; echo "PASS: loader reached the shell-ready marker"; exit 0; fi
echo "FAIL: OMLOADER_SHELL_READY not seen; serial log tail:"; tail -40 "$LOG"; exit 1
