package homekit

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/hap/camera"
	"github.com/rs/zerolog"
)

func TestCharacteristicWriteDiagnosticDoesNotLogValue(t *testing.T) {
	var output bytes.Buffer
	previous := log
	log = zerolog.New(&output).Level(zerolog.DebugLevel)
	t.Cleanup(func() { log = previous })

	accessory := camera.NewAccessory("HomeLoom", "Camera Kernel", "Test", "-", "test")
	active := accessory.GetCharacter("B0")
	if active == nil {
		t.Fatal("camera accessory is missing Active")
	}
	server := &server{stream: "camera-test", accessory: accessory}
	canary := "srtp-key-salt-canary"
	server.SetCharacteristic(nil, accessory.AID, active.IID, canary, false)

	diagnostic := output.String()
	if strings.Contains(diagnostic, canary) {
		t.Fatalf("HomeKit diagnostic leaked characteristic value: %s", diagnostic)
	}
	for _, required := range []string{
		`"characteristic":"active"`,
		`"characteristic_type":"B0"`,
		`"value_length":` + strconv.Itoa(len(canary)),
		`"[homekit] characteristic write"`,
	} {
		if !strings.Contains(diagnostic, required) {
			t.Fatalf("HomeKit diagnostic missing %q: %s", required, diagnostic)
		}
	}
}

func TestHomeKitDiagnosticNamesCoverLiveSessionCharacteristics(t *testing.T) {
	for characteristicType, expected := range map[string]string{
		camera.TypeStreamingStatus:                   "streaming-status",
		camera.TypeSupportedVideoStreamConfiguration: "supported-video-stream-configuration",
		camera.TypeSupportedAudioStreamConfiguration: "supported-audio-stream-configuration",
		camera.TypeSupportedRTPConfiguration:         "supported-rtp-configuration",
		camera.TypeSelectedStreamConfiguration:       "selected-stream-configuration",
		camera.TypeSetupEndpoints:                    "setup-endpoints",
		"B0":                                         "active",
		"11A":                                        "microphone-mute",
	} {
		if actual := diagnosticCharacteristicName(characteristicType); actual != expected {
			t.Fatalf("diagnosticCharacteristicName(%q) = %q, want %q", characteristicType, actual, expected)
		}
	}
	for command, expected := range map[byte]string{
		camera.SessionCommandEnd:         "end",
		camera.SessionCommandStart:       "start",
		camera.SessionCommandSuspend:     "suspend",
		camera.SessionCommandResume:      "resume",
		camera.SessionCommandReconfigure: "reconfigure",
	} {
		if actual := diagnosticSessionCommandName(command); actual != expected {
			t.Fatalf("diagnosticSessionCommandName(%d) = %q, want %q", command, actual, expected)
		}
	}
}
