#!/usr/bin/env sh

set -eu
umask 077

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BINARY=${HOMELOOM_BINARY:-$ROOT/backend/bin/homeloom}
DATABASE_URL=${HOMELOOM_DATABASE_URL:-postgres://homeloom:homeloom-dev@127.0.0.1:54329/homeloom?sslmode=disable}
MASTER_KEY=${HOMELOOM_MASTER_KEY:-$ROOT/backend/data/homeloom.key}
DESTINATION_DIR=${1:-$ROOT/backups}
STAMP=$(date -u '+%Y%m%dT%H%M%SZ')
DESTINATION="$DESTINATION_DIR/homeloom-$STAMP.json"

if [ ! -x "$BINARY" ]; then echo "missing executable: $BINARY (run ./scripts/build.sh <version> first)" >&2; exit 1; fi
mkdir -p "$DESTINATION_DIR"
HOMELOOM_DATABASE_URL="$DATABASE_URL" HOMELOOM_MASTER_KEY="$MASTER_KEY" "$BINARY" -backup "$DESTINATION"
printf '%s\n' "$DESTINATION"
if [ -f "$DESTINATION.key" ]; then printf '%s\n' "$DESTINATION.key"; fi
