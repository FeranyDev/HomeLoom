package go2rtc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPublisherConfigUsesProtectedPlaceholdersAndRetainsPairings(t *testing.T) {
	runtimeDir := t.TempDir()
	config := publisherTestConfig(runtimeDir)
	path := filepath.Join(runtimeDir, config.StreamID, "go2rtc.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writePublisherConfig(path, config); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{config.Secrets.HomeKitPIN, config.Secrets.HomeKitDevicePrivate} {
		if strings.Contains(text, secret) {
			t.Fatalf("publisher config contains a secret: %q", secret)
		}
	}
	for _, required := range []string{"modules: [api, rtsp, srtp, homekit, xiaomi, streams, mp4, exec, ffmpeg]", "homekit: warn", "allow_paths: [/pair-setup, /pair-verify, /api/stream.mp4, /api/frame.mp4, /api/frame.jpeg, /api/homekit/session, /api/matter/webrtc]", "exec:\n  allow_paths: [\"/opt/homeloom/ffmpeg\"]", "ffmpeg:\n  bin: \"/opt/homeloom/ffmpeg\"", "  global: \"-hide_banner -nostats\"", "ffmpeg:camera-main#video=h264", "${HOMELOOM_CAMERA_SOURCE_CAMERA_MAIN}"} {
		if !strings.Contains(text, required) {
			t.Fatalf("publisher config missing %q:\n%s", required, text)
		}
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime dir permissions = %v, %v", info.Mode(), err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %v, %v", info.Mode(), err)
	}
	if err := os.WriteFile(path, append(data, []byte("# retained pairing\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePublisherConfig(path, config); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(restored), "# retained pairing") {
		t.Fatal("existing pairing config was overwritten")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("repaired config permissions = %v, %v", info.Mode(), err)
	}
	for _, env := range publisherEnvironment(config) {
		if !strings.Contains(env, "=") {
			t.Fatalf("invalid child environment entry %q", env)
		}
	}
}

func TestPreviewOnlyConfigDoesNotExposeHomeKitAccessory(t *testing.T) {
	config := publisherTestConfig(t.TempDir())
	config.PublishHomeKit = false
	text := publisherYAML(config)
	if strings.Contains(text, "\nhomekit:\n") || strings.Contains(text, "/pair-setup") || strings.Contains(text, "homekit,") {
		t.Fatalf("preview-only config advertises HomeKit:\n%s", text)
	}
	if !strings.Contains(text, "/api/stream.mp4") || !strings.Contains(text, "streams:") {
		t.Fatalf("preview-only config is missing media endpoints:\n%s", text)
	}
	for _, value := range publisherEnvironment(config) {
		if strings.HasPrefix(value, "HOMELOOM_HOMEKIT_") {
			t.Fatalf("preview-only environment contains HomeKit secret: %q", value)
		}
	}
}

func TestPublisherConfigImplementsConnectionModes(t *testing.T) {
	tests := map[string]string{
		"on_demand": "",
		"preload":   "preload:\n  \"camera-main\": \"video\"",
		"always_on": "preload:\n  \"camera-main\": \"video=h264&audio=opus\"",
	}
	for mode, expected := range tests {
		config := publisherTestConfig(t.TempDir())
		config.ConnectionMode = mode
		config.Audio = true
		text := publisherYAML(config)
		if expected == "" && strings.Contains(text, "\npreload:\n") {
			t.Fatalf("%s config unexpectedly preloads:\n%s", mode, text)
		}
		if expected != "" && !strings.Contains(text, expected) {
			t.Fatalf("%s config missing %q:\n%s", mode, expected, text)
		}
	}
}

func TestHomeKitConfigModeUpgradePreservesPairings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go2rtc.yaml")
	config := publisherTestConfig(filepath.Dir(path))
	config.ConnectionMode = "preload"
	if err := os.WriteFile(path, []byte(publisherYAML(config)), 0o600); err != nil {
		t.Fatal(err)
	}
	config.ConnectionMode = "always_on"
	config.Audio = true
	if err := upgradePublisherConfig(path, config); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "video=h264&audio=opus") || !strings.Contains(text, "client_id=controller") ||
		strings.Contains(text, "\"video\"\n") {
		t.Fatalf("mode upgrade did not preserve pairing or replace preload:\n%s", text)
	}
}

