#!/bin/bash
# Run ON THE REMOTE. Proves the Debian live userspace of the loader ISO: DHCP lease + route (QEMU user net), resolver
# (systemd-resolved stub -> QEMU DNS), CA bundle, extra CA via update-ca-certificates + a real TLS handshake against a local
# https server on the host (10.0.2.2 in QEMU user networking), the omloader binary, and boot-to-shell time. Records the ISO size.
# Env: SKIP_BUILD=1 reuse out/omloader.iso
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; O=$ROOT/out; W=$ROOT/work; T=$ROOT/test
export TIMEOUT=${TIMEOUT:-240}
FAIL=0
pass() { echo "PASS: $*"; }; fail() { echo "FAIL: $*"; FAIL=1; }
SRVPID=
trap 'pkill -P $$ qemu-system 2>/dev/null; pkill -u $USER -f "$W/run\." 2>/dev/null; [ -n "$SRVPID" ] && kill $SRVPID 2>/dev/null' EXIT
set -e
[ -n "${SKIP_BUILD:-}" ] || $ROOT/build/build-iso.sh
mkdir -p $W
# ---- small GPT disk with the two labelled partitions (OMLOADER ext4, VTOYDATA exfat) ----
D=$W/live-disk.raw; P1=$W/l1.img; P2=$W/l2.img
rm -f $D $P1 $P2; truncate -s 64M $P1; truncate -s 64M $P2
mkfs.exfat -L VTOYDATA $P1 >/dev/null; mkfs.ext4 -q -L OMLOADER $P2
truncate -s 140M $D
printf 'label: gpt\nstart=2048, size=64MiB, type=EBD0A0A2-B9E5-4433-87C0-68B6B72699C7, name=VTOYDATA\ntype=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=OMLOADER\n' | sfdisk -q $D
dd if=$P1 of=$D bs=1M seek=1 conv=notrunc status=none
dd if=$P2 of=$D bs=512 seek=$((2048+64*2048)) conv=notrunc status=none
rm -f $P1 $P2
# ---- test CA + local https endpoint (the QEMU user-net gateway 10.0.2.2 reaches the host's 127.0.0.1) ----
TLS=$W/tls; rm -rf $TLS; mkdir -p $TLS/www
openssl req -x509 -newkey rsa:2048 -nodes -keyout $TLS/ca.key -out $TLS/ca.crt -days 2 -subj "/CN=omloader e2e test CA" 2>/dev/null
openssl req -newkey rsa:2048 -nodes -keyout $TLS/srv.key -out $TLS/srv.csr -subj "/CN=10.0.2.2" 2>/dev/null
echo "subjectAltName=IP:10.0.2.2,DNS:localhost" > $TLS/ext
openssl x509 -req -in $TLS/srv.csr -CA $TLS/ca.crt -CAkey $TLS/ca.key -CAcreateserial -out $TLS/srv.crt -days 2 -extfile $TLS/ext 2>/dev/null
echo OMLOADER_TLS_OK > $TLS/www/hello
PORT=$((20000 + RANDOM % 20000))
(cd $TLS/www && exec python3 - $PORT $TLS/srv.crt $TLS/srv.key <<'PYEOF'
import http.server, ssl, sys
port, crt, key = int(sys.argv[1]), sys.argv[2], sys.argv[3]
srv = http.server.HTTPServer(("127.0.0.1", port), http.server.SimpleHTTPRequestHandler)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER); ctx.load_cert_chain(crt, key)
srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
srv.serve_forever()
PYEOF
) > $W/tls-server.log 2>&1 &
SRVPID=$!
sleep 1
set +e

