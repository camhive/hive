#!/usr/bin/env bash
# Fetch the pinned MediaMTX for every hive release target into
# $1/<os>_<arch>/ (default: build/mediamtx), checksums verified by
# fetch-mediamtx.sh. GoReleaser runs it before building and bundles the
# engine next to the hive in archives and packages (#62).
set -euo pipefail

DEST="${1:-build/mediamtx}"
here="$(cd "$(dirname "$0")" && pwd)"
for target in linux_amd64 linux_arm64 windows_amd64; do
  TARGET_OS="${target%_*}" TARGET_ARCH="${target#*_}" "$here/fetch-mediamtx.sh" "$DEST/$target"
done
