#!/usr/bin/env sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
RACE=false
if [ "${1:-}" = "--race" ]; then
  RACE=true
elif [ "$#" -gt 0 ]; then
  echo "usage: $0 [--race]" >&2
  exit 2
fi

cd "$ROOT"
./scripts/dev-env.sh sh -c 'cd backend && go test -timeout 60s ./...'
./scripts/check-camera-kernel-scope.sh
./scripts/dev-env.sh sh -c 'cd camera-kernel && go test -timeout 60s . ./internal/api ./internal/matterwebrtc ./internal/mp4 ./internal/xiaomi ./pkg/rtsp ./pkg/onvif ./pkg/hap/camera ./pkg/homekit ./pkg/srtp'
./scripts/dev-env.sh sh -c 'cd camera-kernel && go test -timeout 60s ./internal/ffmpeg -run "TestSnapshot|TestBoundedSnapshot"'
if [ "$RACE" = true ]; then
  ./scripts/dev-env.sh sh -c 'cd backend && go test -race -timeout 90s ./internal/provider ./internal/providers/mqtt ./internal/providers/virtual ./internal/providers/xiaomi ./internal/providers/sonoff/... ./internal/mediaruntime/... ./internal/runtime/providermanager ./internal/runtime/targetmanager ./internal/application ./internal/platform/httpapi ./internal/targets/homekit ./internal/targets/matter'
  ./scripts/dev-env.sh sh -c 'cd camera-kernel && go test -race -timeout 90s . ./internal/matterwebrtc ./internal/xiaomi ./pkg/rtsp ./pkg/onvif'
fi
./scripts/dev-env.sh sh -c 'cd matter-runtime && npm run test && npm run typecheck'
./scripts/dev-env.sh sh -c 'cd frontend && npm run test:coverage && npm run lint && npm run build:embed'
./scripts/dev-env.sh sh -c 'cd backend && go test -tags embed_webui ./internal/webui ./internal/platform/httpapi'
