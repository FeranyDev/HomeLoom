#!/usr/bin/env sh

set -eu
umask 077

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BINARY=${HOMELOOM_BINARY:-$ROOT/backend/bin/homeloom}
DATABASE=${HOMELOOM_DATABASE:-$ROOT/backend/data/homeloom.db}
DESTINATION_DIR=${1:-$ROOT/backups}
STAMP=$(date -u '+%Y%m%dT%H%M%SZ')
DESTINATION="$DESTINATION_DIR/homeloom-$STAMP.db"

if [ ! -x "$BINARY" ]; then echo "missing executable: $BINARY (run ./scripts/build.sh first)" >&2; exit 1; fi
if [ ! -f "$DATABASE" ]; then echo "database not found: $DATABASE" >&2; exit 1; fi
mkdir -p "$DESTINATION_DIR"
HOMELOOM_DATABASE="$DATABASE" "$BINARY" -backup "$DESTINATION"
printf '%s\n' "$DESTINATION"
if [ -f "$DESTINATION.key" ]; then printf '%s\n' "$DESTINATION.key"; fi
