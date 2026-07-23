#!/usr/bin/env sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: $0 <version> [output]" >&2
  exit 2
fi

VERSION=$1
case "$VERSION" in
  ''|*[!A-Za-z0-9._+-]*)
    echo "version may contain only letters, numbers, dot, underscore, plus and hyphen" >&2
    exit 2
    ;;
esac
COMMIT=${HOMELOOM_COMMIT:-$(git -C "$ROOT" rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')}
BUILD_TIME=${HOMELOOM_BUILD_TIME:-$(date -u '+%Y-%m-%dT%H:%M:%SZ')}
PACKAGE=github.com/feranydev/homeloom/backend/internal/buildinfo
OUTPUT=${2:-$ROOT/backend/bin/homeloom}
case "$OUTPUT" in
  /*) ;;
  *) OUTPUT="$ROOT/$OUTPUT" ;;
esac

cd "$ROOT"
mkdir -p "$(dirname "$OUTPUT")"
(cd matter-runtime && "$ROOT/scripts/dev-env.sh" npm run build)
(cd frontend && "$ROOT/scripts/dev-env.sh" npm run build:embed)
(cd backend && CGO_ENABLED=0 "$ROOT/scripts/dev-env.sh" go build -trimpath -tags embed_webui \
  -ldflags "-s -w -X ${PACKAGE}.Version=${VERSION} -X ${PACKAGE}.Commit=${COMMIT} -X ${PACKAGE}.BuildTime=${BUILD_TIME}" \
  -o "$OUTPUT" ./cmd/homeloom)
test -s "$OUTPUT"
"$OUTPUT" -version
printf 'built HomeLoom core %s (%s): %s\n' "$VERSION" "$COMMIT" "$OUTPUT"
printf 'built Matter sidecar (requires Node.js 20+): %s\n' "$ROOT/matter-runtime/dist/src/cli.js"
