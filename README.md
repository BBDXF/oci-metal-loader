# OCI Metal Loader (`omloader`)

A Ventoy-style UEFI x86_64 bare-metal (“metal”) loader for **LXD images**: put LXD images on a USB stick/disk, boot the loader ISO and run them
directly on the hardware, without installing anything.

- **LXD VM images** (disk images): `dm-snapshot` stack over the read-only base + `kexec` into the guest's own kernel.
- **LXD container images** (`rootfs.squashfs`): squashfs + overlayfs on the loader's kernel, then `switch-root` into the container's init.
- Snapshots (tree, rollback, prune, flatten, export), persistent data volumes, and online pull (M4) with mirrors/proxy, a content-addressed cache and an offline mode.

**Docker/OCI images are experimental only**: the code exists (import of oci-dir/oci-archive/docker-archive, boot as a container) but it is
**disabled by default**, not part of the project focus and not planned to be extended (there will be no registry client). Reason: Docker/OCI images are
application-centric (no init system, no users/login/network setup, one process), whereas LXD images are full systems that boot like a distribution.
To opt in: `experimental.oci_images: true` in `omloader.yaml`, `--experimental-oci` on `import`/`boot`, `OML_EXPERIMENTAL=oci` in the environment, or
`omloader.experimental=oci` on the loader kernel command line. Without it, `type: oci` images are rejected by config validation, `omloader import oci-*|docker-archive` and `omloader boot` refuse.

> Disclaimer: this project is not an official Open Container Initiative (OCI) project and is not affiliated with or endorsed by the OCI
> (or by the LXD/Canonical projects). "OCI" in the name is historical; the supported focus is LXD images.

## Project status / roadmap

| Milestone | Scope | State |
|---|---|---|
| M0 | config layers, schemas, validation, snapshot tree metadata | ✅ done |
| M1 | VM boot: dm-snapshot + kexec, guest-side reassembly | ✅ done (QEMU/OVMF-tested) |
| M2 | snapshot management: list/create/rollback/delete/gc/prune/verify/flatten/export, space guard, container data volumes | ✅ done |
| M3 | container boot: squashfs + overlayfs, firstboot adapter, `systemctl switch-root` handoff | ✅ done (the OCI part is now experimental, off by default) |
| Live loader | Debian bookworm live system (systemd, networkd, resolved, tools, firmware), 254 MiB ISO | ✅ done |
| M4 | online pull for LXD: simplestreams client, mirrors/proxy, cache/offline mode, LXD VM import (qcow2 → raw) | ✅ done (QEMU-tested: `make e2e-pull`) |
| Usability | container console login (tty1 + ttyS0 autologin, users/passwords/sudo/ssh keys, busybox debug-shell fallback) and clean shutdown of the OMLOADER/VTOYDATA partitions | ✅ done for containers (VM: see limitations) |
| M5 | device isolation, real-hardware testing, Secure Boot, LUKS | ⬜ |
| TUI | interactive menu | ⬜ |
| Windows | VHDX differencing disks | ⬜ last |

Rough overall estimate: M0–M4, the Debian live loader and container usability are done; about **70-75 %** of the planned scope. The rest is real-hardware
work (M5), Ventoy integration checks, polish and optimisations.

### Next steps (queued, not started)

1. **Ventoy compatibility check**: boot `omloader.iso` through Ventoy (normal and GRUB2 mode) in QEMU/OVMF; stage 1 should also be able to find the ISO *file* on a data partition (`findiso`-style) instead of relying on the `OMLOADER_LIVE` label of a mapped device.
2. **Store-file mode**: an ext4 image file (Ventoy `.dat` convention, label `OMLOADER`) on an exFAT/NTFS partition as the snapshot/data store, so no separate Linux partition is needed (NTFS: dirty-flag / read-only fallback).
3. **Installer** (`omloader install`, UEFI/GPT, free space only) to run from the loader on an internal disk; Ventoy2Disk can already install Ventoy itself to a local disk.
4. **Optimisations** (deliberately postponed until the core is finished): COW size margin (dm-snapshot needs ~0.4 % more than the base size in the worst case; currently the COW is sized exactly to the base) and usage warnings; storing `base.raw` inside squashfs to save space on exFAT (no sparse files); VM data volumes; hot snapshots from a running guest; more LXD images in a compatibility matrix (currently one container and one VM fixture only); LXD template rendering / cloud-init.
5. M5 (device isolation, real hardware, Secure Boot, LUKS), TUI, Windows VHDX differencing disks.

