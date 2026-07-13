#!/usr/bin/env sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=${HOMELOOM_VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || printf 'dev')}
COMMIT=${HOMELOOM_COMMIT:-$(git -C "$ROOT" rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')}
BUILD_TIME=${HOMELOOM_BUILD_TIME:-$(date -u '+%Y-%m-%dT%H:%M:%SZ')}
PACKAGE=github.com/feranydev/homeloom/backend/internal/buildinfo

cd "$ROOT"
mkdir -p backend/bin
./scripts/dev-env.sh sh -c "cd backend && go build -trimpath -ldflags '-X ${PACKAGE}.Version=${VERSION} -X ${PACKAGE}.Commit=${COMMIT} -X ${PACKAGE}.BuildTime=${BUILD_TIME}' -o bin/homeloom ./cmd/homeloom"
./scripts/dev-env.sh sh -c 'cd frontend && npm run build'
printf 'built HomeLoom %s (%s)\n' "$VERSION" "$COMMIT"

