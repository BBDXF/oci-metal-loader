#!/bin/bash
# Builds out/omloader-<version>-config.zip: a small user-facing package to unzip into the ROOT of the Ventoy data partition
# (omloader/boot.yaml, omloader/sources.yaml, omloader/images/, omloader/firmware/, ventoy/ventoy.json.example, schema/, examples/, README-FIRST.txt).
# Needs `zip` (falls back to python3 zipfile). Env: OML_VERSION, SOURCE_DATE_EPOCH (default: last commit time) for a deterministic zip.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VER=$("$ROOT/build/version.sh")
SDE=${SOURCE_DATE_EPOCH:-$(git -C "$ROOT" log -1 --format=%ct 2>/dev/null || date +%s)}
OUT=$ROOT/out/omloader-$VER-config.zip
S=$(mktemp -d); trap 'rm -rf "$S"' EXIT
cp -r "$ROOT/build/config-pkg/." "$S/"
cp "$ROOT/configs/sources.yaml" "$S/omloader/sources.yaml"
cp -r "$ROOT/schema" "$ROOT/examples" "$S/"
mkdir -p "$ROOT/out"; rm -f "$OUT"
find "$S" -exec touch -h -d "@$SDE" {} +
if command -v zip >/dev/null; then
  (cd "$S" && find . -type f | LC_ALL=C sort | sed 's#^\./##' | zip -X -q "$OUT" -@)
else
  python3 - "$S" "$OUT" "$SDE" <<'PY'
import os, sys, time, zipfile
src, out, sde = sys.argv[1], sys.argv[2], int(sys.argv[3])
t = time.gmtime(max(sde, 315532800))[:6]
files = sorted(os.path.relpath(os.path.join(d, f), src) for d, _, fs in os.walk(src) for f in fs)
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for f in files:
        zi = zipfile.ZipInfo(f, t); zi.compress_type = zipfile.ZIP_DEFLATED; zi.external_attr = 0o644 << 16
        z.writestr(zi, open(os.path.join(src, f), "rb").read())
PY
fi
echo "built $OUT"; ls -l "$OUT"