func TestScopedNetworkSourcesStayOutOfPublisherConfig(t *testing.T) {
	for protocol, uri := range map[string]string{
		"rtsp":  "rtsp://viewer:rtsp-secret@192.0.2.10:554/live/main",
		"onvif": "onvif://viewer:onvif-secret@192.0.2.11:80?subtype=profile-main",
	} {
		config := publisherTestConfig(t.TempDir())
		config.SourceProtocol = protocol
		config.SourceURI = uri
		config.Xiaomi = XiaomiSource{}
		text := publisherYAML(config)
		if strings.Contains(text, "viewer") || strings.Contains(text, "secret") || strings.Contains(text, "192.0.2.") {
			t.Fatalf("%s source escaped into publisher config:\n%s", protocol, text)
		}
		if !strings.Contains(text, "${HOMELOOM_CAMERA_SOURCE_CAMERA_MAIN}") {
			t.Fatalf("%s publisher source placeholder missing:\n%s", protocol, text)
		}
		environment := strings.Join(publisherEnvironment(config), "\n")
		if !strings.Contains(environment, uri) {
			t.Fatalf("%s source was not passed through child environment", protocol)
		}
		if err := config.validate(); err != nil {
			t.Fatalf("%s publisher config rejected: %v", protocol, err)
		}
	}
}

func TestPreviewOnlyConfigIsPromotedToCompleteHomeKitPublisher(t *testing.T) {
	runtimeDir := t.TempDir()
	config := publisherTestConfig(runtimeDir)
	config.PublishHomeKit = false
	path := filepath.Join(runtimeDir, config.StreamID, "go2rtc.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writePublisherConfig(path, config); err != nil {
		t.Fatal(err)
	}

	config.PublishHomeKit = true
	if err := writePublisherConfig(path, config); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, required := range []string{
		"modules: [api, rtsp, srtp, homekit, xiaomi, streams, mp4, exec, ffmpeg]",
		"allow_paths: [/pair-setup, /pair-verify, /api/stream.mp4, /api/frame.mp4, /api/frame.jpeg, /api/homekit/session, /api/matter/webrtc]",
		"\nhomekit:\n",
		"    pin: \"${HOMELOOM_HAP_PIN_CAMERA_MAIN}\"",
		"    device_id:",
		"      - \"client_id=controller\"",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("promoted config missing %q:\n%s", required, text)
		}
	}
}

func TestXiaomiPrimaryChannelIsOmittedFromPublisherURI(t *testing.T) {
	source := XiaomiSource{UserID: "account", Region: "cn", LocalIP: "192.0.2.20", DID: "did", Model: "camera", Channel: 1}
	if uri := source.URI(); strings.Contains(uri, "channel=") {
		t.Fatalf("primary camera URI contains a secondary channel selector: %s", uri)
	}
	source.Channel = 2
	if uri := source.URI(); !strings.Contains(uri, "channel=2") {
		t.Fatalf("secondary camera URI missing channel selector: %s", uri)
	}
}

func TestXiaomiPreauthorizedSourceBypassesCloudAndStaysOutOfConfig(t *testing.T) {
	config := publisherTestConfig(t.TempDir())
	config.Xiaomi.ClientPublic = strings.Repeat("a", 64)
	config.Xiaomi.ClientPrivate = strings.Repeat("b", 64)
	config.Xiaomi.DevicePublic = strings.Repeat("c", 64)
	config.Xiaomi.Sign = "signature-canary"
	config.Xiaomi.Vendor = "cs2"
	text := publisherYAML(config)
	for _, secret := range []string{config.Xiaomi.ClientPublic, config.Xiaomi.ClientPrivate, config.Xiaomi.DevicePublic, config.Xiaomi.Sign} {
		if strings.Contains(text, secret) {
			t.Fatalf("in-memory Xiaomi handshake material was written to config: %q", secret)
		}
	}
	if !strings.Contains(text, "${HOMELOOM_CAMERA_SOURCE_CAMERA_MAIN}") {
		t.Fatalf("publisher config is missing the in-memory source placeholder:\n%s", text)
	}
	uri := config.Xiaomi.URI()
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.User != nil || parsed.Query().Get("client_public") != config.Xiaomi.ClientPublic ||
		parsed.Query().Get("client_private") != config.Xiaomi.ClientPrivate ||
		parsed.Query().Get("device_public") != config.Xiaomi.DevicePublic ||
		parsed.Query().Get("sign") != config.Xiaomi.Sign {
		t.Fatalf("preauthorized Xiaomi URI = %s", uri)
	}
	found := false
	for _, value := range publisherEnvironment(config) {
		if strings.HasPrefix(value, "HOMELOOM_CAMERA_SOURCE_CAMERA_MAIN=") {
			found = strings.Contains(value, "signature-canary")
		}
	}
	if !found {
		t.Fatal("preauthorized source was not passed through the child environment")
	}
}

