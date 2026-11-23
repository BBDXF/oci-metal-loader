#!/bin/bash
# Builds out/omloader-<version>.iso (+ out/omloader.iso copy, out/SHA256SUMS) with podman or docker. No remote host needed.
# Usage: build/build-iso.sh      (needs bin/omloader = the static binary: `make bin`; or just run `make iso`)
# Host requirements: podman or docker, `mksquashfs` (squashfs-tools >= 4.4 with zstd), network access to the Debian mirror and Docker Hub.
# Privileges: none beyond running the container engine (rootless podman works; docker needs the usual docker group/root).
#             The builder container is NOT --privileged: it only runs grub-mkstandalone, mtools and xorriso on plain files.
# Env: CONTAINER_ENGINE (podman|docker; default: podman if installed, else docker)
#      DEBIAN_MIRROR (default http://deb.debian.org/debian)   OML_VERSION (default: build/version.sh)
#      SOURCE_DATE_EPOCH (default: last commit time; makes squashfs/ISO timestamps stable)
#   out/omloader-<version>-packages.txt + out/omloader-release: the live base's package list / build info (also at the ISO root)
#   out/live.squashfs     Debian live userspace (cached in cache/ by image id: only re-squashed when the image changes)
#   out/omloader.squashfs omloader binary + units/scripts (build/live/rootfs), rebuilt every time (small)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
[ -x "$ROOT/bin/omloader" ] || { echo "missing $ROOT/bin/omloader (static binary; run 'make bin')"; exit 1; }
command -v mksquashfs >/dev/null || { echo "mksquashfs not found (apt install squashfs-tools)"; exit 1; }
ENGINE=${CONTAINER_ENGINE:-$(command -v podman >/dev/null && echo podman || echo docker)}
command -v "$ENGINE" >/dev/null || { echo "container engine '$ENGINE' not found"; exit 1; }
MIRROR=${DEBIAN_MIRROR:-http://deb.debian.org/debian}
REL=${DEBIAN_RELEASE:-bookworm}   # Debian stable release of the live base (always its latest packages)
VER=$("$ROOT/build/version.sh")
SDE=${SOURCE_DATE_EPOCH:-$(git -C "$ROOT" log -1 --format=%ct 2>/dev/null || date +%s)}
export SOURCE_DATE_EPOCH=$SDE
mkdir -p "$ROOT/out" "$ROOT/work" "$ROOT/cache"
T0=$(date +%s)
[ "$ENGINE" = podman ] && PFX=localhost/ || PFX=
echo "build-iso: engine=$ENGINE mirror=$MIRROR version=$VER SOURCE_DATE_EPOCH=$SDE"
$ENGINE build -q --target live --build-arg DEBIAN_MIRROR="$MIRROR" --build-arg DEBIAN_RELEASE="$REL" -t omloader-live -f "$ROOT/build/Containerfile" "$ROOT/build" >/dev/null
ID=$($ENGINE image inspect omloader-live --format '{{.Id}}' | sed 's/^sha256://' | cut -c1-16)
SQ=$ROOT/cache/live-$ID.squashfs
if [ ! -s "$SQ" ]; then
  echo "building live.squashfs (image $ID)"
  c=$($ENGINE create omloader-live /sbin/init)
  # uid/gid come from the tar headers (no -all-root); zstd keeps the ISO small
  $ENGINE export "$c" | mksquashfs - "$SQ.tmp" -tar -noappend -comp zstd -Xcompression-level 15 -b 256K -quiet -no-progress
  mv "$SQ.tmp" "$SQ"; $ENGINE rm "$c" >/dev/null
fi
ln -f "$SQ" "$ROOT/out/live.squashfs" 2>/dev/null || cp "$SQ" "$ROOT/out/live.squashfs"
$ENGINE build -q --target builder --build-arg DEBIAN_MIRROR="$MIRROR" --build-arg DEBIAN_RELEASE="$REL" -t omloader-builder -f "$ROOT/build/Containerfile" "$ROOT/build" >/dev/null
# docker runs as real root: hand the outputs back to the invoking user afterwards (rootless podman needs no chown)
OWNER=; [ "$ENGINE" = docker ] && OWNER="$(id -u):$(id -g)"
GC=$(git -C "$ROOT" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
$ENGINE run --rm -e SOURCE_DATE_EPOCH="$SDE" -e OML_VERSION="$VER" -e GIT_COMMIT="$GC" -e HOST_OWNER="$OWNER" -v "$ROOT":/work:Z "${PFX}omloader-builder" \
  bash -c 'bash /work/build/mk-iso-inner.sh; rc=$?; [ -z "$HOST_OWNER" ] || chown -R "$HOST_OWNER" /work/out; exit $rc'
ISO=omloader-$VER.iso
ln -f "$ROOT/out/omloader.iso" "$ROOT/out/$ISO" 2>/dev/null || cp "$ROOT/out/omloader.iso" "$ROOT/out/$ISO"
cp "$ROOT/out/packages.txt" "$ROOT/out/omloader-$VER-packages.txt"
(cd "$ROOT/out" && sha256sum "$ISO" "omloader-$VER-packages.txt" > SHA256SUMS)
ls -l "$ROOT/out/$ISO" "$ROOT/out/SHA256SUMS"
echo "OMLOADER_BUILD_SECONDS=$(( $(date +%s) - T0 )) ISO_BYTES=$(stat -c %s "$ROOT/out/$ISO") VERSION=$VER"
