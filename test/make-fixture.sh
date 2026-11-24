#!/bin/bash
# Run ON THE REMOTE. Builds out/fixture.raw (GPT, 1 ext4 partition label OMLROOT, Debian trixie
# + kernel + initramfs w/ dm-snapshot; /sbin/init prints OMLOADER_GUEST_OK, bumps counter, powers off)
# plus out/fixture-vmlinuz, out/fixture-initrd.img for direct qemu boot. Fully rootless.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; mkdir -p $O $ROOT/work
SIZE_MB=${FIXTURE_MB:-1024}
podman build -q --build-arg DEBIAN_MIRROR=${DEBIAN_MIRROR:-http://deb.debian.org/debian} -t omloader-fixture -f $ROOT/test/fixture/Containerfile $ROOT/test/fixture
c=$(podman create localhost/omloader-fixture /sbin/init)
podman export $c > $ROOT/work/rootfs.tar
rm -rf $ROOT/work/bootdir; podman cp $c:/boot $ROOT/work/bootdir
cp $(ls $ROOT/work/bootdir/vmlinuz-* | head -1) $O/fixture-vmlinuz; cp $(ls $ROOT/work/bootdir/initrd.img-* | head -1) $O/fixture-initrd.img
rm -rf $ROOT/work/bootdir; podman rm $c >/dev/null
P=$ROOT/work/fixture-p1.img; rm -f $P
truncate -s $((SIZE_MB-2))M $P
# e2fsprogs here can't -d a tarball: extract + mkfs inside a user namespace so uids map to 0
podman unshare bash -c "set -e; rm -rf $ROOT/work/rootfs; mkdir $ROOT/work/rootfs; tar -xpf $ROOT/work/rootfs.tar -C $ROOT/work/rootfs --numeric-owner; mkfs.ext4 -q -L OMLROOT -d $ROOT/work/rootfs $P; rm -rf $ROOT/work/rootfs"
F=$O/fixture.raw; rm -f $F; truncate -s ${SIZE_MB}M $F
printf 'label: gpt\nstart=2048, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=OMLROOT\n' | sfdisk -q $F
dd if=$P of=$F bs=512 seek=2048 conv=notrunc status=none
rm -f $P
sfdisk -l $F | tail -3; ls -l $O