func TestExistingPublisherConfigGainsPreviewWithoutLosingPairing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go2rtc.yaml")
	legacy := "app:\n  modules: [api, rtsp, srtp, homekit, xiaomi, streams]\napi:\n  allow_paths: [/pair-setup, /pair-verify]\nstreams:\n  \"camera\": \"xiaomi://account:cn@192.0.2.20?channel=1&did=old\"\nhomekit:\n  camera:\n    pairings:\n      - client_id=controller\n"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	config := publisherTestConfig(filepath.Dir(path))
	config.StreamID = "camera"
	config.Xiaomi.Channel = 1
	if err := upgradePublisherConfig(path, config); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, required := range []string{"streams, mp4, exec, ffmpeg]", "/api/stream.mp4", "client_id=controller", "ffmpeg:camera#video=h264#audio=opus/16000#width=1280#height=720"} {
		if !strings.Contains(text, required) {
			t.Fatalf("upgraded config missing %q:\n%s", required, text)
		}
	}
	if !strings.Contains(text, "${HOMELOOM_CAMERA_SOURCE_CAMERA}") {
		t.Fatalf("upgraded config missing native source:\n%s", text)
	}
	if strings.Contains(text, "channel=1") {
		t.Fatalf("upgraded primary camera retained a secondary channel selector:\n%s", text)
	}
	if runtime.GOOS == "darwin" && !strings.Contains(text, "-c:v libx264") {
		t.Fatalf("upgraded macOS publisher missing stable software encoder:\n%s", text)
	}
	if err := upgradePublisherConfig(path, config); err != nil {
		t.Fatal(err)
	}
	repeated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(repeated), "unix_listen:") != 1 || strings.Count(string(repeated), "hap_listen:") != 1 {
		t.Fatalf("repeated upgrade duplicated API listeners:\n%s", repeated)
	}
}

func TestPublisherConfigSeparatesPublicHAPFromPrivatePreview(t *testing.T) {
	config := publisherTestConfig(t.TempDir())
	text := publisherYAML(config)
	for _, required := range []string{
		"listen: \"\"", "unix_listen:", "media.sock", "hap_listen: \"0.0.0.0:51826\"",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("publisher config missing %q:\n%s", required, text)
		}
	}
}

func TestLowLatencyDarwinTemplateBoundsEncoderAndKeyframeDelay(t *testing.T) {
	template := ffmpegH264TemplateFor("darwin")
	for _, required := range []string{
		"-c:v libx264",
		"-preset:v superfast",
		"-tune:v zerolatency",
		"-r:v 20",
		"-g 20",
		"-keyint_min 20",
		"-sc_threshold 0",
		"-bf 0",
		"-profile:v main",
		"-level:v 4.0",
		"-crf:v 23",
		"-x264-params repeat-headers=1",
		"-flush_packets 1",
		"-max_delay 0",
	} {
		if !strings.Contains(template, required) {
			t.Fatalf("low-latency template missing %q: %s", required, template)
		}
	}
	if strings.Contains(template, "-g 50") {
		t.Fatalf("low-latency template retained two-second GOP: %s", template)
	}
	if strings.Contains(template, "videotoolbox") {
		t.Fatalf("unstable hardware-only encoder retained: %s", template)
	}
	for _, forbidden := range []string{"-b:v 299k", "-maxrate:v 299k", "-bufsize:v 598k"} {
		if strings.Contains(template, forbidden) {
			t.Fatalf("low-quality global bitrate clamp %q retained: %s", forbidden, template)
		}
	}
}

