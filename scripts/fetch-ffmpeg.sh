#!/usr/bin/env sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUTPUT=${1:-"$ROOT/backend/bin/ffmpeg"}
VERSION=b6.1.1
OS=$(uname -s)
ARCH=$(uname -m)

case "$OS/$ARCH" in
  Darwin/arm64)
    ASSET=ffmpeg-darwin-arm64.gz
    SHA256=8923876afa8db5585022d7860ec7e589af192f441c56793971276d450ed3bbfa
    ;;
  Darwin/x86_64)
    ASSET=ffmpeg-darwin-x64.gz
    SHA256=929b375c1182d956c51f7ac25e0b2b0411fb01f6f407aa15c9758efeb4242106
    ;;
  Linux/aarch64|Linux/arm64)
    ASSET=ffmpeg-linux-arm64.gz
    SHA256=754a678672298bc68156adff58aa7385a592c2b30b1d0ae8750c45c915c4bac0
    ;;
  Linux/x86_64|Linux/amd64)
    ASSET=ffmpeg-linux-x64.gz
    SHA256=bfe8a8fc511530457b528c48d77b5737527b504a3797a9bc4866aeca69c2dffa
    ;;
  *)
    echo "unsupported FFmpeg platform: $OS/$ARCH" >&2
    exit 1
    ;;
esac

CACHE_DIR="$ROOT/.cache/ffmpeg-static/$VERSION"
ARCHIVE="$CACHE_DIR/$ASSET"
URL="https://github.com/eugeneware/ffmpeg-static/releases/download/$VERSION/$ASSET"
mkdir -p "$CACHE_DIR" "$(dirname "$OUTPUT")"

verify() {
  ACTUAL=$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')
  test "$ACTUAL" = "$SHA256"
}

if ! test -s "$ARCHIVE" || ! verify; then
  curl --fail --location --silent --show-error --output "$ARCHIVE.tmp" "$URL"
  mv "$ARCHIVE.tmp" "$ARCHIVE"
  if ! verify; then
    echo "FFmpeg archive checksum mismatch" >&2
    exit 1
  fi
fi

gzip -dc "$ARCHIVE" >"$OUTPUT.tmp"
chmod 0755 "$OUTPUT.tmp"
mv "$OUTPUT.tmp" "$OUTPUT"
"$OUTPUT" -version | head -n 1
