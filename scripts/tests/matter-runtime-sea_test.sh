#!/usr/bin/env sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
RUNTIME=${1:-$ROOT/backend/bin/homeloom-matter-runtime}
ADAPTER=${HOMELOOM_MATTER_SEA_SMOKE_ADAPTER:-fake}
if [ ! -x "$RUNTIME" ]; then
  echo "Matter SEA runtime is missing or not executable: $RUNTIME" >&2
  exit 2
fi

RUNTIME_DIR=$(mktemp -d "$ROOT/.cache/matter-runtime/sea-smoke.XXXXXX")
cleanup() {
  rm -rf "$RUNTIME_DIR"
}
trap cleanup EXIT INT TERM

SOCKET_PATH="$RUNTIME_DIR/runtime.sock"
LOG_PATH="$RUNTIME_DIR/runtime.log"
"$RUNTIME" --socket "$SOCKET_PATH" --target sea-smoke --adapter "$ADAPTER" >"$LOG_PATH" 2>&1 &
PID=$!

READY=0
for _ in $(seq 1 120); do
  if [ -S "$SOCKET_PATH" ]; then
    READY=1
    break
  fi
  if ! kill -0 "$PID" 2>/dev/null; then
    cat "$LOG_PATH" >&2
    exit 1
  fi
  sleep 0.05
done

if [ "$READY" -ne 1 ]; then
  cat "$LOG_PATH" >&2
  exit 1
fi
grep -F '"event":"ready"' "$LOG_PATH" >/dev/null
kill "$PID"
wait "$PID" 2>/dev/null || true
echo "Matter SEA smoke passed: $RUNTIME (adapter=$ADAPTER)"
