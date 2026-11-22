#!/bin/sh
D=/var/lib/omloader-test
mkdir -p $D
n=$(cat $D/counter 2>/dev/null || echo 0); n=$((n+1)); echo $n > $D/counter; sync
M=$(tr ' ' '\n' < /proc/cmdline | sed -n 's/^omloader\.marker=//p' | head -1)
[ -n "$M" ] && echo "$M" > $D/mark-$M
echo OMLOADER_GUEST_OK
echo OMLOADER_MARKS=$(ls $D | sed -n 's/^mark-//p' | tr '\n' ',')
for v in /var/lib/omloader-data /srv/dirdata; do   # data volumes (snapshot: false): live outside the snapshot layers
  mkdir -p $v; c=$(cat $v/counter 2>/dev/null || echo 0); c=$((c+1)); echo $c > $v/counter
done
echo OMLOADER_DATA_EXT4=$(cat /var/lib/omloader-data/counter)
echo OMLOADER_DATA_DIR=$(cat /srv/dirdata/counter)
echo OMLOADER_DATA_FSTYPE=$(awk '$2=="/var/lib/omloader-data"{print $3}' /proc/mounts)
echo OMLOADER_COUNTER=$n
echo OMLOADER_GUEST_KERNEL=$(uname -r)
echo OMLOADER_GUEST_HOSTNAME=$(cat /etc/hostname)
echo OMLOADER_GUEST_ROOTFS=$(awk '$2=="/"{t=$3} END{print t}' /proc/mounts)
[ -f /.omloader-adapted ] && echo OMLOADER_GUEST_ADAPTED=yes
[ -d /lib/modules/$(uname -r)/kernel ] && echo OMLOADER_GUEST_MODULES=yes
i=0; while [ $i -lt 30 ]; do
  if ls /run/systemd/netif/leases/* >/dev/null 2>&1; then echo OMLOADER_GUEST_NET=lease; break; fi
  sleep 1; i=$((i+1))
done
ls /sys/class/net | tr '\n' ' ' | sed 's/^/OMLOADER_GUEST_IFACES=/'; echo
echo OMLOADER_DIAG_MODDIR=$(ls /lib/modules/$(uname -r) 2>&1 | tr '\n' ' ')
echo OMLOADER_DIAG_MOUNT=$(grep modules /proc/mounts | tr '\n' ';')
echo OMLOADER_DIAG_NETIF=$(ls -R /run/systemd/netif 2>&1 | tr '\n' ' ')
echo OMLOADER_DIAG_NETCTL=$(networkctl list 2>&1 | tr '\n' ';')
journalctl -u systemd-networkd -b --no-pager 2>&1 | tail -8 | sed 's/^/OMLOADER_DIAG_JRN /'
sync
systemctl --no-block poweroff
