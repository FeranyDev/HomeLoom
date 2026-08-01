package opus

import (
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func TestRepackToHAPUsesControllerSampleRate(t *testing.T) {
	var packets []*rtp.Packet
	handler := RepackToHAP(24000, 20, func(packet *rtp.Packet) {
		packets = append(packets, packet)
	})
	handler(&rtp.Packet{Payload: []byte{0x78, 1}})
	handler(&rtp.Packet{Payload: []byte{0x78, 2}})

	if len(packets) != 2 || packets[0].Timestamp != 480 || packets[1].Timestamp != 960 {
		t.Fatalf("24 kHz HomeKit timestamps = %#v", packets)
	}
}

func TestRepackToHAPSixtyMillisecondOutputIsPacedNotBursty(t *testing.T) {
	var mu sync.Mutex
	var arrivals []time.Time
	handler := RepackToHAP(24000, 60, func(packet *rtp.Packet) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
	})

	// Simulate the real camera/ffmpeg cadence: 20 ms Opus frames delivered in
	// bursts of two every 40 ms (a source that packetizes audio at 40 ms).
	payload := make([]byte, 61)
	payload[0] = 0xd8
	for burst := 0; burst < 12; burst++ {
		handler(&rtp.Packet{Payload: payload})
		handler(&rtp.Packet{Payload: payload})
		time.Sleep(40 * time.Millisecond)
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		mu.Lock()
		count := len(arrivals)
		mu.Unlock()
		if count >= 6 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(arrivals) < 6 {
		t.Fatalf("paced output emitted only %d packets", len(arrivals))
	}
	// Every 60 ms packet must arrive roughly every 60 ms of wall time; the
	// pre-fix flush-on-frame-count logic produced alternating 40/80 ms gaps.
	for i := 1; i < len(arrivals); i++ {
		ms := arrivals[i].Sub(arrivals[i-1]).Milliseconds()
		if ms < 45 || ms > 80 {
			t.Fatalf("paced audio interval = %d ms, want ~60 ms", ms)
		}
	}
}

func TestRepackToHAPBuildsValidSixtyMillisecondVBRPacket(t *testing.T) {
	var output *rtp.Packet
	handler := RepackToHAP(16000, 60, func(packet *rtp.Packet) {
		output = packet
	})
	for i, size := range []int{10, 300, 20} {
		payload := make([]byte, size+1)
		payload[0] = 0x78
		handler(&rtp.Packet{
			Header:  rtp.Header{SequenceNumber: uint16(i + 10)},
			Payload: payload,
		})
	}

	// The multi-frame packer paces output on a real-time schedule (one 60 ms
	// bundle per 60 ms of wall time) instead of flushing on frame count, so
	// the first bundle arrives after the timer fires.
	deadline := time.Now().Add(500 * time.Millisecond)
	for output == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if output == nil {
		t.Fatal("60 ms Opus packet was not emitted")
	}
	if output.Timestamp != 960 || output.Payload[1] != 0x83 {
		t.Fatalf("60 ms Opus header/timestamp = %x/%d", output.Payload[:2], output.Timestamp)
	}
	// Frame sizes are encoded for M-1 frames: 10 and 300. The latter is
	// represented as 252,12 according to RFC 6716 section 3.
	wantPrefix := []byte{0x7b, 0x83, 10, 252, 12}
	if len(output.Payload) < len(wantPrefix) {
		t.Fatalf("short Opus packet: %x", output.Payload)
	}
	for i, want := range wantPrefix {
		if output.Payload[i] != want {
			t.Fatalf("Opus prefix = %x, want %x", output.Payload[:len(wantPrefix)], wantPrefix)
		}
	}
}
