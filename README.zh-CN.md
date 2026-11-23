# OCI Metal Loader (`omloader`)

**从 U 盘直接在裸机上启动 LXD 镜像，并且自带快照。**

[English](README.md) | 简体中文

`omloader.iso` 是一个 UEFI 加载器，可以放在 [Ventoy](https://github.com/ventoy/Ventoy) 盘里（也能单独启动）。把 **LXD VM（整盘）镜像和容器镜像**放进盘里，选一个，
它就直接在真实硬件上运行：**不用安装，不用虚拟化**，支持可分支、可回滚的快照，数据重启后不丢。

> **Alpha 阶段。** 目前所有测试都在 QEMU/OVMF 里完成，还没有在真机上验证。见[状态](#状态)。

## 有什么不同

- **真正的裸机，没有任何虚拟化。** 不涉及 QEMU/KVM/Hyper-V。"LXD VM 镜像"只是**镜像格式**的名字（带自己内核的整盘镜像，LXD 给虚拟机用的那种）：加载器准备好之后，用 `kexec` 把硬件交给镜像自带的内核，所以它是原生运行的。容器镜像（只有根文件系统）则作为完整系统在加载器的内核上启动。直接使用原生驱动、显卡和磁盘。
- **快照是内置的。** 每次启动都可以开一个新分支。支持创建、回滚、打标签、清理、压平和导出。基础镜像只读并带校验和。
  虚拟机用 `dm-snapshot`，容器用 `overlayfs`。
- **数据不丢。** 快照和数据卷都放在持久的存储区；容器的数据卷独立于系统层，回滚系统不会影响你的文件。
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

1. **获取 ISO。** 目前没有预编译的发布版，需要从源码构建（rootless podman），见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)。
2. **放进 Ventoy 盘**，在 Ventoy 菜单里启动它（[docs/ventoy.md](docs/ventoy.md)）。
3. **获取镜像**（在加载器里执行，或者把镜像复制到数据分区的 `images/` 目录）：
   ```sh
   omloader images list --os ubuntu --type vm
   omloader pull lxc:ubuntu/noble --vm --name ubuntu-noble --out /mnt/images/images
   ```
4. **启动一个镜像**，并开一个新的快照分支：
   ```sh
   omloader boot --image ubuntu-noble --snapshot new:try1
   ```
5. **管理快照：**
   ```sh
   omloader snapshot list ubuntu-noble --tree
   omloader snapshot create ubuntu-noble before-upgrade --note "clean install"
   omloader snapshot rollback ubuntu-noble before-upgrade
   ```
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
| `vm` | LXD **VM 镜像**：整盘镜像（导入时把 `disk.qcow2` 转成 `base.raw`）。"VM"只是格式的名字，不涉及虚拟化。 | 镜像自带（`kexec`） | `dm-snapshot` 的 COW 文件 | 需要 UEFI + GPT，根文件系统为 ext4 或 xfs，`/boot` 里要有内核和 initrd；不支持 LVM/LUKS 根；客户机的 initramfs 必须能被加载器扩展（目前只在 Debian/initramfs-tools 的客户机上验证过）；没有独立数据卷；没有账号/控制台配置和干净卸载（由客户机自己管理）；Secure Boot 会阻止 `kexec` |
| `lxd-container` | LXD **容器镜像**：`rootfs.squashfs`（加 `metadata.yaml`） | 加载器的内核（Debian 6.1），模块以只读绑定方式提供给客户机 | `overlayfs` 的可写层 | 需要带 init 的 Linux 根；硬件支持取决于加载器内核；首次启动时配置控制台登录和用户（仅 systemd 客户机，其他的只有 busybox 调试 shell）；数据卷支持 `dir` / `ext4-file` |
| `oci` | Docker/OCI 镜像——**实验性，默认关闭**（`experimental.oci_images: true`） | 加载器的内核 | `overlayfs` | 应用镜像通常没有 init 和登录；不支持从 registry 拉取 |

所有类型的共同点：基础镜像只读并带校验和；启动已有子节点的快照会被拒绝（请用 `new:<名字>`）；只支持 Linux 客户机（Windows 请用 Ventoy）；只支持 x86_64 UEFI。
详情见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)。

## 对比

| | **omloader** | Ventoy | Kairos / bootc | LinuxKit |
|---|---|---|---|---|
| 无需安装，直接从 U 盘启动 | ✅ | ✅ | ❌（要安装到磁盘） | ✅（在内存里运行） |
| LXD 虚拟机 / 容器镜像 | ✅ | ❌ | ❌ | ❌ |
| 用镜像自带的内核原生运行磁盘镜像（无虚拟化层） | ✅（LXD VM 镜像） | ✅（ISO/vDisk） | ✅ | ✅ |
| 快照与回滚 | ✅ | ❌ | ✅（A/B） | ❌ |
| 持久数据卷 | ✅ | ✅（`.dat`） | ✅ | ❌ |
| 启动 Windows ISO / VHD(x) | ➖ 用 Ventoy | ✅ | ❌ | ❌ |
| 带 mirror/代理的在线拉取与离线模式 | ✅ | ❌ | ✅ | ➖ |

omloader 是对 Ventoy 的补充，不是替代。

## 状态

Alpha。已经可用的：LXD 虚拟机和容器启动、快照、数据卷、在线拉取、存储文件、经 Ventoy 启动、终端界面——都由 QEMU/OVMF 里的端到端测试验证过。
缺少或未验证的：真机、Secure Boot、LUKS/LVM 客户机、Windows（请用 Ventoy）、公共服务器的实测（拉取目前只对本地模拟的 simplestreams 服务器测过）、非 Debian 系的虚拟机客户机。
Docker/OCI 镜像是**实验性功能，默认关闭**（它们是应用镜像，不是完整系统）。更详细的说明和全部已知限制见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)。

## 文档

- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) — 架构、状态、路线图、限制、测试
- [docs/ventoy.md](docs/ventoy.md) · [docs/store-file.md](docs/store-file.md) · [docs/tui.md](docs/tui.md) · [build/README.md](build/README.md)

## 许可

[GPL-3.0](LICENSE)，与 Ventoy 相同。本项目不是 Open Container Initiative 的官方项目，与 OCI、Canonical 或 LXD 项目无关，也未获得它们的认可。