func TestTranscodeSourceMatchesAdvertisedHomeKitMedia(t *testing.T) {
	uri := transcodeURI("camera-main")
	for _, required := range []string{
		"#video=h264",
		"#audio=opus/16000",
		"#width=1280",
		"#height=720",
	} {
		if !strings.Contains(uri, required) {
			t.Fatalf("HomeKit transcode URI missing %q: %s", required, uri)
		}
	}
	if strings.Contains(uri, "hardware") || strings.Contains(uri, "video=h264/") {
		t.Fatalf("software fallback URI must not request hardware: %s", uri)
	}
}

func TestTranscodeFallbackChainEndsWithSoftware(t *testing.T) {
	uris := sharedTranscodeURIs("camera-main")
	if len(uris) < 1 {
		t.Fatalf("expected software transcode URI, got %#v", uris)
	}
	last := uris[len(uris)-1]
	if last != "ffmpeg:camera-main#video=h264#audio=opus/16000#width=1280#height=720" {
		t.Fatalf("software fallback = %q", last)
	}
	if runtime.GOOS == "darwin" {
		if len(uris) != 1 || strings.Contains(uris[0], "hardware") || strings.Contains(uris[0], "videotoolbox") {
			t.Fatalf("darwin must use software-only shared transcode, got %#v", uris)
		}
	}
}

func TestPublisherPrefersNativeH264AndKeepsTranscodeFallback(t *testing.T) {
	text := publisherYAML(publisherTestConfig(t.TempDir()))
	transcode := strings.Index(text, `ffmpeg:camera-main#video=h264#audio=opus/16000#width=1280#height=720`)
	native := strings.Index(text, `"${HOMELOOM_CAMERA_SOURCE_CAMERA_MAIN}"`)
	if transcode < 0 || native < 0 || native > transcode {
		t.Fatalf("native producer must precede the transcode fallback:\n%s", text)
	}
}

func TestExistingPublisherConfigReplacesOldVideoToolboxTemplate(t *testing.T) {
	old := "ffmpeg:\n  bin: \"/opt/homeloom/ffmpeg\"\n  h264: \"-c:v h264_videotoolbox -g 50 -bf 0\"\nstreams:\n  camera: source\n"
	updated := applyFFmpegH264Template(old, ffmpegH264TemplateFor("darwin"))
	for _, required := range []string{"-c:v libx264", "-preset:v superfast", "-g 20", "-flush_packets 1"} {
		if !strings.Contains(updated, required) {
			t.Fatalf("upgraded template missing %q:\n%s", required, updated)
		}
	}
	if strings.Contains(updated, "-g 50") || strings.Contains(updated, "videotoolbox") {
		t.Fatalf("old encoder template was not replaced:\n%s", updated)
	}
}

func TestPublisherMediaWarmupFailureDoesNotBlockLifecycle(t *testing.T) {
	finished := make(chan struct{})
	go func() {
		warmPublisherMedia(20*time.Millisecond, mediaHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}), "127.0.0.1:51826", "camera-1")
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("non-fatal publisher warmup did not respect its deadline")
	}
}

func TestPinnedFakeBinaryVerification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homeloom-camera-kernel")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'go2rtc version 1.9.14-homeloom.1'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyBinary(context.Background(), path); err != nil {
		t.Fatalf("verify fake binary: %v", err)
	}
}

func TestVerifyExecutableRejectsMissingAndNonExecutableFFmpeg(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "ffmpeg")
	if err := verifyExecutable(missing, "FFmpeg"); err == nil || !strings.Contains(err.Error(), "is unavailable") {
		t.Fatalf("missing executable error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyExecutable(path, "FFmpeg"); err == nil || !strings.Contains(err.Error(), "is not an executable file") {
		t.Fatalf("non-executable error = %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyExecutable(path, "FFmpeg"); err != nil {
		t.Fatalf("executable rejected: %v", err)
	}
}

func TestPublisherReadinessRequiresAllTCPListeners(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	probes := 0
	probe := func(address string) bool {
		probes++
		return address == "127.0.0.1:51826"
	}
	if err := waitForPublisherReady(ctx, make(chan error), probe, "127.0.0.1:51826", "127.0.0.1:18554"); err == nil {
		t.Fatal("publisher reported ready with one listener missing")
	}
	if probes < 2 {
		t.Fatalf("not all listeners were probed: %d", probes)
	}
}

func TestPublisherReadinessAcceptsWildcardListenerAddresses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var probed []string
	probe := func(address string) bool {
		probed = append(probed, address)
		return true
	}
	if err := waitForPublisherReady(ctx, make(chan error), probe, "0.0.0.0:51826", "127.0.0.1:18554"); err != nil {
		t.Fatalf("ready listeners rejected: %v", err)
	}
	if strings.Join(probed, ",") != "127.0.0.1:51826,127.0.0.1:18554" {
		t.Fatalf("probe addresses = %v", probed)
	}
}

