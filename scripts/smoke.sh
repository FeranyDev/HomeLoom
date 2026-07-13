#!/usr/bin/env sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BINARY=${HOMELOOM_BINARY:-$ROOT/backend/bin/homeloom}
ADDRESS=${HOMELOOM_SMOKE_ADDRESS:-127.0.0.1:18090}
BASE_URL="http://$ADDRESS"
TEMP=$(mktemp -d "${TMPDIR:-/tmp}/homeloom-smoke.XXXXXX")
PID=""

cleanup() {
  if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
    kill -TERM "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
  rm -rf "$TEMP"
}
trap cleanup EXIT INT TERM

if [ ! -x "$BINARY" ]; then
  echo "missing executable: $BINARY (run ./scripts/build.sh first)" >&2
  exit 1
fi

cd "$TEMP"
HOMELOOM_HTTP_ADDRESS="$ADDRESS" HOMELOOM_DATABASE="$TEMP/homeloom.db" "$BINARY" >"$TEMP/server.log" 2>&1 &
PID=$!

ready=false
for _ in $(seq 1 40); do
  if curl -fsS "$BASE_URL/ready" >/dev/null 2>&1; then ready=true; break; fi
  if ! kill -0 "$PID" 2>/dev/null; then cat "$TEMP/server.log" >&2; exit 1; fi
  sleep 0.25
done
if [ "$ready" != true ]; then cat "$TEMP/server.log" >&2; exit 1; fi

health=$(curl -fsS "$BASE_URL/health")
version=$(curl -fsS "$BASE_URL/api/v1/system/version")
devices=$(curl -fsS "$BASE_URL/api/v1/devices")
case "$health" in *'"status":"ok"'*) ;; *) echo "unexpected health response: $health" >&2; exit 1;; esac
case "$version" in *'"version"'*) ;; *) echo "unexpected version response: $version" >&2; exit 1;; esac
case "$devices" in *'"schemaVersion":1'*) ;; *) echo "unexpected devices response: $devices" >&2; exit 1;; esac

kill -TERM "$PID"
wait "$PID"
PID=""
printf 'smoke test passed (%s)\n' "$ADDRESS"

