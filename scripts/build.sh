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
MATTER_RUNTIME_MODE=${HOMELOOM_MATTER_RUNTIME_MODE:-js}
OUTPUT=${2:-$ROOT/backend/bin/homeloom}
case "$OUTPUT" in
  /*) ;;
  *) OUTPUT="$ROOT/$OUTPUT" ;;
esac
CAMERA_KERNEL_OUTPUT=$(dirname "$OUTPUT")/homeloom-camera-kernel
FFMPEG_OUTPUT=$(dirname "$OUTPUT")/ffmpeg
MATTER_RUNTIME_OUTPUT=$(dirname "$OUTPUT")/homeloom-matter-runtime
MCP_AGENT_OUTPUT=$(dirname "$OUTPUT")/homeloom-mcp-agent

case "$MATTER_RUNTIME_MODE" in
  js|sea) ;;
  *)
    echo "unsupported HOMELOOM_MATTER_RUNTIME_MODE: $MATTER_RUNTIME_MODE (expected js or sea)" >&2
    exit 2
    ;;
esac

cd "$ROOT"
mkdir -p "$(dirname "$OUTPUT")"
(cd matter-runtime && "$ROOT/scripts/dev-env.sh" npm run build)
if [ "$MATTER_RUNTIME_MODE" = sea ]; then
  (cd matter-runtime && "$ROOT/scripts/dev-env.sh" npm run build:sea -- --output "$MATTER_RUNTIME_OUTPUT")
fi
(cd frontend && "$ROOT/scripts/dev-env.sh" npm run build:embed)
(cd backend && CGO_ENABLED=0 "$ROOT/scripts/dev-env.sh" go build -trimpath -tags embed_webui \
  -ldflags "-s -w -X ${PACKAGE}.Version=${VERSION} -X ${PACKAGE}.Commit=${COMMIT} -X ${PACKAGE}.BuildTime=${BUILD_TIME}" \
  -o "$OUTPUT" ./cmd/homeloom)
(cd backend && CGO_ENABLED=0 "$ROOT/scripts/dev-env.sh" go build -trimpath \
  -ldflags "-s -w -X ${PACKAGE}.Version=${VERSION} -X ${PACKAGE}.Commit=${COMMIT} -X ${PACKAGE}.BuildTime=${BUILD_TIME}" \
  -o "$MCP_AGENT_OUTPUT" ./cmd/homeloom-mcp-agent)
"$ROOT/scripts/check-camera-kernel-scope.sh"
(cd camera-kernel && CGO_ENABLED=0 "$ROOT/scripts/dev-env.sh" go build -trimpath \
  -o "$CAMERA_KERNEL_OUTPUT" .)
test -s "$OUTPUT"
test -s "$MCP_AGENT_OUTPUT"
test -s "$CAMERA_KERNEL_OUTPUT"
if [ "$MATTER_RUNTIME_MODE" = sea ]; then
  test -x "$MATTER_RUNTIME_OUTPUT"
  "$ROOT/scripts/tests/matter-runtime-sea_test.sh" "$MATTER_RUNTIME_OUTPUT"
fi
"$ROOT/scripts/fetch-ffmpeg.sh" "$FFMPEG_OUTPUT"
test -x "$FFMPEG_OUTPUT"
"$OUTPUT" -version
printf 'built HomeLoom core %s (%s): %s\n' "$VERSION" "$COMMIT" "$OUTPUT"
printf 'built HomeLoom MCP Agent sidecar: %s\n' "$MCP_AGENT_OUTPUT"
printf 'built HomeLoom camera kernel: %s\n' "$CAMERA_KERNEL_OUTPUT"
printf 'built FFmpeg media transcoder: %s\n' "$FFMPEG_OUTPUT"
if [ "$MATTER_RUNTIME_MODE" = sea ]; then
  printf 'built Matter SEA runtime: %s\n' "$MATTER_RUNTIME_OUTPUT"
else
  printf 'built Matter JS runtime entry: %s\n' "$ROOT/matter-runtime/dist/src/cli.js"
fi