func TestPublisherMediaReadinessRequiresH264KeyframeFragment(t *testing.T) {
	var requested *http.Request
	client := mediaHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
		requested = request
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"video/mp4; codecs=\"avc1.640029\""}},
			Body:       io.NopCloser(strings.NewReader("ftyp-moov-moof-mdat")),
		}, nil
	})
	if err := waitForPublisherMediaReady(context.Background(), client, "0.0.0.0:51826", "camera-1"); err != nil {
		t.Fatalf("valid keyframe response rejected: %v", err)
	}
	if requested.URL.Host != "127.0.0.1:51826" || requested.URL.Path != "/api/frame.mp4" ||
		requested.URL.Query().Get("src") != "camera-1" || requested.URL.Query().Get("video") != "h264" {
		t.Fatalf("media readiness request = %s", requested.URL)
	}
}

func TestPublisherMediaReadinessRejectsHeaderOnlyMP4(t *testing.T) {
	client := mediaHTTPClientFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"video/mp4"}},
			Body:       io.NopCloser(strings.NewReader("ftyp-moov")),
		}, nil
	})
	if err := waitForPublisherMediaReady(context.Background(), client, "127.0.0.1:51826", "camera-1"); err == nil {
		t.Fatal("publisher reported media ready without a keyframe fragment")
	}
}

func TestPublisherMediaReadinessRejectsHEVCKeyframe(t *testing.T) {
	client := mediaHTTPClientFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"video/mp4; codecs=\"hvc1.1.6.L93.B0\""}},
			Body:       io.NopCloser(strings.NewReader("ftyp-moov-moof-mdat")),
		}, nil
	})
	if err := waitForPublisherMediaReady(context.Background(), client, "127.0.0.1:51826", "camera-1"); err == nil {
		t.Fatal("publisher reported H.264 media ready after receiving only HEVC")
	}
}

type mediaHTTPClientFunc func(*http.Request) (*http.Response, error)

