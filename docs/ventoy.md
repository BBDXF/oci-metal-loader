# Using omloader.iso with Ventoy

`omloader.iso` boots from a Ventoy stick (UEFI x86_64), in **normal mode and GRUB2 mode**. Tested with Ventoy 1.1.17 in QEMU/OVMF
(`make e2e-ventoy`; the test builds a rootless Ventoy-layout disk, see `test/make-ventoy-disk.sh`).

## Setup
1. Install Ventoy as usual (Ventoy2Disk). The first (exFAT/NTFS/ext4/XFS/FAT32) partition holds the files; NTFS is fine (stage 1 reads it with ntfs-3g).
2. Copy `omloader.iso` to the Ventoy partition: `/omloader/omloader-<version>.iso` (any path works, see below; the loader keeps everything in the one folder `omloader/`).
3. Optional `ventoy/ventoy.json`:
   ```json
   {
     "control": [ { "VTOY_DEFAULT_IMAGE": "/omloader/omloader-<version>.iso" }, { "VTOY_MENU_TIMEOUT": "5" } ],
     "menu_alias": [ { "image": "/omloader/omloader-<version>.iso", "alias": "OCI Metal Loader" } ]
   }
   ```
4. Loader data, all inside `omloader/` of the data partition: `omloader-<ver>.iso`, `boot.yaml`, `sources.yaml`, `images/<name>/image.yaml`, `store.img`.
   The **data partition** is, in order: `omloader.data=LABEL=x|UUID=x|/dev/x` (kernel flag), the partition labelled `VTOYDATA`, otherwise **the partition holding the ISO** (whatever its label, e.g. `VHDX`; no relabelling needed).
   An `OMLOADER` ext4 partition (snapshots) is looked up by label as without Ventoy. `ventoy/ventoy.json` is Ventoy's own file and the only loader-related thing outside `omloader/`.

## How the loader finds the ISO
Ventoy cannot pass kernel parameters and (for an unknown distribution) does not inject a hook, so the kernel never sees the ISO as a CD-ROM.
Stage 1 (`build/stage1-init`) therefore searches, in order: (a) `omloader.live=`/label `OMLOADER_LIVE`, (b) any block device
holding `live/live.squashfs` (`/dev/sr*`, `/dev/mapper/*`, disks), (c) **iso-scan**: every partition (exfat, **ntfs**, vfat, ext2/3/4, xfs)
is mounted read-only and searched for `omloader.iso` at `/`, `/omloader/` (also `omloader-<ver>.iso`), `/ISO/`, then for any `*.iso` within two directory levels whose volume label is
`OMLOADER_LIVE`. `omloader.isofile=/path.iso` [`omloader.isodev=LABEL=…|UUID=…|/dev/…`] pins it (in GRUB2 mode `grub.cfg` passes `omloader.isofile=` itself
when Ventoy exports `vt_chosen_path`). The ISO is loop-mounted; the host file system stays mounted (`/run/medium-host`, details in `/run/omloader/medium.env`) so the
loop device stays valid. The serial/tty log says how: `OMLOADER_STAGE1: live medium /dev/nvme0n1p1 (iso-scan: dev=… fs=exfat file=/omloader.iso)`.
If the host partition is the data partition (see 4.) or labelled `OMLOADER` it is switched to read-write in stage 1 so `omloader-mounts` can use it as images/snapshot store without a conflicting second mount.
(NTFS cannot be remounted: stage 1 releases the ISO, mounts ntfs-3g read-write again and re-attaches the ISO; a dirty or hibernated volume — Windows Fast Startup — stays **read-only**, images remain usable.) A partition holding the ISO while a different `VTOYDATA`/`omloader.data=` partition exists stays read-only. At shutdown `mounts.sh stop` unmounts the data partitions in the right order.

