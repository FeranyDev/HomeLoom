#!/usr/bin/env sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BINARY=${HOMELOOM_BINARY:-$ROOT/backend/bin/homeloom}
ADDRESS=${HOMELOOM_SMOKE_ADDRESS:-127.0.0.1:18090}
DATABASE_URL=${HOMELOOM_SMOKE_DATABASE_URL:-}
BASE_URL="http://$ADDRESS"
TEMP=$(mktemp -d "${TMPDIR:-/tmp}/homeloom-smoke.XXXXXX")
COOKIE_JAR="$TEMP/cookies.txt"
PID=""
HAP_PORT=$("$ROOT/scripts/dev-env.sh" node -e 'const server=require("net").createServer(); server.listen(0,"127.0.0.1",()=>{console.log(server.address().port);server.close()})')
SECOND_HAP_PORT=$("$ROOT/scripts/dev-env.sh" node -e 'const server=require("net").createServer(); server.listen(0,"127.0.0.1",()=>{console.log(server.address().port);server.close()})')
while [ "$SECOND_HAP_PORT" = "$HAP_PORT" ]; do
  SECOND_HAP_PORT=$("$ROOT/scripts/dev-env.sh" node -e 'const server=require("net").createServer(); server.listen(0,"127.0.0.1",()=>{console.log(server.address().port);server.close()})')
done

cleanup() {
  if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
    kill -TERM "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
  rm -rf "$TEMP"
}
trap cleanup EXIT INT TERM

start_server() {
  HOMELOOM_HTTP_ADDRESS="$ADDRESS" HOMELOOM_DATABASE_URL="$DATABASE_URL" HOMELOOM_MASTER_KEY="$TEMP/homeloom.key" "$BINARY" >"$TEMP/server.log" 2>&1 &
  PID=$!
  ready=false
  for _ in $(seq 1 40); do
    if curl -fsS "$BASE_URL/ready" >/dev/null 2>&1; then ready=true; break; fi
    if ! kill -0 "$PID" 2>/dev/null; then cat "$TEMP/server.log" >&2; exit 1; fi
    sleep 0.25
  done
  if [ "$ready" != true ]; then cat "$TEMP/server.log" >&2; exit 1; fi
}

stop_server() {
  kill -TERM "$PID"
  wait "$PID"
  PID=""
}

if [ ! -x "$BINARY" ]; then
  echo "missing executable: $BINARY (run ./scripts/build.sh first)" >&2
  exit 1
fi
if [ -z "$DATABASE_URL" ]; then
  echo "HOMELOOM_SMOKE_DATABASE_URL must point to an empty, disposable PostgreSQL database or schema" >&2
  exit 2
fi

cd "$TEMP"
start_server