## Known limitations

- **VM guests: no account/console provisioning and no clean unmount of the OMLOADER partition.** `runtime.users`/`autologin`/`debug_shell`/`shutdown_sync` are container-only: a VM boots its own
  disk image through kexec and the loader never edits the guest's root disk. Its COW files live on the OMLOADER partition that the guest's *initramfs* mounted (`/omlmnt-*`); that mount is
  kept busy by the loop/dm-snapshot stack under the guest root, so it can be neither unmounted nor remounted read-only by the guest at poweroff (the loader cannot hook the guest's shutdown
  without modifying the guest's root filesystem). After a VM poweroff the OMLOADER ext4 is therefore left needing journal replay (the next loader mount replays it; the guest's own `sync` at
  shutdown makes data loss unlikely, an abrupt power cut can still lose the last writes). Workaround: `poweroff` from the guest's normal shutdown path, then boot the loader once (it replays the journal).
- **Container console login is only implemented for systemd guests** (autologin drop-ins); sysvinit/busybox-init/OpenRC guests get the busybox debug shell (`debug_shell`) and best-effort shutdown hooks
  (sysvinit `K99omloader-sync`, busybox-init `::shutdown:`; OpenRC: none). The tty1 console is only useful with a display; with `console=tty0 console=ttyS0,115200` (the loader's grub.cfg) the guest's `/dev/console` is ttyS0.
  Removing a user from `runtime.users` does not delete the account (accounts are only created/updated); passwords in the image config are only hashed on the loader (prefer `password_hash`).
- **VM path has no independent data volumes**: all guest data lives in the COW layer of the snapshot node (data volumes exist for containers only).
- **VM path is verified only on the Debian/initramfs-tools fixture.** The `rdinit` guest reassembly is generic in principle but untested elsewhere; no Windows guests, no LVM or encrypted (LUKS) root.
- **Secure Boot is not supported** (unsigned GRUB/kernel chain); kexec has restrictions under Secure Boot / kernel lockdown.
- **Only QEMU/OVMF has been tested, no real hardware yet** (NIC/disk/USB enumeration timing, firmware quirks and kexec on real drivers are unknown).
- **The live system's upper layer is tmpfs (RAM)**: `/tmp`, flatten staging and the like use memory, so flattening big images needs enough RAM.
- **Wi-Fi is not configured**: iwd and wireless-regdb are installed, nothing sets them up; wired DHCP only.
- **Online pull**: only Simplestreams sources (images.linuxcontainers.org layout); SHA256 verification only (no GPG: the index/catalog is not signed-checked, blobs are digest-checked against it; a malicious *mirror* could therefore lie in the index when the primary is unreachable — a warning is printed). No Wi-Fi, no registry/Docker pull. The VM kernel probe needs root (losetup/kpartx/mount) — inside the loader, or use `--no-probe`.
- **Data persistence rules**: data persists only when you boot the same snapshot node and the OMLOADER partition is writable. `--snapshot base` creates an ephemeral `tmp-*`
  child (removed with `snapshot gc`), `new:<name>` starts a fresh child; volumes declared `snapshot: false` live outside the layers and survive rollback.

## Architecture of the loader ISO

    UEFI -> GRUB -> kernel + stage-1 initramfs (busybox + modules)          build/stage1-init
              -> live medium (label OMLOADER_LIVE): live/live.squashfs (Debian bookworm userspace, zstd)
                                                    live/omloader.squashfs (omloader binary, units, scripts)
              -> overlay (squashfs layers + tmpfs upper) -> switch_root -> systemd (PID 1)
    systemd: omloader-mounts.service   (OMLOADER ext4 -> /mnt/snap, VTOYDATA -> /mnt/images, <=15 s wait if omloader.* flags)
             omloader-autoboot.service (omloader.hook / selftest / image= : `omloader boot` -> kexec (VM) or systemctl switch-root (container))
             systemd-networkd (DHCP on en*/eth*) + systemd-resolved, serial-getty/getty autologin root + banner  (iwd installed, not configured yet)

The loader is a real minimal Debian 12 system (systemd, udev, kmod, e2fsprogs, exfatprogs, xfsprogs, lvm2/dmsetup/kpartx, cryptsetup, parted/gdisk,
kexec-tools, qemu-utils, iproute2, curl + ca-certificates, iwd + wireless-regdb, kernel + firmware), so networking/TLS/mirrors (M4), Wi-Fi, a TUI and
diagnostics have a proper userspace. Kernel command line flags (all optional): `omloader.image=NAME`, `omloader.snapshot=S`, `omloader.poweroff_on_fail=1`,
`omloader.selftest=1`, `omloader.hook=1` (test helper: runs `hook.sh` from an `OMLSTAGE` ext4 disk, then powers off), `omloader.live=/dev/X` (stage 1 medium override).
Without `omloader.image=` you get the autologin root shell with a banner. Details: [build/README.md](build/README.md); the Debian package set and
licensing: [build/THIRD-PARTY.md](build/THIRD-PARTY.md) (`make licenses`).

## Related projects

No existing project covers the whole idea (Ventoy-style boot of OCI / LXD / VM image files on bare metal, no install, with snapshots and data volumes).
Closest ones: [Ventoy](https://github.com/ventoy/Ventoy) and [vtoyboot](https://github.com/ventoy/vtoyboot) (boot disk image files from USB, no OCI/snapshots),
[Kairos](https://github.com/kairos-io/kairos), [bootc](https://github.com/bootc-dev/bootc) and [ABRoot](https://github.com/Vanilla-OS/ABRoot) (OCI images as OS with A/B rollback, but installed to disk),
[LinuxKit](https://github.com/linuxkit/linuxkit) and [u-root](https://github.com/u-root/u-root) (in-memory initramfs / kexec).

## License

GPL-3.0-or-later, the same license family as Ventoy. See [LICENSE](LICENSE). Copyright (C) 2026 the oci-metal-loader authors.

## Config layers
`built-in defaults (configs/defaults.yaml, embedded)` < `omloader.yaml` < `image.yaml`,
deep-merged per field. `snapshot.max_chain_depth` seeds `snapshots.max_chain_depth` of the image.
Validation errors include `file:line`. Schemas: `schema/*.schema.json`.

## Usage
    go run ./cmd/omloader config validate -c examples/omloader.yaml -i examples/ubuntu-24.04/image.yaml
    go run ./cmd/omloader config show --resolved -c examples/omloader.yaml   # value  # layer (file:line)

    go run ./cmd/omloader snapshot create --root /tmp/r ubuntu snap1 [--from base|<name>]
    go run ./cmd/omloader snapshot list|rollback|delete --root /tmp/r ubuntu [name]

Tree is stored at `<root>/<image>/tree.json`. Exit codes: 0 ok, 1 error.

## Test
    gofmt -l . && go vet ./... && go test ./...

## Boot (M1: VM, dm-snapshot, UEFI x86_64)
    omloader boot --image NAME [--snapshot NAME|current|base|new:NAME] [-c global.yaml]
        [--images-root DIR] [--snap-root DIR] [--dry-run] [--no-kexec] [--exec-cmd-log FILE]

Image dir `<images-root>/<name>/image.yaml`; tree at `<snap-root>/<name>/tree.json` (created with base only if missing).
Flow: verify base sha256 (skipped if `verify.base_checksum: skip` or no sha256) -> `losetup -r` base ->
per chain node: sparse COW file (truncate to base size if missing) + `losetup` + `dmsetup create oml-<image>-<node>`
(`0 <sectors> snapshot <origin> <cow> P 8`, origin = previous layer) -> `kpartx -a` -> pick root partition
(`kernel.root_partition` N, else first ext4/xfs via blkid) -> mount ro -> `kexec -l` (`{ROOT}` in cmdline is
replaced by the root partition device) -> `kexec -e` unless `--no-kexec`/`--dry-run`.
`--dry-run` prints the exact command list; `--exec-cmd-log` appends executed commands to a file.
`new:<name>` creates a child of the current snapshot (metadata saved) and boots it with a fresh COW.
Base-only boot uses the read-only loop device directly.

### Snapshot targets and writability
Only the top layer of the stack is written; lower COW files are frozen. Therefore:
- booting an existing snapshot that has children is refused (clear error) unless `--force`;
- `--snapshot base` (or `current` resolving to base) boots an **ephemeral child of base** named `tmp-<UTC timestamp>` (metadata `current` is left unchanged; remove with `snapshot delete`), unless `--ro`, which boots the base read-only through a `linear` readonly dm map;
- `--snapshot new:<name>` creates a child of `current` and boots it.

### Surviving kexec: guest-side reassembly (v1: Debian/Ubuntu, initramfs-tools)
The loader's loop/dm devices die at `kexec -e`, so `boot` appends an extra gzip'd newc cpio to the guest initrd
(concatenated archives are supported by the kernel) and rewrites the cmdline: `root=/dev/mapper/<part> rw|ro rdinit=/omloader/preinit omloader.plan=/omloader/plan.json`
(any `root=`/`ro`/`rw` from the image cmdline is replaced). `--no-inject` disables this.
The extra initrd contains `/omloader/omloader` (copy of the running static binary), `dmsetup kpartx losetup blkid insmod`
with their shared libs and ld.so (run via the bundled loader, so guest libc is irrelevant), kernel modules
(loop, dm-mod, dm-snapshot, ext4, xfs, exfat, vfat, nvme/virtio/sd/ahci/usb storage and deps) taken from the guest's
`/lib/modules/<kver>`, `/omloader/plan.json` and `/omloader/preinit`. `preinit` (kernel `rdinit=`) runs
`omloader guest-assemble --plan /omloader/plan.json` and then `exec /init`, i.e. it runs *before* the guest's own
initramfs scripts (deviation from an init-premount hook: initramfs-tools `ORDER` files are generated per image and
cannot be safely extended from a second cpio). `guest-assemble` loads the modules, finds the storages by UUID/label
(`blkid -U/-L`), mounts them (rw if they hold a COW file, else ro), recreates the loop + dm-snapshot stack with the same
names, runs `kpartx` and waits for the root partition node. Before `kexec -e` the loader tears down its own
mount/kpartx/dm/loop devices and syncs so COW headers are consistent.
dracut-based guests are not supported yet (the `rdinit` approach would likely work but is untested).
Kernel/initrd paths are resolved *inside* the image root (absolute symlinks like `/boot/vmlinuz -> /boot/vmlinuz-X` work; no escaping the root).
`kernel.inject_modules` is ignored (warning). Storage filesystems stay mounted in the initramfs after switch_root (not cleanly unmounted at poweroff; see Known limitations: nothing on the guest disk may be changed, so the VM path has no shutdown hook).

`cache.offline`: `auto` | `on` (never go online) | `off` (always check upstream).

## Containers: `type: lxd-container` (and experimental `type: oci`) (M3: squashfs + overlayfs, loader kernel, no kexec)
`omloader boot --image NAME` dispatches on `image.yaml` `type`. `vm` = M1 above. Container images (`format: squashfs`,
`snapshots.mode: overlayfs`) run on **the loader's own kernel**; there is no kexec and no kernel/initrd in the image.

Flow: verify sha256 -> `mount -t squashfs -o loop,ro base.squashfs /run/omloader/base` -> per tree node
`<snap-root>/<image>/<node>/{upper,work}` (on the ext4 OMLOADER partition; `work` must be on the same fs as `upper`) ->
`mount -t overlay -o lowerdir=<parent uppers newest-first>:/run/omloader/base,upperdir=<node>/upper,workdir=<node>/work /newroot`
-> firstboot adapter -> bind loader modules -> write `/run/omloader-switch` and return 0 (line 1 = new root, then init and args, one per line).
The loader is a systemd system, so `omloader-autoboot.service` (`build/live/rootfs/usr/local/lib/omloader/autoboot.sh`) does the root switch:
it writes a tiny wrapper `/.omloader/switch-init` into the new root (`#!/.omloader/busybox sh` + `exec <init> <args>`), mounts a fresh tmpfs on `<newroot>/run`
and calls `systemctl switch-root /newroot /.omloader/switch-init`. PID 1 kills the live system's processes, moves /proc /sys /dev into the new root,
pivots (the old live root is detached; the overlay holds private clones of its lower/upper mounts, so the OMLOADER partition stays in use) and execs the wrapper,
which execs the guest's init. Using a wrapper as "init" makes systemd start the guest init *fresh* (otherwise a systemd guest would `--deserialize` the live system's state).
Snapshot semantics are the same as M1: `new:<name>` creates a child, `base` boots an ephemeral `tmp-<ts>` child, booting a node
that has children is refused unless `--force` (its upper is a lower layer of the children), `--ro` boots the squashfs read-only.
Limits: kernel allows 500 lower layers and the mount option string must fit one page: `omloader` refuses (> `snapshots.max_chain_depth`,
> 500 lowers or > 4000 option bytes) and suggests `omloader snapshot flatten`.
`data:` entries with `backend: dir` are bind-mounted from `<snap-root>/<image>/data/<name>` (other backends: TODO).

### Firstboot adapter (marker `/.omloader-adapted`, written into the node's upper; skipped when already present in the stack)
`/etc/hostname` (runtime.hostname, else image name), `/etc/hosts` (if missing), `/etc/fstab` (no entries), `/etc/resolv.conf`
(loader's if it has nameservers, else 1.1.1.1/8.8.8.8; an existing symlink is replaced, never followed outside the root),
`runtime.network: dhcp`: systemd guests get `/etc/systemd/network/80-omloader-dhcp.network` (Type=ether, DHCP=ipv4) and
systemd-networkd enabled; others get `/.omloader/net.sh` (loader busybox `udhcpc`) + a sysv `omloader-net` script (best effort).
`runtime.env` -> systemd `DefaultEnvironment`. Console login, accounts, debug shell and shutdown hooks: next section.
`/.omloader/busybox` (copy of the loader's static busybox, ~1MB) is installed for the wrapper below.
Init selection: `runtime.entrypoint` (+`cmd`) -> `/sbin/init` -> `runtime.cmd` -> `/bin/sh` (warning). If the chosen command's basename is
`init`/`systemd` it is exec'd directly; otherwise it is wrapped by `/.omloader/entrypoint` (static-busybox shell script: mounts
proc/sys/dev/devpts/run, applies `runtime.env`/`workdir` from `/.omloader/env`, starts DHCP, `exec "$@"`; the app becomes PID 1).
Guest `/proc /sys /dev` are moved from the loader; systemd/sysv mount the rest themselves.

### Console login and clean shutdown (containers; part of the firstboot adapter)
Written offline into the node's upper (symlink-safe: every path is resolved inside the new root and never escapes it) and **re-applied whenever the hash of these keys changes**
(the hash is stored as `ui-config=` in `/.omloader-adapted`; unchanged config => no-op, so accounts are never duplicated; editing the config and booting again updates them).

    runtime:
      autologin: true            # getty@tty1 AND serial-getty@ttyS0 autologin (systemd guests); default false
      autologin_user: alice      # default root
      root_password_hash: '$6$...'   # or root_password: plain text (hashed with SHA-512 crypt by the loader, warned about, never logged)
      users:
        - name: alice
          password_hash: '$6$salt$...'   # `openssl passwd -6`; or password: <plain> (discouraged)
          groups: [sudo, users]          # missing groups are created; members are added in /etc/group (+gshadow)
          shell: /bin/bash               # default /bin/bash if present, else /bin/sh
          sudo: true                     # /etc/sudoers.d/90-omloader-alice (0440): NOPASSWD if no password is configured, else password required
          ssh_authorized_keys: ["ssh-ed25519 AAAA... alice@host"]   # ~/.ssh/authorized_keys 0600 (rewritten from the config)
      debug_shell: auto          # auto | always | never (see below)
      shutdown_sync: true        # default true

Accounts: `/etc/passwd`, `/etc/shadow`, `/etc/group`, `/etc/gshadow` are edited in place (modes/owners kept; uid/gid allocated from 1000, a same-named private group is created,
`/etc/skel` is copied, home directory and keys are chowned by numeric id). Existing users keep uid/gid/home and only get the changed fields.
Autologin uses the guest's own `agetty` (`--autologin <user>`); a `getty.target.wants/getty@tty1.service` link is added so tty1 always has a getty; ttyS0 is spawned by systemd's getty generator
because the loader passes `console=tty0 console=ttyS0,115200` (the guest shares the loader kernel command line).
**Debug shell fallback**: if the guest has no usable getty+login (no `agetty|getty` and `login`; a guest `busybox` counts for both) `debug_shell: auto` starts the omloader static busybox
(`/.omloader/dbgshell`) as a respawning shell on `/dev/console`: a `omloader-debug-shell.service` for systemd, a `::respawn:` line for busybox init, a `respawn` line for sysvinit;
with the entrypoint wrapper (no init) only `debug_shell: always` runs it in the background. The shell prints `OMLOADER_DEBUG_SHELL ...`.
**Clean shutdown**: before `switch-root`, `autoboot.sh` bind-mounts the OMLOADER and VTOYDATA partitions at `/.omloader/snap` and `/.omloader/images` inside the new root and records
`<name> <device> <fstype>` in `/.omloader/devs`. The adapter installs `/.omloader/sync.sh` (`sync`, `mount -o remount,ro`, `sync` per partition; if systemd-shutdown already detached the bind
mounts it mounts the device again, which shares the same superblock, and remounts that read-only) and runs it: systemd = `/usr/lib/systemd/system-shutdown/omloader-sync.sh`
(very end of poweroff/reboot) + `omloader-sync.service` (`ExecStop`, after all normal services are stopped); busybox init = `::shutdown:`; sysvinit = `/etc/init.d/omloader-sync` + `K99` links; OpenRC: none.
The remount marks the ext4 journal clean (`needs_recovery` cleared) and clears the exfat dirty flag. The loader's own poweroff paths in `autoboot.sh` (hook/selftest/failure) do `sync` + remount-ro too.
`shutdown_sync: false` disables the guest hooks (tests use it to reproduce the old behaviour).

### Kernel modules in the guest
The guest runs the loader kernel, so it needs the loader's modules to `modprobe`. The live system has the complete Debian kernel module tree
(plus firmware under `/lib/firmware`); `omloader` bind-mounts `/lib/modules/<uname -r>` **read-only** on `<newroot>/lib/modules/<uname -r>` (no copy).
The bind mountpoint dir is created in the upper layer. Other kernel versions in the guest are not supported.
After the root switch the OMLOADER ext4 stays in use by the overlay, so it can never be *unmounted* while the guest runs; instead it is made clean by remounting read-only (see "Console login and clean shutdown").

### Import (rootless, build-time helper; `oci-*`/`docker-archive` need the experimental opt-in)
    omloader import oci-dir|oci-archive|docker-archive|lxd PATH --name N --out IMAGES_ROOT [--force] [--autologin]
Writes `<out>/<name>/{base.squashfs,image.yaml}` (sha256 filled). OCI/docker layers (gzip or plain tar; zstd/xz layers are rejected) are merged
in Go (whiteouts `.wh.*`, opaque `.wh..wh..opq`, dir<->file replacement, hardlinks, symlinks, devices, uid/gid + xattr PAX records preserved)
and piped as a tar to `mksquashfs - OUT -tar` (no `-all-root`, so it runs unprivileged). Image config -> `runtime.{entrypoint,cmd,env,workdir}`
and `data:` (declared VOLUMEs -> `backend: dir`). `lxd`: PATH = directory (or `lxd.tar.xz`) next to `rootfs.squashfs`; the squashfs is copied and
`metadata.yaml` is read (architecture check; `.tar.xz` is read through `tar -xJOf`).

## Snapshot management (M2, both modes)
`snapshot` operations work on `<snap-root>/<image>/tree.json` **and** the real layers; the mode comes from `image.yaml` (`--images-root`):
dm-snapshot = `<node>.cow` sparse files (sized to the base), overlayfs = `<node>/{upper,work}`. If no `image.yaml` is found only the metadata is managed.
Common flags: `--root SNAPROOT --images-root IMGROOT -c omloader.yaml` (defaults from `storage.*.path`).

    omloader snapshot list <image> [--tree] [--sizes]     # current '*', frozen (has children), ephemeral, tags, note, created, apparent/allocated size
    omloader snapshot create <image> <name> [--from NODE|base] [--note T] [--tag X]...   # metadata + empty layer; parent data untouched
    omloader snapshot rollback <image> <node|base> [--discard]   # moves 'current'; --discard wipes a LEAF's own layer; frozen nodes are never wiped
    omloader snapshot delete <image> <node> [--recursive] [--force]   # refuses nodes with children / the current one; removes layers
    omloader snapshot gc <image> [--tmp] [--older-than 24h] [--dry-run]   # ephemeral tmp-* nodes (from `--snapshot base`)
    omloader snapshot prune <image> [--dry-run]     # snapshot.retention.keep_last newest leaves + tagged (keep_tagged) + current + all their ancestors survive
    omloader snapshot tag|untag <image> <node> <tag>...    omloader snapshot note <image> <node> <text>
    omloader snapshot df <image>      omloader snapshot verify <image> [--deep]
    omloader snapshot export <image> <node> --squashfs OUT | --raw OUT | --qcow2 OUT
    omloader snapshot flatten <image> <node> [--name NEW]
    omloader snapshot merge <image> <node>          # unsupported in v1 (validates leaf-only, then points to flatten)

- **Frozen nodes**: a node with children is a lower layer of them; it can be rolled back to (metadata) but `--discard`, boot (without `--force`) and writes are refused.
- **Chain depth**: `create` and boot refuse beyond `max_chain_depth` and suggest `flatten`.
- **Space guard**: `create` and `boot` (not `--dry-run`) refuse when the snapshot partition has less than `snapshot.retention.min_free_percent` free (statfs); nothing is created before the check. `snapshot df` shows the numbers.
  (The config key is the existing `snapshot.retention.min_free_percent`; there is no separate `snapshot.min_free_percent`.)
- **verify** (read-only, exit 1 on any problem): tree consistency (parents, cycles, current), `tree.json base_sha256` vs `image.yaml sha256` (`--deep` also hashes the base),
  missing/orphan layers, and for dm the COW header (`SnAp` magic, version 1, valid=1, power-of-two chunk size; an all-zero header is a fresh COW).
  `boot` runs a lite check first (base sha consistency + existence of all frozen ancestor layers) and refuses with a hint.
- **flatten** (needs root: loop/dm/mount, run it in the loader shell or via the hook mechanism): overlay = read-only overlay of base+uppers -> `mksquashfs`;
  dm = loop+dm snapshot stack (`oml-x-<image>-<node>`) -> sparse copy of the top device (Go, skips zero blocks; the loader's busybox `dd` has no `conv=sparse`) -> `base.raw`.
  Both create a NEW image `<images-root>/<image>-flat-<node>` (or `--name`) with a copy of `image.yaml` (new name + sha256) and an empty snapshot tree; the original is untouched.
- **export**: overlay -> squashfs of the node's own upper; dm -> raw disk image of base+chain, or qcow2 via `qemu-img convert` (shipped in the Debian loader; without it: clear error with a hint).

### Data volumes (containers)
`data:` entries of `type: oci|lxd-container` images are mounted into the new root *after* the overlay, outside the snapshot layers, so they survive rollback, delete, prune and flatten
(`snapshot: false`; `snapshot: true` is not implemented and warns). Backends: `dir` (bind of `<data-root>/<image>/<name>/`) and `ext4-file` (sparse `<data-root>/<image>/<name>.img`,
`mkfs.ext4` on first use, size from `size`, default 1G, loop-mounted). `<data-root>` = `--data-root`, default `<dirname snap-root>/data` (same partition). A flattened image gets its own, new data volumes.
`import --data name:/mount:backend[:size[:snapshot]]` declares/overrides volumes. **VM images: data volumes are not implemented (TODO)** (the boot warns): the guest is kexec'd
and the loader's loop devices die with it; it would need a guest-assemble post-hook that attaches the file and edits the guest fstab.

## Online pull (M4)
Commands (all CLI, `-c omloader.yaml` optional):

    omloader images list [--source S] [--type container|vm] [--os ubuntu] [--release noble] [--variant default]
    omloader pull lxc:ubuntu/noble[/variant] [--vm|--container] [--name N] [--out IMAGES_ROOT] [--offline] [--force] [--no-probe]
    omloader import lxd-vm DISK.qcow2|DIR [--name N --out ROOT] [--no-probe]
    omloader source list|test [NAME]      omloader cache ls|gc [--max-size-gb N]      omloader net status

- `pull` reads `streams/v1/index.json` + `images.json`, picks the latest version for amd64 (variant `default` unless given), downloads `lxd.tar.xz` + `rootfs.squashfs`
  (container) or `disk.qcow2` (VM) into the cache, verifies SHA256, and produces `<images-root>/<name>/{image.yaml,base.*}` (default name `<os>-<release>[-variant][-vm]`).
  Default `--out` inside the loader is the mounted images partition (`IMAGES_ROOT` from `/run/omloader/env`).
- VM import: `qemu-img convert -O raw`, then (unless `--no-probe`) the raw disk is attached read-only (`losetup`, `kpartx`), the ext4/xfs partition holding `/boot/vmlinuz*` is mounted
  read-only, and `kernel.path`/`initrd`/`root_partition` are written (symlinks resolved only inside the image). With `--no-probe` fill those fields by hand.
- Cache: `cache.path` (default `/OMLOADER/cache`; inside the loader that means `cache/` on the OMLOADER partition, mounted at `/mnt/snap`), blobs by sha256 (`blobs/aa/<sha>`), atomic rename,
  `Range` resume of `tmp/<sha>.part`, `cache gc` evicts least recently used blobs above `cache.max_size_gb`.
  `cache.offline`: `auto` (use cached index/catalog younger than `mirror_policy.cache_ttl_hours`, fall back to stale on network failure), `on` (never touch the network; also `--offline`,
  and implied by `policy.allow_online: false`), `off` (always re-check upstream metadata).
- Mirrors: `sources.<n>.mirrors` + `mirror_policy.strategy` = `fallback` (primary then mirrors), `fastest` (HEAD timing probe, health-checked order) or `mirror-only`; `sources.<n>.rewrite`
  (`from`/`to` URL prefix rules). **Mirrors are never trusted for content**: every blob is checked against the SHA256 from the index, a mismatch is discarded and the next server tried.
  `mirror_policy.region_preset: cn` adds the **unverified** presets `mirrors.tuna.tsinghua.edu.cn/lxc-images` and `mirror.nju.edu.cn/lxc-images` to `lxc-images`.
- Network: `network.proxy` (http/https/socks5), `no_proxy`, per-source `proxy` (`direct` disables), `extra_ca` (PEM added to the system pool), `timeout_sec`, `retries`. `policy.allowed_sources` restricts usable sources.

```yaml
network: {proxy: "socks5://127.0.0.1:1080", no_proxy: "10.0.0.0/8,.lan", extra_ca: /OMLOADER/corp-ca.pem, retries: 3}
mirror_policy: {strategy: fastest, region_preset: cn}
sources:
  lxc-images:
    type: simplestreams
    url: https://images.linuxcontainers.org
    mirrors: [https://mirror.example.org/lxc-images]
    rewrite: [{from: "https://images.linuxcontainers.org", to: "https://cache.lan/lxc"}]
cache: {path: /OMLOADER/cache, max_size_gb: 20, offline: auto}
policy: {allow_online: true, allowed_sources: [lxc-images]}
```
`make e2e-pull` (`test/e2e-pull.sh`): fake simplestreams tree on the remote host (python http.server, guest sees it as 10.0.2.2), dead primary + working mirror, pull + boot of a container and a VM,
then server stopped and `--offline` / `cache.offline: on` pulls from the cache that boot again.

## Tests (remote KVM)
`make e2e-snap` (`test/e2e-snap.sh`): snapshot management on the OCI fixture (create/list/stacking/rollback with data still on disk/delete refusal/gc/prune/verify/export/space guard/overlay flatten + boot of the flattened image/data volumes surviving rollback)
and on the VM fixture (create/list/verify/discard/prune --dry-run/qcow2 export/dm flatten + boot of the flattened VM). Snapshot commands that need root run in the loader through the `omloader.hook=1` stage disk.

## Usability test (remote KVM)
    make e2e-ui      # test/e2e-ui.sh: users/autologin on tty1+ttyS0, password+sudo checks, 2nd boot idempotency, busybox-only image (debug shell), ext4/exfat clean-state check, shutdown_sync:false baseline
The clean-state check reads the ext4 superblock (`s_state`, `needs_recovery`) and the exfat `VolumeDirty` flag straight from the disk image after the guest powered off.

## M3 test (remote KVM)
    export OML_REMOTE_PASS=...   # never stored
    make e2e-oci                # builds ISO, OCI fixture (podman, rootless), disk, then boots 3 times
See `test/make-fixture-oci.sh` and `test/e2e-oci.sh`; assertions: guest kernel == loader kernel, overlay root, counter 1 / 1 (ephemeral base) / 2 (persisted `run1`), adapter applied once, DHCP lease, modules visible.
