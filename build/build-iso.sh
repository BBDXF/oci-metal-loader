#!/bin/bash
# Run ON THE REMOTE (or any host with rootless podman). Builds out/omloader.iso
# Usage: build/build-iso.sh   (cwd = dir containing build/ and bin/omloader)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
[ -x "$ROOT/bin/omloader" ] || { echo "missing $ROOT/bin/omloader (static binary)"; exit 1; }
mkdir -p "$ROOT/out" "$ROOT/work"
podman build -q --build-arg DEBIAN_MIRROR=${DEBIAN_MIRROR:-http://mirrors.ustc.edu.cn/debian} -t omloader-builder -f "$ROOT/build/Containerfile" "$ROOT/build"
podman run --rm -v "$ROOT":/work:Z localhost/omloader-builder bash /work/build/mk-iso-inner.sh
ls -l "$ROOT/out/omloader.iso"
