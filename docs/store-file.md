# Store-file mode: the snapshot/data store as an ext4 image file

By default the loader keeps snapshots (`tree.json`, dm-snapshot COW files, overlay uppers), data volumes and the pull cache on an ext4 partition
labelled `OMLOADER`. **Store-file mode** puts the same ext4 filesystem into an *image file* on the Windows-readable host partition
(the Ventoy data partition, label `VTOYDATA`, exFAT or NTFS), so no separate Linux partition is needed. The design is unchanged
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
      glob: [/omloader/store.img, /ventoy/*.dat]  # Ventoy .dat convention; label must be OMLOADER
      host_match: {label: VTOYDATA, uuid: ""}     # uuid wins over label
      size: 20G                                   # default for `omloader store create`
      prealloc: auto                              # auto: yes on exFAT/vfat (no sparse files), no elsewhere
```

The loader reads `/omloader.yaml` from the root of the host partition if present (`-c`). The host partition is the `VTOYDATA`-labelled one;
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
| NTFS | Debian bookworm's 6.1 loader kernel has **no ntfs3**: the loader uses **ntfs-3g (FUSE)**; if a kernel with `ntfs3` is present it is preferred. A *dirty* or *hibernated* volume (Windows **Fast Startup**, hibernation, unclean removal) refuses read-write: the loader mounts it **read-only** and prints a warning. Images stay readable, but the store file cannot be used (snapshots stay ephemeral). Fix: shut Windows down fully (`shutdown /s /t 0`, disable Fast Startup) or `chkdsk`/`ntfsfix`, then reboot. The ntfs-3g daemon runs as `@ntfs-3g` (systemd "root storage daemon" convention) so it survives the container root switch, and is terminated cleanly at the end of the guest's shutdown hook. |
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
