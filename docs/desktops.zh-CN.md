# 桌面环境：去哪里获取

([English](desktops.md)) 下列目录数据读取于 **2026-11-23**，来自 `omloader images list` 对 `lxc-images`、`ubuntu-releases`、
`ubuntu-daily` 三个源的查询（目录每天都在变，请用 `omloader images list --os X --variant desktop` 复核）。
镜像站 `tuna` / `nju` / `cernet` 提供同一份 `lxc-images` 目录（见 [sources.md](sources.md)）。

**状态用语：**「已验证」= 在该日期的目录中确实看到；「未验证」= 来自调研，未在此处核实。
**尚未测试：** 在真实硬件上用 omloader 启动桌面 VM（Ubuntu 桌面 VM 的启动测试仍待做）。
**不是 omloader 的功能（暂时）：** 首次启动时自动安装桌面。桌面要么已在镜像里（表 1），要么由你在客户机内手动安装
（`apt` / `dnf` / `pacman` / `zypper`），然后做快照。

## 1. 现成的桌面镜像（已验证）

| 镜像（pull 规格） | 类型 | 源 | 桌面 | 大小 | 备注 |
|---|---|---|---|---|---|
| `lxc:ubuntu/jammy/desktop` | lxd-vm | `lxc-images` | GNOME | 1.0 GiB | 22.04 |
| `lxc:ubuntu/noble/desktop` | lxd-vm | `lxc-images` | GNOME | 1.1 GiB | 24.04 LTS |
| `lxc:ubuntu/resolute/desktop` | lxd-vm | `lxc-images` | GNOME | 1.3 GiB | 较新版本 |
| `lxc:ubuntu/stonking/desktop` | lxd-vm | `lxc-images` | GNOME | 1.4 GiB | 开发版 |
| `lxc:opensuse/tumbleweed/desktop-kde` | lxd-vm | `lxc-images` | KDE Plasma | 1.0 GiB | 滚动更新 |

```sh
omloader pull lxc:ubuntu/noble/desktop --vm --name ubuntu-desktop --out /mnt/images/images
omloader pull lxc:opensuse/tumbleweed/desktop-kde --vm --name tumbleweed-kde --out /mnt/images/images
omloader images list --os ubuntu --variant desktop        # 浏览；或 TUI 第 2 页搜索 "desktop"
```

## 2. 只有服务器用户空间，需自行安装桌面

| 发行版 | 类型 | 源 | 镜像内有桌面？ | 如何获得桌面 |
|---|---|---|---|---|
| Ubuntu（`noble`、`jammy` 等） | container + vm | `lxc-images`（`default`、`cloud`） | 无 | `apt install ubuntu-desktop-minimal`（GNOME）、`kubuntu-desktop`、`xubuntu-desktop`、`ubuntu-mate-desktop`、`ubuntu-budgie-desktop`、`cinnamon-desktop-environment` |
| Ubuntu releases / daily | container + vm | `ubuntu-releases`、`ubuntu-daily` | 无，仅服务器镜像（只有 `default` 变体；已核对） | 同上 |
| Debian（`bookworm`、`trixie`、`forky`） | container + vm | `lxc-images` | 无 | `apt install task-gnome-desktop` / `task-kde-desktop` / `task-xfce-desktop` |
| Fedora（43-45） | container + vm | `lxc-images` | 无 | `dnf group install workstation-product-environment`（GNOME）、`kde-desktop-environment`、`xfce-desktop-environment` |
| Arch Linux | container + vm | `lxc-images` | 无 | `pacman -S gnome` / `plasma` / `xfce4` + 显示管理器 |
| openSUSE（16.x、Tumbleweed） | container + vm | `lxc-images` | 仅 Tumbleweed `desktop-kde` 有 | `zypper install -t pattern gnome` / `kde` / `xfce` |
| Kali | container | `lxc-images` | 无 | `apt install kali-desktop-xfce`（未验证） |
| Linux Mint（`vanessa` … `zena`，共 8 个版本） | **仅 container** | `lxc-images`（`default`、`cloud`） | 无 | 见下 |

服务器镜像大小：容器约 45-170 MiB，VM 约 70-750 MiB（以 `images list` 为准）。

**Ubuntu 衍生版**（Kubuntu、Xubuntu、MATE、Budgie、Cinnamon 等）没有现成镜像：用 `ubuntu/noble/desktop` 或 `default`，
再 `apt install` 对应的 `*-desktop` 包。

**Linux Mint** 只有 `container` 镜像，约 141-152 MiB（wilma/xia/zara/zena）或约 255-265 MiB（vanessa…virginia），
未预装桌面。做法：在容器里安装 `mint-meta-cinnamon` 加显示管理器（`lightdm`）或 `xrdp`；
或用 distrobuilder 基于 lxc-ci 的 `images/mint.yaml` 加上 meta 包自行构建 VM（未验证）。

## 3. 我们检查过的所有位置都没有现成镜像

Deepin、Manjaro、EndeavourOS、Pop!_OS、Zorin、elementary、KDE neon、openKylin、统信 UOS：`lxc-images` 中没有 LXD/Incus 镜像
（`images list --os deepin`、`--os manjaro` 结果为空）。

- **Deepin：** 只有 ISO（`mirrors.tuna.tsinghua.edu.cn/deepin-cd`）。Docker Hub 的 `linuxdeepin/apricot` 版本很旧（20.8）且未验证；lxc-ci 没有对应配方。
  做法：用 distrobuilder 构建（参照 `debian.yaml` 写 `deepin.yaml`；软件源如 `community-packages.deepin.com/beige/` **未验证**），
  或在 QEMU 中把 ISO 装进 qcow2，再 `omloader import lxd-vm disk.qcow2 --name deepin --out ...`。
- 只提供 ISO 的发行版都可以走「QEMU 安装再导入」这条路。

## 4. OCI 桌面镜像（实验性，不太合适）

`linuxserver/webtop`、`kasmweb/*`、`dorowu/ubuntu-desktop-lxde-vnc` 是在应用容器里跑 X/VNC，通常没有完整的 systemd。
omloader 的 OCI 支持是实验性的且默认关闭；这些镜像未在此处验证，不建议用作真正的桌面，请改用 `lxd-vm` 桌面镜像。

## 如何使用

| 途径 | 命令 |
|---|---|
| 拉取 | `omloader pull <规格> --vm --name N --out IMAGES_ROOT`（或 TUI 第 2 页） |
| 导入自己的磁盘/rootfs | `omloader import lxd-vm disk.qcow2 --name N --out IMAGES_ROOT`、`omloader import lxd DIR ...` |
| 自建 simplestreams 服务器 | 在 `sources.yaml` 的 `sources:` 下添加（[sources.md](sources.md)），然后 `pull --source S` |
| 自行构建 | 用 distrobuilder（lxc-ci 配方）生成 LXD 镜像，再导入 |

手动装好桌面后请做一个快照（TUI 里按 `s`，或 `omloader snapshot create`），以便回退。
