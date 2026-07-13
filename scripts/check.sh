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
if [ "$RACE" = true ]; then
  ./scripts/dev-env.sh sh -c 'cd backend && go test -race -timeout 90s ./internal/provider ./internal/providers/virtual ./internal/runtime/providermanager ./internal/application ./internal/platform/httpapi ./internal/targets/homekit'
fi
./scripts/dev-env.sh sh -c 'cd frontend && npm test && npm run lint && npm run build'

