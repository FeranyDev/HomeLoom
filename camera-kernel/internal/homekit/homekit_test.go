package homekit

import (
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	srtp2 "github.com/AlexxIT/go2rtc/internal/srtp"
	"github.com/AlexxIT/go2rtc/pkg/hap/camera"
	"github.com/AlexxIT/go2rtc/pkg/hap/tlv8"
	pkghomekit "github.com/AlexxIT/go2rtc/pkg/homekit"
	"github.com/AlexxIT/go2rtc/pkg/srtp"
)

func TestAccessoryConfigNumberTracksCurrentCameraSchema(t *testing.T) {
	if accessoryConfigNumber != "10" {
		t.Fatalf("accessory config number = %q", accessoryConfigNumber)
	}
}

func TestFindHomeKitTranscodeSourceSkipsNativeAndUsesLatestH264Fallback(t *testing.T) {
	sources := []string{
		"${HOMELOOM_CAMERA_SOURCE_CAMERA_MAIN}",
		"ffmpeg:camera-main#video=h264#audio=opus/16000#width=1280#height=720",
		"ffmpeg:camera-main#video=h264#audio=opus/16000#width=640#height=360",
	}
	want := sources[2]
	if got := findHomeKitTranscodeSource(sources); got != want {
		t.Fatalf("H264 fallback = %q, want %q", got, want)
	}
	if got := findHomeKitTranscodeSource([]string{"${HOMELOOM_CAMERA_SOURCE_CAMERA_MAIN}"}); got != "" {
		t.Fatalf("native-only stream unexpectedly produced fallback %q", got)
	}
}

