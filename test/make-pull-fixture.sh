#!/bin/bash
# Run ON THE REMOTE. Publishes a tiny simplestreams tree (as images.linuxcontainers.org serves it) under out/lxdsrv/www:
#  omltest:ct:amd64:default  container: lxd.tar.xz + root.squashfs   (rootfs = the OCI fixture's base.squashfs, systemd guest)
#  omltest:vm:amd64:default  VM:        lxd.tar.xz + disk.qcow2      (disk = out/fixture.raw converted with qemu-img)
# Needs out/oci-images/omloci/base.squashfs (make-fixture-oci.sh) and out/fixture.raw (make-fixture.sh).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out
S=$O/lxdsrv; rm -rf $S; mkdir -p $S/www/streams/v1 $S/meta
VER=20240101_0000
for k in ct vm; do mkdir -p $S/www/images/omltest/$k/amd64/default/$VER; done
cat > $S/meta/metadata.yaml <<EOT
architecture: x86_64
creation_date: 1704067200
properties:
  architecture: amd64
  description: omloader pull test image
  os: omltest
  release: ct
EOT
for k in ct vm; do tar -C $S/meta -cJf $S/www/images/omltest/$k/amd64/default/$VER/lxd.tar.xz metadata.yaml; done
cp $O/oci-images/omloci/base.squashfs $S/www/images/omltest/ct/amd64/default/$VER/root.squashfs
qemu-img convert -f raw -O qcow2 -c $O/fixture.raw $S/www/images/omltest/vm/amd64/default/$VER/disk.qcow2
python3 - $S/www <<'PY'
import hashlib, json, os, sys
www = sys.argv[1]
def item(ftype, rel):
    p = os.path.join(www, rel); h = hashlib.sha256()
    with open(p, 'rb') as f:
        for b in iter(lambda: f.read(1 << 20), b''): h.update(b)
    return {"ftype": ftype, "sha256": h.hexdigest(), "size": os.path.getsize(p), "path": rel}
v = "20240101_0000"
def prod(k, root_name, root_ftype):
    d = f"images/omltest/{k}/amd64/default/{v}/"
    return {"aliases": f"omltest/{k},omltest/{k}/default", "arch": "amd64", "os": "omltest", "release": k, "release_title": k,
            "variant": "default", "versions": {v: {"items": {
                "lxd.tar.xz": item("lxd.tar.xz", d + "lxd.tar.xz"),
                root_name: item(root_ftype, d + root_name)}}}}
cat = {"content_id": "images", "datatype": "image-downloads", "format": "products:1.0", "products": {
    "omltest:ct:amd64:default": prod("ct", "root.squashfs", "squashfs"),
    "omltest:vm:amd64:default": prod("vm", "disk.qcow2", "disk-kvm.img")}}
json.dump(cat, open(www + "/streams/v1/images.json", "w"), indent=1)
json.dump({"format": "index:1.0", "index": {"images": {"datatype": "image-downloads", "format": "products:1.0",
    "path": "streams/v1/images.json", "products": list(cat["products"])}}}, open(www + "/streams/v1/index.json", "w"), indent=1)
PY
du -sh $S/www; ls -lR $S/www/images | grep -E 'qcow2|squashfs|xz'
