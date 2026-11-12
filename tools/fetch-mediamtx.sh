#!/usr/bin/env bash
# Download the pinned MediaMTX release into $1 (default: ./bin) and verify the
# published sha256. Targets this machine unless TARGET_OS (linux|windows|darwin)
# and TARGET_ARCH (amd64|arm64) say otherwise; release builds use that to bundle
# the engine for every hive target (#62). Used by `task tools:mediamtx`, CI and
# tools/fetch-mediamtx-release.sh.
set -euo pipefail

MEDIAMTX_VERSION="${MEDIAMTX_VERSION:-1.21.3}"
DEST="${1:-bin}"

case "${TARGET_OS:-$(uname -s)}" in
  linux|Linux*)  os=linux ;;
  darwin|Darwin*) os=darwin ;;
  windows|MINGW*|MSYS*|CYGWIN*|Windows_NT) os=windows ;;
  *) echo "unsupported OS: ${TARGET_OS:-$(uname -s)}" >&2; exit 1 ;;
esac
case "${TARGET_ARCH:-$(uname -m)}" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported arch: ${TARGET_ARCH:-$(uname -m)}" >&2; exit 1 ;;
esac

ext=tar.gz; bin=mediamtx
if [ "$os" = windows ]; then ext=zip; bin=mediamtx.exe; fi
name="mediamtx_v${MEDIAMTX_VERSION}_${os}_${arch}.${ext}"
base="https://github.com/bluenviron/mediamtx/releases/download/v${MEDIAMTX_VERSION}"

mkdir -p "$DEST"
if [ -x "$DEST/$bin" ] && "$DEST/$bin" --version 2>/dev/null | grep -q "v${MEDIAMTX_VERSION}"; then # only runs for this machine's target
  echo "mediamtx v${MEDIAMTX_VERSION} already in $DEST"; exit 0
fi

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
curl -fsSL --retry 5 --retry-all-errors --retry-delay 3 -o "$tmp/checksums" "$base/checksums.sha256"
if ! grep -qE " \*?$name\$" "$tmp/checksums"; then
  echo "MediaMTX v${MEDIAMTX_VERSION} has no build for ${os}/${arch}" >&2; exit 1
fi
curl -fsSL --retry 5 --retry-all-errors --retry-delay 3 -o "$tmp/$name" "$base/$name"
(cd "$tmp" && grep -E " \*?$name\$" checksums | sha256sum -c -)

# The MIT licence travels with the binary (LICENSE.mediamtx).
if [ "$ext" = zip ]; then unzip -q -o "$tmp/$name" "$bin" LICENSE -d "$tmp/x"; else mkdir -p "$tmp/x" && tar xzf "$tmp/$name" -C "$tmp/x" "$bin" LICENSE; fi
mv "$tmp/x/$bin" "$DEST/$bin"
mv "$tmp/x/LICENSE" "$DEST/LICENSE.mediamtx"
chmod +x "$DEST/$bin"
echo "installed $DEST/$bin (MediaMTX v${MEDIAMTX_VERSION} ${os}/${arch})"
