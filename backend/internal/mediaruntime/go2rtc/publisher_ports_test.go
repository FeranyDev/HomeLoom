package go2rtc

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPublisherProducerProbesHashCollisionsAndReleasesPorts(t *testing.T) {
	producer, err := NewPublisherProducer(PublisherProducerConfig{
		RuntimeDir: t.TempDir(), HAPPortBase: 51000, RTSPPortBase: 18000, SRTPPortBase: 20000,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Find two stable IDs with the same preferred offset.
	seen := map[int]string{}
	var first, second string
	for index := 0; index < 10000; index++ {
		id := "camera-" + strconv.Itoa(index)
		offset := publisherPortOffset(id)
		if previous := seen[offset]; previous != "" {
			first, second = previous, id
			break
		}
		seen[offset] = id
	}
	if first == "" {
		t.Fatal("failed to find collision")
	}
	left, releaseLeft, err := producer.reservePorts(first)
	if err != nil {
		t.Fatal(err)
	}
	right, releaseRight, err := producer.reservePorts(second)
	if err != nil {
		t.Fatal(err)
	}
	if left.hap == right.hap || left.rtsp == right.rtsp || left.srtp == right.srtp {
		t.Fatalf("colliding allocations: left=%+v right=%+v", left, right)
	}
	releaseLeft()
	releaseRight()
	again, releaseAgain, err := producer.reservePorts(first)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseAgain()
	if again != left {
		t.Fatalf("released preferred ports not reused: got=%+v want=%+v", again, left)
	}
}

func TestPublisherProducerReusesPersistedCollisionAllocation(t *testing.T) {
	runtimeDir := t.TempDir()
	producer, err := NewPublisherProducer(PublisherProducerConfig{
		RuntimeDir: runtimeDir, HAPPortBase: 51000, RTSPPortBase: 18000, SRTPPortBase: 20000,
	})
	if err != nil {
		t.Fatal(err)
	}
	streamID := "camera-main"
	streamDir := filepath.Join(runtimeDir, streamID)
	if err := os.MkdirAll(streamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writePublisherEndpoint(runtimeDir, streamID, 51999); err != nil {
		t.Fatal(err)
	}
	allocated, release, err := producer.reservePorts(streamID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if allocated.hap != 51999 || allocated.rtsp != 18999 || allocated.srtp != 20999 {
		t.Fatalf("persisted allocation = %+v", allocated)
	}
}
