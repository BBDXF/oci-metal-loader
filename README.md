# OCI Metal Loader (`omloader`)

[![CI](https://github.com/BBDXF/oci-metal-loader/actions/workflows/ci.yml/badge.svg)](https://github.com/BBDXF/oci-metal-loader/actions/workflows/ci.yml) [![Release](https://img.shields.io/github/v/release/BBDXF/oci-metal-loader?include_prereleases)](https://github.com/BBDXF/oci-metal-loader/releases) [![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)

**Boot LXD images on bare metal from a USB drive — with snapshots.**

English | [简体中文](README.zh-CN.md)

> 📖 **Full install & configuration guide: [docs/INSTALL.md](docs/INSTALL.md)** ([中文](docs/INSTALL.zh-CN.md)) — every config key, file layout, CLI, FAQ.

## Download

Get `omloader-<version>.iso` (+ `SHA256SUMS`, static binary, example configs) from the [Releases page](https://github.com/BBDXF/oci-metal-loader/releases), verify with `sha256sum -c --ignore-missing SHA256SUMS`, and copy the ISO to a Ventoy stick (UEFI, Secure Boot off). Or build it: `make iso` ([build/README.md](build/README.md)).

`omloader.iso` is a UEFI loader that sits on a [Ventoy](https://github.com/ventoy/Ventoy) drive (or boots on its own). Put **LXD VM (whole-disk) and container images** on the drive,
pick one, and it runs directly on the real hardware: **no installation, no hypervisor**, with VM-style snapshots and data that survives reboots.

> **Alpha.** Everything is tested in QEMU/OVMF only — not on real hardware yet. See [Status](#status).

## Why it is different

- **Real bare metal, nothing is virtualised.** There is no QEMU/KVM/Hyper-V layer. "LXD VM image" only names the *image format* (a whole-disk image with its own kernel, as LXD publishes for VMs): the loader prepares it and hands the hardware over to that kernel with `kexec`, so it runs natively. Container images (rootfs only) boot as full systems on the loader's kernel. Native drivers, GPU and disks.
- **Snapshots are built in.** VM-style: create, revert, delete, tag, prune, flatten and export. Base images stay read-only and checksummed.
  VMs use `dm-snapshot`, containers use `overlayfs`.
- **Your data survives.** Snapshots and data volumes live in a persistent store; container data volumes are independent of the system layer, so rolling back the OS keeps your files.
- **Windows-friendly drive.** Images sit on exFAT. The store can be an *ext4 file* on an exFAT/NTFS partition (Ventoy's `.dat` convention) — no extra Linux partition needed.
- **Plays well with Ventoy.** It is just another entry in the Ventoy menu; Ventoy keeps booting your Windows/Linux ISOs and VHD(x) files natively.
- **Online or fully offline.** Pull from `images.linuxcontainers.org`-style (simplestreams) sources with mirrors, proxy and a content-addressed cache; SHA-256 verified. Offline mode never touches the network.
- **Simple to operate.** One static Go binary, plain YAML config, a keyboard-only terminal UI and a scriptable CLI.

## How it works

```
Ventoy / UEFI → GRUB → loader (Debian live: systemd, networkd, tools)
                          ├─ LXD VM image        → dm-snapshot stack over base.raw → kexec → the guest's own kernel
                          └─ LXD container image → squashfs + overlayfs upper       → switch-root → the container's init
snapshots / data / cache → ext4 store (a partition, or an ext4 file on exFAT/NTFS)
```

## Quick start

1. **Get the ISO** from [Releases](https://github.com/BBDXF/oci-metal-loader/releases) (see *Download*), or build it with `make iso`. Step-by-step: [docs/INSTALL.md](docs/INSTALL.md).
2. **Put it on a Ventoy drive** and boot it from the Ventoy menu ([docs/ventoy.md](docs/ventoy.md)).
3. **Get images** (inside the loader, or copy them to `images/` on the data partition):
   ```sh
   omloader images list --os ubuntu --type vm
   omloader pull lxc:ubuntu/noble --vm --name ubuntu-noble --out /mnt/images/images
   ```
4. **Boot one** (continues your live state, "current"):
   ```sh
   omloader boot --image ubuntu-noble
   omloader boot --image ubuntu-noble --snapshot base     # throwaway boot of the original image
   ```
5. **Manage snapshots:**
   ```sh
   omloader snapshot list ubuntu-noble --tree
   omloader snapshot create ubuntu-noble before-upgrade --note "clean install"
   omloader snapshot revert ubuntu-noble before-upgrade     # or: revert ubuntu-noble base [--keep experiment]
   omloader snapshot delete ubuntu-noble before-upgrade
   ```

**Snapshots work like VM snapshots.** `base` is the original, read-only image. `current` is your one writable live state: booting always *continues* it (no new layer per boot).
`snapshot create` freezes the live state under a label and continues on a fresh empty layer on top; `revert <label|base>` throws the live state away and continues from that point
(`--keep NAME` first saves it as a snapshot); `delete` removes a snapshot — a snapshot between two others is merged into its child (overlay images; dm-snapshot VM images refuse and suggest `flatten`).
`--snapshot <label>` or `--ephemeral` boots a throwaway layer on top (nothing is kept, the snapshot is never modified).
**Snapshots are created, reverted and deleted from the loader (CLI/TUI) before the image is booted; a running system cannot be snapshotted or reverted — shut it down, return to the loader, then manage snapshots (hot snapshots are not supported).**
Mind the names: the CLI flags `--vm` and `images list --type vm` keep LXD's image-type spelling; in `image.yaml` the type is `lxd-vm`.
6. **Auto-boot** a default image with a countdown (Ventoy cannot pass kernel parameters) via `omloader/boot.yaml` on the data partition:
   ```yaml
   boot:
     default_image: ubuntu-noble
     timeout: 5
   ```

Prefer menus? Run `omloader tui` ([docs/tui.md](docs/tui.md)).

## Supported image types

| `type` | What it is | Kernel | Snapshots | Main limits |
|---|---|---|---|---|
| `lxd-vm` | LXD *VM* image: a whole-disk image (`disk.qcow2` is converted to `base.raw` on import). "VM" names the format — nothing is virtualised. | the image's own (`kexec`) | `dm-snapshot` COW files | UEFI + GPT, root filesystem ext4 or xfs with `/boot` holding the kernel and initrd; no LVM/LUKS root; guest must use an initramfs the loader can extend (verified only on Debian/initramfs-tools guests); no independent data volumes; default accounts only (`oci`/`Oci123`, sudo, root, autologin — public password, see below), no clean unmount |
| `lxd-container` | LXD *container* image: `rootfs.squashfs` (+ `metadata.yaml`) | the loader's (Debian 6.1), modules bind-mounted into the guest | `overlayfs` upper layers | needs a Linux root with an init; hardware support is whatever the loader's kernel offers; default console accounts are provisioned at boot (autologin: systemd and busybox-init guests; others get a busybox debug shell); data volumes `dir` / `ext4-file` |
| `oci` | Docker/OCI image — **experimental, off by default** (`experimental.oci_images: true`) | the loader's | `overlayfs` | application images usually have no init or login; no registry pull |

Common to all types: the base image is read-only and checksummed, snapshots are never modified by booting (a snapshot boots as a throwaway layer), Linux guests only (use Ventoy for Windows),
x86_64 UEFI only. Details: [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

## How it compares

| | **omloader** | Ventoy | Kairos / bootc | LinuxKit |
|---|---|---|---|---|
| Boots from a USB drive without installing | ✅ | ✅ | ❌ (installs to disk) | ✅ (in RAM) |
| LXD VM / container images | ✅ | ❌ | ❌ | ❌ |
| Runs a disk image natively with its own kernel (no hypervisor) | ✅ (LXD VM images) | ✅ (ISO/vDisk) | ✅ | ✅ |
| Snapshots and revert | ✅ | ❌ | ✅ (A/B) | ❌ |
| Persistent data volumes | ✅ | ✅ (`.dat`) | ✅ | ❌ |
| Windows ISO / VHD(x) boot | ➖ use Ventoy | ✅ | ❌ | ❌ |
| Online pull with mirrors/proxy and offline mode | ✅ | ❌ | ✅ | ➖ |

omloader complements Ventoy rather than replacing it.

## Status

Alpha. What works: LXD VM and container boot, snapshots, data volumes, online pull, store file, Ventoy booting, TUI — verified by the end-to-end suites in QEMU/OVMF.
What is missing or unverified: real hardware (testing postponed), LUKS/LVM guests, Windows (use Ventoy), public-server testing (pull is tested against a local simplestreams server), non-Debian VM guests.
**Default login (public password!):** every image — LXD container, LXD VM and experimental OCI — gets the account `oci` / `Oci123` (sudo with password), root enabled with the same password, and console autologin as `oci`. The password is public: change or disable it with `guest_defaults:` in `omloader/boot.yaml` on the data partition, or per image with `runtime.users` / `root_password_hash` / `autologin` in `image.yaml` (details: [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md#default-accounts-guest_defaults-all-image-types)). The loader only writes into the image's own COW layer.

**VM guests and the ESP:** the guest's `/boot/efi` (ESP) is deliberately not mounted — the loader marks that fstab entry `nofail` (`boot.vm_fix_esp_fstab`, default on); the guest is started by `kexec` and never needs the ESP, but tools that write to it (e.g. `update-grub`) have nothing to write to.

**Hostname:** every guest (container, VM, OCI) gets the image name as hostname (`vm-ubuntu-noble`, not `distrobuilder-<uuid>`); change or disable it with `guest_defaults.hostname` (`image` | `keep` | a name) or `runtime.hostname` in `image.yaml`.

**Limitations:** UEFI **Secure Boot is not supported** and must be disabled. omloader is deliberately bound to Ventoy: put `omloader.iso` on a Ventoy stick (there is no `omloader install`).
Docker/OCI images are **experimental and off by default** (they are application images, not systems). Details and the full list of limitations: [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

## Documentation

- [docs/INSTALL.md](docs/INSTALL.md) ([中文](docs/INSTALL.zh-CN.md)) — install, config reference, layout, CLI, FAQ
- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) — architecture, status, roadmap, limitations, tests
- [docs/ventoy.md](docs/ventoy.md) · [docs/store-file.md](docs/store-file.md) · [docs/tui.md](docs/tui.md) · [build/README.md](build/README.md)
- Sources, mirrors (USTC/NJU/TUNA/CERNET/official), Deepin/Mint status: [docs/sources.md](docs/sources.md) — `omloader source list|test`, `pull --source S --mirror M`.

## License

[GPL-3.0](LICENSE), like Ventoy. Not an official Open Container Initiative project and not affiliated with or endorsed by the OCI, Canonical or the LXD project.