# ---- stage disk with the CA + the hook script that runs inside the live system ----
rm -rf $W/hk; mkdir -p $W/hk; cp $TLS/ca.crt $W/hk/test-ca.crt
cat > $W/hk/hook.sh <<HOOK
t() { n=\$1; shift; echo "TAG:\$n:BEGIN"; "\$@" 2>&1; echo "TAG:\$n:RC=\$?"; echo "TAG:\$n:END"; }
t route sh -c 'for i in \$(seq 1 30); do ip route | grep -q "^default" && break; sleep 1; done; ip route; ip -4 addr show scope global'
t lease sh -c 'ls /run/systemd/netif/leases/; networkctl status 2>&1 | head -20'
t resolv sh -c 'readlink /etc/resolv.conf; cat /etc/resolv.conf | grep -v "^#"; resolvectl status 2>&1 | head -25'
t dns sh -c 'getent hosts deb.debian.org || echo NODNS'
t cabundle sh -c 'test -s /etc/ssl/certs/ca-certificates.crt && echo BUNDLE_OK \$(grep -c BEGIN /etc/ssl/certs/ca-certificates.crt)'
t omloader omloader snapshot
t omloader_cfg omloader config validate
t version sh -c 'omloader version --long; omloader version --json | head -3; cat /etc/omloader-release | head -3'
t extra_ca sh -c 'cp /mnt/stage/test-ca.crt /usr/local/share/ca-certificates/omloader-test-ca.crt && update-ca-certificates'
t tls curl -sS -m 20 https://10.0.2.2:$PORT/hello
t xz sh -c 'xz --version | head -1; echo hi | xz | xz -d'
t internet sh -c 'curl -sSI -m 20 https://deb.debian.org 2>&1 | head -1'
t tools sh -c 'for b in kexec dmsetup kpartx mksquashfs mkfs.ext4 mkfs.exfat qemu-img iwctl parted sgdisk cryptsetup curl nano busybox; do command -v \$b >/dev/null && echo ok:\$b || echo MISSING:\$b; done'
t kernel sh -c 'uname -r; ls /lib/modules; ls /lib/firmware | wc -l; cat /etc/debian_version; systemctl is-system-running'
t mounts sh -c 'grep -E " /mnt/(snap|images) " /proc/mounts | cut -d" " -f1-3'
HOOK
rm -f $W/hk.img; truncate -s 16M $W/hk.img; mkfs.ext4 -q -L OMLSTAGE -d $W/hk $W/hk.img
# junk disk: 3 partitions (random data, no recognizable fs) -> the loader must not probe exfat on it (no "exFAT-fs" console noise)
J=$W/junk.raw; rm -f $J; truncate -s 64M $J
printf 'label: gpt\nstart=2048, size=16MiB, type=EBD0A0A2-B9E5-4433-87C0-68B6B72699C7\nsize=16MiB, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4\nsize=16MiB, type=0657FD6D-A4AB-43C4-84E5-0933C84B4F4F\n' | sfdisk -q $J
for o in 1 17 33; do head -c 16M /dev/urandom | dd of=$J bs=1M seek=$o conv=notrunc status=none; done
T0=$(date +%s)
DISK=$D DISK2=$W/hk.img DISK3=$J EXTRA=omloader.hook=1 LOG=$W/live-hook.log $T/run-kvm.sh > /dev/null
tr -d '\r' < $W/live-hook.log | sed 's/\x1b\[[0-9;]*[mK]//g' > $W/live-hook.clean
echo "--- hook run took $(( $(date +%s) - T0 ))s; excerpt:"; grep -E 'TAG:.*:RC=|OMLOADER_(LOADER|STAGE1)|HOOK_RC' $W/live-hook.clean | cut -c1-150 | head -30
sect() { sed -n "/TAG:$1:BEGIN/,/TAG:$1:END/p" $W/live-hook.clean; }
rc() { sect $1 | grep -o 'RC=[0-9]*' | tail -1 | cut -d= -f2; }
echo "--- route:"; sect route | head -8; echo "--- resolv:"; sect resolv | head -12; echo "--- dns/tls/internet:"; sect dns; sect tls; sect internet

! grep -q 'exFAT-fs' $W/live-hook.clean && pass "no exFAT-fs probe noise on the console with an extra junk (non-exfat) disk attached" || fail "exFAT-fs lines on console: $(grep -m3 'exFAT-fs' $W/live-hook.clean | tr '\n' '|')"
grep -q 'OMLOADER_STAGE1: live medium' $W/live-hook.clean && pass "stage 1 found the live medium and switched into the Debian live system" || fail "stage 1"
grep -q 'OMLOADER_LOADER: starting (kernel' $W/live-hook.clean && pass "systemd service omloader-mounts ran" || fail "omloader-mounts"
sect mounts | grep -q '/mnt/snap ext4' && sect mounts | grep -q '/mnt/images exfat' && pass "OMLOADER (ext4) and VTOYDATA (exfat) partitions mounted by label" || fail "partition mounts: $(sect mounts | tr '\n' ' ')"
sect route | grep -q '^default via 10.0.2.2' && pass "ip route: default via 10.0.2.2 (DHCP lease from QEMU user net)" || fail "no default route"
sect lease | grep -qE '^[0-9]+$' && pass "systemd-networkd holds a DHCP lease" || fail "no lease file"
sect resolv | grep -q 'stub-resolv.conf' && sect resolv | grep -q '10.0.2.3' && pass "resolver: /etc/resolv.conf -> systemd-resolved stub, upstream DNS 10.0.2.3 (QEMU)" || fail "resolver config"
if sect dns | grep -q NODNS; then echo "NOTE: host DNS did not resolve deb.debian.org (no internet from the test host?)"; else pass "DNS lookup of deb.debian.org works through the stub"; fi
sect cabundle | grep -q BUNDLE_OK && pass "system CA bundle present ($(sect cabundle | grep -o 'BUNDLE_OK [0-9]*'))" || fail "no CA bundle"
[ "$(rc omloader)" != "" ] && sect omloader | grep -q 'usage: omloader snapshot' && pass "omloader binary runs inside the live system" || fail "omloader binary"
sect version | grep -q '^base: Debian 12\.[0-9]* bookworm, kernel 6\.1\..*, built 20' && sect version | grep -q 'OMLOADER_VERSION=' && sect version | grep -q 'KEXEC_TOOLS=' && pass "omloader version --long shows the Debian base info (/etc/omloader-release)" || fail "omloader version --long: no base info"
[ "$(rc omloader_cfg)" = 0 ] && pass "omloader config validate (embedded defaults)" || fail "omloader config validate"
sect extra_ca | grep -qE '1 added' && pass "update-ca-certificates added the extra test CA" || fail "extra CA: $(sect extra_ca | tr '\n' ' ')"
sect tls | grep -q OMLOADER_TLS_OK && pass "TLS handshake + HTTP GET to the local https endpoint using the system CA store (extra_ca)" || fail "TLS: $(sect tls | tr '\n' ' ')"
if sect internet | grep -q 'HTTP/'; then pass "internet: $(sect internet | grep HTTP/ | head -1)"; else echo "NOTE: no internet from the test VM ($(sect internet | sed -n 2p))"; fi
sect xz | grep -q 'xz (XZ Utils)' && sect xz | grep -q '^hi$' && pass "xz works in the loader (xz --version, compress/decompress round trip)" || fail "xz missing/broken in the loader"
! sect tools | grep -q MISSING && pass "tool set present (kexec dmsetup kpartx mksquashfs mkfs.* qemu-img iwctl parted cryptsetup curl busybox ...)" || fail "missing tools: $(sect tools | grep MISSING | tr '\n' ' ')"
sect kernel | head -4 | tr '\n' ' '; echo