func TestNormalizeConnectionModeDefaultsToOnDemand(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "", want: "on_demand"},
		{input: "invalid", want: "on_demand"},
		{input: "preload", want: "preload"},
		{input: "always_on", want: "always_on"},
	} {
		if got := normalizeConnectionMode(test.input); got != test.want {
			t.Fatalf("normalizeConnectionMode(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestExpiredPreloadSessionReleasesTemporaryInput(t *testing.T) {
	srv := &server{
		stream:              "camera-main",
		inputStream:         "camera-main__homeloom_h264",
		connectionMode:      "preload",
		previewPreloaded:    true,
		previewExpired:      true,
		activeMediaSessions: 0,
	}

	input, release := srv.acquireHomeKitSessionInput()
	if input != "camera-main__homeloom_h264" {
		t.Fatalf("session input = %q", input)
	}
	release()
	if srv.inputStream != "camera-main" || srv.previewPreloaded {
		t.Fatalf("expired preload input was not released: %#v", srv)
	}
}

func TestAlwaysOnSessionInputDoesNotOwnPreviewLease(t *testing.T) {
	srv := &server{
		stream:         "camera-main",
		inputStream:    "camera-main",
		connectionMode: "always_on",
	}

	input, release := srv.acquireHomeKitSessionInput()
	release()
	if input != "camera-main" || srv.activeMediaSessions != 0 {
		t.Fatalf("always_on session unexpectedly owned temporary input: input=%q sessions=%d", input, srv.activeMediaSessions)
	}
}

func TestHAPConnectionCloseStopsOwnedMediaConsumer(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	consumer := pkghomekit.NewConsumer(local, srtp.NewServer("127.0.0.1:0"))
	accessory := camera.NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	srv := &server{
		accessory: accessory,
	}
	slot, ok := srv.streamSlotForCharacteristic(accessory.Services[1].GetCharacter(camera.TypeSetupEndpoints))
	if !ok {
		t.Fatal("first RTP service has no stream slot")
	}
	srv.setConsumer(slot, consumer)

	srv.stopConsumersForConnection(local)

	if !consumer.Stopped() {
		t.Fatal("HAP connection close left media consumer running")
	}
	if srv.currentConsumer() != nil {
		t.Fatal("HAP connection close left stream status in use")
	}
}

func TestHomeKitSessionSourceUsesControllerVideoSelection(t *testing.T) {
	source := homeKitSessionSource("camera-main", pkghomekit.VideoSelection{
		Width:           1920,
		Height:          1080,
		Framerate:       24,
		MaxBitrate:      512,
		ProfileID:       camera.VideoCodecProfileMain,
		Level:           camera.VideoCodecLevel31,
		AudioSampleRate: 24000,
		AudioPacketTime: 20,
		AudioMaxBitrate: 24,
	})
	want := "ffmpeg:camera-main#video=h264#audio=opus/24000#profile=main#level=3.1#width=1920#height=1080#framerate=20#keyframe_interval=1#bitrate=2000K#audio_bitrate=48K"
	if source != want {
		t.Fatalf("HomeKit session source = %q, want %q", source, want)
	}
	sources := homeKitSessionSources("camera-main", pkghomekit.VideoSelection{
		Width: 1920, Height: 1080, Framerate: 24, MaxBitrate: 512,
		ProfileID: camera.VideoCodecProfileMain, Level: camera.VideoCodecLevel31,
		AudioSampleRate: 24000, AudioPacketTime: 20, AudioMaxBitrate: 24,
	})
	if len(sources) != 1 || sources[0] != want {
		t.Fatalf("session sources = %#v, want software-only [%q]", sources, want)
	}
}

func TestHomeKitSessionSourceRaisesUndersizedBitrates(t *testing.T) {
	cases := []struct {
		name    string
		width   uint16
		height  uint16
		bitrate uint16
		minKbps string
	}{
		{name: "1080p", width: 1920, height: 1080, bitrate: 512, minKbps: "2000"},
		{name: "720p", width: 1280, height: 720, bitrate: 299, minKbps: "1000"},
		{name: "540p", width: 960, height: 540, bitrate: 240, minKbps: "800"},
		{name: "360p", width: 640, height: 360, bitrate: 132, minKbps: "500"},
		{name: "240p", width: 320, height: 240, bitrate: 68, minKbps: "300"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := homeKitSessionSource("camera-main", pkghomekit.VideoSelection{
				Width: tc.width, Height: tc.height, Framerate: 30, MaxBitrate: tc.bitrate,
			})
			want := fmt.Sprintf(
				"ffmpeg:camera-main#video=h264#audio=opus/24000#profile=baseline#level=3.1#width=%d#height=%d#framerate=20#keyframe_interval=1#bitrate=%sK",
				tc.width, tc.height, tc.minKbps,
			)
			if source != want {
				t.Fatalf("HomeKit session source = %q, want %q", source, want)
			}
		})
	}
}

func TestHomeKitSessionSourceOmitsUnspecifiedBitrate(t *testing.T) {
	source := homeKitSessionSource("camera-main", pkghomekit.VideoSelection{
		Width: 640, Height: 360, Framerate: 15,
	})
	want := "ffmpeg:camera-main#video=h264#audio=opus/24000#profile=baseline#level=3.1#width=640#height=360#framerate=15#keyframe_interval=1"
	if source != want {
		t.Fatalf("HomeKit session source = %q, want %q", source, want)
	}
}

func TestHomeKitSessionSourceRaisesAudioQuality(t *testing.T) {
	cases := []struct {
		name     string
		width    uint16
		height   uint16
		sample   int
		audioBr  uint16
		wantOpus string
		wantBr   string
	}{
		{name: "240p_raises_bitrate_and_sample_rate", width: 320, height: 240, sample: 16000, audioBr: 24, wantOpus: "24000", wantBr: "32K"},
		{name: "360p_raises_bitrate", width: 640, height: 360, sample: 24000, audioBr: 24, wantOpus: "24000", wantBr: "40K"},
		{name: "720p_keeps_bitrate_above_floor", width: 1280, height: 720, sample: 24000, audioBr: 64, wantOpus: "24000", wantBr: "64K"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := homeKitSessionSource("camera-main", pkghomekit.VideoSelection{
				Width: tc.width, Height: tc.height, Framerate: 30,
				AudioSampleRate: tc.sample, AudioPacketTime: 60, AudioMaxBitrate: tc.audioBr,
			})
			want := fmt.Sprintf(
				"ffmpeg:camera-main#video=h264#audio=opus/%s#profile=baseline#level=3.1#width=%d#height=%d#framerate=20#keyframe_interval=1#audio_bitrate=%s",
				tc.wantOpus, tc.width, tc.height, tc.wantBr,
			)
			if source != want {
				t.Fatalf("HomeKit session source = %q, want %q", source, want)
			}
		})
	}
}

