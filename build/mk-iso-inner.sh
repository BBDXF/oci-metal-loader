#!/bin/bash
# Runs INSIDE the builder container. /work is the bind-mounted tree.
set -euo pipefail
W=/work; R=/tmp/rd; OUT=$W/out; ISO=/tmp/iso
KVER=$(ls /lib/modules | head -1)
rm -rf $R $ISO; mkdir -p $R/{usr/bin,usr/lib/modules,dev,proc,sys,etc,run,tmp,mnt,root} $ISO/boot/grub
ln -s usr/bin $R/bin; ln -s usr/bin $R/sbin; ln -s usr/lib $R/lib; mkdir -p $R/usr/lib64; ln -s usr/lib64 $R/lib64
cp /usr/bin/busybox $R/usr/bin/busybox
for a in $($R/usr/bin/busybox --list); do [ "$a" = busybox ] || ln -sf busybox $R/usr/bin/$a; done
addlibs() { ldd "$1" 2>/dev/null | grep -o '/[^ ]*' | while read -r l; do
    d=$(readlink -f "$(dirname "$l")"); mkdir -p "$R$d"; [ -e "$R$d/$(basename "$l")" ] || cp -L "$l" "$R$d/"; done; }
addbin() { local p; p=$(command -v "$1" 2>/dev/null) || { echo "skip $1"; return 0; }
  p=$(readlink -f "$p"); rm -f "$R/usr/bin/$1"; cp -L "$p" "$R/usr/bin/$1"; addlibs "$p"; }
for b in kexec dmsetup lvm kpartx blkid losetup mount umount findmnt lsblk sfdisk fdisk \
         mkfs.ext4 e2fsck resize2fs tune2fs mksquashfs unsquashfs kmod depmod modprobe insmod lsmod \
         mkfs.exfat fsck.exfat setsid cttyhack; do addbin $b; done
# glibc's pthread_exit dlopen()s libgcc_s at runtime (mksquashfs aborts without it)
for l in $(find /lib /usr/lib -name 'libgcc_s.so.1' 2>/dev/null | head -1); do d=$(readlink -f "$(dirname "$l")"); mkdir -p "$R$d"; cp -L "$l" "$R$d/"; done
ln -sf kmod $R/usr/bin/modprobe 2>/dev/null || true
# dynamic loader (ldd of static busybox has none)
cp -L /lib64/ld-linux-x86-64.so.2 $R/usr/lib64/ 2>/dev/null || { mkdir -p $R/usr/lib64; cp -L /lib64/ld-linux-x86-64.so.2 $R/usr/lib64/; }
# omloader
install -m755 $W/bin/omloader $R/usr/bin/omloader
# modules
MODS="dm-mod dm-snapshot dm-linear dm-bufio loop squashfs overlay ext4 xfs exfat vfat fat isofs nls_cp437 nls_iso8859-1 nls_utf8 nvme virtio_blk virtio_pci virtio_scsi sd_mod sr_mod ahci ata_piix libahci usb-storage uas xhci-pci xhci-hcd ehci-pci uhci-hcd crc32c_generic virtio_net net_failover failover e1000 e1000e r8169 realtek igb igc tg3 8139too 8139cp pcnet32 vmxnet3 af_packet unix virtio_console virtio_rng hid_generic usbhid"
echo $MODS > $R/etc/modules-list
mkdir -p $R/usr/lib/modules/$KVER
for m in $MODS; do { modprobe -d / -S $KVER --show-depends $m 2>/dev/null || true; } | awk '$1=="insmod"{print $2}'; done | sort -u | while read -r f; do
  rel=${f#*/lib/modules/$KVER/}   # builder is usrmerged: modprobe reports /usr/lib/modules/... (was nested under the initrd tree)
  mkdir -p "$R/usr/lib/modules/$KVER/$(dirname "$rel")"; cp "$f" "$R/usr/lib/modules/$KVER/$rel"; done
cp /lib/modules/$KVER/modules.builtin* /lib/modules/$KVER/modules.order $R/usr/lib/modules/$KVER/ 2>/dev/null || true
depmod -b $R $KVER
cp $W/build/init $R/init; chmod +x $R/init
(cd $R && find . | cpio -o -H newc --quiet | xz -T0 -C crc32 -6 > $ISO/boot/initrd.img)
cp /boot/vmlinuz-$KVER $ISO/boot/vmlinuz
echo marker > $ISO/boot/omloader.marker
cp $W/build/grub.cfg $ISO/boot/grub/grub.cfg
cat > /tmp/embed.cfg <<'EOC'
search --no-floppy --file --set=root /boot/omloader.marker
configfile ($root)/boot/grub/grub.cfg
EOC
grub-mkstandalone -O x86_64-efi -o /tmp/BOOTX64.EFI \
  --modules="part_gpt part_msdos fat iso9660 search search_fs_file configfile normal echo test serial terminal linux all_video" \
  "boot/grub/grub.cfg=/tmp/embed.cfg"
dd if=/dev/zero of=$ISO/boot/efiboot.img bs=1M count=24 status=none
mformat -i $ISO/boot/efiboot.img ::
mmd -i $ISO/boot/efiboot.img ::/EFI ::/EFI/BOOT
mcopy -i $ISO/boot/efiboot.img /tmp/BOOTX64.EFI ::/EFI/BOOT/BOOTX64.EFI
mkdir -p $OUT
xorriso -as mkisofs -o $OUT/omloader.iso -V OMLOADER_LOADER -R -J \
  -e boot/efiboot.img -no-emul-boot -isohybrid-gpt-basdat $ISO 2>&1 | tail -3
echo "kernel=$KVER initrd=$(du -h $ISO/boot/initrd.img | cut -f1)"
