#!/usr/bin/env sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DEPS=$(
  cd "$ROOT/camera-kernel"
  "$ROOT/scripts/dev-env.sh" go list -deps .
)

# Packages that must never re-enter the Camera Kernel dependency closure.
# Keep this list aligned with main.go's capability allow-list and the
# implementation plan's non-goals.
for package in \
  github.com/AlexxIT/go2rtc/internal/alsa \
  github.com/AlexxIT/go2rtc/internal/bubble \
  github.com/AlexxIT/go2rtc/internal/debug \
  github.com/AlexxIT/go2rtc/internal/doorbird \
  github.com/AlexxIT/go2rtc/internal/dvrip \
  github.com/AlexxIT/go2rtc/internal/echo \
  github.com/AlexxIT/go2rtc/internal/eseecloud \
  github.com/AlexxIT/go2rtc/internal/expr \
  github.com/AlexxIT/go2rtc/internal/flussonic \
  github.com/AlexxIT/go2rtc/internal/gopro \
  github.com/AlexxIT/go2rtc/internal/hass \
  github.com/AlexxIT/go2rtc/internal/hls \
  github.com/AlexxIT/go2rtc/internal/http \
  github.com/AlexxIT/go2rtc/internal/isapi \
  github.com/AlexxIT/go2rtc/internal/ivideon \
  github.com/AlexxIT/go2rtc/internal/mjpeg \
  github.com/AlexxIT/go2rtc/internal/mpegts \
  github.com/AlexxIT/go2rtc/internal/multitrans \
  github.com/AlexxIT/go2rtc/internal/nest \
  github.com/AlexxIT/go2rtc/internal/ngrok \
  github.com/AlexxIT/go2rtc/internal/pinggy \
  github.com/AlexxIT/go2rtc/internal/ring \
  github.com/AlexxIT/go2rtc/internal/roborock \
  github.com/AlexxIT/go2rtc/internal/rtmp \
  github.com/AlexxIT/go2rtc/internal/tapo \
  github.com/AlexxIT/go2rtc/internal/tuya \
  github.com/AlexxIT/go2rtc/internal/v4l2 \
  github.com/AlexxIT/go2rtc/internal/webrtc \
  github.com/AlexxIT/go2rtc/internal/webtorrent \
  github.com/AlexxIT/go2rtc/internal/wyoming \
  github.com/AlexxIT/go2rtc/internal/wyze \
  github.com/AlexxIT/go2rtc/internal/yandex \
  github.com/AlexxIT/go2rtc/pkg/alsa \
  github.com/AlexxIT/go2rtc/pkg/doorbird \
  github.com/AlexxIT/go2rtc/pkg/dvrip \
  github.com/AlexxIT/go2rtc/pkg/hls \
  github.com/AlexxIT/go2rtc/pkg/mqtt \
  github.com/AlexxIT/go2rtc/pkg/ring \
  github.com/AlexxIT/go2rtc/pkg/rtmp \
  github.com/AlexxIT/go2rtc/pkg/tapo \
  github.com/AlexxIT/go2rtc/pkg/tuya \
  github.com/AlexxIT/go2rtc/pkg/v4l2 \
  github.com/AlexxIT/go2rtc/pkg/webrtc \
  github.com/AlexxIT/go2rtc/pkg/webtorrent \
  github.com/AlexxIT/go2rtc/pkg/wyoming \
  github.com/AlexxIT/go2rtc/pkg/wyze \
  github.com/AlexxIT/go2rtc/pkg/xiaomi \
  github.com/AlexxIT/go2rtc/pkg/xiaomi/legacy \
  github.com/AlexxIT/go2rtc/pkg/tutk/dtls
do
  case "
$DEPS
" in
    *"
$package
"*)
      echo "camera kernel contains excluded package: $package" >&2
      exit 1
      ;;
  esac
done

printf 'camera kernel dependency scope verified\n'
