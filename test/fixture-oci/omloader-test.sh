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
# host disks (default on, rw): the data partition must be visible and writable under /mnt/disks/<disk>/<label>
echo OMLOADER_HOSTDISKS=$(ls -d /mnt/disks/*/* 2>/dev/null | tr '\n' ',')
for hd in /mnt/disks/*/VTOYDATA /mnt/disks/*/VHDX; do [ -d "$hd" ] || continue
  touch "$hd/.omloader-hd-test" 2>/dev/null && { echo OMLOADER_HOSTDISKS_RW=yes; rm -f "$hd/.omloader-hd-test"; } || echo OMLOADER_HOSTDISKS_RW=no
  [ -d "$hd/omloader" ] && echo OMLOADER_HOSTDISKS_OMLDIR=yes; done
[ -f /mnt/disks/README.txt ] && echo OMLOADER_HOSTDISKS_README=yes
echo OMLOADER_GUEST_KERNEL=$(uname -r)
echo OMLOADER_GUEST_HOSTNAME=$(cat /etc/hostname)
echo OMLOADER_GUEST_TZLINK=$(readlink /etc/localtime)
echo OMLOADER_GUEST_TIMEZONE=$(cat /etc/timezone 2>/dev/null)
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
# ---- console login / accounts checks (only when the image configures users; see test/e2e-ui.sh) ----
if id alice >/dev/null 2>&1; then
  i=0; while [ $i -lt 25 ]; do [ "$(cat /tmp/omloader-login-* 2>/dev/null | wc -l)" -ge 2 ] && break; sleep 1; i=$((i+1)); done
  cat /tmp/omloader-login-* 2>/dev/null
  echo OMLOADER_UI_USERS=$(grep -c '^\(alice\|bob\|carol\):' /etc/passwd)
  echo OMLOADER_UI_SHADOW_ENTRIES=$(grep -c '^\(root\|alice\|bob\|carol\):' /etc/shadow)
  echo OMLOADER_UI_ALICE_HOME=$(stat -c '%U:%G:%a' /home/alice)
  echo OMLOADER_UI_ALICE_KEYS=$(stat -c '%U:%a' /home/alice/.ssh/authorized_keys)
  echo OMLOADER_UI_SUDOERS=$(stat -c '%a' /etc/sudoers.d/90-omloader-alice)
  pwok() { h=$(awk -F: -v u="$1" '$1==u{print $2}' /etc/shadow); perl -e 'exit(crypt($ARGV[0],$ARGV[1]) eq $ARGV[1] ? 0 : 1)' "$2" "$h" && echo ok || echo bad; }
  echo OMLOADER_UI_PW_ALICE=$(pwok alice alicepw)
  echo OMLOADER_UI_PW_BOB=$(pwok bob bobpw)
  echo OMLOADER_UI_PW_ROOT=$(pwok root rootpw)
  echo OMLOADER_UI_PW_WRONG=$(pwok alice nope)
  echo OMLOADER_UI_SUDO_ALICE=$(runuser -u alice -- sh -c 'echo alicepw | sudo -S -p "" id -u' 2>&1 | tail -1)
  echo OMLOADER_UI_SUDO_CAROL=$(runuser -u carol -- sudo -n id -u 2>&1 | tail -1)
  runuser -u bob -- sudo -n id -u >/dev/null 2>&1 && echo OMLOADER_UI_SUDO_BOB=allowed || echo OMLOADER_UI_SUDO_BOB=denied
  echo OMLOADER_UI_SNAPMNT=$(grep -c ' /.omloader/snap ' /proc/mounts)
fi
sync
systemctl --no-block poweroff
