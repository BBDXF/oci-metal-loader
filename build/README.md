# OCI Metal Loader (omloader): loader ISO + KVM test harness

Everything builds/tests on a remote host over ssh (rootless podman, KVM). Nothing needs root.

## Env vars
- `OML_REMOTE` (default `philips@192.168.8.26`), `OML_REMOTE_PASS` (ssh password, read from env only), `OML_RDIR` (remote dir, default `omloader-test`, under `$HOME`).
- Remote-side scripts: `FIXTURE_MB` (1024), `DISK_GB` (4), `P1_MB` (2048), `NAME` (omlfixture), `TIMEOUT`, `BUS` (nvme|virtio|usb), `SKIP_BUILD=1`.

## Make targets (run locally)
`make iso` · `make fixture` · `make disk` · `make e2e` · `make e2e-oci` · `make e2e-snap` · `make e2e-live` · `make licenses` · `make clean-remote`
(each syncs `build/ test/` via tar-over-ssh and the static binary to `~/omloader-test/bin/omloader`; remote has no rsync).
Artifacts on remote: `~/omloader-test/out/{omloader.iso,fixture.raw,usb.raw,usb-run.raw}`, logs in `work/*.log`.

## Loader (Debian live system)
Pipeline (`build/build-iso.sh`, runs on the remote with rootless podman; needs `mksquashfs` with zstd on the host):
1. `Containerfile` target `live` = debian:bookworm + the package set in `build/THIRD-PARTY.md` (`--no-install-recommends`; non-free-firmware enabled; docs/man/locales/apt lists stripped,
   `copyright` files kept; `/boot/initrd.img-*` removed). `podman export` -> `mksquashfs - live.squashfs -tar -comp zstd` (cached in `cache/live-<imageid>.squashfs`, so a rebuild only
   re-squashes when the package set changed; `make clean-remote` keeps `cache/`, delete it for a from-scratch build).
2. `build/live/rootfs/` + `bin/omloader` -> `omloader.squashfs` (small, rebuilt every time): systemd units, scripts, network config, getty autologin, masks for slow units (apt timers, fstrim, e2scrub, getty-static, ...).
3. Target `builder` = live + grub/xorriso/mtools: `mk-iso-inner.sh` builds the stage-1 initramfs (busybox, util-linux mount/blkid/losetup, kmod, ~40 boot-critical modules; `stage1-init`), standalone GRUB EFI, and the hybrid UEFI ISO (volume label `OMLOADER_LIVE`, `-iso-level 3`).
- Note: the remote can't reach Docker Hub; the base image was pulled from a mirror and tagged locally:
  `podman pull docker.m.daocloud.io/library/debian:bookworm && podman tag docker.m.daocloud.io/library/debian:bookworm docker.io/library/debian:bookworm`
- Stage 1 (`stage1-init`): load modules, find the medium (`omloader.live=` override, label `OMLOADER_LIVE`, else scan all block devices for `live/live.squashfs`), loop-mount both squashfs, overlay (`lowerdir=omloader.squashfs:live.squashfs`, tmpfs upper), move /dev /proc /sys /run, `switch_root` to `/sbin/init` (systemd). On failure it drops to a rescue shell.
- `omloader-mounts.service` (`mounts.sh`): waits <=15 s for fs label `OMLOADER` (ext4 -> `/mnt/snap`) and `VTOYDATA` (-> `/mnt/images`, images at `/mnt/images/images`; fallback `/mnt/snap/images`) **only** when an `omloader.*` flag asks for an automatic action (a plain shell is never delayed); writes `/run/omloader/env`.
- `omloader-autoboot.service` (`autoboot.sh`): `omloader.hook=1` (run `/mnt/stage/hook.sh`, power off), `omloader.selftest=1`, `omloader.image=<n>` [`omloader.snapshot=<s>`] -> `omloader boot ...`. The udev exec queue is stopped while loop/dm/kpartx devices are assembled so nothing holds them open before `kexec -e`
  (VM path unchanged: M1 `kexec -l/-e` directly from the live system). Container images: `systemctl switch-root` (see top-level README). `omloader.poweroff_on_fail=1` powers off on failure. Without flags the unit exits and the autologin shell (serial ttyS0 + tty1) prints the banner and `OMLOADER_SHELL_READY uptime=N`.
