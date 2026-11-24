#!/bin/sh
# Runs INSIDE the live image build (build/Containerfile, last step of the `live` target). Writes the static part of the
# base description: /usr/share/omloader/base-release (KEY=VALUE) and /usr/share/omloader/packages.txt (name<TAB>version).
# build/mk-iso-inner.sh adds OMLOADER_VERSION / BUILD_DATE / GIT_COMMIT and installs the result as /etc/omloader-release.
set -eu
D=${OML_BASE_DIR:-/usr/share/omloader}
mkdir -p "$D"
ver() { dpkg-query -W -f='${Version}' "$1" 2>/dev/null || true; }
codename=$(. /etc/os-release && echo "${VERSION_CODENAME:-}")
kpkg=$(dpkg-query -W -f='${Package} ${Version}\n' 'linux-image-[0-9]*' 2>/dev/null | head -1)
kver=$(ls /lib/modules 2>/dev/null | head -1)
{
  echo "DEBIAN_VERSION=$(cat /etc/debian_version)"
  echo "DEBIAN_CODENAME=$codename"
  echo "KERNEL_VERSION=$kver"
  echo "KERNEL_PACKAGE=$kpkg"
  echo "KEXEC_TOOLS=$(ver kexec-tools)"
  echo "QEMU_UTILS=$(ver qemu-utils)"
  echo "BASE_IMAGE=${BASE_IMAGE:-debian:trixie}"
  echo "DEBIAN_MIRROR=${DEBIAN_MIRROR:-}"
} > "$D/base-release"
dpkg-query -W -f='${Package}\t${Version}\n' | sort > "$D/packages.txt"
