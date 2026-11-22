#!/bin/bash
# Run ON THE REMOTE. Rootless: builds a debian:bookworm-slim + systemd container, exports it with podman,
# and converts it with `omloader import` (own layer merge + mksquashfs) to out/oci-images/omloci/{base.squashfs,image.yaml}.
# make-disk.sh copies out/oci-images/* into images/ on the VTOYDATA partition.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; mkdir -p $O $W
NAME=${OCI_NAME:-omloci}
MIRROR=${DEBIAN_MIRROR:-http://mirrors.ustc.edu.cn/debian}
BASE=docker.io/library/debian:bookworm-slim
# remote can't reach Docker Hub: pull via a mirror and tag locally (see build/README.md)
if ! podman image exists $BASE; then
  podman pull docker.m.daocloud.io/library/debian:bookworm-slim && podman tag docker.m.daocloud.io/library/debian:bookworm-slim $BASE \
    || { echo "WARNING: bookworm-slim unavailable, falling back to debian:bookworm"; BASE=docker.io/library/debian:bookworm; }
fi
podman build -q --build-arg BASE=$BASE --build-arg DEBIAN_MIRROR=$MIRROR -t omloader-fixture-oci -f $ROOT/test/fixture-oci/Containerfile $ROOT/test/fixture-oci
rm -f $W/oci.tar; podman save --format oci-archive -o $W/oci.tar localhost/omloader-fixture-oci
rm -rf $O/oci-images; mkdir -p $O/oci-images
$ROOT/bin/omloader import oci-archive $W/oci.tar --name $NAME --out $O/oci-images --autologin --tmp $W --data omldata:/var/lib/omloader-data:ext4-file:64M:false
rm -f $W/oci.tar
ls -l $O/oci-images/$NAME; cat $O/oci-images/$NAME/image.yaml
