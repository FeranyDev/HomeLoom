package opus

import (
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/pion/rtp"
)

// Some info about this magic:
// - Apple has no respect for RFC 7587 standard and using RFC 3550 for RTP timestamps
// - Apple can request packets with 20ms duration over LAN connection and 60ms over LTE
// - FFmpeg produce packets with 20ms duration by default and only one frame per packet
// - FFmpeg should use "-min_comp 0" option, so every packet will be same duration
// - Apple doesn't care about real sample rate of track
// - Apple only cares about proper timestamp based on REQUESTED sample rate

// RepackToHAP - convert standart RTP packet with OPUS to HAP packet
// We expect that:
// - incoming packet will be 20ms duration and only one frame per packet
// - outgouing packet will be 20ms or 60ms duration
// - incoming sample rate will be any (but not very big if we needs 60ms packets for output)
// - outgoing timestamps follow the sample rate selected by the controller
// https://github.com/AlexxIT/go2rtc/issues/667
func RepackToHAP(sampleRate int, rtpTime byte, handler core.HandlerFunc) core.HandlerFunc {
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	switch rtpTime {
	case 20:
		return repackToHAP20(sampleRate, handler)
	case 40, 60:
		return repackToHAPMulti(sampleRate, rtpTime, handler)
	}
	return handler
}

// repackToHAP20 - just fix RTP timestamp from RFC 7587 to RFC 3550
func repackToHAP20(sampleRate int, handler core.HandlerFunc) core.HandlerFunc {
	var timestamp uint32
	step := uint32(sampleRate * 20 / 1000)

	return func(pkt *rtp.Packet) {
		timestamp += step

		clone := *pkt
		clone.Timestamp = timestamp
		handler(&clone)
	}
}

// repackToHAPMulti buffers incoming Opus frames and emits one HAP bundle every
// rtpTime of wall time on a fixed schedule, tolerating any input cadence.
// FFmpeg delivers the 20 ms frames in bursts (a camera that packetizes audio
// at 40 ms yields two frames at once), so flushing on frame count emitted
// 60 ms of audio at irregular 40/80 ms real intervals and made Apple's audio
// stutter. Accumulating by the TOC-signaled frame duration instead of a fixed
// frame count also keeps the bundle's RTP timestamp aligned with the actual
// audio it carries whether the input is 20 ms or 40 ms frames. The chained
// timer self-terminates when the queue drains so no goroutine leaks.
// RFC 6716 code 3 VBR packets carry lengths for the first M-1 frames only.
// thanks to @civita idea https://github.com/AlexxIT/go2rtc/pull/843
func repackToHAPMulti(sampleRate int, rtpTime byte, handler core.HandlerFunc) core.HandlerFunc {
	interval := time.Duration(rtpTime) * time.Millisecond

	var mu sync.Mutex
	var queue []*rtp.Packet
	var scheduled bool
	var sequence uint16
	var timestamp uint32

	var flush func()
	flush = func() {
		mu.Lock()
		scheduled = false
		if len(queue) == 0 {
			mu.Unlock()
			return
		}
		// Take frames until the bundle holds at least rtpTime of audio. 20 ms
		// input gives exactly three frames; 40 ms input gives two. Timestamp
		// advances by the real audio duration so playback speed stays correct.
		batch := queue[:1]
		durationMs := opusFrameDurationMs(batch[0].Payload[0])
		queue = queue[1:]
		for len(queue) > 0 && durationMs < int(rtpTime) {
			frame := queue[0]
			queue = queue[1:]
			batch = append(batch, frame)
			durationMs += opusFrameDurationMs(frame.Payload[0])
		}
		sequence++
		timestamp += uint32(sampleRate) * uint32(durationMs) / 1000
		seq, ts := sequence, timestamp
		if len(queue) > 0 {
			scheduled = true
			time.AfterFunc(interval, flush)
		}
		mu.Unlock()

		handler(buildHAPCode3Packet(batch[0], batch, seq, ts))
	}

	return func(pkt *rtp.Packet) {
		if len(pkt.Payload) < 2 {
			return
		}
		mu.Lock()
		queue = append(queue, pkt)
		if !scheduled {
			scheduled = true
			time.AfterFunc(interval, flush)
		}
		mu.Unlock()
	}
}

// opusFrameDurationMs returns the frame size in milliseconds signaled by an
// Opus TOC byte per RFC 6716 Table 2. Only the 20 ms case matters for ffmpeg
// output, but 40 ms source-packetized frames are handled too.
func opusFrameDurationMs(toc byte) int {
	switch cfg := int(toc >> 3); {
	case cfg < 12: // SILK-only, 10/20/40/60 ms
		return []int{10, 20, 40, 60}[cfg%4]
	case cfg < 16: // Hybrid, 10/20 ms
		return 10 + 10*(cfg%2)
	default: // CELT-only, 2.5/5/10/20 ms
		return []int{2, 5, 10, 20}[cfg%4]
	}
}

func buildHAPCode3Packet(first *rtp.Packet, batch []*rtp.Packet, sequence uint16, timestamp uint32) *rtp.Packet {
	toc := first.Payload[0]
	payload := make([]byte, 2, 2+2*len(batch)+sumPayloads(batch))
	payload[0] = toc | 0b11 // code 3 (multiple frames per packet)
	payload[1] = 0b1000_0000 | byte(len(batch))
	for _, frame := range batch[:len(batch)-1] {
		payload = appendOpusFrameSize(payload, len(frame.Payload)-1)
	}
	for _, frame := range batch {
		payload = append(payload, frame.Payload[1:]...)
	}

	clone := *first
	clone.Payload = payload
	clone.SequenceNumber = sequence
	clone.Timestamp = timestamp
	return &clone
}

func sumPayloads(batch []*rtp.Packet) int {
	n := 0
	for _, frame := range batch {
		n += len(frame.Payload) - 1
	}
	return n
}

func appendOpusFrameSize(dst []byte, size int) []byte {
	if size < 252 {
		return append(dst, byte(size))
	}
	first := 252 + size%4
	return append(dst, byte(first), byte((size-first)/4))
}
