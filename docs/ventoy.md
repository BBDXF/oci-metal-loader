# Using omloader.iso with Ventoy

`omloader.iso` boots from a Ventoy stick (UEFI x86_64), in **normal mode and GRUB2 mode**. Tested with Ventoy 1.1.17 in QEMU/OVMF
(`make e2e-ventoy`; the test builds a rootless Ventoy-layout disk, see `test/make-ventoy-disk.sh`).

## Setup
1. Install Ventoy as usual (Ventoy2Disk). The first (exFAT/NTFS/ext4/XFS/FAT32) partition holds the files.
2. Copy `omloader.iso` to the Ventoy partition: `/omloader.iso` (any path works, see below).
3. Optional `ventoy/ventoy.json`:
   ```json
   {
     "control": [ { "VTOY_DEFAULT_IMAGE": "/omloader.iso" }, { "VTOY_MENU_TIMEOUT": "5" } ],
     "menu_alias": [ { "image": "/omloader.iso", "alias": "OCI Metal Loader" } ]
   }
   ```
4. Loader data: the loader looks for the labelled partitions `VTOYDATA` (images, e.g. `images/<name>/image.yaml`) and `OMLOADER` (ext4, snapshots)
   exactly as without Ventoy. They may be separate partitions/disks; the Ventoy partition itself may be relabelled `VTOYDATA`.

## How the loader finds the ISO
Ventoy cannot pass kernel parameters and (for an unknown distribution) does not inject a hook, so the kernel never sees the ISO as a CD-ROM.
Stage 1 (`build/stage1-init`) therefore searches, in order: (a) `omloader.live=`/label `OMLOADER_LIVE`, (b) any block device
holding `live/live.squashfs` (`/dev/sr*`, `/dev/mapper/*`, disks), (c) **iso-scan**: every partition (exfat, vfat, ext2/3/4, xfs)
is mounted read-only and searched for `omloader.iso` at `/`, `/omloader/`, `/ISO/`, then for any `*.iso` within two directory levels whose volume label is
`OMLOADER_LIVE`. `omloader.isofile=/path.iso` [`omloader.isodev=LABEL=…|UUID=…|/dev/…`] pins it (in GRUB2 mode `grub.cfg` passes `omloader.isofile=` itself
when Ventoy exports `vt_chosen_path`). The ISO is loop-mounted; the host file system stays mounted (`/run/medium-host`, details in `/run/omloader/medium.env`) so the
loop device stays valid. The serial/tty log says how: `OMLOADER_STAGE1: live medium /dev/nvme0n1p1 (iso-scan: dev=… fs=exfat file=/omloader.iso)`.
If the host partition is labelled `VTOYDATA`/`OMLOADER` it is switched to read-write in stage 1 so `omloader-mounts` can use it as images/snapshot store without a conflicting second mount.
Any other partition holding the ISO stays read-only. At shutdown `mounts.sh stop` unmounts the data partitions in the right order.

## Default image without kernel parameters
Create `omloader/boot.yaml` on VTOYDATA (or `/omloader/boot.yaml` next to the ISO on the Ventoy partition; also `omloader.yaml` on VTOYDATA/OMLOADER is read):
```yaml
boot:
  default_image: myimage        # empty/absent = just the shell
  default_snapshot: current    # optional (default "current"; a snapshot label boots it ephemerally)
  timeout: 5                    # seconds countdown; press Enter on the serial console or keyboard to get a shell instead
  extra_cmdline_flags: [poweroff_on_fail=1]   # omloader.<flag> equivalents: poweroff_on_fail=1, experimental=oci, ...
```
Without `omloader.image=` on the kernel command line `autoboot.sh` reads this file (`omloader config get boot.default_image` etc.), prints the countdown, then runs
`omloader boot` as if `omloader.image=` had been given. A kernel `omloader.image=` always wins. `omloader config get [-c FILE] KEY` prints any resolved config value (lists one per line).

## Findings (Ventoy 1.1.17, OVMF)
- **Normal mode: works.** Ventoy loads the ISO's EFI bootloader (our standalone GRUB), which loads kernel+initrd from the virtual CD. The kernel sees only the real disk; stage 1 iso-scan finds the ISO file.
- **GRUB2 mode: works.** Ventoy's GRUB loop-mounts the ISO and runs our `/boot/grub/grub.cfg`; same iso-scan.
- No `injection` archive or hook is needed. Parameters can only come from `boot.yaml` (or by editing the GRUB entry with `e`).
- Menu: after Enter on the image Ventoy shows "Boot in normal mode / grub2 mode / memdisk"; with `VTOY_SECONDARY_BOOT_MENU: 0` it boots directly in normal mode. Memdisk mode is not supported (UEFI).
- Ventoy's menu is graphical (VGA) only; the loader's own GRUB menu and the whole boot log are on the serial console and tty.
- **Secure Boot** is not supported: disable it in the firmware.
- Limits: the data partition that holds the ISO cannot be unmounted while the loader runs; **an ISO on an NTFS partition is not found by stage 1** (the loader kernel has no ntfs3 driver; ntfs-3g is FUSE and only exists in the full live system) — use exFAT/ext4/xfs/FAT32 for the Ventoy partition; ISO path with spaces is found by scan only (not passed via `omloader.isofile=`); `omloader-mounts` waits for USB data partitions only when an `omloader.*` flag is on the command line, so `boot.yaml` on a slow USB VTOYDATA may be missed (put it on the Ventoy partition, which stage 1 already mounted).

## Display
In Ventoy's GRUB2 mode `grub.cfg` does not touch the video mode (no `gfxmode`/`gfxterm`, no resolution submenu): Ventoy drives the display. The Linux console font is still sized to the framebuffer by `omloader-console.service` (see `docs/tui.md`, *Console font and resolution*).
