#!/bin/bash
# Local-safe unit test of build/live/rootfs/usr/local/lib/omloader/console-font.sh font selection (no root, no framebuffer needed).
S="$(cd "$(dirname "$0")/.." && pwd)/build/live/rootfs/usr/local/lib/omloader/console-font.sh"
D=$(mktemp -d); trap 'rm -rf $D' EXIT
for f in Uni3-TerminusBold32x16 Uni3-TerminusBold28x14 Uni3-TerminusBold24x12 Uni3-TerminusBold20x10 Uni3-TerminusBold16; do : > $D/$f.psf.gz; done
export OMLOADER_CONSOLEFONTS=$D
FAIL=0
t() { local got; got=$(bash "$S" pick $1 $2); [ "$got" = "$3" ] && echo "ok   $1x$2 -> $got" || { echo "FAIL $1x$2 -> '$got' want '$3'"; FAIL=1; }; }
t 3840 2160 Uni3-TerminusBold32x16
t 1920 1080 Uni3-TerminusBold32x16   # 120x33
t 1600 900  Uni3-TerminusBold28x14   # 114x32
t 1366 768  Uni3-TerminusBold24x12   # 113x32
t 1280 800  Uni3-TerminusBold24x12   # 106x33
t 1280 720  Uni3-TerminusBold24x12   # 106x30
t 1024 768  Uni3-TerminusBold20x10   # 102x38
t 800 600   Uni3-TerminusBold16      # 100x37
t 640 480   Uni3-TerminusBold16      # 80x30
t 0 0 ""
# a missing font file is skipped
rm $D/Uni3-TerminusBold32x16.psf.gz
t 1920 1080 Uni3-TerminusBold28x14   # 137x38
# source it: functions usable, nothing runs
bash -c ". $S; type pick_font >/dev/null" || { echo "FAIL source"; FAIL=1; }
# override file / kernel arg parsing
cat > $D/console.conf <<'EOT'
font = Uni3-Terminus16
EOT
got=$(bash -c ". $S; MEDIUM_HOST_MNT=$D; sed -n 's/^[[:space:]]*font[[:space:]]*=[[:space:]]*\([A-Za-z0-9_.-]*\).*/\1/p' $D/console.conf")
[ "$got" = Uni3-Terminus16 ] && echo "ok   console.conf font=" || { echo "FAIL conf '$got'"; FAIL=1; }
exit $FAIL
