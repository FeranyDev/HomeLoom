#!/usr/bin/env sh

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

export GOCACHE="$ROOT/.cache/go-build"
export GOMODCACHE="$ROOT/.cache/go-mod"
export GOPATH="$ROOT/.cache/go-path"
export NPM_CONFIG_CACHE="$ROOT/.cache/npm"
export HOMELOOM_MATTER_CACHE="$ROOT/.cache/matter-runtime"

mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOPATH" "$NPM_CONFIG_CACHE" "$HOMELOOM_MATTER_CACHE"

exec "$@"
