#!/usr/bin/env sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: $0 <version> [output-directory]" >&2
  exit 2
fi

VERSION=$1
case "$VERSION" in
  ''|*[!A-Za-z0-9._+-]*)
    echo "version may contain only letters, numbers, dot, underscore, plus and hyphen" >&2
    exit 2
    ;;
esac
OUTPUT=${2:-$ROOT/dist}
case "$OUTPUT" in
  /*) ;;
  *) OUTPUT="$ROOT/$OUTPUT" ;;
esac
COMMIT=${HOMELOOM_COMMIT:-$(git -C "$ROOT" rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')}
BUILD_TIME=${HOMELOOM_BUILD_TIME:-$(date -u '+%Y-%m-%dT%H:%M:%SZ')}
PACKAGE=github.com/feranydev/homeloom/backend/internal/buildinfo
TARGETS=${HOMELOOM_TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64}

mkdir -p "$OUTPUT"
(cd "$ROOT/frontend" && "$ROOT/scripts/dev-env.sh" npm run build:embed)
: > "$OUTPUT/SHA256SUMS"

for PLATFORM in $TARGETS; do
  case "$PLATFORM" in
    linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64|windows/arm64) ;;
    *) echo "unsupported target: $PLATFORM" >&2; exit 2 ;;
  esac
  GOOS=${PLATFORM%/*}
  GOARCH=${PLATFORM#*/}
  SUFFIX=""
  if [ "$GOOS" = windows ]; then SUFFIX=".exe"; fi
  TARGET="$OUTPUT/homeloom_${VERSION}_${GOOS}_${GOARCH}${SUFFIX}"
  (cd "$ROOT/backend" && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" "$ROOT/scripts/dev-env.sh" go build \
    -trimpath -tags embed_webui \
    -ldflags "-s -w -X ${PACKAGE}.Version=${VERSION} -X ${PACKAGE}.Commit=${COMMIT} -X ${PACKAGE}.BuildTime=${BUILD_TIME}" \
    -o "$TARGET" ./cmd/homeloom)
  test -s "$TARGET"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$OUTPUT" && sha256sum "$(basename "$TARGET")") >> "$OUTPUT/SHA256SUMS"
  else
    (cd "$OUTPUT" && shasum -a 256 "$(basename "$TARGET")") >> "$OUTPUT/SHA256SUMS"
  fi
  printf 'built %s/%s: %s\n' "$GOOS" "$GOARCH" "$TARGET"
done

printf 'multi-platform single-binary build passed: %s\n' "$OUTPUT"
