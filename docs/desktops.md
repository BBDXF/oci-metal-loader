# Desktop environments: where to get them

([中文](desktops.zh-CN.md)) Catalog data below was read on **2026-11-23** from `omloader images list` against the `lxc-images`,
`ubuntu-releases` and `ubuntu-daily` sources (the catalog changes daily; re-check with
`omloader images list --os X --variant desktop`). Mirrors `tuna` / `nju` / `cernet` serve the same `lxc-images` catalog
(see [sources.md](sources.md)).

**Status words:** *verified* = seen in the catalog on that date; *unverified* = from research, not checked here.
**Not tested:** booting a desktop VM from omloader on real hardware (the Ubuntu desktop VM boot test is still pending).
**Not an omloader feature (yet):** installing a desktop on first boot. A desktop is either already in the image (table 1) or you
install it by hand in the guest (`apt` / `dnf` / `pacman` / `zypper`), then snapshot it.

## 1. Ready-made desktop images (verified)

| Image (pull spec) | Type | Source | Desktop | Size | Notes |
|---|---|---|---|---|---|
| `lxc:ubuntu/jammy/desktop` | lxd-vm | `lxc-images` | GNOME | 1.0 GiB | 22.04 |
| `lxc:ubuntu/noble/desktop` | lxd-vm | `lxc-images` | GNOME | 1.1 GiB | 24.04 LTS |
| `lxc:ubuntu/resolute/desktop` | lxd-vm | `lxc-images` | GNOME | 1.3 GiB | newer release |
| `lxc:ubuntu/stonking/desktop` | lxd-vm | `lxc-images` | GNOME | 1.4 GiB | development release |
| `lxc:opensuse/tumbleweed/desktop-kde` | lxd-vm | `lxc-images` | KDE Plasma | 1.0 GiB | rolling |

```sh
omloader pull lxc:ubuntu/noble/desktop --vm --name ubuntu-desktop --out /mnt/images/images
omloader pull lxc:opensuse/tumbleweed/desktop-kde --vm --name tumbleweed-kde --out /mnt/images/images
omloader images list --os ubuntu --variant desktop        # browse; or TUI: tab 2, search "desktop"
```

## 2. Server userland + install a desktop yourself

| Distro | Type | Source | Desktop in image? | How to get a desktop |
|---|---|---|---|---|
| Ubuntu (`noble`, `jammy`, ...) | container + vm | `lxc-images` (`default`, `cloud`) | no | `apt install ubuntu-desktop-minimal` (GNOME), `kubuntu-desktop`, `xubuntu-desktop`, `ubuntu-mate-desktop`, `ubuntu-budgie-desktop`, `cinnamon-desktop-environment` |
| Ubuntu releases / daily | container + vm | `ubuntu-releases`, `ubuntu-daily` | no, server images only (`default` variant; checked) | as above |
| Debian (`bookworm`, `trixie`, `forky`) | container + vm | `lxc-images` | no | `apt install task-gnome-desktop` / `task-kde-desktop` / `task-xfce-desktop` |
| Fedora (43-45) | container + vm | `lxc-images` | no | `dnf group install workstation-product-environment` (GNOME), `kde-desktop-environment`, `xfce-desktop-environment` |
| Arch Linux | container + vm | `lxc-images` | no | `pacman -S gnome` / `plasma` / `xfce4` + a display manager |
| openSUSE (16.x, Tumbleweed) | container + vm | `lxc-images` | only Tumbleweed `desktop-kde` | `zypper install -t pattern gnome` / `kde` / `xfce` |
| Kali | container | `lxc-images` | no | `apt install kali-desktop-xfce` (unverified) |
| Linux Mint (`vanessa` ... `zena`, 8 releases) | **container only** | `lxc-images` (`default`, `cloud`) | no | see below |

Sizes of the server images: roughly 45-170 MiB as containers, 70-750 MiB as VMs (see `images list`).

**Ubuntu flavours** (Kubuntu, Xubuntu, MATE, Budgie, Cinnamon, ...) have no ready image: take `ubuntu/noble/desktop` or `default`
and `apt install` the flavour's `*-desktop` package.

**Linux Mint** exists only as `container` images, ~141-152 MiB (wilma/xia/zara/zena) or ~255-265 MiB (vanessa...virginia), with
no desktop preinstalled. Options: install `mint-meta-cinnamon` plus a display manager (`lightdm`) or `xrdp` in the container;
or build a VM yourself with distrobuilder from lxc-ci's `images/mint.yaml` adding the meta package (unverified).

## 3. Not available as an image anywhere we checked

Deepin, Manjaro, EndeavourOS, Pop!_OS, Zorin, elementary, KDE neon, openKylin, UOS: no LXD/Incus image in `lxc-images`
(`images list --os deepin` and `--os manjaro` return nothing).

- **Deepin:** only ISOs (`mirrors.tuna.tsinghua.edu.cn/deepin-cd`). Docker Hub `linuxdeepin/apricot` is old (20.8) and unverified; there is no lxc-ci recipe.
  Options: build with distrobuilder (a `deepin.yaml` modelled on `debian.yaml`; the repository, e.g. `community-packages.deepin.com/beige/`, is **unverified**),
  or install the ISO into a qcow2 in QEMU and `omloader import lxd-vm disk.qcow2 --name deepin --out ...`.
- The same QEMU-install-then-import route works for any distro that only ships an ISO.

## 4. OCI desktop images (experimental, weak fit)

`linuxserver/webtop`, `kasmweb/*`, `dorowu/ubuntu-desktop-lxde-vnc` run X/VNC inside an application container, usually
without a full systemd. omloader's OCI support is EXPERIMENTAL (on by default, can be switched off); these are unverified here and not recommended
for a real desktop. Use an `lxd-vm` desktop image instead.

## How to consume

| Route | Command |
|---|---|
| Pull | `omloader pull <spec> --vm --name N --out IMAGES_ROOT` (or TUI tab 2) |
| Import your own disk/rootfs | `omloader import lxd-vm disk.qcow2 --name N --out IMAGES_ROOT`, `omloader import lxd DIR ...` |
| Own simplestreams server | add it under `sources:` in `sources.yaml` ([sources.md](sources.md)), then `pull --source S` |
| Build | distrobuilder (lxc-ci recipes) producing an LXD image, then import |

After installing a desktop by hand, take a snapshot (TUI `s`, or `omloader snapshot create`) so it can be reverted.
