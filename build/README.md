# OCI Metal Loader (omloader): loader ISO + KVM test harness

Everything builds/tests on a remote host over ssh (rootless podman, KVM). Nothing needs root.

## Env vars
- `OML_REMOTE` (default `philips@192.168.8.26`), `OML_REMOTE_PASS` (ssh password, read from env only), `OML_RDIR` (remote dir, default `omloader-test`, under `$HOME`).
- Remote-side scripts: `FIXTURE_MB` (1024), `DISK_GB` (4), `P1_MB` (2048), `NAME` (omlfixture), `TIMEOUT`, `BUS` (nvme|virtio|usb), `SKIP_BUILD=1`.

## Make targets (run locally)
`make iso` · `make fixture` · `make disk` · `make e2e` · `make clean-remote`
(each syncs `build/ test/` via tar-over-ssh and the static binary to `~/omloader-test/bin/omloader`; remote has no rsync).
Artifacts on remote: `~/omloader-test/out/{omloader.iso,fixture.raw,usb.raw,usb-run.raw}`, logs in `work/*.log`.

## Loader
- `Containerfile` + `mk-iso-inner.sh`: debian:bookworm builder; custom busybox initramfs (kexec, dmsetup, lvm, kpartx, e2fsprogs, squashfs, util-linux, kmod + modules), GRUB EFI standalone, ESP `efiboot.img`, hybrid UEFI-only ISO. Console tty0 + ttyS0.
- Note: the remote can't reach Docker Hub; the base image was pulled from a mirror and tagged locally:
  `podman pull docker.m.daocloud.io/library/debian:bookworm && podman tag docker.m.daocloud.io/library/debian:bookworm docker.io/library/debian:bookworm`
- `init`: waits ≤15s for fs label `OMLOADER` (ext4 → `/mnt/snap`) and `VTOYDATA` (→ `/mnt/images`, images at `/mnt/images/images`; fallback `/mnt/snap/images`). Cmdline `omloader.image=<n>` [`omloader.snapshot=<s>`] auto-runs `omloader boot --image … --images-root … --snap-root /mnt/snap/snapshots [--snapshot …]`; otherwise shell. `omloader.selftest=1` prints mounts and powers off.
- Passing args in tests: grub.cfg sources `/boot/grub/extra.cfg` if present; `test/run-kvm.sh` copies the ISO and injects it with xorriso (no rebuild).

## Fixture / disk
Fixture rootfs: `podman export` of bookworm+kernel → `mkfs.ext4 -d tar` → sfdisk/dd (label `OMLROOT`, kernel cmdline `root=LABEL=OMLROOT`, resolved by blkid in an initramfs hook). Its `/sbin/init` prints `OMLOADER_GUEST_OK`, `OMLOADER_COUNTER=N`, bumps `/var/lib/omloader-test/counter`, powers off.
Disk: p1 exfat `VTOYDATA` (populated with exfat-fuse in rootless podman, ext4 fallback recorded in `out/usb.vtoydata-fs`), p2 ext4 `OMLOADER`.
- `DEBIAN_MIRROR` (default `http://mirrors.ustc.edu.cn/debian`; deb.debian.org was ~20KB/s from the remote).

## M3 (OCI/LXD containers)
`make e2e-oci` -> `test/e2e-oci.sh`: ISO, `test/make-fixture-oci.sh` (podman build of `test/fixture-oci/Containerfile` on debian:bookworm-slim + systemd,
`podman save --format oci-archive`, `omloader import oci-archive` -> `out/oci-images/omloci`), `NO_VM=1 test/make-disk.sh` (disk includes `out/oci-images/*`,
and the VM fixture if present), then boots `new:run1`, `base`, `run1`. `build/init` does the `switch_root` handoff (see top-level README).
Extra loader modules (NICs, overlay, squashfs): `MODS` in `mk-iso-inner.sh`. bookworm-slim is pulled via the daocloud mirror and tagged like bookworm (see above); falls back to bookworm.

## M2 (snapshot management)
`make e2e-snap` -> `test/e2e-snap.sh` (SKIP_BUILD / SKIP_ISO / NO_VM env switches). Root-needing `omloader snapshot ...` commands run in the loader VM through the
`omloader.hook=1` mechanism (a `OMLSTAGE` ext4 disk with `hook.sh`; per-hook output is delimited by `TAG:<name>:BEGIN/RC=n/END` lines). The loader ISO now also ships `libgcc_s.so.1` (mksquashfs needs it).
Guest markers: extra cmdline `omloader.marker=NAME` makes the OCI fixture guest write `/var/lib/omloader-test/mark-NAME` and print `OMLOADER_MARKS=`; the fixture declares an ext4-file (`/var/lib/omloader-data`, 64M) and a dir (`/srv/dirdata`) data volume.
`make clean-remote` no longer fails (the remote pkill used to match its own ssh shell).
