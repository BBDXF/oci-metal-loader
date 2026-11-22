#!/bin/bash
# Run ON THE REMOTE. Builds a tiny LXD-style container image `omlbb` WITHOUT getty/login: only the host's static busybox as /sbin/init
# (busybox init + /etc/inittab, no getty entry). Used by test/e2e-ui.sh to prove the debug-shell fallback.
# Adds out/oci-images/omlbb (run after make-fixture-oci.sh, which wipes out/oci-images).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; mkdir -p $O $W
BB=${BUSYBOX:-$(command -v busybox)}
R=$W/bb-root; L=$W/bb-lxd; rm -rf $R $L; mkdir -p $R/{sbin,etc,proc,sys,dev,run,tmp,root,var} $L
cp $BB $R/sbin/init     # named init => busybox init applet; no /bin/busybox, no getty/login in the image on purpose
cat > $R/etc/inittab <<'EOT'
::sysinit:/etc/rcS
::ctrlaltdel:/sbin/init
EOT
cat > $R/etc/rcS <<'EOT'
#!/.omloader/busybox sh
B=/.omloader/busybox
echo OMLOADER_BB_GUEST_OK
echo OMLOADER_BB_KERNEL=$($B uname -r)
echo OMLOADER_BB_HAS_GETTY=$([ -e /sbin/getty ] || [ -e /bin/login ] && echo yes || echo no)
( $B sleep 15
  echo OMLOADER_BB_SHELLS=$($B ps | $B grep -c '[d]bgshell\|[o]mloader/busybox sh')
  $B sync; $B poweroff ) &
EOT
chmod 755 $R/etc/rcS $R/sbin/init
printf 'architecture: x86_64\nproperties:\n  os: busybox\n' > $W/metadata.yaml
tar -cJf $L/lxd.tar.xz -C $W metadata.yaml
mksquashfs $R $L/rootfs.squashfs -noappend -quiet >/dev/null
rm -rf $O/oci-images/omlbb; mkdir -p $O/oci-images
$ROOT/bin/omloader import lxd $L --name omlbb --out $O/oci-images --tmp $W
cat $O/oci-images/omlbb/image.yaml
