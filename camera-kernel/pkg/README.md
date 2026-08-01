# Camera Kernel packages

This tree is a pruned go2rtc v1.9.14 package set for HomeLoom Camera Kernel.

Only packages reachable from `main.go`'s capability allow-list are kept:

- transport/codecs: `rtsp`, `h264`, `h265`, `aac`, `opus`, `pcm`, `mjpeg`, `mp4`, `mpegts`, `mpjpeg`, `flv`, `wav`, `y4m`
- discovery/output: `onvif`, `homekit`, `hap`, `srtp`, `mdns`
- Xiaomi MISS: `xiaomi/miss`, `xiaomi/crypto`, supporting `tutk` helpers
- shared utilities: `core`, `creds`, `ffmpeg`, `magic`, `probe`, `shell`, `tcp`, `xnet`, `yaml`, `bits`, `iso`

Vendor protocol packages (WebRTC, RTMP, HLS, Tapo, Tuya, Ring, Wyze, ALSA, V4L2, etc.)
are intentionally absent. Re-adding one requires Provider contract, security review,
tests, plan update, and an explicit `main.go` initializer.