func TestExpectedHAPConnectionClose(t *testing.T) {
	for _, err := range []error{
		nil,
		io.EOF,
		io.ErrClosedPipe,
		net.ErrClosed,
		syscall.EPIPE,
		syscall.ECONNRESET,
		errors.Join(errors.New("write response"), syscall.EPIPE),
	} {
		if !expectedHAPConnectionClose(err) {
			t.Fatalf("expected peer close was treated as an error: %v", err)
		}
	}
	if expectedHAPConnectionClose(errors.New("decrypt failed")) {
		t.Fatal("unexpected HAP failure was treated as a normal peer close")
	}
}

func TestEndCommandStopsPreparedSessionAndRetainsDiagnostic(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	consumer := pkghomekit.NewConsumer(local, srtp.NewServer("127.0.0.1:0"))
	consumer.SetOffer(&camera.SetupEndpointsRequest{
		SessionID: "session-1",
		Address:   camera.Address{IPVersion: 0, IPAddr: "127.0.0.1", VideoRTPPort: 5000, AudioRTPPort: 5002},
	})
	accessory := camera.NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	srv := &server{accessory: accessory}
	slot, ok := srv.streamSlotForCharacteristic(accessory.Services[1].GetCharacter(camera.TypeSetupEndpoints))
	if !ok {
		t.Fatal("first RTP service has no stream slot")
	}
	srv.setConsumer(slot, consumer)
	selected := accessory.GetCharacter(camera.TypeSelectedStreamConfiguration)
	value, err := tlv8.MarshalBase64(camera.SelectedStreamConfiguration{
		Control: camera.SessionControl{
			SessionID: "session-1",
			Command:   camera.SessionCommandEnd,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	srv.SetCharacteristic(local, accessory.AID, selected.IID, value, false)

	if !consumer.Stopped() || srv.currentConsumer() != nil {
		t.Fatal("END left a prepared HomeKit session active")
	}
	status := srv.sessionStatus()
	if status.Active || status.State != "prepared" {
		t.Fatalf("retained HomeKit session status = %#v", status)
	}
}

func TestSetupEndpointsKeepsRTPServiceSessionsIsolated(t *testing.T) {
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("UDP listen not permitted in this environment")
	}
	_ = probe.Close()

	previousSRTP := srtp2.Server
	srtp2.Server = srtp.NewServer("0.0.0.0:0")
	t.Cleanup(func() { srtp2.Server = previousSRTP })

	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	accessory := camera.NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	srv := &server{accessory: accessory, stream: "camera-main"}
	sessionIDs := []string{"0000000000000001", "0000000000000002"}

	for index, sessionID := range sessionIDs {
		setup := accessory.Services[index+1].GetCharacter(camera.TypeSetupEndpoints)
		offer, err := tlv8.MarshalBase64(camera.SetupEndpointsRequest{
			SessionID: sessionID,
			Address: camera.Address{
				IPVersion: 0, IPAddr: "127.0.0.1", VideoRTPPort: uint16(5000 + index*4), AudioRTPPort: uint16(5002 + index*4),
			},
			VideoCrypto: camera.SRTPCryptoSuite{
				CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80,
				MasterKey:   string(make([]byte, 16)),
				MasterSalt:  string(make([]byte, 14)),
			},
			AudioCrypto: camera.SRTPCryptoSuite{
				CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80,
				MasterKey:   string(make([]byte, 16)),
				MasterSalt:  string(make([]byte, 14)),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		srv.SetCharacteristic(local, accessory.AID, setup.IID, offer, false)

		var answer camera.SetupEndpointsResponse
		if err := tlv8.UnmarshalBase64(srv.GetCharacteristic(local, accessory.AID, setup.IID), &answer); err != nil {
			t.Fatal(err)
		}
		if answer.Status != camera.SetupEndpointsStatusSuccess || answer.SessionID != sessionID {
			t.Fatalf("stream slot %d answer = %#v", index, answer)
		}
	}

	firstSlot := accessory.Services[1].IID
	secondSlot := accessory.Services[2].IID
	first := srv.consumerForSlot(firstSlot)
	second := srv.consumerForSlot(secondSlot)
	if first == nil || second == nil || first == second {
		t.Fatalf("RTP service consumers were not isolated: first=%p second=%p", first, second)
	}
	streamingStatus := func(serviceIndex int) byte {
		t.Helper()
		character := accessory.Services[serviceIndex].GetCharacter(camera.TypeStreamingStatus)
		var status camera.StreamingStatus
		if err := tlv8.UnmarshalBase64(character.Value, &status); err != nil {
			t.Fatal(err)
		}
		return status.Status
	}
	if streamingStatus(1) != camera.StreamingStatusInUse ||
		streamingStatus(2) != camera.StreamingStatusInUse {
		t.Fatal("prepared RTP services were not independently marked in use")
	}

	// A duplicate setup on one occupied slot must leave the other slot intact,
	// and the follow-up GET must return Busy for the new Session ID rather than
	// recomputing the old session's successful answer.
	setup := accessory.Services[1].GetCharacter(camera.TypeSetupEndpoints)
	busySessionID := "0000000000000003"
	offer, err := tlv8.MarshalBase64(camera.SetupEndpointsRequest{
		SessionID: busySessionID,
		Address: camera.Address{
			IPVersion: 0, IPAddr: "127.0.0.1", VideoRTPPort: 6000, AudioRTPPort: 6002,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetCharacteristic(local, accessory.AID, setup.IID, offer, false)
	var busy camera.SetupEndpointsResponse
	if err := tlv8.UnmarshalBase64(srv.GetCharacteristic(local, accessory.AID, setup.IID), &busy); err != nil {
		t.Fatal(err)
	}
	if busy.Status != camera.SetupEndpointsStatusBusy || busy.SessionID != busySessionID {
		t.Fatalf("busy SetupEndpoints GET = %#v", busy)
	}
	if srv.consumerForSlot(firstSlot) != first || srv.consumerForSlot(secondSlot) != second {
		t.Fatal("busy setup replaced an existing RTP service consumer")
	}
	selected := accessory.Services[1].GetCharacter(camera.TypeSelectedStreamConfiguration)
	mismatchedStart, err := tlv8.MarshalBase64(camera.SelectedStreamConfiguration{
		Control: camera.SessionControl{
			SessionID: busySessionID,
			Command:   camera.SessionCommandStart,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetCharacteristic(local, accessory.AID, selected.IID, mismatchedStart, false)
	if first.Stopped() || srv.consumerForSlot(firstSlot) != first {
		t.Fatal("a mismatched START stopped the active RTP service session")
	}

	_ = first.Stop()
	srv.clearConsumer(first)
	if srv.consumerForSlot(firstSlot) != nil || srv.consumerForSlot(secondSlot) != second {
		t.Fatal("clearing one RTP service affected another active slot")
	}
	if streamingStatus(1) != camera.StreamingStatusAvailable ||
		streamingStatus(2) != camera.StreamingStatusInUse {
		t.Fatal("clearing one RTP service changed another slot's streaming status")
	}
	_ = second.Stop()
	srv.clearConsumer(second)
}
