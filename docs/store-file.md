# Store-file mode: the snapshot/data store as an ext4 image file

By default the loader keeps snapshots (`tree.json`, dm-snapshot COW files, overlay uppers), data volumes and the pull cache on an ext4 partition
labelled `OMLOADER`. **Store-file mode** puts the same ext4 filesystem into an *image file* on the Windows-readable host partition
(the loader's data partition: label `VTOYDATA`, else the partition holding the ISO; exFAT or NTFS), so no separate Linux partition is needed. The design is unchanged
(ext4 + dm-snapshot / overlayfs); only the container of the store is different.

## When to use it

- A Ventoy stick/disk with a single exFAT/NTFS partition where you do not want to (or cannot) add a Linux partition.
- A real ext4/xfs/btrfs partition labelled `OMLOADER` is still better (one filesystem layer less, no host-fs dirty-flag issues).

Precedence in the loader (`omloader-mounts.service`):

1. a real `ext4`/`xfs`/`btrfs` partition labelled `OMLOADER` wins (a non-Linux volume that carries that label is ignored);
2. otherwise an ext4 **store file** on the host partition: `storage.snapshots.store_file` (`path`, then every `glob` match, sorted);
   **only files whose ext4 label is `OMLOADER` count** — Ventoy's own persistence `.dat` files (label `vtoycow` or similar) are never touched;
3. otherwise `SNAPOK=0`: `/mnt/snap` stays on the loader's RAM filesystem (ephemeral, nothing persists).

## Configuration

```yaml
storage:
  snapshots:
    store_file:
      enabled: true
      path: ""                                    # exact file inside the host partition (checked first)
      glob: [/omloader/store.img, /omloader/*.dat, /omloader/*.img, /ventoy/*.dat]  # in this order; label must be OMLOADER
      host_match: {label: "", uuid: ""}            # empty = the loader data partition (VTOYDATA, else the ISO partition); label/uuid restrict it
      auto_create: true                           # no store found + host writable: create omloader/store.img at boot
      size: 20G                                   # size of the auto-created store and `omloader store create` default
      prealloc: auto                              # auto: yes on exFAT/vfat (no sparse files), no elsewhere
```

The loader reads `omloader/boot.yaml` of the host partition if present (`-c`). The host partition is the data partition (`omloader.data=`, else `VTOYDATA`, else the partition holding the ISO);
`host_match` is verified against it (a mismatch skips the store file search).
Kernel command line: `omloader.store=off` = never attach a store file (maintenance, offline check/grow).

## Creating, inspecting, growing

Run these in the loader (they need root only for mkfs/loop on a block device; plain files need none). The host partition is mounted at `/mnt/images`.

    omloader store create --path /mnt/images/omloader/store.img --size 20G [--label OMLOADER] [--prealloc auto|yes|no] [--force]
    omloader store info   --path FILE      # host fs, size, allocated, label, uuid, state clean|dirty|errors, attached loop device
    omloader store check  --path FILE      # e2fsck -n; refuses while the loader has the file attached (--release detaches it)
    omloader store grow   --path FILE --size 30G [--release]   # offline: extend the file, e2fsck -fp, resize2fs. No shrinking.

`create` makes `mkfs.ext4 -F -L OMLOADER -m 1 -E lazy_itable_init=1,lazy_journal_init=1` (journal kept, crash safety). On exFAT/vfat the whole file
is allocated up front (`fallocate`, else writing zeros: slow for 20G, but exFAT has no sparse files — a "sparse" 20G file there would cost a
multi-GB zero fill anyway or fail on write when the partition is full). On ext4/xfs/btrfs/NTFS the file is sparse (`truncate`).
`state` reads the ext4 superblock: *dirty* means "not cleanly unmounted / journal needs recovery" (the next mount replays it).

## Filesystem caveats

| Host fs | Notes |
|---|---|
| exFAT | kernel `exfat`. No sparse files: the store is fully preallocated. A power cut can leave the exFAT dirty flag set (harmless for the kernel driver; Windows may offer a check). |
| NTFS | Debian trixie's 6.12 loader kernel is not relied on for **ntfs3**: the loader uses **ntfs-3g (FUSE)** (also in stage 1, so the ISO may sit on the same NTFS partition); if a kernel with `ntfs3` is present it is preferred. A *dirty* or *hibernated* volume (Windows **Fast Startup**, hibernation, unclean removal) refuses read-write: the loader mounts it **read-only** and prints a warning. Images stay readable, but the store file cannot be used (snapshots stay ephemeral). Fix: shut Windows down fully (`shutdown /s /t 0`, disable Fast Startup) or `chkdsk`/`ntfsfix`, then reboot. The ntfs-3g daemon runs as `@ntfs-3g` (systemd "root storage daemon" convention) so it survives the container root switch, and is terminated cleanly at the end of the guest's shutdown hook. |
| vfat | Files are limited to < 4 GiB: `store create/grow` refuse larger sizes. Fully preallocated. |
| ext4 / xfs / btrfs | Works, sparse. Pointless if you can simply label a partition `OMLOADER`. |
| BitLocker / encrypted | Not supported (no LUKS/BitLocker handling yet). |

## Reliability notes

- **Double filesystem layering**: host fs -> loop -> ext4. A host power cut can lose recently written store data that the host cache had not flushed,
  and the host fs and the ext4 journal recover independently. ext4 (journal) inside is robust; the host fs is the weaker link (exFAT/FAT have no journal).
- **Clean shutdown, containers**: the guest's shutdown hook (`/.omloader/sync.sh`) flushes and remounts the **store ext4 read-only first**, then
  syncs the host. The host remount-ro is refused while the loop device still has the file open (ext4 stays mounted by the root overlay), so the host fs is
  made consistent by `sync` (exFAT clears its dirty flag on sync) and, for NTFS, by terminating the ntfs-3g daemon. Verified by `make e2e-store`.
- **Clean shutdown, VMs**: the guest is kexec'd into its own kernel and its disk must not be modified, so it has no omloader shutdown hook:
  the store ext4 and the host exFAT are left "dirty" after a VM poweroff (the next loader mount replays the ext4 journal; `e2fsck -n` is clean after replay).
- **Loader poweroff** (`poweroff` in the loader shell): `omloader-mounts.service` stops in the right order: `umount /mnt/snap` -> `losetup -d` -> `umount /mnt/images`.
- `omloader store grow` is offline (detached from the loader). Do not copy the file while a guest is running.
- Guest VM path: the guest initramfs mounts the host partition **read-write**, attaches the store file through `losetup` and mounts it, then builds the
  dm-snapshot stack as usual. It needs `loop` and the host fs driver (`exfat`/`ntfs3`/`vfat`/...) and `ext4` from the **guest's own kernel modules**; if one
  is missing `omloader boot` fails with a message naming the module. A guest kernel without `ntfs3` cannot use a store on NTFS (exFAT is fine).

## The Ventoy `.dat` convention

Ventoy persistence uses ext4 image files (`*.dat`, created with `CreatePersistentImg.sh`) in `/ventoy/` on the data partition. The default glob
`/ventoy/*.dat` lets you drop such a file in place, but it is only used when its ext4 label is `OMLOADER`
(`CreatePersistentImg.sh -l OMLOADER` or `omloader store create`); other persistence files are left alone. The default location `/omloader/store.img` is checked first.

## Discovery, precedence, auto-create (v0.2.2)

- **Precedence:** a real ext4/xfs/btrfs partition labelled `OMLOADER` > a store file > RAM only (ephemeral).
- **Where files are searched** (host = data partition), in this order, first match wins: `omloader/store.img`, `omloader/*.dat`, `omloader/*.img`
  (sorted), `ventoy/*.dat`. Only a file whose **ext4 label is `OMLOADER`** counts (label an existing one with `e2label FILE OMLOADER`, or create it
  with Ventoy's `CreatePersistentImg.sh -l OMLOADER`). Ventoy's own `vtoycow` persistence files are never touched. The chosen file and the reason
  every other candidate was skipped are in the loader log, `omloader store find` (stderr) and the TUI Storage page.
- **Auto-create:** with no OMLOADER partition and no valid store file the loader creates `omloader/store.img` (ext4, label OMLOADER, same code as
  `omloader store create`) in the **background** (`omloader-store-create.service`, writes `store.img.new`, renames when done; the TUI never waits), printing `OMLOADER_LOADER: creating store ... N GiB`. Size = `storage.snapshots.store_file.size`
  (default `20G`; unrelated to the download-cache limit `cache.max_size_gb`). Config is read from `omloader/boot.yaml` on the data partition, else built-in defaults.
- **Guards (never fatal, fall back to RAM only):** skipped with `auto_create: false` or `omloader.store=off`; skipped if the host is read-only
  (NTFS dirty/hibernated: Windows fast startup/hibernation → shut down fully); needs free space >= size + 1 GiB, otherwise the size shrinks to what fits
  if that is >= 4 GiB, else skipped; idempotent (an existing store is reused; a leftover `.new` from an interrupted creation is deleted; an
  existing non-OMLOADER `store.img` is never overwritten). exFAT/vfat/NTFS are fully allocated: with in-kernel ntfs3 (stage 1 default; ntfs-3g only as fallback) via `fallocate` (instant, no data writes); zero-writing only when fallocate is unsupported.

### Background creation (v0.4.4) / 后台创建
- The loader works RAM-only while the store is created; progress `creating store: X/Y GB` (from `/run/omloader/store-create.status`) is on the Storage page. When done: "store ready, used from next boot" (no live re-attach). Starting an image (`omloader boot`) **cancels** a running creation and deletes the partial `store.img.new`; it restarts at the next boot. A stale `.new` is removed at start.
- 存储文件在后台创建（`omloader-store-create.service`，先写 `store.img.new`，完成后重命名），TUI 不再等待；创建期间快照仅在内存中，存储页显示 `creating store: X/Y GB`，完成后提示“store ready, used from next boot”（下次启动生效）。启动镜像会取消正在进行的创建并删除 `.new`，下次启动重新开始。NTFS 默认使用内核 ntfs3 并以 `fallocate` 分配（瞬间完成），ntfs-3g 仅作回退。