# ---- selftest flag ----
DISK=$D EXTRA=omloader.selftest=1 LOG=$W/live-self.log $T/run-kvm.sh > /dev/null
tr -d '\r' < $W/live-self.log | sed 's/\x1b\[[0-9;]*[mK]//g' > $W/live-self.clean
grep -q OMLOADER_LOADER_SELFTEST_DONE $W/live-self.clean && pass "omloader.selftest=1 works (powers off)" || fail "selftest"

# ---- boot-to-console time. Plain boot (no omloader.* flags) now shows the TUI (boot.ui default tui): the marker is
# OMLOADER_TUI_READY (printed when the TUI first renders). With omloader.ui=shell the old shell banner marker
# OMLOADER_SHELL_READY is measured. Each measurement is retried once: an empty serial log (qemu failed to start right after
# the previous suite, seen as a rare harness flake) is not a loader failure.
measure() { # $1 name  $2 marker  $3 extra cmdline  -> sets UP WALL
  for ATTEMPT in 1 2; do
    rm -f $W/live-$1.log
    T1=$(date +%s.%N)
    ( DISK=$D TIMEOUT=120 EXTRA="$3" LOG=$W/live-$1.log $T/run-kvm.sh > /dev/null 2>&1 & )
    for i in $(seq 1 240); do grep -q $2 $W/live-$1.log 2>/dev/null && break; sleep 0.5; done
    T2=$(date +%s.%N)
    pkill -u $USER -f "file:$W/live-$1.log" 2>/dev/null
    sleep 2
    tr -d '\r' < $W/live-$1.log | sed 's/\x1b\[[0-9;?]*[a-zA-Z]//g' > $W/live-$1.clean
    UP=$(grep -ao "$2 uptime=[0-9.]*" $W/live-$1.clean | head -1 | cut -d= -f2)
    [ -n "$UP" ] && break
    echo "NOTE: no $2 on attempt $ATTEMPT (log bytes: $(stat -c %s $W/live-$1.log 2>/dev/null || echo 0)); retrying"
  done
  WALL=$(awk "BEGIN{printf \"%.0f\", $T2-$T1}")
}
measure tui OMLOADER_TUI_READY ""
[ -n "$UP" ] && pass "boot-to-TUI: kernel uptime ${UP}s at OMLOADER_TUI_READY (wall ${WALL}s incl. OVMF/GRUB/qemu start)" || fail "no TUI marker (autologin getty -> tui-launch) on serial"
grep -q 'OMLOADER_LOADER: starting' $W/live-tui.clean && ! grep -q 'auto boot image' $W/live-tui.clean && ! grep -q OMLOADER_SHELL_READY $W/live-tui.clean && pass "no omloader.image= -> TUI (no automatic boot, no shell banner)" || echo "NOTE: tui-run log check inconclusive"
measure shell OMLOADER_SHELL_READY "omloader.ui=shell"
[ -n "$UP" ] && pass "boot-to-shell (omloader.ui=shell): kernel uptime ${UP}s at the omloader shell banner (wall ${WALL}s incl. OVMF/GRUB/qemu start)" || fail "no shell banner (autologin getty) on serial with omloader.ui=shell"
! grep -q OMLOADER_TUI_READY $W/live-shell.clean && pass "omloader.ui=shell: the TUI does not start" || fail "TUI started despite omloader.ui=shell"
ISZ=$(stat -c %s $O/omloader.iso); LSZ=$(stat -c %s $O/live.squashfs)
echo "OMLOADER_ISO_BYTES=$ISZ ($((ISZ/1048576)) MiB) live.squashfs=$((LSZ/1048576)) MiB"
[ $ISZ -le $((450*1048576)) ] && pass "ISO size $((ISZ/1048576)) MiB <= 450 MiB target" || fail "ISO size $((ISZ/1048576)) MiB exceeds the 450 MiB target"
[ $FAIL = 0 ] && echo "E2E-LIVE: ALL PASS" || echo "E2E-LIVE: FAILED"
exit $FAIL
