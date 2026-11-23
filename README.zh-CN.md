# OCI Metal Loader (`omloader`)

[![CI](https://github.com/BBDXF/oci-metal-loader/actions/workflows/ci.yml/badge.svg)](https://github.com/BBDXF/oci-metal-loader/actions/workflows/ci.yml) [![Release](https://img.shields.io/github/v/release/BBDXF/oci-metal-loader?include_prereleases)](https://github.com/BBDXF/oci-metal-loader/releases) [![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)

**从 U 盘直接在裸机上启动 LXD 镜像，并且自带快照。**

[English](README.md) | 简体中文

> 📖 **完整安装与配置指南：[docs/INSTALL.zh-CN.md](docs/INSTALL.zh-CN.md)**（[English](docs/INSTALL.md)）——全部配置项、目录布局、命令行、FAQ。

## 下载

从 [Releases 页面](https://github.com/BBDXF/oci-metal-loader/releases)下载 `omloader-<版本>.iso`（以及 `SHA256SUMS`、静态二进制和 `omloader-<版本>-config.zip`），用 `sha256sum -c --ignore-missing SHA256SUMS` 校验，把 ISO 拷到 Ventoy 盘（UEFI，关闭 Secure Boot），并把配置包解压到其数据分区根目录（已注释的 `boot.yaml`、`sources.yaml`、示例、`README-FIRST.txt`）。也可自行构建：`make iso`（[build/README.md](build/README.md)）。

`omloader.iso` 是一个 UEFI 加载器，可以放在 [Ventoy](https://github.com/ventoy/Ventoy) 盘里（也能单独启动）。把 **LXD VM（整盘）镜像和容器镜像**放进盘里，选一个，
它就直接在真实硬件上运行：**不用安装，不用虚拟化**，支持类似虚拟机的快照，数据重启后不丢。

> **Alpha 阶段。** 目前所有测试都在 QEMU/OVMF 里完成，还没有在真机上验证。见[状态](#状态)。

## 有什么不同

- **真正的裸机，没有任何虚拟化。** 不涉及 QEMU/KVM/Hyper-V。"LXD VM 镜像"只是**镜像格式**的名字（带自己内核的整盘镜像，LXD 给虚拟机用的那种）：加载器准备好之后，用 `kexec` 把硬件交给镜像自带的内核，所以它是原生运行的。容器镜像（只有根文件系统）则作为完整系统在加载器的内核上启动。直接使用原生驱动、显卡和磁盘。
- **快照是内置的。** 类似虚拟机快照：创建、回退、删除、打标签、清理、压平和导出。基础镜像只读并带校验和。
  虚拟机用 `dm-snapshot`，容器用 `overlayfs`。
- **数据不丢。** 快照和数据卷都放在持久的存储区；容器的数据卷独立于系统层，回退快照不会影响你的文件。
- **对 Windows 友好。** 镜像放在 exFAT 上。存储区可以是 exFAT/NTFS 分区里的一个 *ext4 文件*（沿用 Ventoy 的 `.dat` 约定），不需要额外划分 Linux 分区。
- **和 Ventoy 配合。** 它只是 Ventoy 菜单里的一个条目；Windows/Linux 的 ISO 和 VHD(x) 仍由 Ventoy 原生启动。
- **在线和离线都行。** 可从 `images.linuxcontainers.org` 这类（simplestreams）源拉取，支持 mirror、代理和按内容寻址的缓存，并校验 SHA-256；离线模式不会访问网络。
- **易于使用。** 单个静态 Go 二进制、纯 YAML 配置、键盘操作的终端界面和可脚本化的命令行。

## 工作原理

```
Ventoy / UEFI → GRUB → 加载器（Debian live：systemd、networkd、工具集）
                          ├─ LXD 虚拟机镜像 → 在 base.raw 上叠 dm-snapshot → kexec → 客户机自己的内核
                          └─ LXD 容器镜像   → squashfs + overlayfs 可写层   → switch-root → 容器自己的 init
快照 / 数据 / 缓存 → ext4 存储区（一个分区，或 exFAT/NTFS 上的一个 ext4 文件）
```

## 快速开始

1. **获取 ISO。** 从 [Releases](https://github.com/BBDXF/oci-metal-loader/releases) 下载（见*下载*），或用 `make iso` 构建。分步说明：[docs/INSTALL.zh-CN.md](docs/INSTALL.zh-CN.md)。
2. **放进 Ventoy 盘**，在 Ventoy 菜单里启动它（[docs/ventoy.md](docs/ventoy.md)）。
3. **获取镜像**（在加载器里执行，或者把镜像复制到数据分区的 `images/` 目录）：
   ```sh
   omloader images list --os ubuntu --type vm
   omloader pull lxc:ubuntu/noble --vm --name ubuntu-noble --out /mnt/images/images
   ```
4. **启动一个镜像**（继续你的当前状态 "current"）：
   ```sh
   omloader boot --image ubuntu-noble
   omloader boot --image ubuntu-noble --snapshot base     # 一次性启动原始镜像
   ```
5. **管理快照：**
   ```sh
   omloader snapshot list ubuntu-noble --tree
   omloader snapshot create ubuntu-noble before-upgrade --note "clean install"
   omloader snapshot revert ubuntu-noble before-upgrade     # 或：revert ubuntu-noble base [--keep experiment]
   omloader snapshot delete ubuntu-noble before-upgrade
   ```

**快照的用法与虚拟机快照一致。** `base` 是原始的只读镜像；`current` 是唯一可写的当前状态：每次启动都是*继续*它（不会每次启动新增一层）。
`snapshot create` 把当前状态冻结为带标签的快照，并在其上新建一个空层继续；`revert <标签|base>` 丢弃当前状态并从该点继续（`--keep 名字` 会先把当前状态保存为快照）；
`delete` 删除快照——位于两个快照之间的快照会合并进它的子节点（仅 overlay 镜像；dm-snapshot 的 VM 镜像会拒绝并建议用 `flatten`）。
`--snapshot <标签>` 或 `--ephemeral` 会在其上用一次性层启动（不保留任何改动，快照本身不会被修改）。
**快照只能在加载器里（命令行/TUI）、在镜像启动之前创建、回退和删除；运行中的系统无法做快照或回退——请先关机，回到加载器，再管理快照（不支持热快照）。**
注意命名：命令行参数 `--vm` 和 `images list --type vm` 沿用 LXD 的镜像类型写法；`image.yaml` 里的类型是 `lxd-vm`。
6. **自动启动**默认镜像并倒计时（Ventoy 无法传递内核参数），在数据分区放 `omloader/boot.yaml`：
   ```yaml
   boot:
     default_image: ubuntu-noble
     timeout: 5
   ```

更喜欢菜单？运行 `omloader tui`（[docs/tui.md](docs/tui.md)）。

## 支持的镜像类型

| `type` | 是什么 | 内核 | 快照 | 主要限制 |
|---|---|---|---|---|
| `lxd-vm` | LXD **VM 镜像**：整盘镜像（导入时把 `disk.qcow2` 转成 `base.raw`）。"VM"只是格式的名字，不涉及虚拟化。 | 镜像自带（`kexec`） | `dm-snapshot` 的 COW 文件 | 需要 UEFI + GPT，根文件系统为 ext4 或 xfs，`/boot` 里要有内核和 initrd；不支持 LVM/LUKS 根；客户机的 initramfs 必须能被加载器扩展（目前只在 Debian/initramfs-tools 的客户机上验证过）；没有独立数据卷；只提供默认账号（`oci`/`Oci123`、sudo、root、自动登录——公开密码，见下文），没有干净卸载 |
| `lxd-container` | LXD **容器镜像**：`rootfs.squashfs`（加 `metadata.yaml`） | 加载器的内核（Debian 6.1），模块以只读绑定方式提供给客户机 | `overlayfs` 的可写层 | 需要带 init 的 Linux 根；硬件支持取决于加载器内核；启动时配置默认控制台账号（自动登录支持 systemd 和 busybox-init 客户机，其他的只有 busybox 调试 shell）；数据卷支持 `dir` / `ext4-file` |
| `oci` | Docker/OCI 镜像——**实验性，默认关闭**（`experimental.oci_images: true`） | 加载器的内核 | `overlayfs` | 应用镜像通常没有 init 和登录；不支持从 registry 拉取 |

所有类型的共同点：基础镜像只读并带校验和；启动快照不会修改它（以一次性层启动）；只支持 Linux 客户机（Windows 请用 Ventoy）；只支持 x86_64 UEFI。
详情见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)。

## 对比

| | **omloader** | Ventoy | Kairos / bootc | LinuxKit |
|---|---|---|---|---|
| 无需安装，直接从 U 盘启动 | ✅ | ✅ | ❌（要安装到磁盘） | ✅（在内存里运行） |
| LXD 虚拟机 / 容器镜像 | ✅ | ❌ | ❌ | ❌ |
| 用镜像自带的内核原生运行磁盘镜像（无虚拟化层） | ✅（LXD VM 镜像） | ✅（ISO/vDisk） | ✅ | ✅ |
| 快照与回退 | ✅ | ❌ | ✅（A/B） | ❌ |
| 持久数据卷 | ✅ | ✅（`.dat`） | ✅ | ❌ |
| 启动 Windows ISO / VHD(x) | ➖ 用 Ventoy | ✅ | ❌ | ❌ |
| 带 mirror/代理的在线拉取与离线模式 | ✅ | ❌ | ✅ | ➖ |

omloader 是对 Ventoy 的补充，不是替代。

## 状态

Alpha。已经可用的：LXD 虚拟机和容器启动、快照、数据卷、在线拉取、存储文件、经 Ventoy 启动、终端界面——都由 QEMU/OVMF 里的端到端测试验证过。
缺少或未验证的：真机（测试推后）、LUKS/LVM 客户机、Windows（请用 Ventoy）、公共服务器的实测（拉取目前只对本地模拟的 simplestreams 服务器测过）、非 Debian 系的虚拟机客户机。
**默认登录（公开密码！）：** 所有镜像——LXD 容器、LXD 虚拟机和实验性 OCI——启动时都会得到账号 `oci` / `Oci123`（sudo 需密码）、同密码启用的 root，以及以 `oci` 自动登录控制台。该密码是公开的：请在数据分区的 `omloader/boot.yaml` 里用 `guest_defaults:` 修改或关闭，或在单个镜像的 `image.yaml` 里用 `runtime.users` / `root_password_hash` / `autologin` 覆盖（详见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md#default-accounts-guest_defaults-all-image-types)）。加载器只写入镜像自己的 COW 层。

**虚拟机客户机与 ESP：** 客户机的 `/boot/efi`（ESP）刻意不挂载——加载器会把该 fstab 条目标为 `nofail`（`boot.vm_fix_esp_fstab`，默认开启）；客户机由 `kexec` 启动，用不到 ESP，只是会往 ESP 写入的工具（如 `update-grub`）无处可写。

**主机名：** 所有客户机（容器、虚拟机、OCI）的主机名默认取镜像名（如 `vm-ubuntu-noble`，而不是 `distrobuilder-<uuid>`）；可用 `guest_defaults.hostname`（`image` | `keep` | 自定义名称）或 `image.yaml` 的 `runtime.hostname` 修改或关闭。

**限制：** **不支持 UEFI Secure Boot**，必须关闭。omloader 刻意与 Ventoy 绑定：把 `omloader.iso` 放到 Ventoy U 盘上即可（没有 `omloader install`）。
Docker/OCI 镜像是**实验性功能，默认关闭**（它们是应用镜像，不是完整系统）。更详细的说明和全部已知限制见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)。

## 文档

- [docs/INSTALL.zh-CN.md](docs/INSTALL.zh-CN.md)（[English](docs/INSTALL.md)）— 安装、配置参考、目录布局、命令行、FAQ
- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) — 架构、状态、路线图、限制、测试
- [docs/ventoy.md](docs/ventoy.md) · [docs/store-file.md](docs/store-file.md) · [docs/tui.md](docs/tui.md) · [build/README.md](build/README.md)
- 镜像源与 mirror（USTC/南大/清华/CERNET/官方）、Deepin/Mint 现状：[docs/sources.md](docs/sources.md) — `omloader source list|test`、`pull --source S --mirror M`。

## 许可

[GPL-3.0](LICENSE)，与 Ventoy 相同。本项目不是 Open Container Initiative 的官方项目，与 OCI、Canonical 或 LXD 项目无关，也未获得它们的认可。
