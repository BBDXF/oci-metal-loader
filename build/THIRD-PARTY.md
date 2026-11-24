# Third-party software in the omloader ISO

The ISO (`out/omloader.iso`, never committed) redistributes **Debian GNU/Linux 12 "bookworm"** packages. omloader itself is
GPL-3.0-or-later (see `../LICENSE`); the Debian packages keep their own licenses (mostly GPL-2.0+/LGPL/MIT/BSD). Anyone who
redistributes the ISO must comply with those licenses (source offer / pointer to the Debian source packages, license texts).

* Every package ships its license text in the image: `/usr/share/doc/<package>/copyright` is kept (all other docs, man pages and
  locales are stripped to save space).
* Corresponding source for all Debian packages: <https://snapshot.debian.org/> / `apt-get source <package>` on bookworm.
* Generate the exact list for a build: `make licenses` → `out/licenses.tsv` (`package<TAB>version<TAB>license ids from the copyright files`).
* **Non-free firmware**: the `firmware-*` packages come from Debian's `non-free-firmware` component (installed so real hardware has
  working NICs/Wi-Fi/etc.). They are redistributable binary blobs under their own (non-DFSG-free) licenses, see
  `/usr/share/doc/firmware-*/copyright`. If you need a fully free ISO, remove `firmware-misc-nonfree firmware-realtek firmware-iwlwifi`
  (and the `non-free-firmware` component) from `build/Containerfile`.
* Linux kernel (`linux-image-amd64`): GPL-2.0 with the usual syscall exception; GRUB (`grub-efi-amd64-bin`): GPL-3.0+;
  systemd: LGPL-2.1+; busybox: GPL-2.0.

## Package set (explicitly installed, `--no-install-recommends`; dependencies come along)

| Purpose | Packages |
|---|---|
| init / services | systemd, systemd-sysv, systemd-resolved, udev, dbus, kmod |
| disks / filesystems | util-linux, e2fsprogs, exfatprogs, xfsprogs, squashfs-tools, lvm2, dmsetup, kpartx, parted, gdisk, fdisk, cryptsetup |
| boot / VM tooling | kexec-tools, qemu-utils, busybox-static, xz-utils (lxd.tar.xz metadata) |
| networking / TLS | iproute2, iputils-ping, ca-certificates, curl, iwd, wireless-regdb |
| kernel + firmware | linux-image-amd64, firmware-linux-free, firmware-misc-nonfree, firmware-realtek, firmware-iwlwifi |
| shell comfort | less, nano, vim-tiny, procps, pciutils, usbutils |
| ISO build only (builder image, not in the ISO) | grub-efi-amd64-bin, grub-common, xorriso, mtools, dosfstools, cpio, xz-utils |

The stage-1 initramfs contains busybox, util-linux (`mount`, `umount`, `blkid`, `losetup`), kmod and a handful of kernel modules from the same packages.

## Go dependencies of the `omloader` binary

* `gopkg.in/yaml.v3` — MIT / Apache-2.0 (config parsing).
* The terminal UI (`internal/tui`, see `../docs/tui.md`) is written against the Go standard library only: no extra third-party
  code or licenses were added for it.
* `crane` (google/go-containerregistry, Apache-2.0): static registry client for the experimental `omloader pull docker:IMAGE`; downloaded at build time (release tarball, sha256 pinned in `build/Containerfile`), license text in `/usr/share/doc/crane/copyright`. Adds about 12 MB to the live system.
