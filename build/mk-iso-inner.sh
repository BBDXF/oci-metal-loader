#!/bin/bash
# Runs INSIDE the builder container (= live image + ISO tools). /work is the bind-mounted tree.
# Inputs: /work/out/live.squashfs, /work/bin/omloader, /work/build/{stage1-init,grub.cfg,live/rootfs}
set -euo pipefail
W=/work; R=/tmp/rd; OUT=$W/out; ISO=/tmp/iso; X=/tmp/extra
KVER=$(ls /lib/modules | head -1)
[ -s $OUT/live.squashfs ] || { echo "missing $OUT/live.squashfs"; exit 1; }

# ---- omloader.squashfs: our files on top of the Debian layer (binary, units, scripts, config) ----
rm -rf $X; mkdir -p $X
cp -a $W/build/live/rootfs/. $X/
install -D -m755 $W/bin/omloader $X/usr/local/bin/omloader
install -D -m644 $W/configs/sources.yaml $X/etc/omloader/sources.yaml   # sources + mirrors catalog (docs/sources.md); override: <data partition>/omloader/sources.yaml
# base info: static part from the live layer (build/gen-base-info.sh) + build-time keys -> /etc/omloader-release, packages.txt
BD=$(date -u -d "@${SOURCE_DATE_EPOCH:-$(date +%s)}" +%Y-%m-%d)
mkdir -p $X/etc $X/usr/share/omloader
{ echo "OMLOADER_VERSION=${OML_VERSION:-dev}"; echo "BUILD_DATE=$BD"; echo "GIT_COMMIT=${GIT_COMMIT:-unknown}"; cat /usr/share/omloader/base-release; } > $X/etc/omloader-release
cp /usr/share/omloader/packages.txt $X/usr/share/omloader/packages.txt
cp $X/etc/omloader-release $OUT/omloader-release; cp /usr/share/omloader/packages.txt $OUT/packages.txt
mksquashfs $X $OUT/omloader.squashfs -noappend -comp zstd -quiet -no-progress

# ---- stage-1 initramfs: busybox + mount/blkid/modprobe + the boot-critical modules ----
rm -rf $R $ISO; mkdir -p $R/{usr/bin,usr/lib/modules,dev,proc,sys,etc,run,tmp,mnt,root} $ISO/boot/grub $ISO/live
ln -s usr/bin $R/bin; ln -s usr/bin $R/sbin; ln -s usr/lib $R/lib; mkdir -p $R/usr/lib64; ln -s usr/lib64 $R/lib64
cp /usr/bin/busybox $R/usr/bin/busybox
for a in $($R/usr/bin/busybox --list); do [ "$a" = busybox ] || ln -sf busybox $R/usr/bin/$a; done
addlibs() { ldd "$1" 2>/dev/null | grep -o '/[^ ]*' | while read -r l; do
    d=$(readlink -f "$(dirname "$l")"); mkdir -p "$R$d"; [ -e "$R$d/$(basename "$l")" ] || cp -L "$l" "$R$d/"; done; }
addbin() { local p; p=$(command -v "$1" 2>/dev/null) || { echo "skip $1"; return 0; }
  p=$(readlink -f "$p"); rm -f "$R/usr/bin/$1"; cp -L "$p" "$R/usr/bin/$1"; addlibs "$p"; }
