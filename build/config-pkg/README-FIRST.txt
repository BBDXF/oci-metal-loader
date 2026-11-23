OCI Metal Loader - config package / 配置包
==========================================

EN
1. Install Ventoy on your USB stick (UEFI machine, Secure Boot OFF in the firmware).
2. Copy omloader-<version>.iso to the Ventoy data partition (e.g. /omloader.iso).
   Verify it first: sha256sum -c --ignore-missing SHA256SUMS (from the release page).
3. Unzip THIS package into the ROOT of the same data partition (files go to omloader/, images/, ...).
   Do not overwrite an existing ventoy/ventoy.json: merge ventoy/ventoy.json.example by hand.
4. Edit omloader/boot.yaml (everything is commented out), copy images into images/, boot the stick, pick the ISO.
SECURITY: every guest gets the PUBLIC default login  oci / Oci123  (root: same password, console autologin).
Change it in omloader/boot.yaml (guest_defaults) before connecting a guest to a network.
Docs: docs/INSTALL.md (English), docs/INSTALL.zh-CN.md (中文) in https://github.com/BBDXF/oci-metal-loader

中文
1. 在 U 盘上安装 Ventoy（UEFI 电脑，固件里关闭 Secure Boot）。
2. 把 omloader-<版本>.iso 拷到 Ventoy 数据分区（如 /omloader.iso）。
   先校验：sha256sum -c --ignore-missing SHA256SUMS（来自发布页）。
3. 把本压缩包解压到同一数据分区的【根目录】（会生成 omloader/、images/ 等）。
   不要覆盖已有的 ventoy/ventoy.json：请手工合并 ventoy/ventoy.json.example。
4. 编辑 omloader/boot.yaml（全部已注释），把镜像放进 images/，用 U 盘启动并选择 ISO。
安全提示：所有客户系统都有公开的默认登录  oci / Oci123（root 同密码，控制台自动登录）。
接入网络前请在 omloader/boot.yaml 的 guest_defaults 里修改。
文档：https://github.com/BBDXF/oci-metal-loader 中的 docs/INSTALL.md（English）、docs/INSTALL.zh-CN.md（中文）
