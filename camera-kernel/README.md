# HomeLoom Camera Kernel

This directory contains HomeLoom's constrained camera-media fork, based on
go2rtc v1.9.14 under its MIT license. Unused upstream protocol packages are
removed from the source tree, not only left unregistered in `main.go`.

The executable intentionally initializes only:

- RTSP input and loopback RTSP transport;
- ONVIF discovery/profile resolution to RTSP;
- Xiaomi MISS input;
- allow-listed FFmpeg transcoding;
- independent HomeKit Camera output and SRTP;
- the two MP4 endpoints used by HomeLoom Device Center.

It does not initialize the generic go2rtc Web UI, WebSocket endpoint, WebRTC,
RTMP, HLS, DVR, tunnelling, MQTT, cloud-camera, or other vendor modules.
HomeLoom-generated configuration additionally applies an HTTP path allow-list,
loopback-only RTSP listeners, deterministic per-camera ports, protected runtime
directories, and environment-only secret injection.

`main.go` is the compile-time capability allow-list. Adding a protocol requires
an explicit HomeLoom Provider contract, security review, tests, and plan update.
