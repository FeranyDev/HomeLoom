#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
checker="$project_root/scripts/check-matter-camera-controller.sh"
test_tmp="$(mktemp -d "${TMPDIR:-/tmp}/homeloom-matter-camera-check.XXXXXX")"
trap 'rm -rf "$test_tmp"' EXIT

expect_status() {
  local expected="$1"
  shift

  set +e
  "$@" >"$test_tmp/stdout" 2>"$test_tmp/stderr"
  local actual=$?
  set -e

  if [[ "$actual" -ne "$expected" ]]; then
    echo "expected status $expected, got $actual: $*" >&2
    sed -n '1,120p' "$test_tmp/stdout" >&2
    sed -n '1,120p' "$test_tmp/stderr" >&2
    exit 1
  fi
}

expect_status 2 "$checker" --controller "$test_tmp/missing"
grep -Fq "NOT RUN" "$test_tmp/stderr"

invalid="$test_tmp/not-camera-controller"
printf '#!/usr/bin/env bash\nexit 0\n' >"$invalid"
chmod +x "$invalid"
expect_status 2 "$checker" --controller "$invalid"
grep -Fq "expected Camera Controller command surface" "$test_tmp/stderr"

valid="$test_tmp/chip-camera-controller"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  '# Commands for camera live view.' \
  '# Commands for WebRTC.' \
  'exit 0' >"$valid"
chmod +x "$valid"
expect_status 0 "$checker" --controller "$valid"
grep -Fq "READY" "$test_tmp/stdout"
grep -Fq "does not prove commissioning" "$test_tmp/stdout"

echo "PASS: check-matter-camera-controller.sh"
