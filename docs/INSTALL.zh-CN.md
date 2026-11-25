# 安装、配置与使用 OCI Metal Loader

[English](INSTALL.md) | 简体中文 · [README](../README.zh-CN.md) · [开发文档（英文）](DEVELOPMENT.md)

> **Alpha 阶段。** 这里描述的内容均在 QEMU/OVMF 中验证过（包括 Ventoy 1.1.17 启动该 ISO）。**尚未在真机上测试**；涉及真机的说法都标注了 *(真机未测试)*。
> 截至目前，GitHub Actions 上的 ISO 构建也还没有完整跑通过。

目录：[1 需求](#1-需求) · [2 下载与校验](#2-下载与校验) · [3 把 ISO 放到 Ventoy 盘](#3-把-iso-放到-ventoy-盘) · [4 首次启动](#4-首次启动) · [5 获取镜像](#5-获取镜像) ·
[6 默认账号与安全](#6-默认账号与安全) · [7 终端界面 TUI](#7-终端界面-tui) · [8 快照流程](#8-快照流程) · [9 配置参考](#9-配置参考) · [10 文件与目录布局](#10-文件与目录布局) ·
[11 内核命令行参数](#11-内核命令行参数omloader) · [12 环境变量](#12-环境变量) · [13 命令行参考](#13-命令行参考) · [14 故障排查 / FAQ](#14-故障排查--faq) · [15 限制](#15-限制)

## 1. 需求

| | |
|---|---|
| 机器 | 支持 **UEFI** 的 x86_64 电脑。不支持传统 BIOS/CSM 启动。 |
| **Secure Boot（安全启动）** | **必须在固件里关闭。** ISO 里的 GRUB 和内核没有 Secure Boot 签名。 |
| U 盘/移动硬盘 | 能放下 ISO（约 295 MiB）加上你的镜像即可。LXD VM 镜像每个 1–10 GiB（raw），容器镜像 100–500 MiB；快照和数据还需要额外空间（建议从 32 GiB 起步）。 |
| [Ventoy](https://github.com/ventoy/Ventoy) | 先在盘上安装（测试版本 1.1.17）。omloader 刻意绑定 Ventoy，没有单独的安装器。 |
| 内存 | 加载器运行在内存里（squashfs 之上的 tmpfs overlay）。测试虚拟机用 2 GiB；建议 **4 GiB 或更多**，展平（flatten）大镜像时需要更多。*(真机未测试)* |
| 网络（可选） | 仅支持有线 DHCP（`en*`/`eth*`）。已安装 Wi-Fi 工具但没有配置。只有在线拉取镜像才需要网络，也可以直接把镜像拷到盘里。 |
| 客户系统 | 仅 Linux（LXD VM 与容器镜像）。Windows ISO/VHD(x) 请在同一个盘上直接用 Ventoy。 |

## 2. 下载与校验

从 [Releases 页面](https://github.com/BBDXF/oci-metal-loader/releases)下载：`omloader-<版本>.iso`（加载器）、`omloader-<版本>-linux-amd64.tar.gz`（静态二进制，可在其他 Linux 主机上做脚本/导入）、
`omloader-<版本>-config.zip`（配置包：已注释的 `boot.yaml`、`sources.yaml`、`omloader/images/` 占位、`ventoy.json.example`、示例、schema、`README-FIRST.txt`；解压到 Ventoy 数据分区根目录，见第 3 节）和 `SHA256SUMS`。开发版 ISO 可用手动工作流 *ISO (manual)* 按需构建（Actions → Run workflow → *Artifacts*），不会在每次推送时构建。

    sha256sum -c --ignore-missing SHA256SUMS         # Linux/macOS
    Get-FileHash .\omloader-0.1.0.iso -Algorithm SHA256   # Windows PowerShell，与 SHA256SUMS 比对

也可以自己构建：`make iso`（podman 或 docker，见 [build/README.md](../build/README.md)）。

## 3. 把 ISO 放到 Ventoy 盘

1. 用 Ventoy2Disk 在盘上安装 Ventoy，它会建立一个大的数据分区（默认 exFAT）和一个很小的 `VTOYEFI` 分区。
2. 把 `omloader-<版本>.iso` 拷到数据分区，例如 `/omloader/omloader-<版本>.iso`（加载器的一切都放在唯一的文件夹 `omloader/` 里）。路径随意，加载器会扫描查找（`/`、`/omloader/`、`/ISO/`，以及两层目录内卷标为 `OMLOADER_LIVE` 的 ISO）。详见 [ventoy.md](ventoy.md)。
3. 把 `omloader-<版本>-config.zip` 解压到同一分区的**根目录**（直接是文件，没有版本号目录）。会生成 `omloader/boot.yaml`（全部已注释）、`omloader/sources.yaml`、`omloader/images/README.txt`、`ventoy/ventoy.json.example`、`schema/`、`examples/` 和 `README-FIRST.txt`。包里不含 `ventoy.json`：请手工把 `.example` 合并进你自己的文件（菜单别名/超时，见 [ventoy.md](ventoy.md)）。
4. 从盘启动电脑（UEFI），选择该 ISO。Ventoy 会提供 *normal* 与 *GRUB2* 两种模式，都可用（不支持 memdisk 模式）。

**镜像、快照和配置放在哪？** 三种方式（加载器会自动识别，见[第 10 节](#10-文件与目录布局)）：

| 方式 | 做法 | 说明 |
|---|---|---|
| A. **存储文件**（最简单） | 保留 Ventoy 的单个 exFAT/NTFS 分区（卷标改为 `VTOYDATA`），镜像放进 `omloader/images/`。在加载器里创建一次存储：`omloader store create --path /mnt/images/omloader/store.img --size 20G`（或在 TUI 的 Storage 页）。 | 不需要 Linux 分区。exFAT 不支持稀疏文件：创建时会占满整个大小。见 [store-file.md](store-file.md)。 |
| B. **`OMLOADER` 分区**（最稳） | 增加一个卷标为 `OMLOADER` 的 ext4（也接受 xfs/btrfs）分区（如 `mkfs.ext4 -L OMLOADER /dev/sdX3`；可在 Ventoy2Disk 的 *选项 → 分区设置 → 保留空间* 里预留），Ventoy 分区保持 `VTOYDATA`。 | 少一层文件系统；真正的 `OMLOADER` 分区总是优先于存储文件。 |
| C. **都不要** | 直接启动。 | 快照/缓存只在内存里，关机即丢（日志里会显示 `SNAPOK=0`）。适合试用。 |

**唯一的文件夹：** 一切都在数据分区根目录的 `omloader/` 里：`omloader/omloader-<版本>.iso`、`boot.yaml`、`sources.yaml`、`images/`、`store.img`。只有 `ventoy/ventoy.json` 在外面（它是 Ventoy 自己的文件）。
**哪个分区是数据分区？** 1. 内核参数 `omloader.data=LABEL=x|UUID=x|/dev/x` 指定的分区；2. 卷标为 `VTOYDATA` 的分区；3. 否则就是**存放 ISO 文件的那个分区**，不管卷标是什么（例如 `VHDX`），无需改名。
**NTFS** 处处可用，包括存放 ISO 的分区：第一阶段自带 ntfs-3g（FUSE），因为加载器所用的 Debian trixie 6.12 内核不使用 `ntfs3`（一律走 ntfs-3g）。若 Windows 让卷处于休眠（快速启动）或脏状态，则以**只读**挂载（镜像可用，其上的存储文件不可用）。

## 4. 首次启动

选择 ISO 后，GRUB 菜单停留 1 秒，随后基于 Debian 的加载器启动（通常一分钟内，USB 慢时更久），**TUI** 会出现在 tty1 和串口控制台。除非配置，否则不会自动启动任何镜像：

- **带倒计时的自动启动**（Ventoy 不能传内核参数）：在数据分区创建 `omloader/boot.yaml`：
  ```yaml
  boot:
    default_image: ubuntu-noble   # 留空/不写 = 显示 TUI；`last` = TUI 上次启动的镜像
    default_snapshot: current     # current | base | 快照标签（标签/base = 一次性启动）
    timeout: 10                   # 秒；按任意键取消倒计时并打开 TUI
  ```
  控制台（tty1 和串口）每秒显示 `Booting 'ubuntu-noble' (current) in N s -- press any key for the menu`。按键会打开 TUI（`boot.ui: shell` 时为 shell）；否则按预选的镜像+快照启动，效果与 `omloader boot` 相同。
- 在 TUI 里按 `S`（或 `q` 退出）进入 root shell（自动登录）。`poweroff` / `reboot` 照常使用，加载器会干净地卸载分区。
- 状态行 `OMLOADER_LOADER: snap=... images=...`（串口控制台 / `journalctl -u omloader-mounts`）说明找到了哪个存储与镜像分区。

### 无人值守启动（不按任何键直接启动指定镜像）

两个倒计时串联：**Ventoy** 选 ISO，**omloader** 选镜像。

1. **Ventoy 一侧** —— 数据分区上的 `ventoy/ventoy.json`（这是 Ventoy 自己的配置，不是 omloader 的；配置包只附带 `ventoy/ventoy.json.example`，请手工合并，不要覆盖已有的 `ventoy.json`）：
   ```json
   {
     "control": [ { "VTOY_DEFAULT_IMAGE": "/omloader-0.1.0.iso" }, { "VTOY_MENU_TIMEOUT": "5" } ],
     "menu_alias": [ { "image": "/omloader-0.1.0.iso", "alias": "OCI Metal Loader" } ]
   }
   ```
   路径必须与分区上**真实的 ISO 文件名和位置**一致（区分大小写，以 `/` 开头）。之后 Ventoy 在 5 秒内无按键就自动启动它。
2. **omloader 一侧** —— 同一分区上的 `omloader/boot.yaml`：
   ```yaml
   boot:
     default_image: ubuntu-noble   # 或 `last` = TUI 上次启动的镜像（需要 remember_last，默认开启）
     default_snapshot: current     # current | base | 快照标签（标签/base = 一次性启动）
     timeout: 10                   # 秒
   ```
   加载器起来后会打印 `Booting 'ubuntu-noble' (current) in N s -- press any key for the menu`，然后启动该镜像。

**取消：** 在 Ventoy 菜单倒计时内按任意键（如方向键）即可停止并手动选择；omloader 的倒计时现在显示在 TUI 内（底部横幅 `Booting … in N s`，首帧画出后才开始计时，且不短于 5 秒；不会等待查看者，VNC 连接得比这更晚就可能错过）；按**任意键**（键盘、VNC 或串口控制台）取消并留在 TUI；`boot.ui: shell` 时仍由 shell 在 tty1/串口打印倒计时。
**不经 Ventoy** 时（或在 GRUB 条目上按 `e` 编辑），可用内核参数：`omloader.image=ubuntu-noble omloader.snapshot=current` 立即启动、没有倒计时（优先于 `boot.yaml`）；更多参数见 `boot.extra_cmdline_flags`（[第 11 节](#11-内核命令行参数omloader)）。
**故障排查：** *Ventoy 显示菜单并一直等待* → `VTOY_DEFAULT_IMAGE` 与 ISO 路径/文件名不符（或 `ventoy.json` 不在第一分区的 `ventoy/` 下）。*Ventoy 询问启动模式* → 选 *normal*（或 *GRUB2*）一次；memdisk 模式对该 ISO 无效，见 [ventoy.md](ventoy.md)。*进入了 TUI 而不是镜像* → 找不到/无效的 `boot.yaml`（`omloader config validate -c boot.yaml`）、`default_image` 为空、`last` 尚无记录，或按过键。快照要持久保存需要存储（第 3 节）。*（仅在 QEMU 中验证，真机未测试。）*

### 加载器如何选择 ISO / 数据分区 / 配置 / 存储
| 项目 | 顺序（先匹配者生效） |
|---|---|
| ISO | `omloader.live=`/标签 `OMLOADER_LIVE`/光驱 > iso-scan：先查看起来像 Ventoy 的分区；每个分区内 `omloader.isofile=` > `VTOY_DEFAULT_SEARCH_ROOT` > `VTOY_DEFAULT_IMAGE` > `omloader/` 目录 > 标签为 `OMLOADER_LIVE` 的任意 `*.iso`（见 [ventoy.md](ventoy.md)） |
| 数据分区 | `omloader.data=` > 标签 `VTOYDATA` > 存放 ISO 的分区 |
| `boot.yaml` | 数据分区的 `omloader/boot.yaml` > `OMLOADER` 分区 > ISO 所在分区 |
| 存储 | **任意**磁盘上标签为 `OMLOADER` 的 ext4/xfs/btrfs 分区 > `omloader/` 下的存储文件（见 [store-file.md](store-file.md)）> 内存（不持久） |

警告：内置磁盘上的 `VTOYDATA` 或 `OMLOADER` 标签同样会被识别；不同分区上有多个 omloader ISO 会有歧义（按上述顺序取第一个，请查看 `OMLOADER_STAGE1: iso:` 日志行）。

## 5. 获取镜像

镜像位于 `omloader/images/<名称>/`（`image.yaml` + `lxd-vm` 的 `base.raw` 或 `lxd-container` 的 `base.squashfs`）。三种方式：

1. **在加载器里拉取**（需要网络）。在 TUI 的 *Pull* 页（`2`）选择，或用命令行：
   ```sh
   omloader images list --os ubuntu --type vm            # 浏览目录（--type container|vm）
   omloader pull lxc:ubuntu/noble --vm --name ubuntu-noble --out /mnt/images/omloader/images
   omloader pull lxc:debian/bookworm --container --name debian-bookworm --out /mnt/images/omloader/images
   ```
   源与镜像站（官方、USTC、南大、清华、CERNET……）：`omloader source list`、`omloader source test`、`--source S --mirror M|auto|official`、`mirror_policy.region_preset: cn`。
   全部说明见 [sources.md](sources.md)。下载会校验 sha256 并缓存（`/mnt/snap/cache`，`omloader cache ls|gc`）；`--offline` 绝不联网。
2. **导入已有文件**（LXD 导出：含 `rootfs.squashfs` 的目录或 `lxd.tar.xz`；VM 为 `disk.qcow2`）：
   ```sh
   omloader import lxd /path/to/lxd-container-dir --name mybox --out /mnt/images/omloader/images
   omloader import lxd-vm /path/to/disk.qcow2 --name myvm --out /mnt/images/omloader/images     # 转成 base.raw；--no-probe 跳过内核探测
   ```
   导入也可在任意 Linux 电脑上用发布包里的 `omloader` 完成（`--out` 指向已挂载盘的 `omloader/images/`）；VM 的内核探测需要 root，此时用 `--no-probe` 并自己在 `image.yaml` 里写 `kernel:`。
3. **直接拷贝现成的镜像目录**（`image.yaml` + 基础文件，见[9.3](#93-imageyaml单个镜像的配置)）到盘的 `omloader/images/`。

桌面环境（哪些源有现成的桌面 VM、如何给服务器镜像加桌面）：[desktops.zh-CN.md](desktops.zh-CN.md)。

**ISO 里有什么：** Debian stable 的 live 基础系统加 `omloader` 二进制。用 `omloader version --long` 查看具体版本（读取 `/etc/omloader-release`，TUI 的 `?` 帮助里也有）；不启动也能看：ISO 根目录的 `omloader-release` 和 `packages.txt`（Release 另附 `omloader-<ver>-packages.txt`）。

校验手写配置：`omloader config validate -c omloader.yaml -i omloader/images/NAME/image.yaml`；`omloader config show --resolved` 会显示每个值及其来源层/行号。

## 6. 默认账号与安全

每个镜像——LXD 容器、LXD VM 和实验性 OCI——启动时都会得到**公开的默认登录**：用户 **`oci`**，密码 **`Oci123`**（sudo 需密码），`root` 使用同一密码，并以 `oci` 在控制台（tty1 与串口）自动登录。
**任何能接触到客户系统控制台或网络服务的人都知道这个密码，请在联网前修改：**

```yaml
# VTOYDATA 上的 omloader/boot.yaml（对所有镜像生效）
guest_defaults:
  autologin: false
  root_password_hash: "!"        # 锁定 root；或 $6$... 哈希（优先于明文 root_password）
  users:
    - {name: alice, password_hash: "$6$...", sudo: true, ssh_authorized_keys: ["ssh-ed25519 AAAA... alice@pc"]}
```
单个镜像可在 `image.yaml` 里用 `runtime.users` / `runtime.root_password_hash` / `runtime.autologin` 覆盖 `guest_defaults`（合并规则见 [DEVELOPMENT.md](DEVELOPMENT.md#default-accounts-guest_defaults-all-image-types)）。
加载器只写入镜像自己的 COW 层，从不修改基础镜像。哈希可用 `openssl passwd -6` 生成。

## 7. 终端界面 TUI

默认启动 `omloader tui`。按键：`1`–`5` 切换标签页（Images、Pull、Storage、Network、Settings），方向键/`j`/`k`，Enter，Esc，`?` 或 `F1` 查看当前页帮助，`S` 进 shell，`P` 重启/关机，`q`/Esc 退出到 shell，`Ctrl+C` 取消正在运行的操作（不会退出 TUI）。
**一键启动**（没有子菜单；只有会丢数据时才确认一次）：

| 位置 | 按键 | 作用 |
|---|---|---|
| Images | `Enter` | 立即启动该镜像（继续 `current`）；上次启动的镜像会被预选 |
| Images | `o`/`→`、`e`、`Space`/`m` | 打开快照树、一次性（ephemeral）启动、操作菜单 |
| 快照树 | 在 `current` 上 `Enter` | 立即启动（在 live 状态所在的快照上也一样） |
| 快照树 | 在冻结快照或 `base` 上 `Enter` | 把 live 状态还原到它并启动。**若 live 状态有改动，弹出一个对话框提示改动将丢失**（`Enter`/`y` = 还原并启动，**`K` = 先把当前状态存为快照**——标签默认 `snap-YYYYMMDD-HHMM`，回车接受——`Esc`/`n` = 取消）。没有改动（从未启动/懒创建）则不弹窗 |
| 快照树 | `e`、`s`、`r`、`d`、`Space`/`m` | 选中状态的一次性启动、创建快照、只还原不启动（同一对话框）、删除（会确认）、操作菜单（终端支持时也可用 `Alt+Enter`/`Ctrl+Enter`） |

删除、prune/GC 和存储操作仍保留确认；启动与还原只有上面这一个对话框。

完整按键与界面说明、颜色/ASCII 降级、控制台字体规则见 [tui.md](tui.md)。界面目前只有英文。`boot.ui: shell` 或内核参数 `omloader.ui=shell` 可改为启动到 shell。

## 8. 快照流程

模型（类似虚拟机）：**`base`** 是原始只读镜像；**`current`** 是唯一的可写实时状态；带标签的**快照**是冻结点。启动 `current` 是*继续*它（每次启动不新增层）。

```sh
omloader snapshot list ubuntu-noble --tree
omloader snapshot create ubuntu-noble before-upgrade --note "clean install"   # 把 current 冻结成快照，并在新的空层上继续
omloader snapshot revert ubuntu-noble before-upgrade [--keep experiment]      # 丢弃 current（或用 --keep 先存为快照），从标签（或 "base"）继续
omloader snapshot delete ubuntu-noble before-upgrade                          # 容器（overlay）镜像会把它合并进子层；VM（dm-snapshot）镜像拒绝：请用 flatten
omloader boot --image ubuntu-noble                                            # 继续 current
omloader boot --image ubuntu-noble --snapshot before-upgrade                  # 临时：在其上加一层一次性层，不保留
omloader boot --image ubuntu-noble --ephemeral                                # 在 current 之上一次性启动
```
**快照只能在镜像启动之前，于加载器（命令行/TUI）里创建、回滚、删除。** 正在运行的系统无法打快照或回滚：先关机、回到加载器，再管理快照（不支持热快照）。
另有 `snapshot tag|untag|note|gc|prune|df|verify|export|flatten`（见[第 13 节](#13-命令行参考)）。声明为 `snapshot: false` 的数据卷在各层之外，`revert` 不会影响它。
VM 客户系统通过 `kexec` 启动，关机时无法卸载存储，所以下次进入加载器时存储 ext4 可能需要日志回放（自动完成）。

## 9. 配置参考

三类 YAML 文件，全部可选（每项都有内置默认值）：

| 文件 | 位置 | Schema |
|---|---|---|
| 全局配置 `omloader.yaml` / **`boot.yaml`**（格式与键相同；盘上通常叫 `boot.yaml`） | `<数据分区>/omloader/boot.yaml`、`<OMLOADER>/omloader/boot.yaml`、`<ISO 所在分区>/omloader/boot.yaml`（取第一个存在的）；命令行：`-c FILE` | [`schema/omloader.schema.json`](../schema/omloader.schema.json) |
| 内置默认值 `configs/defaults.yaml` | 嵌入二进制；见 [9.1](#91-全局配置omloaderyaml--bootyaml) | 同上 |
| `sources.yaml`（镜像源与 mirror 目录） | 内置 `configs/sources.yaml`、ISO 内 `/etc/omloader/sources.yaml`，然后是你放在 `<数据分区>/omloader/sources.yaml` 的副本…… | [`schema/sources.schema.json`](../schema/sources.schema.json)、[sources.md](sources.md) |
| 单镜像 `image.yaml` | `<images>/<名称>/image.yaml` | [`schema/image.schema.json`](../schema/image.schema.json) |

层次：内置默认值 < `omloader.yaml`/`boot.yaml` < `image.yaml`，按键深度合并。未知键会报错（附文件和行号）。大小可写 `512M`、`20G`、`1.5G` 或字节数。
标记**保留**的键会被接受和校验，但当前代码尚未使用。

### 9.1 全局配置（`omloader.yaml` / `boot.yaml`）

**`storage`**

| 键 | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `storage.images.label` | 字符串列表 | `[VTOYDATA, OMLOADER]` | **保留。** 加载器脚本自己按卷标 `VTOYDATA`（镜像）和 `OMLOADER`（存储）查找。 |
| `storage.images.path` | 字符串 | `/omloader/images` | 命令行 `--images-root` 的默认值。在加载器里会显式传 `/mnt/images/omloader/images`。 |
| `storage.snapshots.label` | 字符串 | `OMLOADER` | **保留**（卷标 `OMLOADER` 固定写在 `mounts.sh` 里）。 |
| `storage.snapshots.path` | 字符串 | `/snapshots` | 命令行 `--snap-root`/`--root` 的默认值。在加载器里为 `/mnt/snap/snapshots`。 |
| `storage.snapshots.fs_required` | 列表 | `[ext4, btrfs, xfs]` | **保留**（加载器固定接受这几种文件系统）。 |
| `storage.snapshots.fallback` | `tmpfs` \| `none` | `tmpfs` | **保留**（没有存储时加载器总是用内存）。 |
| `storage.snapshots.store_file.enabled` | bool | `true` | 在宿主分区上查找 ext4 存储文件（[store-file.md](store-file.md)）。 |
| `…store_file.path` | 字符串 | `""` | 宿主分区内的确切路径，优先检查。 |
| `…store_file.glob` | 列表 | `[/omloader/store.img, /omloader/*.dat, /omloader/*.img, /ventoy/*.dat]` | 宿主分区内的匹配模式，按此顺序，先匹配者胜；只有 ext4 卷标为 `OMLOADER` 的文件才算（`e2label` 或 `CreatePersistentImg.sh -l OMLOADER`）；Ventoy 的 `vtoycow` 文件会被忽略。 |
| `…store_file.auto_create` | 布尔 | `true` | 没有 OMLOADER 分区/文件且宿主可写时，启动时自动创建 `omloader/store.img`（只读、`omloader.store=off`、剩余空间不足 大小+1 GiB 时跳过；≥ 4G 时缩小到可用空间）。 |
| `…store_file.host_match.label` / `.uuid` | 字符串 | `""` / `""` | 可选：限制哪个数据分区可存放该文件（空 = 数据分区：VTOYDATA，否则是存放 ISO 的分区；uuid 优先于卷标；不匹配则不搜索存储文件）。 |
| `…store_file.size` | 大小 | `20G` | 自动创建的存储文件大小，也是 `omloader store create` 的默认大小（与下载缓存 `cache.max_size_gb` 无关）。 |
| `…store_file.prealloc` | `auto`\|`yes`\|`no` | `auto` | `auto` = exFAT/vfat 上完整分配（无稀疏文件），其他文件系统为稀疏。 |

**`snapshot`、`verify`**

| 键 | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `snapshot.max_chain_depth` | 整数 ≥ 1 | `5` | 一个镜像的最大层数（作为 `image.yaml snapshots.max_chain_depth` 的初值）；更深时启动/创建会被拒绝。 |
| `snapshot.persistent` | bool | `true` | **保留。** |
| `snapshot.retention.keep_last` | 整数 ≥ 1 | `10` | `snapshot prune` 保留最新的这么多个快照。 |
| `snapshot.retention.min_free_percent` | 整数 0–99 | `10` | 空间保护：存储剩余空间低于该百分比时拒绝创建/启动。 |
| `snapshot.retention.keep_tagged` | bool | `true` | `prune` 绝不删除带标签（tag）的快照。 |
| `verify.base_checksum` | `sha256`\|`skip` | `sha256` | 启动时按 `image.yaml sha256` 校验基础镜像；`skip` 关闭。 |

**`boot`**

| 键 | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `boot.default_image` | 字符串 | `""` | 内核命令行没有 `omloader.image=` 时，倒计时 `timeout` 秒后自动启动的镜像。空 = TUI/shell；`last` = TUI 上次启动的镜像。 |
| `boot.timeout` | 整数 ≥ 0 | `10` | 倒计时秒数，在 TUI 内以横幅显示（按任意键取消；TUI 的倒计时不会短于 5 秒）。 |
| `boot.remember_last` | 布尔 | `true` | TUI 把上次启动的镜像/快照记录到 `<数据分区>/omloader/last-boot`（3 行：镜像、快照、`ephemeral`），下次预选；`default_image: last` 会自动启动它。不可写则静默跳过。 |
| `boot.default_snapshot` | 字符串 | `current` | `current`、`base` 或快照标签（标签/base 为一次性层）。同时是 `omloader boot --snapshot` 的默认值。 |
| `boot.kernel_cmdline_extra` | 字符串 | `""` | 追加到 **VM** 客户系统内核命令行（在 `image.yaml kernel.cmdline` 之后）。 |
| `boot.extra_cmdline_flags` | 列表 | `[]` | 不带前缀的 `omloader.*` 参数，效果等同写在内核行，如 `[poweroff_on_fail=1, experimental=oci]`。 |
| `boot.vm_fix_esp_fstab` | bool | `true` | VM 客户系统：把其 `/boot/efi` 的 fstab 条目标为 `nofail`（ESP 不会被映射；避免 90 秒的紧急模式超时）。 |
| `boot.ui` | `tui`\|`shell` | `tui` | 没有自动启动时的控制台。内核参数 `omloader.ui=` 优先。 |

**`data`、`devices`** —— 全部**保留**（接受并校验，当前不起作用；数据卷是按镜像声明的，见 9.3）：
`data.default_backend`（`dir`；可选 `dir ext4-file partition disk network`）、`data.default_vm_backend`（`ext4-file`）、`data.default_location`（`/OMLOADER/data`）、`data.auto_home_volume`（`true`）、
`data.fallback_if_missing`（`system-layer`；`fail`、`tmpfs`）、`devices.policy`（`isolate`；`passthrough`、`hide`）、`devices.windows_ntfs`（`ro_or_hide`；`ro`、`hide`、`rw`）、`devices.hibernated_fs`（`refuse`；`ro`、`ignore`）。

**`network`、`mirror_policy`、`cache`、`policy`**

| 键 | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `network.timeout_sec` | 整数 ≥ 1 | `15` | HTTP 超时。 |
| `network.retries` | 整数 ≥ 0 | `3` | 每个请求的重试次数。 |
| `network.proxy` | URL | `""` | 所有下载使用的 `http://`、`https://` 或 `socks5://` 代理。 |
| `network.no_proxy` | 字符串 | `""` | 逗号分隔的不走代理的主机、域名后缀（`.lan`）或 CIDR。 |
| `network.extra_ca` | 路径 | `""` | 额外 CA 证书的 PEM 文件（如公司代理的 CA）。 |
| `network.dns` | 列表 | `[]` | **保留**（仅供参考；解析器是 systemd-resolved，用 DHCP 下发的 DNS）。 |
| `mirror_policy.strategy` | `fallback`\|`fastest`\|`mirror-only` | `fallback` | `fallback`：先官方后镜像站；`fastest`：探测并选最快；`mirror-only`：从不使用官方服务器。 |
| `mirror_policy.health_check` | bool | `true` | **保留。** |
| `mirror_policy.cache_ttl_hours` | 整数 ≥ 0 | `24` | 缓存的目录索引多久内视为新鲜（`cache.offline: auto`）。 |
| `mirror_policy.region_preset` | 字符串 | `global` | `cn`：同时把所有源里 `region: cn` 的服务器作为后备。 |
| `cache.path` | 路径 | `/OMLOADER/cache` | 下载缓存；该默认值在存储已挂载时表示加载器里的 `/mnt/snap/cache`。 |
| `cache.max_size_gb` | 整数 ≥ 0 | `20` | `omloader cache gc` 的默认上限。 |
| `cache.offline` | `auto`\|`on`\|`off` | `auto` | `on` = 绝不联网（只用缓存）；`off` = 总是检查上游；`auto` = 缓存新鲜就用，否则联网。 |
| `policy.allow_online` | bool | `true` | `false` = 全部离线模式。 |
| `policy.allowed_sources` | 列表 | `[]` | 仅允许这些源名称（空 = 全部）。 |

**`sources`、`default_source`** —— 与 `sources.yaml` 的键相同（见 [sources.md](sources.md)）；`default_source` 默认 `lxc-images`。每个源：`type`（`simplestreams`\|`oci-registry`\|`http`\|`local`）、
`url`、`mirrors`（纯 URL 列表）、`proxy`（`direct` = 不用代理）、`rewrite`（`[{from, to}]`）、`title`、`note`、`enabled`、`mirror`（`official`\|`auto`\|服务器 id）、`servers.<id>`（`title`、`url`、`path`、`region`、`note`、`enabled`、`rewrite`）。

**`experimental`**：`experimental.oci_images`（bool，默认 **`true`**）—— 实验性的 Docker/OCI 镜像导入、拉取与启动；设为 `false` 关闭（也可用 `OML_EXPERIMENTAL=off`、`omloader.experimental=off`；`oci` / `--experimental-oci` 强制开启）。

**`guest_defaults`**（公开的默认值，见[第 6 节](#6-默认账号与安全)）

| 键 | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `guest_defaults.hostname` | 字符串 | `image` | `image` = 镜像名转成 RFC 1123 标签（`vm-ubuntu-noble`），`keep` = 保留客户系统自己的，或写一个具体名字。`runtime.hostname` 优先。 |
| `guest_defaults.autologin` | bool | `true` | 控制台自动登录（tty1 与 ttyS0）。 |
| `guest_defaults.autologin_user` | 字符串 | `oci` | 自动登录账号（不在用户列表里时回退为 root）。 |
| `guest_defaults.root_password` | 字符串 | `Oci123` | root 明文密码（由加载器哈希）。不能与 `root_password_hash` 同时设置。`root_password_hash: '!'` 锁定 root。 |
| `guest_defaults.root_password_hash` | 字符串 | – | `$6$…` crypt 哈希、`!` 或 `*`。 |
| `guest_defaults.users[]` | 列表 | 一个用户 `oci` | 离线创建的账号：`name`、`password` 或 `password_hash`、`groups`、`shell`（绝对路径）、`sudo`（bool）、`ssh_authorized_keys`。默认：`{name: oci, password: Oci123, sudo: true, groups: [sudo]}`。 |

示例：[`examples/omloader.yaml`](../examples/omloader.yaml) 与随附的 [`configs/defaults.yaml`](../configs/defaults.yaml)。

### 9.2 `sources.yaml`

定义镜像从哪里来。内置：`lxc-images`（images.linuxcontainers.org 及清华、南大、USTC、CERNET 等镜像站）、`lxd-images`、`ubuntu-releases`、`ubuntu-daily`、`dockerhub`（实验性）。
把你自己的副本放在 `<数据分区>/omloader/sources.yaml`，它会合并到内置目录之上（后者优先；`enabled: false` 关闭某个源）。顶层只允许 `sources` 与 `default_source`。
完整格式、合并规则和已验证的镜像站列表见 [sources.md](sources.md)。

### 9.3 `image.yaml`（单个镜像的配置）

位于 `<images>/<名称>/image.yaml`；由 `pull`/`import` 写出，也可手工编辑。必填：`name`、`type`、`format`、`base`。

| 键 | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `name` | 字符串 | – | 镜像名（= 目录名；不含路径分隔符）。 |
| `type` | `lxd-vm` \| `lxd-container` \| `oci` | – | `lxd-vm`：整盘镜像，用自带内核经 `kexec` 启动（**不是**虚拟化；旧写法 `vm` 已被拒绝）。`lxd-container`：根文件系统跑在加载器内核上。`oci`：实验性（默认开启）。 |
| `format` | `raw` \| `squashfs` | – | `lxd-vm` 必须是 `raw`；容器/oci 必须是 `squashfs`。 |
| `base` | 路径 | – | 基础文件：相对镜像目录（`base.raw` / `base.squashfs`，pull/import 写出的形式）或加载器文件系统上的绝对路径。`boot` 不支持 `://` URL。 |
| `sha256` | 64 位十六进制 | – | 基础文件校验和，启动时校验（`verify.base_checksum`）。 |
| `kernel.path` | 客户系统内路径 | – | VM：客户根文件系统里的内核（如 `/boot/vmlinuz`）。VM 启动必填。 |
| `kernel.initrd` | 客户系统内路径 | – | VM：initrd（需要同时有 `kernel.path`）。 |
| `kernel.cmdline` | 字符串 | – | VM：内核命令行（`root=`/`ro`/`rw` 会被加载器替换；之后追加 `boot.kernel_cmdline_extra`）。 |
| `kernel.root_partition` | 整数 ≥ 0 | `0` = 自动 | VM：根分区编号（自动检测第一个 ext4/xfs 根）。 |
| `kernel.inject_modules` | 列表 | – | VM：要放进注入 initrd 的额外客户内核模块（如 `[dm_snapshot, overlay]`）。 |
| `snapshots.mode` | `dm-snapshot`\|`overlayfs` | 按类型 | `lxd-vm` = `dm-snapshot`，容器 = `overlayfs`（其他值被拒绝）。 |
| `snapshots.path` | 路径 | `<snap-root>/<名称>` | 快照树与各层的位置。 |
| `snapshots.max_chain_depth` | 整数 ≥ 1 | 取自 `snapshot.max_chain_depth` | 单镜像的上限。 |
| `runtime.hostname` | 字符串 | `guest_defaults.hostname` | 具体名字，或 `keep`。 |
| `runtime.network` | 字符串 | – | 容器：`dhcp` 安装 DHCP 客户端配置，`none` 不动网络。 |
| `runtime.env` | 字符串→字符串 映射 | – | 容器 init/入口的环境变量。 |
| `runtime.entrypoint` / `runtime.cmd` / `runtime.workdir` | 列表 / 列表 / 路径 | – | 要运行的命令（OCI 风格镜像）；LXD 容器使用自己的 init。 |
| `runtime.autologin` / `runtime.autologin_user` | bool / 字符串 | `guest_defaults` | 控制台自动登录；设置其中任一项则两者都取自镜像（用户为空 = root）。 |
| `runtime.users[]` | 列表 | `guest_defaults.users` | 账号（`name`、`password`\|`password_hash`、`groups`、`shell`、`sudo`、`ssh_authorized_keys`）。只要设置（哪怕 `[]`）就替换默认列表。 |
| `runtime.root_password` / `runtime.root_password_hash` | 字符串 | `guest_defaults` | 替换默认的 root 密码对。 |
| `runtime.shell` | `auto`\|`image`\|`busybox` | `auto` | 容器：客户系统没有 login/getty 或没有 init 时的控制台 shell：`auto` = 镜像自带 shell（有就用），否则把加载器的静态 busybox 绑定挂载进去（不会进入快照）；`image` = 从不用 busybox；`busybox` = 强制。`debug_shell: always\|never` 是旧写法。单次启动覆盖：内核参数 `omloader.shell=`、`omloader boot --shell`、TUI 菜单「Boot with the busybox shell」。shell 能看到整个客户系统（所有进程，无命名空间）；busybox 小工具只是 PATH 的后备，选项比 GNU 工具少。 |
| `runtime.shutdown_sync` | bool | `true` | 容器：安装关机钩子，关机时把加载器分区以只读重新挂载。 |
| `data[]` | 列表 | – | 持久数据卷（仅容器）：`name`、`mount`（客户系统内绝对路径）、`backend`、`path`、`size`、`snapshot`、`shared`。 |
| `data[].backend` | `dir`\|`ext4-file`\|… | – | **已实现：`dir`**（绑定挂载 `<data-root>/<镜像>/<名称>`，默认 data root 为 `/mnt/snap/data`）**和 `ext4-file`**（`<path>` 或 `<data-root>/<镜像>/<名称>.img`，按 `size` 创建，默认 1G）。`partition`、`disk`、`network` 被接受但会警告并跳过。 |
| `data[].snapshot` | bool | `false` | `true` 尚未实现（按持久处理并警告）。 |
| `data[].shared` | bool | `false` | 接受；**保留**。 |

完整示例：[`examples/ubuntu-24.04/image.yaml`](../examples/ubuntu-24.04/image.yaml)。`omloader import … --data name:/mount:backend[:size[:snapshot]]` 可在导入时声明数据卷。

## 10. 文件与目录布局

**代码仓库**（给开发者；详见 [DEVELOPMENT.md](DEVELOPMENT.md#repository-layout)）：

```
oci-metal-loader/
├── cmd/omloader/  internal/        Go 源码（命令行、配置、快照、启动、拉取、TUI）
├── configs/        defaults.yaml, sources.yaml   （嵌入二进制）
├── schema/         omloader / image / sources 的 JSON schema
├── examples/       omloader.yaml, ubuntu-24.04/image.yaml
├── build/          Containerfile, build-iso.sh, mk-iso-inner.sh, stage1-init, grub.cfg, live/rootfs/（unit 与脚本）
├── test/           QEMU 端到端测试与夹具
├── docs/           本指南, ventoy.md, store-file.md, sources.md, tui.md, DEVELOPMENT.md
└── .github/workflows/   ci.yml（快速检查）, iso.yml（手动构建 ISO）, release.yml（打 tag 发布）
```

**Ventoy 盘上**（由你创建）：

```
数据分区（exFAT/NTFS/…；优先卷标 VTOYDATA，否则是存放 ISO 的分区） ── 在加载器里挂载到 /mnt/images
├── ventoy/ventoy.json            可选的 Ventoy 菜单配置（Ventoy 自己的文件；可从配置包里的 ventoy.json.example 合并）
└── omloader/                     加载器的一切
    ├── omloader-0.1.0.iso        加载器（名称/路径随意，靠扫描找到）
    ├── images/                   镜像根目录（images_root）
    │   ├── ubuntu-noble/         每个镜像一个目录
    │   │   ├── image.yaml
    │   │   └── base.raw          （lxd-vm）  |  base.squashfs（lxd-container）
    │   └── debian-bookworm/ …
    ├── boot.yaml                 全局配置（自动启动、guest_defaults、网络……）
    ├── sources.yaml              你的源/镜像站（合并到内置目录之上）
    ├── console.conf              可选：font=NAME，指定 Linux 控制台字体
    └── store.img                 没有 OMLOADER 分区时的 ext4 存储文件（卷标 OMLOADER，方式 A）

OMLOADER 分区（ext4/xfs/btrfs，可选） ── 挂载到 /mnt/snap
```

**存储**（`/mnt/snap`，即 `OMLOADER` 分区或存储文件）：

```
/mnt/snap/
├── snapshots/<镜像>/    快照根目录（snap-root）：tree.json、<id>.cow（VM 的 dm-snapshot 层）或 <id>/{upper,work}（容器 overlay 层）、in-use.json
├── data/<镜像>/<卷>      持久数据卷（dir），或 <卷>.img（ext4-file）
├── cache/               下载缓存（blob、目录索引、tmp/*.part）
├── omloader/            可选的配置副本（boot.yaml、sources.yaml、console.conf）——这里也会被读取
└── omloader/images/     没有数据分区时的镜像目录（仅存储分区时）

（数据分区）omloader/firmware/  可选的额外固件，路径同 /lib/firmware（见 FAQ）
```

**live ISO 内部**（只读 squashfs 层 + 内存 overlay；只有上面两个分区里的内容会持久保存）：

```
/usr/local/bin/omloader                静态二进制
/etc/omloader/sources.yaml             内置目录副本（优先级最低）
/usr/local/lib/omloader/*.sh           mounts.sh, autoboot.sh, console-font.sh, tui-launch.sh, switch-root.sh
/etc/systemd/system/omloader-*.service mounts, console, autoboot
/run/omloader/{env,ui,uicfg,medium.env}  运行时状态：IMAGES_ROOT、SNAPDEV、SNAPOK、STOREFILE、选中的配置文件、ISO 宿主挂载点
/mnt/images  /mnt/snap                 VTOYDATA 与存储的挂载点
ISO：/boot/{vmlinuz,initrd.img,grub/grub.cfg,efiboot.img}  /live/{live.squashfs,omloader.squashfs}   卷标 OMLOADER_LIVE
```

加载器启动决策读取配置的顺序（第一个存在且有效的文件）：`/mnt/images/omloader/boot.yaml`、`/mnt/snap/omloader/boot.yaml`、`<ISO 所在分区>/omloader/boot.yaml`。
缓存位置：`/mnt/snap/cache`（没有存储时只在内存里）。`sources.yaml` 的各层见 [sources.md](sources.md)。

## 11. 内核命令行参数（`omloader.*`）

Ventoy 不能传内核参数，请改用 `boot.yaml`（`boot.extra_cmdline_flags`）。当运行的是加载器自己的 GRUB（不经 Ventoy 的盘，或在条目上按 `e` 编辑）时，可把参数写在内核行，或写到 `/boot/grub/extra.cfg`（`set omloader_args="..."`）。

| 参数 | 含义 |
|---|---|
| `omloader.image=NAME` | 自动启动该镜像（不进 TUI）。 |
| `omloader.snapshot=S` | 配合 `omloader.image`：`current`（默认）、`base` 或快照标签。 |
| `omloader.ui=tui\|shell` | 没有自动启动时的控制台界面（优先于 `boot.ui`）。 |
| `omloader.poweroff_on_fail=1` | 自动启动失败时关机。 |
| `omloader.shell=auto\|image\|busybox` | 容器/OCI 启动的控制台 shell 策略（覆盖 `runtime.shell`）。 |
| `omloader.experimental=off\|oci` | `off` 关闭默认开启的实验性 OCI 支持；`oci` 强制开启（等同 `OML_EXPERIMENTAL`）。 |
| `omloader.store=off` | 不挂载存储文件（维护用：离线检查/扩容）。 |
| `omloader.font=NAME\|auto\|default` | Linux 控制台字体（[tui.md](tui.md)）。 |
| `omloader.gfx=WxH\|off` | GRUB 视频模式（通过 `omloader_args`；Ventoy GRUB2 模式下无效）。 |
| `omloader.data=LABEL=x\|UUID=x\|/dev/x` | 指定数据分区（默认：卷标 `VTOYDATA`，否则是存放 ISO 的分区）。 |
| `omloader.live=/dev/X`、`omloader.isofile=/path.iso`、`omloader.isodev=LABEL=…\|UUID=…\|/dev/…` | 第一阶段：指定 live 介质 / ISO 文件位置。 |
| `omloader.selftest=1`、`omloader.hook=1`、`omloader.marker=NAME` | 测试辅助（挂载检查后关机；从 `OMLSTAGE` 盘运行 `hook.sh`；测试客户系统的标记）。 |

加载器自己的内核命令行是 `console=tty0 console=ttyS0,115200 loglevel=4 systemd.show_status=auto`。

## 12. 环境变量

| 变量 | 使用者 | 含义 |
|---|---|---|
| `OML_EXPERIMENTAL` | `omloader` | 逗号分隔的实验功能；`off` 关闭实验性 OCI 镜像（默认开启），`oci` 强制开启。 |
| `OML_SOURCES` | `omloader` | 额外的 `sources.yaml`，优先于盘/ISO 上的副本。 |
| `OMLOADER_VERSION` | TUI | 二进制构建时没有版本号时，TUI 顶栏显示的版本。 |
| `OML_VERSION` | `build/version.sh`、`make` | 覆盖构建版本（默认：git tag，否则 `VERSION`）。 |
| `CONTAINER_ENGINE`、`DEBIAN_MIRROR`、`SOURCE_DATE_EPOCH` | `build/build-iso.sh` | `podman`/`docker`；Debian 镜像站（默认 `http://deb.debian.org/debian`，国内可用 `http://mirrors.ustc.edu.cn/debian`）；稳定输出的时间戳。 |
| `OML_REMOTE`、`OML_REMOTE_PASS`、`OML_RDIR` | `Makefile` 远程测试目标 | 可选的 ssh 测试主机（无默认值）；切勿提交凭据。 |

## 13. 命令行参考

不带参数运行 `omloader` 的输出（v0.1.x）：

```
usage:
  omloader version
  omloader config validate [-c omloader.yaml] [-i image.yaml]
  omloader config show --resolved [-c omloader.yaml] [-i image.yaml]
  omloader config get [-c omloader.yaml] KEY   (print one resolved value, e.g. boot.default_image; for scripts)
  omloader boot --image NAME [--snapshot current|base|LABEL] [--ephemeral] [-c global.yaml]
            [--images-root DIR] [--snap-root DIR] [--dry-run] [--no-kexec] [--exec-cmd-log FILE]
            [--force] [--ro] [--no-inject] [--data-root DIR]
  omloader tui [-c omloader.yaml] [--images-root DIR] [--snap-root DIR] [--data-root DIR] [--ascii]   (interactive terminal UI; docs/tui.md)
  omloader guest-assemble --plan /omloader/plan.json   (runs inside the guest initramfs)
  omloader snapshot <op> ...   (run 'omloader snapshot' for the operations)
  omloader store create|info|check|grow|find ...   (ext4 store FILE on exFAT/NTFS: see docs/store-file.md)
  omloader import lxd PATH --name N --out IMAGES_ROOT [--force] [--autologin] [--data SPEC]...
  omloader images list [--source S] [--mirror M] [--type container|vm] [--os X] [--release R] [--variant V] [--arch amd64] [--offline] [-c omloader.yaml]
  omloader pull lxc:<os>/<release>[/<variant>] [--vm|--container] [--arch amd64] [--name N] [--out IMAGES_ROOT] [--source S] [--mirror M] [--variant V]
  omloader pull docker:<image[:tag]> [--mirror auto|ID|official] [--name N] [--out IMAGES_ROOT] [--experimental-oci]   # EXPERIMENTAL, on by default; uses the bundled crane (sources.md)
            [-c omloader.yaml] [--offline] [--force] [--no-probe] [--tmp DIR]
  omloader import lxd-vm PATH --name N --out IMAGES_ROOT [--no-probe]   (disk.qcow2 | dir with disk.qcow2 [+lxd.tar.xz])
  omloader source list|test [NAME] [--mirror M] [-c omloader.yaml] [--offline]   (M: official | auto | a server id from sources.yaml; docs/sources.md)
  omloader source ref IMAGE [--source dockerhub] [--mirror M] [-c omloader.yaml]    (Docker Hub image reference rewritten for a proxy mirror; experimental)
  omloader cache ls|gc [--max-size-gb N] [-c omloader.yaml]
  omloader net status [-c omloader.yaml] [--offline]
  EXPERIMENTAL Docker/OCI images (on by default; switch off with experimental.oci_images: false, OML_EXPERIMENTAL=off or omloader.experimental=off):
    omloader import oci-dir|oci-archive|docker-archive PATH --name N --out IMAGES_ROOT ...   and  boot of type: oci images
```

`omloader snapshot`（不带参数）：

```
usage: omloader snapshot <op> [flags] <image> [args]   common flags: --root SNAPROOT --images-root IMGROOT -c omloader.yaml
Snapshots are managed here, in the loader, before the image is booted (no hot snapshots).
Model: base = the original image (read-only), current = your live state; snapshots are labelled frozen points in between.
  list <image> [--tree] [--sizes]
  create <image> <label> [--note TEXT] [--tag T]...       freeze the live state as <label>
  revert <image> <label|base> [--keep LABEL]              discard the live state, continue from <label>/base (alias: rollback)
  delete <image> <label> [--recursive] [--force]          leaf: removed; one child: merged into it (overlay only); --recursive: subtree
  gc <image> [--ephemeral] [--dry-run]                    remove leftovers of ephemeral boots
  prune <image> [--dry-run]                               snapshot.retention.keep_last / keep_tagged
  tag <image> <label> <tag>...   untag <image> <label> <tag>...   note <image> <label> <text>
  df <image>        verify <image> [--deep]
  export <image> <label> --squashfs OUT (overlay) | --raw OUT | --qcow2 OUT (dm; root, run in the loader)
  flatten <image> <label> [--name NEW]                    new image NEW (default <image>-flat-<label>); needs root
Boot: omloader boot --image <image> [--snapshot current|base|<label>] [--ephemeral]   (current continues the live state)
```

存储文件命令见 [store-file.md](store-file.md)。`omloader version` 打印内嵌的版本号。退出码：0 成功，1 出错。需要挂载、loop 或 `kexec` 的命令要 root（加载器的 shell 就是 root）。

## 14. 故障排查 / FAQ

- **选择 ISO 后黑屏 / 没有菜单。** 多半是 Secure Boot 没关：请关闭。试试 Ventoy 的另一种启动模式（normal ↔ GRUB2）。Ventoy 菜单只在 VGA 显示；加载器自己的 GRUB 与启动日志同时输出到串口（`console=ttyS0,115200`）。
  帧缓冲控制台会按分辨率挑选字体（`omloader.font=`、`console.conf`）。*真机表现未测试。*
- **第一阶段提示找不到 live 介质 / 进入救援 shell。** 加载器找不到它的 ISO：ISO 可放在 exFAT、NTFS、ext4、xfs 或 FAT32 上，位置 `/omloader.iso`、`/omloader/` 或 `/ISO/`，或用 `omloader.isofile=` 指定。
- **虚拟机里启动后控制台分辨率变低（如 1920x1080 -> 1280x800）。** 虚拟机的显示驱动替换了固件帧缓冲。请选择 GRUB 菜单项「safe display」（`nomodeset`），或在虚拟机里固定视频模式；真机上由原生驱动选择模式。详见 [ventoy.md](ventoy.md)。
- **GPU 初始化错误 / 黑屏（如 AMD 上的 `fatal error during GPU init`）。** ISO 在 live 根文件系统中内置 amdgpu（`firmware-amd-graphics`）、Intel i915 GuC/HuC（`firmware-intel-graphics`）、bnx2/bnx2x 网卡、Realtek、Intel 无线及 misc 固件；NVIDIA 使用免固件的 nouveau/efifb。绕过办法：在 GRUB 选择「safe display (nomodeset)」，或在内核参数中加 `nomodeset` / `amdgpu.modeset=0`；控制台仍可通过固件帧缓冲工作。反馈问题：进入 shell（`S`）后发送 `dmesg | grep -i -E 'amdgpu|firmware'` 的输出。
- **Secure Boot。** 不支持（没有签名的 shim/内核）。请在固件设置里关闭。
- **NTFS。** 加载器不依赖内核 `ntfs3`；NTFS 分区（包括存放 ISO 的分区）通过第一阶段自带的 ntfs-3g（FUSE）挂载。若 Windows 让卷处于休眠（快速启动）或脏状态，则以**只读**挂载：镜像可读，但其上的存储文件不可用（快照只在内存里）。请彻底关闭 Windows（`shutdown /s /t 0`）或运行 `chkdsk`。
- **exFAT 与稀疏文件。** exFAT 不能创建稀疏文件，所以 `store create` 和 `ext4-file` 数据卷会立即占满全部大小（20G 以上会很慢）；vfat 文件限制 < 4 GiB。可以减小 `--size`，或改用 `OMLOADER` ext4 分区。
- **关机后快照消失。** 没有找到存储（日志里 `SNAPOK=0`）：创建存储文件或 `OMLOADER` 分区（[第 3 节](#3-把-iso-放到-ventoy-盘)）。
- **VM 客户系统：`update-grub`/ESP 报错。** 客户的 `/boot/efi` 刻意不挂载（`boot.vm_fix_esp_fstab`）：客户系统由 `kexec` 启动，从不使用 ESP；要写入 ESP 的工具无处可写。
- **VM 镜像无法启动 / “no ext4/xfs root partition”。** 支持：UEFI/GPT 磁盘，ext4 或 xfs 根分区且内核与 initrd 在 `/boot` 下（设置 `kernel.path`/`kernel.initrd`、`kernel.root_partition`）；不支持 LVM/LUKS 根；仅在 Debian 风格（initramfs-tools）客户系统上验证过。
- **拉取很慢或失败。** 使用镜像站：`omloader source test`、`omloader pull … --mirror auto`、`mirror_policy.region_preset: cn`，或设置 `network.proxy`。详见 [sources.md](sources.md)。`--offline` 只用缓存。
- **网络起不来。** 仅对有线 `en*`/`eth*` 配置了 DHCP；检查 `networkctl`、`omloader net status`。Wi-Fi 未配置，ISO 不内置 Wi-Fi 固件：把固件文件放到数据分区 `omloader/firmware/`（路径同 /lib/firmware，如 `iwlwifi-cc-a0-77.ucode`、`rtw88/*.bin`；来源：Debian 的 firmware-iwlwifi/firmware-atheros 等包 `apt download` + `dpkg -x`，或 https://git.kernel.org/pub/scm/linux/kernel/git/firmware/linux-firmware.git），启动时会叠加到 /lib/firmware 并重载缺固件的驱动；对 GPU 固件无效（早期启动需要，已内置）；之后用 shell 里的 `iwctl` 手动连接。
- **想改/去掉默认登录。** 见[第 6 节](#6-默认账号与安全)。
- **`Docker/OCI images are experimental and have been disabled here`。** Docker/OCI 支持是实验性的、默认开启；有设置把它关了（`experimental.oci_images: false`、`OML_EXPERIMENTAL=off` 或 `omloader.experimental=off`）。去掉该设置，或用 `--experimental-oci` 强制开启。
- **日志在哪？** 串口控制台与 `journalctl`；`OMLOADER_LOADER:` 和 `OMLOADER_STAGE1:` 行汇总了检测结果。

## 15. 限制

Alpha 阶段，仅在 QEMU/OVMF 中测试。仅 UEFI x86_64；不支持 Secure Boot；仅 Linux 客户系统；VM 客户系统：ext4/xfs 根且 `/boot` 可用，无 LVM/LUKS，仅在 Debian 风格客户系统上验证，无独立数据卷，关机时不能干净卸载存储；
容器的控制台登录仅对 systemd 客户系统实现（其他得到 busybox 调试 shell）；无热快照；dm-snapshot 的 VM 快照不能“合并删除”（请用 `flatten`）；Wi-Fi 未配置；拉取只支持 Simplestreams 源（sha256 校验，无 GPG）；
Docker/OCI 镜像是实验性功能（默认开启；需要带 systemd 或 shell 的镜像，不支持端口映射，代理支持不均衡；可用 `experimental.oci_images: false` 关闭）。完整列表见 [DEVELOPMENT.md](DEVELOPMENT.md#known-limitations)。

### 常见问题：TUI 显示 "store RAM only"

快照只在内存中，断电即失。优先级：`OMLOADER` 分区 > 存储文件（`omloader/store.img`、`omloader/*.dat|*.img`、`ventoy/*.dat`，ext4 卷标必须为 `OMLOADER`）> 内存。默认首次启动时加载器会自动创建 `omloader/store.img`（`storage.snapshots.store_file.size`，20G）。以下情况不会创建：宿主只读（Windows 快速启动/休眠导致的 NTFS → 请完全关机）、剩余空间 < 4G + 1G、`auto_create: false`、`omloader.store=off`。原因见 Storage 页面/加载器日志，详见 [store-file.md](store-file.md)。
