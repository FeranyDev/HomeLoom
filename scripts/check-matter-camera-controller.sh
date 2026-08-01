#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 [--controller PATH] [--run]"
  echo
  echo "Checks for connectedhomeip v1.5.1.0 chip-camera-controller."
  echo "--run starts its interactive shell; it does not claim test success."
}

controller="${CHIP_CAMERA_CONTROLLER:-}"
run=false

while (($# > 0)); do
  case "$1" in
    --controller)
      if (($# < 2)); then
        echo "ERROR: --controller requires a path" >&2
        exit 64
      fi
      controller="$2"
      shift 2
      ;;
    --run)
      run=true
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "ERROR: unknown argument: $1" >&2
      usage >&2
      exit 64
      ;;
  esac
done

if [[ -z "$controller" ]]; then
  controller="$(command -v chip-camera-controller || true)"
fi

if [[ -z "$controller" ]]; then
  for candidate in \
    "./out/linux-x64-camera-controller/chip-camera-controller" \
    "./connectedhomeip/out/linux-x64-camera-controller/chip-camera-controller"
  do
    if [[ -x "$candidate" ]]; then
      controller="$candidate"
      break
    fi
  done
fi

if [[ -z "$controller" ]]; then
  echo "NOT RUN: chip-camera-controller was not found." >&2
  echo "Set CHIP_CAMERA_CONTROLLER or pass --controller PATH." >&2
  echo "Build reference: connectedhomeip v1.5.1.0 examples/camera-controller/README.md" >&2
  exit 2
fi

if [[ ! -f "$controller" || ! -x "$controller" ]]; then
  echo "NOT RUN: controller is not an executable regular file: $controller" >&2
  exit 2
fi

if ! grep -aFq "Commands for camera live view." "$controller" ||
   ! grep -aFq "Commands for WebRTC." "$controller"; then
  echo "NOT RUN: executable does not expose the expected Camera Controller command surface: $controller" >&2
  exit 2
fi

echo "READY: Camera Controller command surface found at $controller"
echo "This preflight does not prove commissioning, WebRTC, Snapshot, or conformance."
echo "Record the connectedhomeip tag/commit and this file's SHA-256 before testing."
echo "Checklist: docs/Matter_Camera_MC6_验证清单.md"

if [[ "$run" == true ]]; then
  exec "$controller"
fi