for b in mount umount blkid losetup kmod; do addbin $b; done
# ntfs-3g (FUSE) for an ISO on an NTFS partition (stage 1 uses ntfs-3g even though Debian 13's 6.12 kernel has ntfs3: one code path, tested): installed as "@ntfs-3g" (see stage1-init)
p=$(readlink -f "$(command -v ntfs-3g)"); cp -L "$p" "$R/usr/bin/@ntfs-3g"; addlibs "$p"; ln -sf /proc/mounts $R/etc/mtab
ln -sf kmod $R/usr/bin/modprobe; ln -sf kmod $R/usr/bin/depmod; ln -sf kmod $R/usr/bin/insmod
cp -L /lib64/ld-linux-x86-64.so.2 $R/usr/lib64/ 2>/dev/null || { mkdir -p $R/usr/lib64; cp -L /lib64/ld-linux-x86-64.so.2 $R/usr/lib64/; }
# modules needed to find and assemble the live medium (everything else is loaded by udev inside the live system)
MODS="loop squashfs overlay isofs udf xfs nls_ascii cdrom sr_mod sd_mod nls_cp437 nls_iso8859-1 nls_utf8 ext4 exfat vfat fat fuse crc32c_generic ahci libahci ata_piix ata_generic libata scsi_mod nvme nvme_core virtio_blk virtio_scsi virtio_pci virtio_ring usb-storage uas usbhid xhci-pci xhci-hcd ehci-pci ehci-hcd uhci-hcd ohci-pci ohci-hcd mmc_block sdhci-pci hid_generic"
echo $MODS > $R/etc/modules-list
mkdir -p $R/usr/lib/modules/$KVER
for m in $MODS; do { modprobe -d / -S $KVER --show-depends $m 2>/dev/null || true; } | awk '$1=="insmod"{print $2}'; done | sort -u | while read -r f; do
  rel=${f#*/lib/modules/$KVER/}
  mkdir -p "$R/usr/lib/modules/$KVER/$(dirname "$rel")"; cp "$f" "$R/usr/lib/modules/$KVER/$rel"; done
cp /lib/modules/$KVER/modules.builtin* /lib/modules/$KVER/modules.order $R/usr/lib/modules/$KVER/ 2>/dev/null || true
depmod -b $R $KVER
cp $W/build/stage1-init $R/init; chmod +x $R/init
(cd $R && find . | cpio -o -H newc --quiet | xz -T0 -C crc32 -6 > $ISO/boot/initrd.img)
cp /boot/vmlinuz-$KVER $ISO/boot/vmlinuz
cp $OUT/live.squashfs $OUT/omloader.squashfs $ISO/live/
echo marker > $ISO/boot/omloader.marker
cp $OUT/omloader-release $OUT/packages.txt $ISO/   # readable without booting (mount the ISO)
cp $W/build/grub.cfg $ISO/boot/grub/grub.cfg
# grub font for gfxterm (grub.cfg: loadfont ($root)/boot/grub/fonts/unicode.pf2); grub-common ships it (~2.3 MB on the ISO)
mkdir -p $ISO/boot/grub/fonts; cp /usr/share/grub/unicode.pf2 $ISO/boot/grub/fonts/unicode.pf2
cat > /tmp/embed.cfg <<'EOC'
search --no-floppy --file --set=root /boot/omloader.marker
configfile ($root)/boot/grub/grub.cfg
EOC
grub-mkstandalone -O x86_64-efi -o /tmp/BOOTX64.EFI \
  --modules="part_gpt part_msdos fat iso9660 search search_fs_file configfile normal echo test serial terminal linux all_video gfxterm gfxterm_background font video video_fb regexp" \
  "boot/grub/grub.cfg=/tmp/embed.cfg"
dd if=/dev/zero of=$ISO/boot/efiboot.img bs=1M count=24 status=none
mformat -i $ISO/boot/efiboot.img ::
mmd -i $ISO/boot/efiboot.img ::/EFI ::/EFI/BOOT
mcopy -i $ISO/boot/efiboot.img /tmp/BOOTX64.EFI ::/EFI/BOOT/BOOTX64.EFI
mkdir -p $OUT
xorriso -as mkisofs -o $OUT/omloader.iso -V OMLOADER_LIVE -R -J -iso-level 3 \
  -e boot/efiboot.img -no-emul-boot -isohybrid-gpt-basdat $ISO 2>&1 | tail -3
echo "kernel=$KVER initrd=$(du -h $ISO/boot/initrd.img | cut -f1) live=$(du -h $OUT/live.squashfs | cut -f1) iso=$(du -h $OUT/omloader.iso | cut -f1)"