health=$(curl -fsS "$BASE_URL/health")
version=$(curl -fsS "$BASE_URL/api/v1/system/version")
unauthorized=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE_URL/api/v1/devices")
if [ "$unauthorized" != 401 ]; then echo "management API did not require authentication: $unauthorized" >&2; exit 1; fi
auth=$(curl -fsS -c "$COOKIE_JAR" -H 'Content-Type: application/json' -d '{"username":"smoke-admin","password":"smoke-test-password"}' "$BASE_URL/api/v1/auth/setup")
csrf=$(awk '$6 == "homeloom_csrf" { print $7 }' "$COOKIE_JAR")
if [ -z "$csrf" ]; then echo "authentication did not set CSRF cookie" >&2; exit 1; fi
main_target=$(curl -fsS -b "$COOKIE_JAR" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -X PUT -d "{\"type\":\"apple-hap\",\"name\":\"HomeLoom 主桥\",\"enabled\":true,\"address\":\"127.0.0.1:$HAP_PORT\",\"pin\":\"00102003\",\"setupId\":\"HLM1\"}" "$BASE_URL/api/v1/targets/apple-main")
devices=$(curl -fsS -b "$COOKIE_JAR" "$BASE_URL/api/v1/devices")
config_export=$(curl -fsS -b "$COOKIE_JAR" "$BASE_URL/api/v1/system/config-export")
diagnostic_bundle=$(curl -fsS -b "$COOKIE_JAR" "$BASE_URL/api/v1/system/diagnostic-bundle")
mapping_preview=$(curl -fsS -b "$COOKIE_JAR" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -d '{"profile":{"schemaVersion":1,"id":"smoke-map","version":1,"kind":"capability","inputType":"number","outputType":"number","transforms":[{"type":"scale","factor":1.8,"offset":32}]},"direction":"forward","value":{"type":"number","number":20}}' "$BASE_URL/api/v1/mapping/preview")
mapping_profiles=$(curl -fsS -b "$COOKIE_JAR" "$BASE_URL/api/v1/mapping/profiles")
curl -fsS -b "$COOKIE_JAR" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -d '{"schemaVersion":1,"id":"smoke-invert","version":1,"kind":"provider","inputType":"bool","outputType":"bool","transforms":[{"type":"invert"}]}' "$BASE_URL/api/v1/mapping/profiles" >/dev/null
stored_mapping_preview=$(curl -fsS -b "$COOKIE_JAR" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -d '{"profileId":"smoke-invert","direction":"forward","value":{"type":"bool","bool":true}}' "$BASE_URL/api/v1/mapping/preview")
second_target=$(curl -fsS -b "$COOKIE_JAR" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -d "{\"id\":\"smoke-second\",\"type\":\"apple-hap\",\"name\":\"Smoke Second\",\"enabled\":true,\"address\":\"127.0.0.1:$SECOND_HAP_PORT\",\"pin\":\"00909009\",\"setupId\":\"SMK2\"}" "$BASE_URL/api/v1/targets")
targets_with_second=$(curl -fsS -b "$COOKIE_JAR" "$BASE_URL/api/v1/targets")
case "$health" in *'"status":"ok"'*) ;; *) echo "unexpected health response: $health" >&2; exit 1;; esac
case "$version" in *'"version"'*) ;; *) echo "unexpected version response: $version" >&2; exit 1;; esac
case "$auth" in *'"initialized":true'*'"authenticated":true'*) ;; *) echo "unexpected authentication response: $auth" >&2; exit 1;; esac
case "$main_target" in *'"id":"apple-main"'*'"status":"running"'*) ;; *) echo "default HomeKit target did not start on its isolated port: $main_target" >&2; exit 1;; esac
case "$devices" in *'"schemaVersion":1'*) ;; *) echo "unexpected devices response: $devices" >&2; exit 1;; esac
case "$config_export" in *'"formatVersion":1'*'"providers"'*'"targets"'*'"profiles"'*) ;; *) echo "unexpected config export: $config_export" >&2; exit 1;; esac
case "$diagnostic_bundle" in *'"formatVersion":1'*) ;; *) echo "unexpected diagnostic bundle version: $diagnostic_bundle" >&2; exit 1;; esac
case "$diagnostic_bundle" in *'"configuration"'*) ;; *) echo "diagnostic bundle has no configuration: $diagnostic_bundle" >&2; exit 1;; esac
case "$diagnostic_bundle" in *'"metrics"'*) ;; *) echo "diagnostic bundle has no metrics: $diagnostic_bundle" >&2; exit 1;; esac
case "$mapping_preview" in *'"profileId":"smoke-map"'*'"number":68'*) ;; *) echo "unexpected mapping preview: $mapping_preview" >&2; exit 1;; esac
case "$mapping_profiles" in *'"builtin-active-low"'*'"builtIn":true'*) ;; *) echo "unexpected built-in profiles: $mapping_profiles" >&2; exit 1;; esac
case "$stored_mapping_preview" in *'"profileId":"smoke-invert"'*'"bool":false'*) ;; *) echo "unexpected stored mapping preview: $stored_mapping_preview" >&2; exit 1;; esac
case "$second_target" in *'"id":"smoke-second"'*'"status":"running"'*) ;; *) echo "second HomeKit target did not start: $second_target" >&2; exit 1;; esac
case "$targets_with_second" in *'"id":"apple-main"'*'"id":"smoke-second"'*) ;; *) echo "multiple HomeKit targets were not listed: $targets_with_second" >&2; exit 1;; esac
curl -fsS -b "$COOKIE_JAR" -H "X-CSRF-Token: $csrf" -X DELETE "$BASE_URL/api/v1/mapping/profiles/smoke-invert" >/dev/null
curl -fsS -b "$COOKIE_JAR" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -X PUT -d "{\"type\":\"apple-hap\",\"name\":\"Smoke Second Disabled\",\"enabled\":false,\"address\":\"127.0.0.1:$SECOND_HAP_PORT\",\"pin\":\"00909009\",\"setupId\":\"SMK2\"}" "$BASE_URL/api/v1/targets/smoke-second" >/dev/null
curl -fsS -b "$COOKIE_JAR" -H "X-CSRF-Token: $csrf" -X DELETE "$BASE_URL/api/v1/targets/smoke-second" >/dev/null

stable_targets=$(curl -fsS -b "$COOKIE_JAR" "$BASE_URL/api/v1/targets")
stable_setup_uri=$(printf '%s' "$stable_targets" | sed -n 's/.*"id":"apple-main".*"setupUri":"\([^"]*\)".*/\1/p')
if [ -z "$stable_setup_uri" ] || [ ! -d "$TEMP/data/hap/apple-main" ]; then
  echo "default HomeKit identity was not initialized: $stable_targets" >&2
  exit 1
fi

stop_server
for attempt in 1 2 3; do
  start_server
  restarted_targets=$(curl -fsS -b "$COOKIE_JAR" "$BASE_URL/api/v1/targets")
  restarted_setup_uri=$(printf '%s' "$restarted_targets" | sed -n 's/.*"id":"apple-main".*"setupUri":"\([^"]*\)".*/\1/p')
  if [ "$restarted_setup_uri" != "$stable_setup_uri" ]; then
    echo "HomeKit setup identity changed after restart $attempt: $restarted_targets" >&2
    exit 1
  fi
  stop_server
done
HOMELOOM_DATABASE_URL="$DATABASE_URL" HOMELOOM_MASTER_KEY="$TEMP/homeloom.key" "$BINARY" -backup "$TEMP/backup.json" >/dev/null
if [ ! -s "$TEMP/backup.json" ] || [ ! -s "$TEMP/backup.json.key" ]; then echo "backup smoke test produced incomplete snapshot/key pair" >&2; exit 1; fi
HOMELOOM_DATABASE_URL="$DATABASE_URL" HOMELOOM_MASTER_KEY="$TEMP/homeloom.key" "$BINARY" -restore "$TEMP/backup.json" -restore-replace >/dev/null
printf 'smoke test passed (%s)\n' "$ADDRESS"