- Network: systemd-networkd DHCP on `en*`/`eth*` (`80-omloader-dhcp.network`), systemd-resolved stub (`/etc/resolv.conf` symlink created by tmpfiles because podman bind-mounts it at build time). iwd + wireless-regdb installed, unconfigured.
- Kernel cmdline from `grub.cfg`: `console=tty0 console=ttyS0,115200 loglevel=4 systemd.show_status=auto`. Container guests share it (so tty1 *and* ttyS0 consoles work; `/dev/console` = ttyS0, the last `console=`).
- Before `systemctl switch-root`, `autoboot.sh` bind-mounts `/mnt/snap` and `/mnt/images` to `<newroot>/.omloader/{snap,images}` and writes `<newroot>/.omloader/devs`; the guest's shutdown hooks (installed by the firstboot adapter) remount them read-only at poweroff. The loader's own poweroff paths (`poweroff_now` in autoboot.sh) run `sync` + `remount,ro`.
- Passing args in tests: grub.cfg sources `/boot/grub/extra.cfg` if present; `test/run-kvm.sh` copies the ISO and injects it with xorriso (no rebuild).
- `make licenses` -> `out/licenses.tsv` (package, version, license ids); `build/THIRD-PARTY.md` has the licensing notes.
- `make e2e-live` (`test/e2e-live.sh`): DHCP lease/route, resolver, CA bundle, extra CA via `update-ca-certificates` + TLS to a local https server (QEMU user-net 10.0.2.2), tool set, selftest flag, boot-to-shell time, ISO size.

## Fixture / disk
Fixture rootfs: `podman export` of bookworm+kernel → `mkfs.ext4 -d tar` → sfdisk/dd (label `OMLROOT`, kernel cmdline `root=LABEL=OMLROOT`, resolved by blkid in an initramfs hook). Its `/sbin/init` prints `OMLOADER_GUEST_OK`, `OMLOADER_COUNTER=N`, bumps `/var/lib/omloader-test/counter`, powers off.
Disk: p1 exfat `VTOYDATA` (populated with exfat-fuse in rootless podman, ext4 fallback recorded in `out/usb.vtoydata-fs`), p2 ext4 `OMLOADER`.
- `DEBIAN_MIRROR` (default `http://mirrors.ustc.edu.cn/debian`; deb.debian.org was ~20KB/s from the remote).

## M3 (container boot; OCI fixture = experimental, opt-in via omloader.experimental=oci)
`make e2e-oci` -> `test/e2e-oci.sh`: ISO, `test/make-fixture-oci.sh` (podman build of `test/fixture-oci/Containerfile` on debian:bookworm-slim + systemd,
`podman save --format oci-archive`, `omloader import oci-archive` -> `out/oci-images/omloci`), `NO_VM=1 test/make-disk.sh` (disk includes `out/oci-images/*`,
and the VM fixture if present), then boots `new:run1`, `base`, `run1`. `autoboot.sh` does the `systemctl switch-root` handoff (see top-level README).
bookworm-slim is pulled via the daocloud mirror and tagged like bookworm (see above); falls back to bookworm.

## M2 (snapshot management)
`make e2e-snap` -> `test/e2e-snap.sh` (SKIP_BUILD / SKIP_ISO / NO_VM env switches). Root-needing `omloader snapshot ...` commands run in the loader VM through the
`omloader.hook=1` mechanism (a `OMLSTAGE` ext4 disk with `hook.sh`; per-hook output is delimited by `TAG:<name>:BEGIN/RC=n/END` lines). 
Guest markers: extra cmdline `omloader.marker=NAME` makes the OCI fixture guest write `/var/lib/omloader-test/mark-NAME` and print `OMLOADER_MARKS=`; the fixture declares an ext4-file (`/var/lib/omloader-data`, 64M) and a dir (`/srv/dirdata`) data volume.
`make clean-remote` no longer fails (the remote pkill used to match its own ssh shell).
