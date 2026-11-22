#!/bin/bash
# Run ON THE REMOTE after build/build-iso.sh: dumps the package/license list of the live system to out/licenses.tsv
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; mkdir -p "$ROOT/out"
podman run --rm -v "$ROOT/build":/b:ro,Z localhost/omloader-live bash /b/licenses-inner.sh > "$ROOT/out/licenses.tsv"
wc -l "$ROOT/out/licenses.tsv"
