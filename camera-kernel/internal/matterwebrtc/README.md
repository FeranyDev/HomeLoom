# Matter WebRTC output

This package is the private media plane used by HomeLoom's standalone Matter
Camera target. It is not the upstream go2rtc WebRTC module and deliberately
does not expose a web UI or WebSocket API.

The only endpoint is `POST /api/matter/webrtc`, normally reachable through the
camera-kernel Unix socket and the API `allow_paths` list. Requests use one of
four operations:

- `open`: `{"operation":"open","streamId":"...","sdp":"..."}`
- `reoffer`: `{"operation":"reoffer","sessionId":"...","sdp":"..."}`
- `addIce`: `{"operation":"addIce","sessionId":"...","candidate":{...}}`
- `close`: `{"operation":"close","sessionId":"..."}`

Only one session may be active. The peer registers H.264
`packetization-mode=1/90000` and Opus `48000`, gathers UDP host candidates
without STUN/TURN, and forwards RTP directly from the existing streams graph.
All operations enforce bounded JSON, SDP, candidate, stream, and session input.