func (f mediaHTTPClientFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPublisherProducerAllowsWorkerStartupWithoutPIN(t *testing.T) {
	producer, err := NewPublisherProducer(PublisherProducerConfig{RuntimeDir: t.TempDir(), HAPPortBase: 51826, RTSPPortBase: 18554, SRTPPortBase: 18443})
	if err != nil || producer == nil {
		t.Fatalf("producer startup config = %v, %v", producer, err)
	}
	config := publisherTestConfig(t.TempDir())
	config.Secrets.HomeKitPIN = ""
	if err := config.validate(); err != nil {
		t.Fatalf("missing PIN is generated per stream, got %v", err)
	}
}

func TestPublisherProducerAcceptsOnlyScopedSourceSchemes(t *testing.T) {
	for protocol, scheme := range map[string]string{
		"rtsp": "rtsp", "onvif": "onvif", "xiaomi-miss": "xiaomi",
	} {
		if !sourceSchemeMatches(protocol, scheme) {
			t.Fatalf("scoped source rejected: protocol=%s scheme=%s", protocol, scheme)
		}
	}
	for _, pair := range [][2]string{{"tapo", "tapo"}, {"homekit-camera", "homekit"}, {"rtsp", "http"}} {
		if sourceSchemeMatches(pair[0], pair[1]) {
			t.Fatalf("out-of-scope source accepted: protocol=%s scheme=%s", pair[0], pair[1])
		}
	}
}

func TestXiaomiAuthorizationPublicAcceptsCoreNativeExtensionFields(t *testing.T) {
	raw := json.RawMessage(`{"userId":"42","region":"cn","did":"1178028045","model":"chuangmi.camera.079ac1","localIP":"192.168.101.179","subtype":"hd","channel":1,"devicePublic":"","vendor":"","uid":""}`)
	var material xiaomiAuthorizationPublic
	if err := decodeExact(raw, &material); err != nil {
		t.Fatalf("Core Xiaomi authorization material rejected: %v", err)
	}
	if material.UserID != "42" || material.LocalIP != "192.168.101.179" {
		t.Fatalf("decoded material = %#v", material)
	}
}

func TestProtectedIdentityIsStableAndDoesNotLeakInPairingOutput(t *testing.T) {
	directory := t.TempDir()
	first, err := ensureIdentity(directory, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !validIdentity(first) {
		t.Fatalf("generated invalid identity: %#v", first)
	}
	path := filepath.Join(directory, identityFilename)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := ensureIdentity(directory, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("identity changed after restart: %#v != %#v", first, second)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("identity permissions = %v, %v", info.Mode(), err)
	}
	output := PublisherOutput{Name: "Living Camera", StreamID: "stream-1", PIN: first.PIN, HAPHost: "0.0.0.0:51826"}
	visible := "name=" + output.Name + " stream=" + output.StreamID + " pin=" + output.PIN + " hap_port=51826"
	if strings.Contains(visible, first.DevicePrivate) || strings.Contains(visible, "token") {
		t.Fatalf("pairing output leaks secret: %s", visible)
	}
}

func TestHAPPINValidationMatchesUpstreamRejectedPatterns(t *testing.T) {
	for _, pin := range []string{"000-00-000", "111-11-111", "222-22-222", "999-99-999", "123-45-678", "876-54-321", "12345678", "123-4-5678", "123-45-67A"} {
		if validHAPPIN(pin) {
			t.Fatalf("accepted invalid HAP PIN %q", pin)
		}
	}
	for _, pin := range []string{"123-45-679", "864-20-135"} {
		if !validHAPPIN(pin) {
			t.Fatalf("rejected valid HAP PIN %q", pin)
		}
	}
	for range 32 {
		pin, err := randomPIN()
		if err != nil || !validHAPPIN(pin) {
			t.Fatalf("random PIN = %q, %v", pin, err)
		}
	}
}

func TestPreviewOnlyRestartPreservesHomeKitPairings(t *testing.T) {
	runtimeDir := t.TempDir()
	config := publisherTestConfig(runtimeDir)
	path := filepath.Join(runtimeDir, config.StreamID, "go2rtc.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writePublisherConfig(path, config); err != nil {
		t.Fatal(err)
	}
	// Simulate go2rtc persisting a controller pairing after setup.
	paired := strings.Replace(
		publisherYAML(config),
		"pairings:\n      - \"client_id=controller\"\n",
		"pairings:\n      - \"client_id=controller&client_public=abcd&permissions=1\"\n",
		1,
	)
	if err := os.WriteFile(path, []byte(paired), 0o600); err != nil {
		t.Fatal(err)
	}

	// Core may restart the publisher as preview-only before Target manager
	// re-enables apple-home. The runtime YAML must stop exposing HomeKit while
	// the durable sidecar preserves the controller pairing.
	config.PublishHomeKit = false
	config.HomeKit.Pairings = nil
	if err := writePublisherConfig(path, config); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Contains(text, "\nhomekit:\n") ||
		strings.Contains(text, "client_id=controller&client_public=abcd&permissions=1") ||
		strings.Contains(text, "modules: [api, rtsp, srtp, homekit,") ||
		strings.Contains(text, "hap_listen:") ||
		strings.Contains(text, "/pair-setup") ||
		strings.Contains(text, "/pair-verify") {
		t.Fatalf("preview-only restart retained an active HomeKit configuration:\n%s", text)
	}
	durablePath := durableHomeKitPairingsPath(runtimeDir, config.StreamID)
	pairings, ok := readDurableHomeKitPairings(durablePath)
	if !ok || len(pairings) != 1 || pairings[0] != "client_id=controller&client_public=abcd&permissions=1" {
		t.Fatalf("preview-only restart durable pairings = %#v ok=%v", pairings, ok)
	}

	// Re-enabling the Target must restore the HomeKit runtime routes without
	// replacing the durable controller pairing.
	config.PublishHomeKit = true
	config.HomeKit.Pairings = pairings
	if err := writePublisherConfig(path, config); err != nil {
		t.Fatal(err)
	}
	reenabled, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reenabledText := string(reenabled)
	for _, required := range []string{
		"modules: [api, rtsp, srtp, homekit,",
		"hap_listen:",
		"/pair-setup",
		"client_id=controller&client_public=abcd&permissions=1",
	} {
		if !strings.Contains(reenabledText, required) {
			t.Fatalf("re-enabled HomeKit publisher missing %q:\n%s", required, reenabledText)
		}
	}
}

func TestStartPublisherReseedsPairingsFromDisk(t *testing.T) {
	runtimeDir := t.TempDir()
	config := publisherTestConfig(runtimeDir)
	path := filepath.Join(runtimeDir, config.StreamID, "go2rtc.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	persisted := publisherYAML(config)
	persisted = strings.Replace(persisted, "pairings:\n      - \"client_id=controller\"\n", "pairings:\n      - \"client_id=alive&client_public=ef01&permissions=1\"\n", 1)
	if err := os.WriteFile(path, []byte(persisted), 0o600); err != nil {
		t.Fatal(err)
	}
	pairings, ok := readPersistedHomeKitPairings(path, config.StreamID)
	if !ok || len(pairings) != 1 || pairings[0] != "client_id=alive&client_public=ef01&permissions=1" {
		t.Fatalf("readPersistedHomeKitPairings = %#v, %v", pairings, ok)
	}
}

func publisherTestConfig(runtimeDir string) PublisherConfig {
	return PublisherConfig{
		RuntimeDir: runtimeDir, StreamID: "camera-main", APIListen: "0.0.0.0:51826", RTSPListen: "127.0.0.1:18554", SRTPListen: "0.0.0.0:18443", FFmpegBinary: "/opt/homeloom/ffmpeg",
		ConnectionMode: "on_demand",
		Xiaomi:         XiaomiSource{UserID: "account", Region: "cn", LocalIP: "192.0.2.20", DID: "did-123", Model: "isa.camera.demo", Subtype: "hd", Channel: 2},
		Secrets:        PublisherSecrets{HomeKitPIN: "111-22-333", HomeKitDevicePrivate: "private-canary"},
		HomeKit:        HomeKitPublisher{Name: "Living Camera", DeviceID: "AA:BB:CC:DD:EE:FF", Pairings: []string{"client_id=controller"}},
		PublishHomeKit: true,
	}
}

var _ = url.Values{}

func TestDurableHomeKitPairingsSurviveYAMLRewrite(t *testing.T) {
	runtimeDir := t.TempDir()
	config := publisherTestConfig(runtimeDir)
	config.HomeKit.Pairings = []string{"client_id=controller&client_public=abcd&permissions=1"}
	path := durableHomeKitPairingsPath(runtimeDir, config.StreamID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeDurableHomeKitPairings(path, config.StreamID, config.HomeKit.Pairings); err != nil {
		t.Fatal(err)
	}
	// Simulate a wipe of go2rtc.yaml pairings while durable store remains.
	yamlPath := filepath.Join(runtimeDir, config.StreamID, "go2rtc.yaml")
	wiped := publisherYAML(config)
	wiped = strings.Replace(wiped, "pairings:\n      - \"client_id=controller&client_public=abcd&permissions=1\"\n", "pairings: []\n", 1)
	if err := os.WriteFile(yamlPath, []byte(wiped), 0o600); err != nil {
		t.Fatal(err)
	}
	config.HomeKit.Pairings = nil
	if pairings, ok := readDurableHomeKitPairings(path); !ok || len(pairings) != 1 {
		t.Fatalf("durable pairings = %#v ok=%v", pairings, ok)
	}
	if pairings, ok := readPersistedHomeKitPairings(yamlPath, config.StreamID); !ok || len(pairings) != 0 {
		t.Fatalf("yaml pairings = %#v ok=%v", pairings, ok)
	}
	// StartPublisher reseeds from durable store before rewrite.
	config.HomeKit.Pairings = nil
	if pairings, ok := readDurableHomeKitPairings(durableHomeKitPairingsPath(runtimeDir, config.StreamID)); ok {
		config.HomeKit.Pairings = pairings
	}
	if err := writePublisherConfig(yamlPath, config); err != nil {
		t.Fatal(err)
	}
	if err := ensureHomeKitPairingsInConfig(yamlPath, config); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "client_id=controller&client_public=abcd&permissions=1") {
		t.Fatalf("reseeded yaml missing durable pairings:\n%s", raw)
	}
}
