#!/bin/bash
# Run ON THE REMOTE. Rootless: builds a debian:bookworm-slim + systemd container, exports it with podman,
# and converts it with `omloader import` (own layer merge + mksquashfs) to out/oci-images/omloci/{base.squashfs,image.yaml}.
# make-disk.sh copies out/oci-images/* into images/ on the VTOYDATA partition.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; mkdir -p $O $W
NAME=${OCI_NAME:-omloci}
MIRROR=${DEBIAN_MIRROR:-http://mirrors.ustc.edu.cn/debian}
TAG=${OCI_TAG:-omloader-fixture-oci-$(basename $ROOT)}   # per-checkout tag: several test dirs share one podman store
BASE=docker.io/library/debian:bookworm-slim
# remote can't reach Docker Hub: pull via a mirror and tag locally (see build/README.md)
if ! podman image exists $BASE; then
  podman pull docker.m.daocloud.io/library/debian:bookworm-slim && podman tag docker.m.daocloud.io/library/debian:bookworm-slim $BASE \
    || { echo "WARNING: bookworm-slim unavailable, falling back to debian:bookworm"; BASE=docker.io/library/debian:bookworm; }
fi
podman build -q --build-arg BASE=$BASE --build-arg DEBIAN_MIRROR=$MIRROR -t $TAG -f $ROOT/test/fixture-oci/Containerfile $ROOT/test/fixture-oci
rm -f $W/oci.tar; podman save --format oci-archive -o $W/oci.tar localhost/$TAG
rm -rf $O/oci-images; mkdir -p $O/oci-images
$ROOT/bin/omloader import oci-archive --experimental-oci $W/oci.tar --name $NAME --out $O/oci-images --autologin --tmp $W --data omldata:/var/lib/omloader-data:ext4-file:64M:false
rm -f $W/oci.tar
# OCI_RUNTIME_EXTRA=file: extra `runtime:` keys (relative indentation) merged into image.yaml (e.g. users for test/e2e-ui.sh)
if [ -n "${OCI_RUNTIME_EXTRA:-}" ]; then
  Y=$O/oci-images/$NAME/image.yaml
  IND=$(awk '/^runtime:/{f=1;next} f{match($0,/^ */);print RLENGTH;exit}' $Y); PAD=$(printf '%*s' ${IND:-2} '')
  sed "s/^/$PAD/" $OCI_RUNTIME_EXTRA > $W/rt-extra.yaml; sed -i "/^runtime:/r $W/rt-extra.yaml" $Y
fi
ls -l $O/oci-images/$NAME; cat $O/oci-images/$NAME/image.yaml
