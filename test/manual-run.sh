#!/bin/bash
# Run ON THE REMOTE. A persistent, libvirt-managed VM (qemu:///system, domain "omloader-manual") for trying omloader by hand.
# It shows up in Cockpit -> Virtual machines (system connection) and can be driven with virsh.
#   test/manual-run.sh start [image] [snapshot]   (re)define the domain and start it (no args: loader TUI; image => boots it directly)
#   test/manual-run.sh console                    serial console (virsh console; Ctrl-] detaches, the VM keeps running)
#   test/manual-run.sh status | stop              stop = ACPI shutdown, then destroy after 30 s
#   test/manual-run.sh reset                      drop the domain, its NVRAM and the store; the next start re-copies out/real-usb.raw
# Persistent: out/manual-usb.raw (VTOYDATA = images, OMLOADER = snapshots/cache; libvirt chowns it to libvirt-qemu while the domain runs)
# and the domain's NVRAM (/var/lib/libvirt/qemu/nvram/omloader-manual_VARS.fd).
# The serial output is also logged to work/manual/serial.log (libvirt-qemu owned, world-readable).
# Env: OML_ARGS (extra kernel args, e.g. omloader.ui=shell), ISO, MEM_MIB (default 4096), VCPUS (default 2), DOM (domain name), VNC_PORT
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; M=$ROOT/work/manual; mkdir -p $M; chmod o+rx $ROOT $O $ROOT/work $M 2>/dev/null
ISO=${ISO:-$O/omloader.iso}; DISK=$O/manual-usb.raw; DOM=${DOM:-omloader-manual}
V="virsh -c qemu:///system"
state() { $V domstate $DOM 2>/dev/null | head -1; }
case "${1:-}" in
start)
  [ "$(state)" = running ] && { echo "already running"; exit 0; }
  [ -f $DISK ] || { [ -f $O/real-usb.raw ] || { echo "no $O/real-usb.raw: run test/e2e-real.sh with REAL_KEEP=1 first"; exit 1; }
    cp --sparse=always $O/real-usb.raw $DISK; }
  ARGS="${OML_ARGS:-}"; [ -n "${2:-}" ] && ARGS="$ARGS omloader.image=$2"; [ -n "${3:-}" ] && ARGS="$ARGS omloader.snapshot=$3"
  rm -f $M/boot.iso   # may be owned by libvirt-qemu from the last run
  cp $ISO $M/boot.iso; echo "set omloader_args=\"$ARGS\"" > $M/extra.cfg
  xorriso -dev $M/boot.iso -boot_image any keep -map $M/extra.cfg /boot/grub/extra.cfg -commit -end >/dev/null 2>&1 || { echo "xorriso failed"; exit 3; }
  chmod a+r $M/boot.iso
  UUID=$($V domuuid $DOM 2>/dev/null | head -1)   # keep the uuid (and so the NVRAM) of an existing definition
  cat > $M/domain.xml <<XML
<domain type='kvm'>
  <name>$DOM</name>
${UUID:+  <uuid>$UUID</uuid>
}  <description>omloader manual test VM (test/manual-run.sh)</description>
  <memory unit='MiB'>${MEM_MIB:-4096}</memory>
  <vcpu>${VCPUS:-2}</vcpu>
  <os firmware='efi'>
    <type arch='x86_64' machine='q35'>hvm</type>
    <firmware><feature enabled='no' name='secure-boot'/><feature enabled='no' name='enrolled-keys'/></firmware>
  </os>
  <features><acpi/><apic/></features>
  <cpu mode='host-passthrough'/>
  <on_poweroff>destroy</on_poweroff><on_reboot>restart</on_reboot><on_crash>destroy</on_crash>
  <devices>
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/><source file='$M/boot.iso'/><target dev='sda' bus='sata'/><readonly/><boot order='1'/>
    </disk>
    <disk type='file' device='disk'>
      <driver name='qemu' type='raw'/><source file='$DISK'/><target dev='vda' bus='virtio'/><serial>omloader0</serial>
    </disk>
    <interface type='network'><source network='default'/><model type='virtio'/></interface>
    <serial type='pty'><log file='$M/serial.log' append='off'/><target type='isa-serial' port='0'><model name='isa-serial'/></target></serial>
    <console type='pty'><target type='serial' port='0'/></console>
    <graphics type='vnc' port='${VNC_PORT:--1}' autoport='$([ -n "${VNC_PORT:-}" ] && echo no || echo yes)' listen='127.0.0.1'><listen type='address' address='127.0.0.1'/></graphics>
    <video><model type='virtio'/></video>   <!-- Ubuntu's kernel has virtio-gpu but no bochs driver: with VGA the guest console stays blank after kexec -->
    <memballoon model='virtio' freePageReporting='on'><stats period='5'/></memballoon>   <!-- statistics for Cockpit; free page reporting returns the guest's free RAM to the host (qemu's RSS otherwise stays at the full 4 GiB after a boot) -->
    <input type='tablet' bus='usb'/>
    <rng model='virtio'><backend model='random'>/dev/urandom</backend></rng>
  </devices>
</domain>
XML
  $V define $M/domain.xml >/dev/null || exit 1
  $V start $DOM && echo "started: Cockpit https://<host>:9090 -> Virtual machines -> $DOM; or: virsh -c qemu:///system console $DOM";;
console) echo "(Ctrl-] detaches)"; exec $V console $DOM;;
status) echo "state: $(state)"; $V dominfo $DOM 2>&1 | grep -E "State|CPU\(s\)|Max memory"; $V domdisplay $DOM 2>/dev/null; $V domifaddr $DOM 2>/dev/null | tail -n +3;;
stop)
  [ "$(state)" = running ] || { echo "not running ($(state))"; exit 0; }
  $V shutdown $DOM >/dev/null; for i in $(seq 1 30); do [ "$(state)" = running ] || break; sleep 1; done
  [ "$(state)" = running ] && { echo "forcing"; $V destroy $DOM >/dev/null; }; echo stopped;;
reset)
  [ "$(state)" = running ] && { echo "stop first"; exit 1; }
  $V undefine $DOM --nvram >/dev/null 2>&1; rm -f $DISK; echo "domain, NVRAM and store removed; next start re-copies out/real-usb.raw";;
*) sed -n 2,11p $0; exit 1;;
esac
