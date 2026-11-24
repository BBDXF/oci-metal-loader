# Install, configure and operate OCI Metal Loader

English | [简体中文](INSTALL.zh-CN.md) · [README](../README.md) · [Developer notes](DEVELOPMENT.md)

> **Alpha.** Everything described here was verified in QEMU/OVMF (including Ventoy 1.1.17 booting the ISO). **Nothing has been tested on real hardware yet**;
> statements about real machines below are marked *(untested on hardware)*. The ISO build in CI has not run end to end at the time of writing either.

Contents: [1 Requirements](#1-requirements) · [2 Download and verify](#2-download-and-verify) · [3 Put the ISO on a Ventoy stick](#3-put-the-iso-on-a-ventoy-stick) ·
[4 First boot](#4-first-boot) · [5 Get images](#5-get-images) · [6 Default account](#6-default-account-and-security) · [7 TUI](#7-the-terminal-ui) · [8 Snapshots](#8-snapshots-workflow) ·
[9 Configuration reference](#9-configuration-reference) · [10 File and folder layout](#10-file-and-folder-layout) · [11 Kernel command line](#11-kernel-command-line-omloader) ·
[12 Environment variables](#12-environment-variables) · [13 CLI reference](#13-cli-reference) · [14 Troubleshooting / FAQ](#14-troubleshooting--faq) · [15 Limits](#15-limits)

## 1. Requirements

| | |
|---|---|
| Machine | x86_64 PC with **UEFI**. Legacy BIOS/CSM boot is not supported. |
| **Secure Boot** | **Must be turned off** in the firmware. The GRUB and the kernel on the ISO are not signed for Secure Boot. |
| USB stick/disk | Any size that holds the ISO (~260 MiB) plus your images. LXD VM images are 1–10 GiB each (raw), containers 100–500 MiB; snapshots and data need extra room (a 32 GiB stick is a comfortable start). |
| [Ventoy](https://github.com/ventoy/Ventoy) | Install it on the stick first (tested: 1.1.17). omloader is deliberately bound to Ventoy; there is no separate installer. |
| RAM | The loader runs from RAM (tmpfs overlay over the squashfs). The test VMs used 2 GiB; plan **4 GiB or more**, more when flattening big images. *(untested on hardware)* |
| Network (optional) | Wired DHCP only (`en*`/`eth*`). Wi-Fi tools are installed but nothing configures them. Needed only to pull images; you can copy images to the stick instead. |
| Guests | Linux only (LXD VM and container images). For Windows ISOs/VHD(x) use Ventoy itself on the same stick. |

## 2. Download and verify

Download from the [Releases page](https://github.com/BBDXF/oci-metal-loader/releases): `omloader-<version>.iso` (the loader), `omloader-<version>-linux-amd64.tar.gz` (the static binary, for scripting on other Linux hosts),
`omloader-<version>-config.zip` (the config package: commented `boot.yaml`, `sources.yaml`, `images/` placeholder, `ventoy.json.example`, examples, schemas, `README-FIRST.txt`; unzip it into the root of the Ventoy data partition, see section 3) and `SHA256SUMS`. A development ISO can be built on demand with the manual *ISO (manual)* workflow (Actions → Run workflow → *Artifacts*); it is not built on every push.

    sha256sum -c --ignore-missing SHA256SUMS         # Linux/macOS (coreutils)
    Get-FileHash .\omloader-0.1.0.iso -Algorithm SHA256   # Windows PowerShell: compare with SHA256SUMS

Build it yourself instead: `make iso` (podman or docker; [build/README.md](../build/README.md)).

## 3. Put the ISO on a Ventoy stick

1. Install Ventoy on the stick (Ventoy2Disk). It creates a large data partition (exFAT by default) and a small `VTOYEFI` partition.
2. Copy `omloader-<version>.iso` to the data partition, e.g. as `/omloader.iso`. Any path works; the loader finds the ISO file by scanning
   (`/`, `/omloader/`, `/ISO/`, then up to two directory levels for an ISO with volume label `OMLOADER_LIVE`). Details: [ventoy.md](ventoy.md).
3. Unzip `omloader-<version>-config.zip` into the **root** of the same partition (files, no version folder). It creates `omloader/boot.yaml` (everything commented out), `omloader/sources.yaml`, `images/README.txt`, `ventoy/ventoy.json.example`, `schema/`, `examples/` and `README-FIRST.txt`. It never contains a `ventoy.json`: merge the `.example` into your own by hand (menu alias/timeout, see [ventoy.md](ventoy.md)).
4. Boot the PC from the stick (UEFI), pick the ISO. Ventoy offers *normal* and *GRUB2* mode; both work (memdisk mode does not).

**Where do images, snapshots and config live?** Three options (the loader picks them automatically, see [section 10](#10-file-and-folder-layout)):

| Option | What you do | Notes |
|---|---|---|
| A. **Store file** (simplest) | Keep the single Ventoy exFAT/NTFS partition (label it `VTOYDATA`). Put images in `images/`. Create the store once in the loader: `omloader store create --path /mnt/images/omloader/store.img --size 20G` (or in the TUI Storage tab). | No Linux partition needed. exFAT has no sparse files: the file is fully allocated when created. See [store-file.md](store-file.md). |
| B. **`OMLOADER` partition** (most robust) | Add an ext4 (xfs/btrfs also accepted) partition labelled `OMLOADER` (e.g. `mkfs.ext4 -L OMLOADER /dev/sdX3`, on the free space after reserving it in Ventoy2Disk *Option → Partition configuration → Preserve some space*) and keep the Ventoy partition as `VTOYDATA`. | One filesystem layer less; a real `OMLOADER` partition always wins over a store file. |
| C. **Nothing** | Just boot. | Snapshots/cache live in RAM and vanish at poweroff (the loader says `SNAPOK=0`). Fine for trying it out. |

The images folder is `images/` on the partition labelled `VTOYDATA` (or, if there is none, `images/` on the `OMLOADER` store). You can relabel the Ventoy partition to `VTOYDATA`
(`exfatlabel /dev/sdX1 VTOYDATA`; Windows: right-click → Rename) — Ventoy keeps working. NTFS cautions: the Ventoy partition holding **the ISO** must not be NTFS (stage 1 cannot read it: no `ntfs3` in the loader kernel);
a *separate* NTFS `VTOYDATA` partition works through ntfs-3g, read-only if Windows left it hibernated/dirty.

## 4. First boot

Pick the ISO. GRUB shows its menu for 1 s, the Debian-based loader boots (usually under a minute, longer from slow USB) and the **TUI** opens on tty1 and on the serial console. Nothing is booted automatically
unless you configure it:

- **Auto-boot with a countdown** (Ventoy cannot pass kernel parameters): create `omloader/boot.yaml` on the `VTOYDATA` partition:
  ```yaml
  boot:
    default_image: ubuntu-noble   # empty/absent = show the TUI; `last` = the image the TUI booted last
    default_snapshot: current     # current | base | a snapshot label (label/base = throwaway boot)
    timeout: 10                   # seconds; any key cancels the countdown and opens the TUI
  ```
  The TUI opens at once and shows a banner `Booting ubuntu-noble (current) in N s -- press any key to cancel` (the countdown starts once the first frame is drawn and is never shorter than 5 s; it does not wait for a viewer, so a VNC client that attaches later than that may miss it). Any key cancels and leaves you in the TUI; otherwise the pre-selected image+snapshot boots exactly as Enter would. With `boot.ui: shell` the shell prints the countdown on tty1/serial instead.
- `S` in the TUI (or leaving it with `q`) gives a root shell (autologin). `poweroff` / `reboot` work as usual; the loader unmounts its partitions cleanly.
- The status lines `OMLOADER_LOADER: snap=... images=...` (serial console / `journalctl -u omloader-mounts`) say which store and images partition were found.

### Unattended boot (boot a chosen image with no key presses)

Two independent countdowns chain together: **Ventoy** picks the ISO, then **omloader** picks the image.

1. **Ventoy side** — `ventoy/ventoy.json` on the data partition (Ventoy's own file, not omloader's; the config zip only ships `ventoy/ventoy.json.example`, so merge it by hand and never overwrite an existing `ventoy.json`):
   ```json
   {
     "control": [ { "VTOY_DEFAULT_IMAGE": "/omloader-0.1.0.iso" }, { "VTOY_MENU_TIMEOUT": "5" } ],
     "menu_alias": [ { "image": "/omloader-0.1.0.iso", "alias": "OCI Metal Loader" } ]
   }
   ```
   The path must match the **real ISO file name and location** on the partition (case-sensitive, starting with `/`). Ventoy then boots it after 5 s without a key press.
2. **omloader side** — `omloader/boot.yaml` on the same partition:
   ```yaml
   boot:
     default_image: ubuntu-noble   # or `last` = the image the TUI booted last (needs remember_last, default true)
     default_snapshot: current     # current | base | a snapshot label (label/base = throwaway boot)
     timeout: 10                   # seconds
   ```
   After the loader is up it prints `Booting 'ubuntu-noble' (current) in N s -- press any key for the menu` and then boots the image.

**Cancel:** in Ventoy's menu press any key (e.g. arrow keys) within the timeout to stop its countdown and choose entries by hand; during the omloader countdown press **any key** (keyboard or serial console) and the TUI opens instead.
**Without Ventoy** (or when editing the GRUB entry with `e`) the kernel line can do it: `omloader.image=ubuntu-noble omloader.snapshot=current` boots immediately with no countdown (it wins over `boot.yaml`); `boot.extra_cmdline_flags` lists more flags ([section 11](#11-kernel-command-line-omloader)).
**Troubleshooting:** *Ventoy shows its menu and waits* → `VTOY_DEFAULT_IMAGE` does not match the ISO path/name (or `ventoy.json` is not in `ventoy/` on the first partition). *Ventoy asks for a boot mode* → choose *normal* (or *GRUB2*) once; memdisk mode does not work for this ISO, see [ventoy.md](ventoy.md). *The TUI opens instead of the image* → `boot.yaml` is not found/invalid (`omloader config validate -c boot.yaml`), `default_image` is empty, `last` has nothing recorded yet, or a key was pressed. Needs a store (section 3) for snapshots to persist. *(Verified in QEMU only; real hardware untested.)*

## 5. Get images

Images live in `images/<name>/` (`image.yaml` + `base.raw` for `lxd-vm` or `base.squashfs` for `lxd-container`). Three ways to get them there:

1. **Pull inside the loader** (needs network). In the TUI *Pull* tab (`2`) pick an image, or on the shell:
   ```sh
   omloader images list --os ubuntu --type vm            # browse the catalog (--type container|vm)
   omloader pull lxc:ubuntu/noble --vm --name ubuntu-noble --out /mnt/images/images
   omloader pull lxc:debian/bookworm --container --name debian-bookworm --out /mnt/images/images
   ```
   Sources and mirrors (official, USTC, NJU, TUNA, CERNET...): `omloader source list`, `omloader source test`, `--source S --mirror M|auto|official`, `mirror_policy.region_preset: cn`.
   Everything is described in [sources.md](sources.md). Downloads are sha256-checked and cached (`/mnt/snap/cache`, `omloader cache ls|gc`); `--offline` never touches the network.
2. **Import files you already have** (LXD export: a directory or `lxd.tar.xz` next to `rootfs.squashfs`; for VMs a `disk.qcow2`):
   ```sh
   omloader import lxd /path/to/lxd-container-dir --name mybox --out /mnt/images/images
   omloader import lxd-vm /path/to/disk.qcow2 --name myvm --out /mnt/images/images     # converts to base.raw; --no-probe skips the kernel probe
   ```
   Imports can be run on any Linux PC with the `omloader` binary from the release tarball (`--out` = `images/` folder of the mounted stick), except the VM kernel probe which needs root; use `--no-probe` there and write `kernel:` in `image.yaml` yourself.
3. **Copy a ready image folder** (`image.yaml` + base file, see [section 9.3](#93-imageyaml-per-image-config)) into `images/` of the stick.

Desktop environments (which sources have ready desktop VMs, how to add one to a server image): [desktops.md](desktops.md).

**What is inside the ISO:** a Debian stable live base plus the `omloader` binary. See which with `omloader version --long` (reads `/etc/omloader-release`; also shown in the TUI `?` help), or without booting: `omloader-release` and `packages.txt` at the ISO root (the Release also has `omloader-<ver>-packages.txt`).

Validate a hand-written config: `omloader config validate -c omloader.yaml -i images/NAME/image.yaml`; `omloader config show --resolved` shows every value and the layer/line it came from.

## 6. Default account and security

Every image — LXD container, LXD VM and experimental OCI — gets a **public default login** at boot: user **`oci`**, password **`Oci123`** (sudo with password), `root` with the same password, and console autologin as `oci` (tty1 and serial).
**Anyone who can reach the console or a network service of your guest knows this password. Change it** before exposing a guest to a network:

```yaml
# omloader/boot.yaml on VTOYDATA (applies to all images)
guest_defaults:
  autologin: false
  root_password_hash: "!"        # lock root; or a $6$... hash (preferred over plaintext root_password)
  users:
    - {name: alice, password_hash: "$6$...", sudo: true, ssh_authorized_keys: ["ssh-ed25519 AAAA... alice@pc"]}
```
Per image, `runtime.users` / `runtime.root_password_hash` / `runtime.autologin` in `image.yaml` win over `guest_defaults` (merge rules in [DEVELOPMENT.md](DEVELOPMENT.md#default-accounts-guest_defaults-all-image-types)).
The loader only writes into the image's own COW layer, never into the base image. Generate a hash with `openssl passwd -6`.

## 7. The terminal UI

`omloader tui` starts by default. Keys: `1`–`5` tabs (Images, Pull, Storage, Network, Settings), arrows/`j`/`k`, Enter, Esc, `?` or `F1` help for the current screen, `S` shell, `P` reboot/power off, `q`/Esc leave to the shell, `Ctrl+C` cancels the running operation (never quits the TUI).

**One-key boot** (no submenus, no confirmation unless data would be lost):

| Where | Key | Does |
|---|---|---|
| Images | `Enter` | boot the image now (continues `current`); the image booted last is preselected |
| Images | `o` / `→`, `e`, `Space`/`m` | open the snapshot tree, ephemeral (throwaway) boot, action menu |
| Snapshot tree | `Enter` on `current` | boot it now (also on the snapshot the live state sits on) |
| Snapshot tree | `Enter` on a frozen snapshot or `base` | revert the live state to it and boot. **If the live state has changes, one dialog says they will be LOST** (`Enter`/`y` = revert + boot, **`K` = save them as a snapshot first** — label prompt, default `snap-YYYYMMDD-HHMM`, `Enter` accepts — `Esc`/`n` = cancel). No live changes (never booted / created lazily) = no dialog |
| Snapshot tree | `e`, `s`, `r`, `d`, `Space`/`m` | ephemeral boot of the selected state, create snapshot, revert without booting (same dialog), delete (asks), action menu (also `Alt+Enter`/`Ctrl+Enter` where the terminal sends them) |

Delete, prune/GC and store operations keep their confirmations; booting and reverting have the single dialog above.
Full key and screen reference, colour/ASCII fallbacks and console-font rules: [tui.md](tui.md). The UI is English only. `boot.ui: shell` or the kernel flag `omloader.ui=shell` boots to a shell instead.

## 8. Snapshots workflow

Model (like a VM): **`base`** is the original read-only image; **`current`** is your single live state; labelled **snapshots** are frozen points. Booting `current` *continues* it (no new layer per boot).

```sh
omloader snapshot list ubuntu-noble --tree
omloader snapshot create ubuntu-noble before-upgrade --note "clean install"   # freeze current under a label, continue on a fresh layer
omloader snapshot revert ubuntu-noble before-upgrade [--keep experiment]      # throw current away (or --keep it as a snapshot) and continue from the label (or "base")
omloader snapshot delete ubuntu-noble before-upgrade                          # overlay (container) images merge it into its child; dm-snapshot (VM) images refuse: use flatten
omloader boot --image ubuntu-noble                                            # continue current
omloader boot --image ubuntu-noble --snapshot before-upgrade                  # ephemeral: a throwaway layer on top, nothing is kept
omloader boot --image ubuntu-noble --ephemeral                                # throwaway boot on top of current
```
**Snapshots are managed only from the loader (CLI/TUI) before the image is booted.** A running system cannot be snapshotted or reverted: shut it down, return to the loader, then manage snapshots (no hot snapshots).
Also available: `snapshot tag|untag|note|gc|prune|df|verify|export|flatten` (see [section 13](#13-cli-reference)). Data volumes declared `snapshot: false` live outside the layers and survive `revert`.
VM guests are `kexec`'d and cannot unmount the store at poweroff, so the store ext4 may need a journal replay at the next loader boot (automatic).

## 9. Configuration reference

Three file families, all YAML, all optional (everything has a built-in default):

| File | Where | Schema |
|---|---|---|
| Global config `omloader.yaml` / **`boot.yaml`** (same format and keys; `boot.yaml` is the name used on the stick) | `<VTOYDATA>/omloader/boot.yaml`, `<VTOYDATA>/omloader.yaml`, `<OMLOADER>/omloader/boot.yaml`, `<OMLOADER>/omloader.yaml`, `<ISO host partition>/omloader/boot.yaml` (first found wins); CLI: `-c FILE` | [`schema/omloader.schema.json`](../schema/omloader.schema.json) |
| Built-in defaults `configs/defaults.yaml` | embedded in the binary; shown in [9.1](#91-global-config-omloaderyaml--bootyaml) | same schema |
| `sources.yaml` (catalog of image sources and mirrors) | embedded copy `configs/sources.yaml`, `/etc/omloader/sources.yaml` in the ISO, then your copy at `<VTOYDATA>/omloader/sources.yaml`... | [`schema/sources.schema.json`](../schema/sources.schema.json), [sources.md](sources.md) |
| Per-image `image.yaml` | `<images>/<name>/image.yaml` | [`schema/image.schema.json`](../schema/image.schema.json) |

Layers: built-in defaults < `omloader.yaml`/`boot.yaml` < `image.yaml`, deep-merged per key. Unknown keys are errors (file and line are reported). Sizes accept `512M`, `20G`, `1.5G`, bytes.
Keys marked **reserved** are accepted and validated but nothing in the current code reads them yet.

### 9.1 Global config (`omloader.yaml` / `boot.yaml`)

**`storage`**

| Key | Type | Default | Meaning |
|---|---|---|---|
| `storage.images.label` | list of string | `[VTOYDATA, OMLOADER]` | **Reserved.** The loader scripts look for fs label `VTOYDATA` (images) and `OMLOADER` (store) themselves. |
| `storage.images.path` | string | `/images` | Default for `--images-root` of the CLI. In the loader it passes `/mnt/images/images` explicitly. |
| `storage.snapshots.label` | string | `OMLOADER` | **Reserved** (the label `OMLOADER` is fixed in `mounts.sh`). |
| `storage.snapshots.path` | string | `/snapshots` | Default for `--snap-root`/`--root` of the CLI. In the loader: `/mnt/snap/snapshots`. |
| `storage.snapshots.fs_required` | list | `[ext4, btrfs, xfs]` | **Reserved** (the loader accepts exactly these filesystems on the `OMLOADER` partition). |
| `storage.snapshots.fallback` | `tmpfs` \| `none` | `tmpfs` | **Reserved** (without a store the loader always uses RAM). |
| `storage.snapshots.store_file.enabled` | bool | `true` | Look for an ext4 store file on the host partition ([store-file.md](store-file.md)). |
| `…store_file.path` | string | `""` | Exact path of the file inside the host partition, checked first. |
| `…store_file.glob` | list | `[/omloader/store.img, /ventoy/*.dat]` | Patterns inside the host partition; only files whose ext4 label is `OMLOADER` count. |
| `…store_file.host_match.label` / `.uuid` | string | `VTOYDATA` / `""` | Which partition holds the file (uuid wins over label; mismatch = no store file search). |
| `…store_file.size` | size | `20G` | Default size of `omloader store create`. |
| `…store_file.prealloc` | `auto`\|`yes`\|`no` | `auto` | `auto` = fully allocate on exFAT/vfat (no sparse files), sparse elsewhere. |

**`snapshot`, `verify`**

| Key | Type | Default | Meaning |
|---|---|---|---|
| `snapshot.max_chain_depth` | int ≥ 1 | `5` | Maximum layers under an image (seeds `image.yaml snapshots.max_chain_depth`); boot/create refuse deeper chains. |
| `snapshot.persistent` | bool | `true` | **Reserved.** |
| `snapshot.retention.keep_last` | int ≥ 1 | `10` | `snapshot prune` keeps this many newest snapshots. |
| `snapshot.retention.min_free_percent` | int 0–99 | `10` | Space guard: refuse create/boot when the store has less than this % free. |
| `snapshot.retention.keep_tagged` | bool | `true` | `prune` never deletes tagged snapshots. |
| `verify.base_checksum` | `sha256`\|`skip` | `sha256` | Verify the base image against `image.yaml sha256` at boot; `skip` disables it. |

**`boot`**

| Key | Type | Default | Meaning |
|---|---|---|---|
| `boot.default_image` | string | `""` | Image to boot automatically after `timeout` seconds when the kernel line has no `omloader.image=`. Empty = TUI/shell. `last` = the image the TUI booted last (needs `remember_last`; nothing recorded yet = TUI). |
| `boot.timeout` | int ≥ 0 | `10` | Countdown seconds, shown as a banner inside the TUI (any key cancels; the TUI never counts down less than 5 s). |
| `boot.remember_last` | bool | `true` | The TUI records the last booted image/snapshot in `<data partition>/omloader/last-boot` (3 lines: image, snapshot, `ephemeral`); it preselects them next time and `default_image: last` boots it. Not writable = silently skipped. |
| `boot.default_snapshot` | string | `current` | `current`, `base` or a snapshot label (labels/base boot a throwaway layer). Also the default of `omloader boot --snapshot`. |
| `boot.kernel_cmdline_extra` | string | `""` | Appended to a **VM** guest's kernel command line (after `image.yaml kernel.cmdline`). |
| `boot.extra_cmdline_flags` | list | `[]` | `omloader.*` flags without prefix applied as if on the kernel line, e.g. `[poweroff_on_fail=1, experimental=oci]`. |
| `boot.vm_fix_esp_fstab` | bool | `true` | VM guests: mark the guest's `/boot/efi` fstab entry `nofail` (the ESP is not mapped; avoids a 90 s emergency-mode timeout). |
| `boot.ui` | `tui`\|`shell` | `tui` | Console when nothing boots automatically. The `omloader.ui=` kernel flag wins. |

**`data`, `devices`** — all **reserved** (accepted, validated, not acted on today; data volumes are declared per image, see 9.3):
`data.default_backend` (`dir`; one of `dir ext4-file partition disk network`), `data.default_vm_backend` (`ext4-file`), `data.default_location` (`/OMLOADER/data`), `data.auto_home_volume` (`true`),
`data.fallback_if_missing` (`system-layer`; `fail`, `tmpfs`), `devices.policy` (`isolate`; `passthrough`, `hide`), `devices.windows_ntfs` (`ro_or_hide`; `ro`, `hide`, `rw`), `devices.hibernated_fs` (`refuse`; `ro`, `ignore`).

**`network`, `mirror_policy`, `cache`, `policy`**

| Key | Type | Default | Meaning |
|---|---|---|---|
| `network.timeout_sec` | int ≥ 1 | `15` | HTTP timeout. |
| `network.retries` | int ≥ 0 | `3` | Retries per request. |
| `network.proxy` | URL | `""` | `http://`, `https://` or `socks5://` proxy for all downloads. |
| `network.no_proxy` | string | `""` | Comma separated hosts, domain suffixes (`.lan`) or CIDRs that bypass the proxy. |
| `network.extra_ca` | path | `""` | PEM file with extra CA certificates (e.g. a corporate proxy CA). |
| `network.dns` | list | `[]` | **Reserved** (informational; the resolver is systemd-resolved with DHCP DNS). |
| `mirror_policy.strategy` | `fallback`\|`fastest`\|`mirror-only` | `fallback` | `fallback`: official first, then mirrors; `fastest`: probe and pick the fastest; `mirror-only`: never use the official server. |
| `mirror_policy.health_check` | bool | `true` | **Reserved.** |
| `mirror_policy.cache_ttl_hours` | int ≥ 0 | `24` | How long a cached catalog index is considered fresh (`cache.offline: auto`). |
| `mirror_policy.region_preset` | string | `global` | `cn`: also try every `region: cn` server of every source as fallback. |
| `cache.path` | path | `/OMLOADER/cache` | Download cache; this default means `/mnt/snap/cache` in the loader when the store is mounted. |
| `cache.max_size_gb` | int ≥ 0 | `20` | Default limit of `omloader cache gc`. |
| `cache.offline` | `auto`\|`on`\|`off` | `auto` | `on` = never go online (cache only); `off` = always check upstream; `auto` = use a fresh cache, else go online. |
| `policy.allow_online` | bool | `true` | `false` = offline mode everywhere. |
| `policy.allowed_sources` | list | `[]` | Restrict to these source names (empty = all). |

**`sources`, `default_source`** — same keys as `sources.yaml` (see [sources.md](sources.md)); `default_source` defaults to `lxc-images`. Per source: `type` (`simplestreams`\|`oci-registry`\|`http`\|`local`),
`url`, `mirrors` (plain URL list), `proxy` (`direct` = none), `rewrite` (`[{from, to}]`), `title`, `note`, `enabled`, `mirror` (`official`\|`auto`\|server id), `servers.<id>` (`title`, `url`, `path`, `region`, `note`, `enabled`, `rewrite`).

**`experimental`**: `experimental.oci_images` (bool, `false`) enables Docker/OCI image import and boot (also `OML_EXPERIMENTAL=oci`, `--experimental-oci`, `omloader.experimental=oci`).

**`guest_defaults`** (public defaults, see [section 6](#6-default-account-and-security))

| Key | Type | Default | Meaning |
|---|---|---|---|
| `guest_defaults.hostname` | string | `image` | `image` = image name as RFC 1123 label (`vm-ubuntu-noble`), `keep` = leave the guest's, or a literal name. `runtime.hostname` wins. |
| `guest_defaults.autologin` | bool | `true` | Console autologin (tty1 and ttyS0). |
| `guest_defaults.autologin_user` | string | `oci` | Autologin account (falls back to root if it is not in the user list). |
| `guest_defaults.root_password` | string | `Oci123` | Plaintext root password (hashed by the loader). Not both with `root_password_hash`. `root_password_hash: '!'` locks root. |
| `guest_defaults.root_password_hash` | string | – | `$6$…` crypt hash, `!` or `*`. |
| `guest_defaults.users[]` | list | one user `oci` | Accounts created offline: `name`, `password` or `password_hash`, `groups`, `shell` (absolute path), `sudo` (bool), `ssh_authorized_keys`. Default: `{name: oci, password: Oci123, sudo: true, groups: [sudo]}`. |

Examples: [`examples/omloader.yaml`](../examples/omloader.yaml) and the shipped [`configs/defaults.yaml`](../configs/defaults.yaml).

### 9.2 `sources.yaml`

Defines where images come from. Built in: `lxc-images` (images.linuxcontainers.org with mirrors TUNA, NJU, USTC, CERNET...), `lxd-images`, `ubuntu-releases`, `ubuntu-daily`, `dockerhub` (experimental).
Put your own copy at `<VTOYDATA>/omloader/sources.yaml`; it is merged on top of the built-in one (later files win; `enabled: false` switches a source off). Only `sources` and `default_source` are allowed at top level.
Full format, merge rules and the verified mirror list: [sources.md](sources.md).

### 9.3 `image.yaml` (per-image config)

Located at `<images>/<name>/image.yaml`; written by `pull`/`import`, hand-editable. Required: `name`, `type`, `format`, `base`.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | – | Image name (= folder name; no path separators). |
| `type` | `lxd-vm` \| `lxd-container` \| `oci` | – | `lxd-vm`: whole-disk image booted with its own kernel via `kexec` (**not** virtualised; the old spelling `vm` is rejected). `lxd-container`: root filesystem on the loader's kernel. `oci`: experimental. |
| `format` | `raw` \| `squashfs` | – | `lxd-vm` requires `raw`; containers/oci require `squashfs`. |
| `base` | path | – | Base file: relative to the image folder (`base.raw` / `base.squashfs`, as written by pull/import) or an absolute path on the loader's filesystem. `://` URLs are not supported by `boot`. |
| `sha256` | 64 hex | – | Checksum of the base file, verified at boot (`verify.base_checksum`). |
| `kernel.path` | path in guest | – | VM: kernel inside the guest root (e.g. `/boot/vmlinuz`). Required for VM boot. |
| `kernel.initrd` | path in guest | – | VM: initrd (requires `kernel.path`). |
| `kernel.cmdline` | string | – | VM: kernel command line (`root=`/`ro`/`rw` are replaced by the loader; `boot.kernel_cmdline_extra` is appended). |
| `kernel.root_partition` | int ≥ 0 | `0` = auto | VM: root partition number (auto-detects the first ext4/xfs root). |
| `kernel.inject_modules` | list | – | VM: extra guest kernel modules to put in the injected initrd (e.g. `[dm_snapshot, overlay]`). |
| `snapshots.mode` | `dm-snapshot`\|`overlayfs` | by type | `lxd-vm` = `dm-snapshot`, containers = `overlayfs` (other values rejected). |
| `snapshots.path` | path | `<snap-root>/<name>` | Where the tree and layers live. |
| `snapshots.max_chain_depth` | int ≥ 1 | from `snapshot.max_chain_depth` | Per-image limit. |
| `runtime.hostname` | string | `guest_defaults.hostname` | Literal name, or `keep`. |
| `runtime.network` | string | – | Containers: `dhcp` installs a DHCP client config, `none` leaves networking alone. |
| `runtime.env` | map string→string | – | Environment for the container init/entrypoint. |
| `runtime.entrypoint` / `runtime.cmd` / `runtime.workdir` | list / list / path | – | What to run (OCI-style images); LXD containers use their own init. |
| `runtime.autologin` / `runtime.autologin_user` | bool / string | `guest_defaults` | Console autologin; if you set either, both come from the image (empty user = root). |
| `runtime.users[]` | list | `guest_defaults.users` | Accounts (`name`, `password`\|`password_hash`, `groups`, `shell`, `sudo`, `ssh_authorized_keys`). Setting it (even `[]`) replaces the default list. |
| `runtime.root_password` / `runtime.root_password_hash` | string | `guest_defaults` | Replaces the default root password pair. |
| `runtime.debug_shell` | `auto`\|`always`\|`never` | `auto` | Containers: busybox shell on the console when the guest has no login/getty. |
| `runtime.shutdown_sync` | bool | `true` | Containers: install hooks that remount the loader partitions read-only at poweroff. |
| `data[]` | list | – | Persistent data volumes (containers only): `name`, `mount` (absolute path in guest), `backend`, `path`, `size`, `snapshot`, `shared`. |
| `data[].backend` | `dir`\|`ext4-file`\|… | – | **Implemented: `dir`** (bind mount of `<data-root>/<image>/<name>`, default data root `/mnt/snap/data`) **and `ext4-file`** (`<path>` or `<data-root>/<image>/<name>.img`, created at `size`, default 1G). `partition`, `disk`, `network` are accepted but skipped with a warning. |
| `data[].snapshot` | bool | `false` | `true` is not implemented (treated as persistent, with a warning). |
| `data[].shared` | bool | `false` | Accepted; **reserved**. |

Full example: [`examples/ubuntu-24.04/image.yaml`](../examples/ubuntu-24.04/image.yaml). `omloader import … --data name:/mount:backend[:size[:snapshot]]` declares volumes at import time.

## 10. File and folder layout

**Repository** (for developers; details in [DEVELOPMENT.md](DEVELOPMENT.md#repository-layout)):

```
oci-metal-loader/
├── cmd/omloader/  internal/        Go sources (CLI, config, snapshots, boot, fetch, TUI)
├── configs/        defaults.yaml, sources.yaml   (embedded into the binary)
├── schema/         omloader / image / sources JSON schemas
├── examples/       omloader.yaml, ubuntu-24.04/image.yaml
├── build/          Containerfile, build-iso.sh, mk-iso-inner.sh, stage1-init, grub.cfg, live/rootfs/ (units, scripts)
├── test/           QEMU end-to-end suites and fixtures
├── docs/           this guide, ventoy.md, store-file.md, sources.md, tui.md, DEVELOPMENT.md
└── .github/workflows/   ci.yml, release.yml
```

**On the Ventoy stick** (what you create):

```
Ventoy data partition (exFAT/NTFS/…; label VTOYDATA recommended)  ── mounted at /mnt/images in the loader
├── omloader-0.1.0.iso            the loader (any name/path; found by scanning)
├── ventoy/ventoy.json            optional Ventoy menu config (merge from ventoy.json.example in the config zip)
├── images/                       IMAGES ROOT (images_root)
│   ├── ubuntu-noble/             one folder per image
│   │   ├── image.yaml
│   │   └── base.raw              (lxd-vm)  |  base.squashfs (lxd-container)
│   └── debian-bookworm/ …
└── omloader/
    ├── boot.yaml                 global config (auto-boot, guest_defaults, network, …)
    ├── sources.yaml              your sources/mirrors (merged over the built-in catalog)
    ├── console.conf              optional: font=NAME for the Linux console
    └── store.img                 ext4 store FILE (label OMLOADER) when no OMLOADER partition exists (option A)

OMLOADER partition (ext4/xfs/btrfs, optional)  ── mounted at /mnt/snap
```

**The store** (`/mnt/snap`, either the `OMLOADER` partition or the store file):

```
/mnt/snap/
├── snapshots/<image>/   SNAP ROOT: tree.json, <id>.cow (VM dm-snapshot layers) or <id>/{upper,work} (container overlay layers), in-use.json
├── data/<image>/<vol>   persistent data volumes (dir) or <vol>.img (ext4-file)
├── cache/               download cache (blobs, catalog indexes, tmp/*.part)
├── omloader/            optional config copies (boot.yaml, sources.yaml, console.conf) — also read here
└── images/              images fallback when there is no VTOYDATA partition
```

**Inside the live ISO** (read-only squashfs layers + RAM overlay; nothing persists except on the two partitions above):

```
/usr/local/bin/omloader                the static binary
/etc/omloader/sources.yaml             built-in catalog copy (lowest priority)
/usr/local/lib/omloader/*.sh           mounts.sh, autoboot.sh, console-font.sh, tui-launch.sh, switch-root.sh
/etc/systemd/system/omloader-*.service mounts, console, autoboot
/run/omloader/{env,ui,uicfg,medium.env}  runtime state: IMAGES_ROOT, SNAPDEV, SNAPOK, STOREFILE, chosen config file, ISO host mount
/mnt/images  /mnt/snap                 mount points of VTOYDATA and the store
ISO: /boot/{vmlinuz,initrd.img,grub/grub.cfg,efiboot.img}  /live/{live.squashfs,omloader.squashfs}   volume label OMLOADER_LIVE
```

Config lookup order for the loader's boot decisions (first existing, valid file): `/mnt/images/omloader/boot.yaml`, `/mnt/images/omloader.yaml`, `/mnt/snap/omloader/boot.yaml`, `/mnt/snap/omloader.yaml`, `<ISO host partition>/omloader/boot.yaml`.
Cache location: `/mnt/snap/cache` (RAM-only without a store). Layers of `sources.yaml`: see [sources.md](sources.md).

## 11. Kernel command line (`omloader.*`)

Ventoy cannot pass kernel parameters; use `boot.yaml` (`boot.extra_cmdline_flags`) instead. When the loader's own GRUB runs (stick without Ventoy, or editing the entry with `e`), put flags in the kernel line or in `/boot/grub/extra.cfg` (`set omloader_args="..."`).

| Flag | Meaning |
|---|---|
| `omloader.image=NAME` | Boot that image automatically (no TUI). |
| `omloader.snapshot=S` | With `omloader.image`: `current` (default), `base` or a snapshot label. |
| `omloader.ui=tui\|shell` | Console UI when nothing boots automatically (wins over `boot.ui`). |
| `omloader.poweroff_on_fail=1` | Power off if the automatic boot fails. |
| `omloader.experimental=oci` | Enable the experimental OCI support (same as `OML_EXPERIMENTAL=oci`). |
| `omloader.store=off` | Do not attach a store file (maintenance: offline check/grow). |
| `omloader.font=NAME\|auto\|default` | Linux console font ([tui.md](tui.md)). |
| `omloader.gfx=WxH\|off` | GRUB video mode (via `omloader_args`; not under Ventoy GRUB2 mode). |
| `omloader.live=/dev/X`, `omloader.isofile=/path.iso`, `omloader.isodev=LABEL=…\|UUID=…\|/dev/…` | Stage 1: pin the live medium / the ISO file location. |
| `omloader.selftest=1`, `omloader.hook=1`, `omloader.marker=NAME` | Test helpers (mount check + poweroff; run `hook.sh` from an `OMLSTAGE` disk; marker for the test guests). |

The loader's own kernel line is `console=tty0 console=ttyS0,115200 loglevel=4 systemd.show_status=auto`.

## 12. Environment variables

| Variable | Used by | Meaning |
|---|---|---|
| `OML_EXPERIMENTAL` | `omloader` | Comma separated experimental features; `oci` enables OCI images. |
| `OML_SOURCES` | `omloader` | Extra `sources.yaml` file merged above the stick/ISO copies. |
| `OMLOADER_VERSION` | TUI | Version shown in the header when the binary was built without a version. |
| `OML_VERSION` | `build/version.sh`, `make` | Override the build version (default: git tag, else `VERSION`). |
| `CONTAINER_ENGINE`, `DEBIAN_MIRROR`, `SOURCE_DATE_EPOCH` | `build/build-iso.sh` | `podman`/`docker`; Debian mirror (default `http://deb.debian.org/debian`); timestamp for stable output. |
| `OML_REMOTE`, `OML_REMOTE_PASS`, `OML_RDIR` | `Makefile` remote test targets | Optional ssh test host (no default); never commit credentials. |

## 13. CLI reference

Output of `omloader` without arguments (v0.1.x):

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
            [-c omloader.yaml] [--offline] [--force] [--no-probe] [--tmp DIR]
  omloader import lxd-vm PATH --name N --out IMAGES_ROOT [--no-probe]   (disk.qcow2 | dir with disk.qcow2 [+lxd.tar.xz])
  omloader source list|test [NAME] [--mirror M] [-c omloader.yaml] [--offline]   (M: official | auto | a server id from sources.yaml; docs/sources.md)
  omloader source ref IMAGE [--source dockerhub] [--mirror M] [-c omloader.yaml]    (Docker Hub image reference rewritten for a proxy mirror; experimental)
  omloader cache ls|gc [--max-size-gb N] [-c omloader.yaml]
  omloader net status [-c omloader.yaml] [--offline]
  experimental (disabled by default; --experimental-oci, OML_EXPERIMENTAL=oci or experimental.oci_images: true):
    omloader import oci-dir|oci-archive|docker-archive PATH --name N --out IMAGES_ROOT ...   and  boot of type: oci images
```

`omloader snapshot` (no arguments):

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

Store file commands: see [store-file.md](store-file.md). `omloader version` prints the embedded version. Exit codes: 0 ok, 1 error. Commands that mount, loop-attach or `kexec` need root (the loader shell is root).

## 14. Troubleshooting / FAQ

- **Black screen / no menu after choosing the ISO.** Secure Boot is probably on: disable it. Try Ventoy's other boot mode (normal ↔ GRUB2). Ventoy's menu is VGA only; the loader's GRUB and boot log also go to the serial console
  (`console=ttyS0,115200`). The framebuffer console picks a font to fit the resolution (`omloader.font=`, `console.conf`). *Real-hardware behaviour is untested.*
- **"live medium not found" / rescue shell in stage 1.** The loader could not find its ISO: keep the ISO on exFAT, ext4, xfs or FAT32 (not NTFS), at `/omloader.iso`, `/omloader/` or `/ISO/`, or pin it with `omloader.isofile=`.
- **The console resolution drops after boot in a VM (e.g. 1920x1080 -> 1280x800).** The VM's display driver replaced the firmware framebuffer. Choose the GRUB entry "safe display" (`nomodeset`), or set a fixed video mode in the VM; on real hardware the native driver picks the mode. Details: [ventoy.md](ventoy.md#console-resolution-tty1--vnc).
- **Secure Boot.** Not supported (no signed shim/kernel). Disable it in the firmware setup.
- **NTFS.** The loader kernel has no `ntfs3`; NTFS data partitions are mounted with ntfs-3g (FUSE). If Windows left the volume hibernated (Fast Startup) or dirty, it is mounted **read-only**: images are readable, but a store file on it is not usable (snapshots stay in RAM). Fully shut Windows down (`shutdown /s /t 0`) or run `chkdsk`.
- **exFAT and sparse files.** exFAT cannot create sparse files, so `store create` and `ext4-file` data volumes allocate their whole size immediately (slow for 20G+); vfat files are limited to < 4 GiB. Pick a smaller `--size`, or use an `OMLOADER` ext4 partition.
- **Snapshots vanish at poweroff.** No store was found (`SNAPOK=0` in the log): create a store file or an `OMLOADER` partition ([section 3](#3-put-the-iso-on-a-ventoy-stick)).
- **VM guest: `update-grub`/ESP errors.** The guest's `/boot/efi` is deliberately not mounted (`boot.vm_fix_esp_fstab`): the guest is started by `kexec` and never uses the ESP; tools that write there have nothing to write to.
- **A VM image does not boot / "no ext4/xfs root partition".** Supported: UEFI/GPT disk, ext4 or xfs root with the kernel and initrd under `/boot` (set `kernel.path`/`kernel.initrd`, `kernel.root_partition`); no LVM/LUKS root; verified only on Debian-style (initramfs-tools) guests.
- **Pull is slow or fails.** Use a mirror: `omloader source test`, `omloader pull … --mirror auto`, `mirror_policy.region_preset: cn`, or set `network.proxy`. Details in [sources.md](sources.md). `--offline` uses only the cache.
- **Network does not come up.** DHCP is configured for wired `en*`/`eth*` only; check `networkctl`, `omloader net status`. Wi-Fi is not configured.
- **Forgot the login / want it gone.** See [section 6](#6-default-account-and-security).
- **Where are the logs?** Serial console and `journalctl`; `OMLOADER_LOADER:` and `OMLOADER_STAGE1:` lines summarise detection.

## 15. Limits

Alpha, QEMU/OVMF-tested only. UEFI x86_64 only; no Secure Boot; Linux guests only; VM guests: ext4/xfs root with `/boot`, no LVM/LUKS, verified on Debian-style guests only, no independent data volumes, no clean unmount of the store at poweroff;
container console login is implemented for systemd guests (others get a busybox debug shell); no hot snapshots; dm-snapshot VM snapshots cannot be merge-deleted (use `flatten`); Wi-Fi unconfigured; pull supports Simplestreams sources only (sha256, no GPG);
Docker/OCI images are experimental and off by default. The complete list is in [DEVELOPMENT.md](DEVELOPMENT.md#known-limitations).
