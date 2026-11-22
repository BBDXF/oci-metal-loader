# OCI Metal Loader (`omloader`)

A UEFI x86_64 bare-metal (“metal”) loader ISO that boots LXD VM, LXD container and OCI images, with snapshots
(dm-snapshot + kexec for VMs; squashfs + overlayfs on the loader's own kernel for containers).

> Disclaimer: this project is not an official Open Container Initiative (OCI) project and is not affiliated with or endorsed by the OCI.
> "OCI" here only refers to the image format it consumes.

## Status

Early development (CLI only, UEFI x86_64 only). Implemented: M0 (config/schema/validation), M1 (VM boot via dm-snapshot + kexec),
M2 (snapshot management, flatten/export, data volumes for containers), M3 (OCI / LXD container boot via squashfs + overlayfs).
Planned: M4 (online pull with registry mirrors/proxy, simplestreams), M5 (device isolation, real-hardware testing, Secure Boot, LUKS),
interactive TUI, Windows VHDX differencing disks (last).

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
`kernel.inject_modules` is ignored (warning). Storage filesystems stay mounted in the initramfs after switch_root (not cleanly unmounted at poweroff).

`cache.offline`: `auto` | `on` (never go online) | `off` (always check upstream).

## Containers: `type: oci` / `type: lxd-container` (M3: squashfs + overlayfs, loader kernel, no kexec)
`omloader boot --image NAME` dispatches on `image.yaml` `type`. `vm` = M1 above. Container images (`format: squashfs`,
`snapshots.mode: overlayfs`) run on **the loader's own kernel**; there is no kexec and no kernel/initrd in the image.

Flow: verify sha256 -> `mount -t squashfs -o loop,ro base.squashfs /run/omloader/base` -> per tree node
`<snap-root>/<image>/<node>/{upper,work}` (on the ext4 OMLOADER partition; `work` must be on the same fs as `upper`) ->
`mount -t overlay -o lowerdir=<parent uppers newest-first>:/run/omloader/base,upperdir=<node>/upper,workdir=<node>/work /newroot`
-> firstboot adapter -> bind loader modules -> write `/run/omloader-switch` and return 0. The loader `/init` then
`mount --move`s /dev /proc /sys /run into the new root and does `exec switch_root /newroot <init> [args]` itself
(it must be exec'd by PID 1, which is why `omloader boot` only writes the handoff file: line 1 = new root, then init and args, one per line).
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
`runtime.autologin: true`: serial-getty@ttyS0 autologin drop-in (tests/debug). `runtime.env` -> systemd `DefaultEnvironment`.
`/.omloader/busybox` (copy of the loader's static busybox, ~1MB) is installed for the wrapper below.
Init selection: `runtime.entrypoint` (+`cmd`) -> `/sbin/init` -> `runtime.cmd` -> `/bin/sh` (warning). If the chosen command's basename is
`init`/`systemd` it is exec'd directly; otherwise it is wrapped by `/.omloader/entrypoint` (static-busybox shell script: mounts
proc/sys/dev/devpts/run, applies `runtime.env`/`workdir` from `/.omloader/env`, starts DHCP, `exec "$@"`; the app becomes PID 1).
Guest `/proc /sys /dev` are moved from the loader; systemd/sysv mount the rest themselves.

### Kernel modules in the guest
The guest runs the loader kernel, so it needs the loader's modules to `modprobe`. The loader initramfs ships the modules listed in
`build/mk-iso-inner.sh` (`MODS`: storage, fs: overlay squashfs loop ext4 xfs exfat vfat..., NICs: virtio_net e1000 e1000e r8169 igb igc tg3 ...).
`omloader` copies `/lib/modules/<uname -r>` to tmpfs (`/run/omloader/modules`) and bind-mounts it **read-only** on
`<newroot>/lib/modules/<uname -r>` (an in-initramfs bind would be wiped by busybox `switch_root`'s old-root cleanup).
The bind mountpoint dir is created in the upper layer. Other kernel versions in the guest are not supported.
After switch_root the OMLOADER ext4 stays mounted under the hidden old root and is never cleanly unmounted (journal replay on next boot; guest `sync` on poweroff).

### Import (rootless, build-time helper)
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
- **export**: overlay -> squashfs of the node's own upper; dm -> raw disk image of base+chain, or qcow2 if `qemu-img` exists (the loader does not ship it: clear error with a hint).

### Data volumes (containers)
`data:` entries of `type: oci|lxd-container` images are mounted into the new root *after* the overlay, outside the snapshot layers, so they survive rollback, delete, prune and flatten
(`snapshot: false`; `snapshot: true` is not implemented and warns). Backends: `dir` (bind of `<data-root>/<image>/<name>/`) and `ext4-file` (sparse `<data-root>/<image>/<name>.img`,
`mkfs.ext4` on first use, size from `size`, default 1G, loop-mounted). `<data-root>` = `--data-root`, default `<dirname snap-root>/data` (same partition). A flattened image gets its own, new data volumes.
`import --data name:/mount:backend[:size[:snapshot]]` declares/overrides volumes. **VM images: data volumes are not implemented (TODO)** (the boot warns): the guest is kexec'd
and the loader's loop devices die with it; it would need a guest-assemble post-hook that attaches the file and edits the guest fstab.

## Tests (remote KVM)
`make e2e-snap` (`test/e2e-snap.sh`): snapshot management on the OCI fixture (create/list/stacking/rollback with data still on disk/delete refusal/gc/prune/verify/export/space guard/overlay flatten + boot of the flattened image/data volumes surviving rollback)
and on the VM fixture (create/list/verify/discard/prune --dry-run/qcow2 hint/dm flatten + boot of the flattened VM). Snapshot commands that need root run in the loader through the `omloader.hook=1` stage disk.

## M3 test (remote KVM)
    export OML_REMOTE_PASS=...   # never stored
    make e2e-oci                # builds ISO, OCI fixture (podman, rootless), disk, then boots 3 times
See `test/make-fixture-oci.sh` and `test/e2e-oci.sh`; assertions: guest kernel == loader kernel, overlay root, counter 1 / 1 (ephemeral base) / 2 (persisted `run1`), adapter applied once, DHCP lease, modules visible.
