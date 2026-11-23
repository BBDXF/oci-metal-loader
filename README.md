# OCI Metal Loader (`omloader`)

**Boot LXD images on bare metal from a USB drive — with snapshots.**

English | [简体中文](README.zh-CN.md)

`omloader.iso` is a UEFI loader that sits on a [Ventoy](https://github.com/ventoy/Ventoy) drive (or boots on its own). Put **LXD VM (whole-disk) and container images** on the drive,
pick one, and it runs directly on the real hardware: **no installation, no hypervisor**, with branchable, rollbackable snapshots and data that survives reboots.

> **Alpha.** Everything is tested in QEMU/OVMF only — not on real hardware yet. See [Status](#status).

## Why it is different

- **Real bare metal, nothing is virtualised.** There is no QEMU/KVM/Hyper-V layer. "LXD VM image" only names the *image format* (a whole-disk image with its own kernel, as LXD publishes for VMs): the loader prepares it and hands the hardware over to that kernel with `kexec`, so it runs natively. Container images (rootfs only) boot as full systems on the loader's kernel. Native drivers, GPU and disks.
- **Snapshots are built in.** Every boot can start a new branch. Create, roll back, tag, prune, flatten and export snapshots. Base images stay read-only and checksummed.
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

1. **Get the ISO.** There is no prebuilt release yet; build it from source (rootless podman) — see [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).
2. **Put it on a Ventoy drive** and boot it from the Ventoy menu ([docs/ventoy.md](docs/ventoy.md)).
3. **Get images** (inside the loader, or copy them to `images/` on the data partition):
   ```sh
   omloader images list --os ubuntu --type vm
   omloader pull lxc:ubuntu/noble --vm --name ubuntu-noble --out /mnt/images/images
   ```
4. **Boot one**, in a fresh snapshot branch:
   ```sh
   omloader boot --image ubuntu-noble --snapshot new:try1
   ```
5. **Manage snapshots:**
   ```sh
   omloader snapshot list ubuntu-noble --tree
   omloader snapshot create ubuntu-noble before-upgrade --note "clean install"
   omloader snapshot rollback ubuntu-noble before-upgrade
   ```
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
| `vm` | LXD *VM* image: a whole-disk image (`disk.qcow2` is converted to `base.raw` on import). "VM" names the format — nothing is virtualised. | the image's own (`kexec`) | `dm-snapshot` COW files | UEFI + GPT, root filesystem ext4 or xfs with `/boot` holding the kernel and initrd; no LVM/LUKS root; guest must use an initramfs the loader can extend (verified only on Debian/initramfs-tools guests); no independent data volumes; no account/console provisioning or clean unmount (the guest manages itself); Secure Boot blocks `kexec` |
| `lxd-container` | LXD *container* image: `rootfs.squashfs` (+ `metadata.yaml`) | the loader's (Debian 6.1), modules bind-mounted into the guest | `overlayfs` upper layers | needs a Linux root with an init; hardware support is whatever the loader's kernel offers; console login and users are provisioned at first boot (systemd guests only; others get a busybox debug shell); data volumes `dir` / `ext4-file` |
| `oci` | Docker/OCI image — **experimental, off by default** (`experimental.oci_images: true`) | the loader's | `overlayfs` | application images usually have no init or login; no registry pull |

Common to all types: the base image is read-only and checksummed, booting a snapshot that has children is refused (use `new:<name>`), Linux guests only (use Ventoy for Windows),
x86_64 UEFI only. Details: [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

## How it compares

| | **omloader** | Ventoy | Kairos / bootc | LinuxKit |
|---|---|---|---|---|
| Boots from a USB drive without installing | ✅ | ✅ | ❌ (installs to disk) | ✅ (in RAM) |
| LXD VM / container images | ✅ | ❌ | ❌ | ❌ |
| Runs a disk image natively with its own kernel (no hypervisor) | ✅ (LXD VM images) | ✅ (ISO/vDisk) | ✅ | ✅ |
| Snapshots and rollback | ✅ | ❌ | ✅ (A/B) | ❌ |
| Persistent data volumes | ✅ | ✅ (`.dat`) | ✅ | ❌ |
| Windows ISO / VHD(x) boot | ➖ use Ventoy | ✅ | ❌ | ❌ |
| Online pull with mirrors/proxy and offline mode | ✅ | ❌ | ✅ | ➖ |

omloader complements Ventoy rather than replacing it.

## Status

Alpha. What works: LXD VM and container boot, snapshots, data volumes, online pull, store file, Ventoy booting, TUI — verified by the end-to-end suites in QEMU/OVMF.
What is missing or unverified: real hardware, Secure Boot, LUKS/LVM guests, Windows (use Ventoy), public-server testing (pull is tested against a local simplestreams server), non-Debian VM guests.
Docker/OCI images are **experimental and off by default** (they are application images, not systems). Details and the full list of limitations: [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

## Documentation

- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) — architecture, status, roadmap, limitations, tests
- [docs/ventoy.md](docs/ventoy.md) · [docs/store-file.md](docs/store-file.md) · [docs/tui.md](docs/tui.md) · [build/README.md](build/README.md)

## License

[GPL-3.0](LICENSE), like Ventoy. Not an official Open Container Initiative project and not affiliated with or endorsed by the OCI, Canonical or the LXD project.
