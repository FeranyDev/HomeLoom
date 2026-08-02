#!/usr/bin/env sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: $0 <version> [output-directory]" >&2
  exit 2
fi

VERSION=$1
case "$VERSION" in
  ''|*[!A-Za-z0-9._+-]*)
    echo "version may contain only letters, numbers, dot, underscore, plus and hyphen" >&2
    exit 2
    ;;
esac
OUTPUT=${2:-$ROOT/dist}
case "$OUTPUT" in
  /*) ;;
  *) OUTPUT="$ROOT/$OUTPUT" ;;
esac
COMMIT=${HOMELOOM_COMMIT:-$(git -C "$ROOT" rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')}
BUILD_TIME=${HOMELOOM_BUILD_TIME:-$(date -u '+%Y-%m-%dT%H:%M:%SZ')}
PACKAGE=github.com/feranydev/homeloom/backend/internal/buildinfo
TARGETS=${HOMELOOM_TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64}
MATTER_RUNTIME_MODE=${HOMELOOM_MATTER_RUNTIME_MODE:-js}
SEA_NODE_VERSION=${HOMELOOM_SEA_NODE_VERSION:-26.5.0}
SEA_NODE_CACHE=${HOMELOOM_SEA_NODE_CACHE:-$ROOT/.cache/matter-runtime/node}
SEA_BUILDER_NODE=${HOMELOOM_SEA_BUILDER_NODE:-${HOMELOOM_SEA_NODE:-}}
case "$SEA_NODE_CACHE" in
  /*) ;;
  *) SEA_NODE_CACHE="$ROOT/$SEA_NODE_CACHE" ;;
esac
case "$SEA_NODE_VERSION" in
  ''|*[!A-Za-z0-9._-]*)
    echo "HOMELOOM_SEA_NODE_VERSION contains unsupported characters" >&2
    exit 2
    ;;
esac

case "$MATTER_RUNTIME_MODE" in
  js|sea) ;;
  *)
    echo "unsupported HOMELOOM_MATTER_RUNTIME_MODE: $MATTER_RUNTIME_MODE (expected js or sea)" >&2
    exit 2
    ;;
esac

if [ "$MATTER_RUNTIME_MODE" = sea ] && [ -z "$SEA_BUILDER_NODE" ]; then
  echo "SEA mode requires HOMELOOM_SEA_BUILDER_NODE or HOMELOOM_SEA_NODE" >&2
  exit 2
fi

resolve_target_node() {
  GOOS=$1
  GOARCH=$2
  case "$GOOS/$GOARCH" in
    linux/amd64) NODE_PLATFORM=linux; NODE_ARCH=x64; NODE_ARCHIVE=tar.xz ;;
    linux/arm64) NODE_PLATFORM=linux; NODE_ARCH=arm64; NODE_ARCHIVE=tar.xz ;;
    darwin/amd64) NODE_PLATFORM=darwin; NODE_ARCH=x64; NODE_ARCHIVE=tar.xz ;;
    darwin/arm64) NODE_PLATFORM=darwin; NODE_ARCH=arm64; NODE_ARCHIVE=tar.xz ;;
    windows/amd64) NODE_PLATFORM=win; NODE_ARCH=x64; NODE_ARCHIVE=zip ;;
    windows/arm64) NODE_PLATFORM=win; NODE_ARCH=arm64; NODE_ARCHIVE=zip ;;
    *) echo "unsupported Node SEA target: $GOOS/$GOARCH" >&2; exit 2 ;;
  esac

  NODE_CACHE="$SEA_NODE_CACHE/v${SEA_NODE_VERSION}"
  NODE_ROOT="$NODE_CACHE/node-v${SEA_NODE_VERSION}-${NODE_PLATFORM}-${NODE_ARCH}"
  if [ "$NODE_ARCHIVE" = zip ]; then
    NODE_BINARY="$NODE_ROOT/node.exe"
    ASSET="node-v${SEA_NODE_VERSION}-${NODE_PLATFORM}-${NODE_ARCH}.zip"
  else
    NODE_BINARY="$NODE_ROOT/bin/node"
    ASSET="node-v${SEA_NODE_VERSION}-${NODE_PLATFORM}-${NODE_ARCH}.tar.xz"
  fi
  ARCHIVE="$NODE_CACHE/$ASSET"
  CHECKSUMS="$NODE_CACHE/SHASUMS256.txt"
  mkdir -p "$NODE_CACHE"

  if ! test -s "$ARCHIVE"; then
    curl --fail --location --silent --show-error --retry 3 \
      --output "$ARCHIVE.tmp" \
      "https://nodejs.org/dist/v${SEA_NODE_VERSION}/$ASSET"
    mv "$ARCHIVE.tmp" "$ARCHIVE"
  fi
  if ! test -s "$CHECKSUMS"; then
    curl --fail --location --silent --show-error --retry 3 \
      --output "$CHECKSUMS.tmp" \
      "https://nodejs.org/dist/v${SEA_NODE_VERSION}/SHASUMS256.txt"
    mv "$CHECKSUMS.tmp" "$CHECKSUMS"
  fi
  verify_node_archive "$ARCHIVE" "$ASSET" "$CHECKSUMS"

  if ! test -f "$NODE_BINARY"; then
    if [ "$NODE_ARCHIVE" = zip ]; then
      unzip -q "$ARCHIVE" -d "$NODE_CACHE"
    else
      tar -xJf "$ARCHIVE" -C "$NODE_CACHE"
    fi
  fi
  if ! test -f "$NODE_BINARY"; then
    echo "Node SEA binary was not found after extraction: $NODE_BINARY" >&2
    exit 1
  fi
  printf '%s\n' "$NODE_BINARY"
}

verify_node_archive() {
  ARCHIVE=$1
  ASSET=$2
  CHECKSUMS=$3
  EXPECTED=$(awk -v asset="$ASSET" '$2 == asset { print $1 }' "$CHECKSUMS")
  if [ -z "$EXPECTED" ]; then
    echo "Node checksum is missing for $ASSET" >&2
    exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL=$(sha256sum "$ARCHIVE" | awk '{ print $1 }')
  else
    ACTUAL=$(shasum -a 256 "$ARCHIVE" | awk '{ print $1 }')
  fi
  if [ "$ACTUAL" != "$EXPECTED" ]; then
    echo "Node archive checksum mismatch: $ASSET" >&2
    exit 1
  fi
}

append_checksum() {
  FILE=$1
  RELATIVE_FILE=${FILE#"$OUTPUT/"}
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$OUTPUT" && sha256sum "$RELATIVE_FILE") >> "$OUTPUT/SHA256SUMS"
  else
    (cd "$OUTPUT" && shasum -a 256 "$RELATIVE_FILE") >> "$OUTPUT/SHA256SUMS"
  fi
}

is_host_target() {
  GOOS=$1
  GOARCH=$2
  HOST_OS=$(uname -s)
  HOST_ARCH=$(uname -m)
  case "$HOST_OS/$HOST_ARCH" in
    Darwin/arm64) HOST_PLATFORM=darwin/arm64 ;;
    Darwin/x86_64) HOST_PLATFORM=darwin/amd64 ;;
    Linux/aarch64|Linux/arm64) HOST_PLATFORM=linux/arm64 ;;
    Linux/x86_64|Linux/amd64) HOST_PLATFORM=linux/amd64 ;;
    *) return 1 ;;
  esac
  [ "$HOST_PLATFORM" = "$GOOS/$GOARCH" ]
}

mkdir -p "$OUTPUT"
(cd "$ROOT/frontend" && "$ROOT/scripts/dev-env.sh" npm run build:embed)
(cd "$ROOT/matter-runtime" && "$ROOT/scripts/dev-env.sh" npm run build)
: > "$OUTPUT/SHA256SUMS"

for PLATFORM in $TARGETS; do
  case "$PLATFORM" in
    linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64|windows/arm64) ;;
    *) echo "unsupported target: $PLATFORM" >&2; exit 2 ;;
  esac
  GOOS=${PLATFORM%/*}
  GOARCH=${PLATFORM#*/}
  TARGET_DIR="$OUTPUT/${GOOS}_${GOARCH}"
  SUFFIX=""
  if [ "$GOOS" = windows ]; then SUFFIX=".exe"; fi
  TARGET="$TARGET_DIR/homeloom${SUFFIX}"
  MATTER_RUNTIME_TARGET="$TARGET_DIR/homeloom-matter-runtime${SUFFIX}"
  mkdir -p "$TARGET_DIR"
  SEA_SIGN_ARGUMENT=--skip-sign
  if [ "$GOOS" = darwin ] && [ "$(uname -s)" = Darwin ]; then
    SEA_SIGN_ARGUMENT=
  fi

  (cd "$ROOT/backend" && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" "$ROOT/scripts/dev-env.sh" go build \
    -trimpath -tags embed_webui \
    -ldflags "-s -w -X ${PACKAGE}.Version=${VERSION} -X ${PACKAGE}.Commit=${COMMIT} -X ${PACKAGE}.BuildTime=${BUILD_TIME}" \
    -o "$TARGET" ./cmd/homeloom)
  test -s "$TARGET"

  if [ "$MATTER_RUNTIME_MODE" = sea ]; then
    TARGET_NODE=$(resolve_target_node "$GOOS" "$GOARCH")
    (cd "$ROOT/matter-runtime" && \
      HOMELOOM_SEA_BUILDER_NODE="$SEA_BUILDER_NODE" \
      "$ROOT/scripts/dev-env.sh" npm run build:sea -- \
        --output "$MATTER_RUNTIME_TARGET" \
        --target-node "$TARGET_NODE" \
        --target-node-version "$SEA_NODE_VERSION" \
        --target-platform "$GOOS" \
        $SEA_SIGN_ARGUMENT)
    test -s "$MATTER_RUNTIME_TARGET"
    append_checksum "$MATTER_RUNTIME_TARGET"
  fi

  append_checksum "$TARGET"
  if is_host_target "$GOOS" "$GOARCH" && [ "$MATTER_RUNTIME_MODE" = sea ]; then
    "$ROOT/scripts/tests/matter-runtime-sea_test.sh" "$MATTER_RUNTIME_TARGET"
  else
    printf 'built %s/%s without local runtime execution check\n' "$GOOS" "$GOARCH"
  fi
  printf 'built %s/%s: %s\n' "$GOOS" "$GOARCH" "$TARGET_DIR"
done

printf 'multi-platform %s build passed: %s\n' "$MATTER_RUNTIME_MODE" "$OUTPUT"
