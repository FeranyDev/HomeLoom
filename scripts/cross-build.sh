#!/usr/bin/env sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUTPUT="$ROOT/.cache/cross-build"
mkdir -p "$OUTPUT"

for ARCH in amd64 arm64; do
  TARGET="$OUTPUT/homeloom-linux-$ARCH"
  "$ROOT/scripts/dev-env.sh" sh -c "cd '$ROOT/backend' && CGO_ENABLED=0 GOOS=linux GOARCH='$ARCH' go build -trimpath -o '$TARGET' ./cmd/homeloom"
  test -s "$TARGET"
done

printf 'cross-build passed (linux/amd64, linux/arm64)\n'