### iso-scan order (stage 1)
Partitions that look like Ventoy (label `Ventoy`/`VTOYDATA`, or a `ventoy/` directory) are tried first, then the rest. On each partition the steps are:
pinned `omloader.isofile=` > `ventoy/ventoy.json` `VTOY_DEFAULT_SEARCH_ROOT` (ISOs below that directory, up to 3 levels; same meaning as in Ventoy) >
`VTOY_DEFAULT_IMAGE` (an absolute path; Ventoy also requires it inside the search root) > `omloader/omloader*.iso`, `/omloader.iso`, `/ISO/omloader.iso` > any `*.iso` labelled `OMLOADER_LIVE`.
The first hit wins. The log shows `OMLOADER_STAGE1: iso: <dev> (<fs>, label <l>) <path> via <step>` and, if other partitions hold `omloader*.iso` too,
`iso: skipped other candidates: …`. Avoid several omloader ISOs on different partitions: only the first is used.

## Default image without kernel parameters
Fully unattended (Ventoy default entry + omloader countdown): see [INSTALL.md, Unattended boot](INSTALL.md#unattended-boot-boot-a-chosen-image-with-no-key-presses).
Create `omloader/boot.yaml` on the data partition (next to the ISO; also read from an OMLOADER partition):
```yaml
boot:
  default_image: myimage        # empty/absent = just the shell
  default_snapshot: current    # optional (default "current"; a snapshot label boots it ephemerally)
  timeout: 10                   # countdown seconds (min. 5), shown in the TUI; press any key to cancel
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
- Limits: the data partition that holds the ISO cannot be unmounted while the loader runs; an ISO on an NTFS partition **is** found: Debian trixie's 6.12 kernel has an `ntfs3` module, but the loader still uses ntfs-3g (FUSE) everywhere, so the initramfs carries `ntfs-3g` + `fuse.ko` (about +1 MB; the FUSE daemon is named `@ntfs-3g` so systemd keeps it alive across switch-root; `make e2e-ntfs`); ISO path with spaces is found by scan only (not passed via `omloader.isofile=`); `omloader-mounts` waits for USB data partitions only when an `omloader.*` flag is on the command line, so `boot.yaml` on a slow USB VTOYDATA may be missed (put it on the Ventoy partition, which stage 1 already mounted).

## Display
In Ventoy's GRUB2 mode `grub.cfg` does not touch the video mode (no `gfxmode`/`gfxterm`, no resolution submenu): Ventoy drives the display. The Linux console font is still sized to the framebuffer by `omloader-console.service` (see `docs/tui.md`, *Console font and resolution*).

## Console resolution (tty1 / VNC)

GRUB switches to the best mode of the firmware (up to 1920x1080) and keeps it for the Linux console (`gfxpayload=keep`). In **VMs** (QEMU/libvirt virtio or
bochs video) the kernel's display driver is loaded about 3 s after boot, replaces the firmware framebuffer and picks its own preferred mode, usually
1280x800: the screen "drops" when the TUI appears. The default entry keeps the native drivers on purpose (container guests run on the loader kernel and keep
native GPU drivers, multi-monitor and acceleration). Options:

- Choose the GRUB entry **"OCI Metal Loader (safe display: keep the firmware resolution, nomodeset)"** (it also works from Ventoy's GRUB2 mode menu), or add `nomodeset`
  to the kernel arguments (`/boot/grub/extra.cfg`: `set omloader_args="nomodeset"`; libvirt: `OML_ARGS=nomodeset test/manual-run.sh start`).
- Or fix the video mode of the VM (libvirt: `<video><model type='virtio' heads='1'><resolution x='1920' y='1080'/></model></video>`, which `test/manual-run.sh` does).
- On real hardware the native driver normally chooses the panel's native mode; no action needed.

Inside a booted guest the resolution is the guest's business: change it in the desktop (Settings -> Displays, `xrandr`). A VM guest's kernel line can carry
`video=...` through `boot.kernel_cmdline_extra` in boot.yaml (appended to the VM guest's command line; see INSTALL.md); containers run on the loader kernel and
have no kernel line of their own. Under Ventoy the framebuffer mode before the Linux console is whatever Ventoy's menu set.


## Host disks in guests

By default guests see the data partition (and the other partitions of its disk) under `/mnt/disks/<disk>/<label>`, read-write like on a plain Ventoy stick; nothing is mounted until accessed. Never delete or move `omloader/` (it holds `store.img`). Windows Fast Startup/hibernation makes ntfs3 refuse read-write. Switch off with `guest_defaults.host_disks.mode: off`; details in INSTALL.md 9.4.
