# omloader live shell banner (interactive login shells only)
case $- in *i*) ;; *) return 0 2>/dev/null || exit 0;; esac
export PS1='omloader# '
if [ -z "$OMLOADER_BANNER_SHOWN" ]; then
  export OMLOADER_BANNER_SHOWN=1
  . /run/omloader/env 2>/dev/null
  cat <<BANNER

=== OCI Metal Loader (omloader) live shell ===
  omloader boot --image <name> [--snapshot <n>] --images-root ${IMAGES_ROOT:-/mnt/images/images} --snap-root /mnt/snap/snapshots [--dry-run]
  omloader snapshot list|create|rollback|delete|prune|verify|flatten ...   (omloader snapshot for all operations)
  mounts: /mnt/snap (OMLOADER) /mnt/images (VTOYDATA)    network: networkctl / resolvectl     poweroff: poweroff -f
===============================================
BANNER
  echo "OMLOADER_SHELL_READY uptime=$(cut -d' ' -f1 /proc/uptime)"
fi
