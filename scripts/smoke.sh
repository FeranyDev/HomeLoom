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
config_export=$(curl -fsS "$BASE_URL/api/v1/system/config-export")
diagnostic_bundle=$(curl -fsS "$BASE_URL/api/v1/system/diagnostic-bundle")
mapping_preview=$(curl -fsS -H 'Content-Type: application/json' -d '{"profile":{"schemaVersion":1,"id":"smoke-map","version":1,"kind":"capability","inputType":"number","outputType":"number","transforms":[{"type":"scale","factor":1.8,"offset":32}]},"direction":"forward","value":{"type":"number","number":20}}' "$BASE_URL/api/v1/mapping/preview")
mapping_profiles=$(curl -fsS "$BASE_URL/api/v1/mapping/profiles")
curl -fsS -H 'Content-Type: application/json' -d '{"schemaVersion":1,"id":"smoke-invert","version":1,"kind":"provider","inputType":"bool","outputType":"bool","transforms":[{"type":"invert"}]}' "$BASE_URL/api/v1/mapping/profiles" >/dev/null
stored_mapping_preview=$(curl -fsS -H 'Content-Type: application/json' -d '{"profileId":"smoke-invert","direction":"forward","value":{"type":"bool","bool":true}}' "$BASE_URL/api/v1/mapping/preview")
case "$health" in *'"status":"ok"'*) ;; *) echo "unexpected health response: $health" >&2; exit 1;; esac
case "$version" in *'"version"'*) ;; *) echo "unexpected version response: $version" >&2; exit 1;; esac
case "$devices" in *'"schemaVersion":1'*) ;; *) echo "unexpected devices response: $devices" >&2; exit 1;; esac
case "$config_export" in *'"formatVersion":1'*'"providers"'*'"targets"'*'"profiles"'*) ;; *) echo "unexpected config export: $config_export" >&2; exit 1;; esac
case "$diagnostic_bundle" in *'"formatVersion":1'*) ;; *) echo "unexpected diagnostic bundle version: $diagnostic_bundle" >&2; exit 1;; esac
case "$diagnostic_bundle" in *'"configuration"'*) ;; *) echo "diagnostic bundle has no configuration: $diagnostic_bundle" >&2; exit 1;; esac
case "$diagnostic_bundle" in *'"metrics"'*) ;; *) echo "diagnostic bundle has no metrics: $diagnostic_bundle" >&2; exit 1;; esac
case "$mapping_preview" in *'"profileId":"smoke-map"'*'"number":68'*) ;; *) echo "unexpected mapping preview: $mapping_preview" >&2; exit 1;; esac
case "$mapping_profiles" in *'"builtin-active-low"'*'"builtIn":true'*) ;; *) echo "unexpected built-in profiles: $mapping_profiles" >&2; exit 1;; esac
case "$stored_mapping_preview" in *'"profileId":"smoke-invert"'*'"bool":false'*) ;; *) echo "unexpected stored mapping preview: $stored_mapping_preview" >&2; exit 1;; esac
curl -fsS -X DELETE "$BASE_URL/api/v1/mapping/profiles/smoke-invert" >/dev/null

kill -TERM "$PID"
wait "$PID"
PID=""
HOMELOOM_DATABASE="$TEMP/homeloom.db" "$BINARY" -backup "$TEMP/backup.db" >/dev/null
if [ ! -s "$TEMP/backup.db" ] || [ ! -s "$TEMP/backup.db.key" ]; then echo "backup smoke test produced incomplete database/key pair" >&2; exit 1; fi
HOMELOOM_DATABASE="$TEMP/restored.db" "$BINARY" -restore "$TEMP/backup.db" >/dev/null
if [ ! -s "$TEMP/restored.db" ] || [ ! -s "$TEMP/restored.db.key" ]; then echo "restore smoke test produced incomplete database/key pair" >&2; exit 1; fi
printf 'smoke test passed (%s)\n' "$ADDRESS"
