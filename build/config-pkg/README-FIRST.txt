OCI Metal Loader - config package / 配置包
==========================================

EN
1. Install Ventoy on your USB stick (UEFI machine, Secure Boot OFF in the firmware).
2. Copy omloader-<version>.iso to the Ventoy data partition (e.g. /omloader/omloader-<version>.iso).
   Verify it first: sha256sum -c --ignore-missing SHA256SUMS (from the release page).
3. Unzip THIS package into the ROOT of the same data partition (everything lives in ONE folder: omloader/ = boot.yaml, sources.yaml, images/, store.img; plus ventoy/ventoy.json.example).
   The data partition may be exFAT, NTFS, ext4, xfs or FAT32, any label (VTOYDATA is preferred when present; otherwise the partition holding the ISO is used).
   Do not overwrite an existing ventoy/ventoy.json: merge ventoy/ventoy.json.example by hand.
4. Edit omloader/boot.yaml (everything is commented out), copy images into omloader/images/, boot the stick, pick the ISO.
SECURITY: every guest gets the PUBLIC default login  oci / Oci123  (root: same password, console autologin).
Change it in omloader/boot.yaml (guest_defaults) before connecting a guest to a network.
Docs: docs/INSTALL.md (English), docs/INSTALL.zh-CN.md (中文) in https://github.com/BBDXF/oci-metal-loader

中文
1. 在 U 盘上安装 Ventoy（UEFI 电脑，固件里关闭 Secure Boot）。
2. 把 omloader-<版本>.iso 拷到 Ventoy 数据分区（如 /omloader/omloader-<版本>.iso）。
   先校验：sha256sum -c --ignore-missing SHA256SUMS（来自发布页）。
3. 把本压缩包解压到同一数据分区的【根目录】（所有内容都在【一个】文件夹 omloader/ 里：boot.yaml、sources.yaml、images/、store.img；另有 ventoy/ventoy.json.example）。
   不要覆盖已有的 ventoy/ventoy.json：请手工合并 ventoy/ventoy.json.example。
4. 编辑 omloader/boot.yaml（全部已注释），把镜像放进 omloader/images/，用 U 盘启动并选择 ISO。
安全提示：所有客户系统都有公开的默认登录  oci / Oci123（root 同密码，控制台自动登录）。
接入网络前请在 omloader/boot.yaml 的 guest_defaults 里修改。
文档：https://github.com/BBDXF/oci-metal-loader 中的 docs/INSTALL.md（English）、docs/INSTALL.zh-CN.md（中文）
