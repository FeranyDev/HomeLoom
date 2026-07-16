#!/usr/bin/env sh

set -eu
umask 077

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BINARY=${HOMELOOM_BINARY:-$ROOT/backend/bin/homeloom}
DATABASE_URL=${HOMELOOM_DATABASE_URL:-postgres://homeloom:homeloom-dev@127.0.0.1:54329/homeloom?sslmode=disable}
MASTER_KEY=${HOMELOOM_MASTER_KEY:-$ROOT/backend/data/homeloom.key}
SOURCE=${1:-}
REPLACE=${2:-}

if [ -z "$SOURCE" ]; then echo "usage: $0 BACKUP_JSON [--replace]" >&2; exit 2; fi
if [ "$REPLACE" != "" ] && [ "$REPLACE" != "--replace" ]; then echo "usage: $0 BACKUP_JSON [--replace]" >&2; exit 2; fi
if [ ! -x "$BINARY" ]; then echo "missing executable: $BINARY (run ./scripts/build.sh first)" >&2; exit 1; fi
if [ ! -f "$SOURCE" ]; then echo "backup not found: $SOURCE" >&2; exit 1; fi

if [ "$REPLACE" = "--replace" ]; then
  HOMELOOM_DATABASE_URL="$DATABASE_URL" HOMELOOM_MASTER_KEY="$MASTER_KEY" "$BINARY" -restore "$SOURCE" -restore-replace
else
  HOMELOOM_DATABASE_URL="$DATABASE_URL" HOMELOOM_MASTER_KEY="$MASTER_KEY" "$BINARY" -restore "$SOURCE"
fi
