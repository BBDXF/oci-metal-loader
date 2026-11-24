#!/bin/bash
# `make update-base`: is the last-built live base behind the current Debian stable? Prints old vs current and one line of advice.
# Usage: build/update-base.sh [RELEASE_FILE]   (default: out/omloader-release from the last `make iso`, else /etc/omloader-release)
# Needs podman or docker + network. Nothing is built or changed.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REL=${DEBIAN_RELEASE:-trixie}
ENGINE=${CONTAINER_ENGINE:-$(command -v podman >/dev/null && echo podman || echo docker)}
F=${1:-}
[ -n "$F" ] || { for c in "$ROOT/out/omloader-release" /etc/omloader-release; do [ -r "$c" ] && F=$c && break; done; }
get() { grep -m1 "^$1=" "$F" 2>/dev/null | cut -d= -f2- || true; }
now=$($ENGINE run --rm "debian:$REL" bash -c '
  apt-get update -qq >/dev/null 2>&1
  echo "DEBIAN_VERSION=$(cat /etc/debian_version)"
  echo "KERNEL_VERSION=$(apt-cache depends linux-image-amd64 | sed -n "s/^ *Depends: linux-image-\(.*\)/\1/p" | head -1)"
  echo "KEXEC_TOOLS=$(apt-cache policy kexec-tools | sed -n "s/ *Candidate: //p")"')
cur() { echo "$now" | grep -m1 "^$1=" | cut -d= -f2-; }
if [ -z "$F" ]; then echo "no previous build info (run 'make iso' first, or pass a release file)"; F=/dev/null; fi
echo "base: debian:$REL   (built: ${F})"
printf '%-16s %-26s %s\n' KEY BUILT CURRENT
behind=0
for k in DEBIAN_VERSION KERNEL_VERSION KEXEC_TOOLS; do
  o=$(get $k); n=$(cur $k); m=; [ "$o" = "$n" ] || { m="  <- differs"; behind=1; }
  printf '%-16s %-26s %s%s\n' "$k" "${o:--}" "$n" "$m"
done
if [ $behind = 1 ]; then echo "advice: newer base available -> rebuild (make iso); do it before tagging."
else echo "advice: base is current, no rebuild needed for the base."; fi
