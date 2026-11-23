#!/bin/bash
# Prints the version string (no leading "v"), used by the Makefile, build-iso.sh and CI.
#   1. $OML_VERSION (explicit override)
#   2. a CI tag build ($GITHUB_REF_TYPE=tag -> $GITHUB_REF_NAME)
#   3. `git describe --tags --dirty` (0.1.0, 0.1.0-3-gabc1234, ...)
#   4. fallback: contents of the VERSION file + "-dev"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [ -n "${OML_VERSION:-}" ]; then v=$OML_VERSION
elif [ "${GITHUB_REF_TYPE:-}" = tag ]; then v=$GITHUB_REF_NAME
elif v=$(git -C "$ROOT" describe --tags --dirty 2>/dev/null) && [ -n "$v" ]; then :
else v="$(tr -d ' \n' < "$ROOT/VERSION")-dev"; fi
echo "${v#v}"
