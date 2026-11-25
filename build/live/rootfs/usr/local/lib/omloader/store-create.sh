#!/bin/bash
# omloader-store-create.service: create omloader/store.img in the background (store.img.new, renamed when done).
# Progress: /run/omloader/store-create.status ("creating <bytes> <total>" | "ready" | "failed ..."). One instance (flock).
export PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
. /run/omloader/store-create.env 2>/dev/null
exec 9>/run/omloader/store-create.lock; flock -n 9 || exit 0
grep -qw omloader.alloc=zero /proc/cmdline && export OMLOADER_ALLOC=zero   # test/forced zero-write path
rm -f "$HOSTROOT/omloader/store.img.new"
omloader store autocreate --host-root "$HOSTROOT" ${CFG:+$CFG} > /run/omloader/autocreate.out 2>&1
rc=$?
cat /run/omloader/autocreate.out | tee -a /run/omloader/store.log
if [ $rc = 0 ]; then
  echo "OMLOADER_LOADER: store ready: used from the next boot (this session's snapshots stay in RAM)" | tee -a /run/omloader/store.log
  echo "ready 1 1" > /run/omloader/store-create.status
else
  grep -q '^failed' /run/omloader/store-create.status 2>/dev/null || echo "skipped" > /run/omloader/store-create.status
fi
exit 0
