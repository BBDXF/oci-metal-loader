#!/bin/bash
# Run ON THE REMOTE (or any host with rootless podman + mksquashfs with zstd). Builds out/omloader.iso
# Usage: build/build-iso.sh   (cwd = dir containing build/ and bin/omloader)
#   out/live.squashfs     Debian live userspace (cached in cache/ by image id: only rebuilt when the package set changes)
#   out/omloader.squashfs omloader binary + units/scripts (build/live/rootfs), rebuilt every time (small)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
[ -x "$ROOT/bin/omloader" ] || { echo "missing $ROOT/bin/omloader (static binary)"; exit 1; }
mkdir -p "$ROOT/out" "$ROOT/work" "$ROOT/cache"
MIRROR=${DEBIAN_MIRROR:-http://mirrors.ustc.edu.cn/debian}
T0=$(date +%s)
podman build -q --target live --build-arg DEBIAN_MIRROR=$MIRROR -t omloader-live -f "$ROOT/build/Containerfile" "$ROOT/build"
ID=$(podman image inspect omloader-live --format '{{.Id}}' | cut -c1-16)
SQ=$ROOT/cache/live-$ID.squashfs
if [ ! -s "$SQ" ]; then
  echo "building live.squashfs (image $ID)"
  c=$(podman create omloader-live /sbin/init)
  # rootless: uid/gid come from the tar headers (no -all-root); zstd keeps the ISO small
  podman export "$c" | mksquashfs - "$SQ.tmp" -tar -noappend -comp zstd -Xcompression-level 15 -b 256K -quiet -no-progress
  mv "$SQ.tmp" "$SQ"; podman rm "$c" >/dev/null
fi
ln -f "$SQ" "$ROOT/out/live.squashfs" 2>/dev/null || cp "$SQ" "$ROOT/out/live.squashfs"
podman build -q --target builder --build-arg DEBIAN_MIRROR=$MIRROR -t omloader-builder -f "$ROOT/build/Containerfile" "$ROOT/build"
podman run --rm -v "$ROOT":/work:Z localhost/omloader-builder bash /work/build/mk-iso-inner.sh
ls -l "$ROOT/out/omloader.iso" "$ROOT/out/live.squashfs" "$ROOT/out/omloader.squashfs"
echo "OMLOADER_BUILD_SECONDS=$(( $(date +%s) - T0 )) ISO_BYTES=$(stat -c %s "$ROOT/out/omloader.iso")"
