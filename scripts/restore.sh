#!/usr/bin/env sh

set -eu
umask 077

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BINARY=${HOMELOOM_BINARY:-$ROOT/backend/bin/homeloom}
DATABASE=${HOMELOOM_DATABASE:-$ROOT/backend/data/homeloom.db}
SOURCE=${1:-}
REPLACE=${2:-}

if [ -z "$SOURCE" ]; then echo "usage: $0 BACKUP_DB [--replace]" >&2; exit 2; fi
if [ "$REPLACE" != "" ] && [ "$REPLACE" != "--replace" ]; then echo "usage: $0 BACKUP_DB [--replace]" >&2; exit 2; fi
if [ ! -x "$BINARY" ]; then echo "missing executable: $BINARY (run ./scripts/build.sh first)" >&2; exit 1; fi
if [ ! -f "$SOURCE" ]; then echo "backup not found: $SOURCE" >&2; exit 1; fi

if [ "$REPLACE" = "--replace" ]; then
  HOMELOOM_DATABASE="$DATABASE" "$BINARY" -restore "$SOURCE" -restore-replace
else
  HOMELOOM_DATABASE="$DATABASE" "$BINARY" -restore "$SOURCE"
fi
