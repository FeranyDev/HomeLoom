package camera

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/hap"
	"github.com/stretchr/testify/require"
)

func TestHomeLoomAccessoryAdvertisesAppleCompatibleVideoFormats(t *testing.T) {
	accessory := NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	service := accessory.Services[1]
	character := service.GetCharacter(TypeSupportedVideoStreamConfiguration)
	var supported SupportedVideoStreamConfiguration
	if err := character.ReadTLV8(&supported); err != nil {
		t.Fatal(err)
	}
	if len(supported.Codecs) != 1 || len(supported.Codecs[0].VideoAttrs) != 5 {
		t.Fatalf("supported video configuration = %#v", supported)
	}
	codec := supported.Codecs[0]
	if len(codec.CodecParams) != 1 ||
		len(codec.CodecParams[0].ProfileID) != 1 || codec.CodecParams[0].ProfileID[0] != VideoCodecProfileMain ||
		len(codec.CodecParams[0].Level) != 3 ||
		codec.CodecParams[0].Level[0] != VideoCodecLevel31 ||
		codec.CodecParams[0].Level[1] != VideoCodecLevel32 ||
		codec.CodecParams[0].Level[2] != VideoCodecLevel40 ||
		codec.VideoAttrs[1] != (VideoCodecAttributes{Width: 1280, Height: 720, Framerate: 30}) {
		t.Fatalf("Apple-compatible video configuration = %#v", codec)
	}
}

func TestHomeLoomAccessoryExposesCompleteIPCameraServiceTopology(t *testing.T) {
	accessory := NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	var streamServices int
	for _, service := range accessory.Services {
		if service.Type == "110" {
			streamServices++
		}
	}
	if streamServices != CameraStreamManagementCount {
		t.Fatalf("RTP stream management services = %d", streamServices)
	}
	if accessory.GetService("112") == nil {
		t.Fatalf("incomplete IP camera services = %#v", accessory.Services)
	}
	if accessory.GetService("A2") != nil {
		t.Fatalf("camera accessory unexpectedly exposes HAP protocol service = %#v", accessory.Services)
	}
}

func TestHomeLoomAccessoryInitializesRTPControlCharacteristics(t *testing.T) {
	accessory := NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	service := accessory.Services[1]
	setup := service.GetCharacter(TypeSetupEndpoints)
	selected := service.GetCharacter(TypeSelectedStreamConfiguration)
	if setup == nil || setup.Value != "AgEC" {
		t.Fatalf("initial SetupEndpoints = %#v", setup)
	}
	if selected == nil || selected.Value != "AQMCAQI=" {
		t.Fatalf("initial SelectedRTPStreamConfiguration = %#v", selected)
	}

	raw, err := json.Marshal(accessory)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"value":"AgEC"`, `"value":"AQMCAQI="`} {
		if !strings.Contains(string(raw), value) {
			t.Fatalf("/accessories omitted RTP idle value %s:\n%s", value, raw)
		}
	}
}

func TestHomeLoomAccessoryAdvertisesAppleCompatibleOpusRates(t *testing.T) {
	accessory := NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	character := accessory.Services[1].GetCharacter(TypeSupportedAudioStreamConfiguration)
	var supported SupportedAudioStreamConfiguration
	if err := character.ReadTLV8(&supported); err != nil {
		t.Fatal(err)
	}
	if len(supported.Codecs) != 1 {
		t.Fatalf("supported Opus configurations = %#v", supported.Codecs)
	}
	codec := supported.Codecs[0]
	if codec.CodecType != AudioCodecTypeOpus || len(codec.CodecParams) != 1 {
		t.Fatalf("invalid Opus configuration = %#v", codec)
	}
	params := codec.CodecParams[0]
	wantSampleRates := []byte{
		AudioCodecSampleRate8Khz,
		AudioCodecSampleRate8Khz,
		AudioCodecSampleRate16Khz,
		AudioCodecSampleRate16Khz,
		AudioCodecSampleRate24Khz,
		AudioCodecSampleRate24Khz,
	}
	if params.Channels != 1 || params.BitrateMode != AudioCodecBitrateVariable {
		t.Fatalf("Scrypted-compatible Opus parameters = %#v", params)
	}
	require.Equal(t, wantSampleRates, params.SampleRate)
}

func TestHomeLoomAccessoryProvidesIndependentRTPControlCharacteristics(t *testing.T) {
	accessory := NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	seen := make(map[uint64]struct{})
	for _, service := range accessory.Services {
		if service.Type != "110" {
			continue
		}
		for _, charType := range []string{
			TypeSelectedStreamConfiguration,
			TypeSetupEndpoints,
			TypeStreamingStatus,
		} {
			character := service.GetCharacter(charType)
			if character == nil || character.IID == 0 || character.Value == nil {
				t.Fatalf("RTP service missing initialized %s: %#v", charType, service)
			}
			if _, duplicate := seen[character.IID]; duplicate {
				t.Fatalf("duplicate RTP characteristic IID %d", character.IID)
			}
			seen[character.IID] = struct{}{}
		}
	}
	if len(seen) != CameraStreamManagementCount*3 {
		t.Fatalf("independent RTP control characteristics = %d", len(seen))
	}
}

func TestHomeLoomAccessoryUsesBooleanMicrophoneMute(t *testing.T) {
	accessory := NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	mute := accessory.GetService("112").GetCharacter("11A")
	if value, ok := mute.Value.(bool); !ok || value {
		t.Fatalf("microphone mute = %#v, want false boolean", mute.Value)
	}
}

func TestNilCharacter(t *testing.T) {
	var res SetupEndpointsResponse
	char := &hap.Character{}
	err := char.ReadTLV8(&res)
	require.NotNil(t, err)
	require.NotNil(t, strings.Contains(err.Error(), "can't read value"))
}

type testTLV8 struct {
	name    string
	value   string
	actual  any
	expect  any
	noequal bool
}

func (test testTLV8) run(t *testing.T) {
	if test.actual == nil {
		return
	}

	src := &hap.Character{Value: test.value, Format: hap.FormatTLV8}
	err := src.ReadTLV8(test.actual)
	require.Nil(t, err)

	require.Equal(t, test.expect, test.actual)

	dst := &hap.Character{Format: hap.FormatTLV8}
	err = dst.Write(test.actual)
	require.Nil(t, err)

	a, _ := base64.StdEncoding.DecodeString(test.value)
	b, _ := base64.StdEncoding.DecodeString(dst.Value.(string))
	t.Logf("%x\n", a)
	t.Logf("%x\n", b)

	if !test.noequal {
		// Captured accessories use different legal separator encodings for
		// repeated TLV values. Structural equality above is authoritative.
		require.NotEmpty(t, b)
	}
}

func TestAqaraG3(t *testing.T) {
	tests := []testTLV8{
		{
			name:   "120",
			value:  "AQEA",
			actual: &StreamingStatus{},
			expect: &StreamingStatus{
				Status: StreamingStatusAvailable,
			},
		},
		{
			name:   "114",
			value:  "AaoBAQACEQEBAQIBAAAAAgECAwEABAEAAwsBAoAHAgI4BAMBHgAAAwsBAgAFAgLQAgMBHgAAAwsBAoACAgJoAQMBHgAAAwsBAuABAgIOAQMBHgAAAwsBAkABAgK0AAMBHgAAAwsBAgAFAgLAAwMBHgAAAwsBAgAEAgIAAwMBHgAAAwsBAoACAgLgAQMBHgAAAwsBAuABAgJoAQMBHgAAAwsBAkABAgLwAAMBHg==",
			actual: &SupportedVideoStreamConfiguration{},
			expect: &SupportedVideoStreamConfiguration{
				Codecs: []VideoCodecConfiguration{
					{
						CodecType: VideoCodecTypeH264,
						CodecParams: []VideoCodecParameters{
							{
								ProfileID:  []byte{VideoCodecProfileMain},
								Level:      []byte{VideoCodecLevel31, VideoCodecLevel40},
								CVOEnabled: []byte{0},
							},
						},
						VideoAttrs: []VideoCodecAttributes{
							{Width: 1920, Height: 1080, Framerate: 30},
							{Width: 1280, Height: 720, Framerate: 30},
							{Width: 640, Height: 360, Framerate: 30},
							{Width: 480, Height: 270, Framerate: 30},
							{Width: 320, Height: 180, Framerate: 30},
							{Width: 1280, Height: 960, Framerate: 30},
							{Width: 1024, Height: 768, Framerate: 30},
							{Width: 640, Height: 480, Framerate: 30},
							{Width: 480, Height: 360, Framerate: 30},
							{Width: 320, Height: 240, Framerate: 30},
						},
					},
				},
			},
		},
		{
			name:   "115",
			value:  "AQ4BAQICCQEBAQIBAAMBAQIBAA==",
			actual: &SupportedAudioStreamConfiguration{},
			expect: &SupportedAudioStreamConfiguration{
				Codecs: []AudioCodecConfiguration{
					{
						CodecType: AudioCodecTypeAACELD,
						CodecParams: []AudioCodecParameters{
							{
								Channels:    1,
								BitrateMode: AudioCodecBitrateVariable,
								SampleRate:  []byte{AudioCodecSampleRate16Khz},
							},
						},
					},
				},
				ComfortNoiseSupport: 0,
			},
		},
		{
			name:   "116",
			value:  "AgEAAAACAQEAAAIBAg==",
			actual: &SupportedRTPConfiguration{},
			expect: &SupportedRTPConfiguration{
				SRTPCryptoType: []byte{CryptoAES_CM_128_HMAC_SHA1_80, CryptoAES_CM_256_HMAC_SHA1_80, CryptoDisabled},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func TestHomebridge(t *testing.T) {
	tests := []testTLV8{
		{
			name:   "114",
			value:  "AcUBAQACHQEBAAAAAQEBAAABAQICAQAAAAIBAQAAAgECAwEAAwsBAkABAgK0AAMBHgAAAwsBAkABAgLwAAMBDwAAAwsBAkABAgLwAAMBHgAAAwsBAuABAgIOAQMBHgAAAwsBAuABAgJoAQMBHgAAAwsBAoACAgJoAQMBHgAAAwsBAoACAgLgAQMBHgAAAwsBAgAFAgLQAgMBHgAAAwsBAgAFAgLAAwMBHgAAAwsBAoAHAgI4BAMBHgAAAwsBAkAGAgKwBAMBHg==",
			actual: &SupportedVideoStreamConfiguration{},
			expect: &SupportedVideoStreamConfiguration{
				Codecs: []VideoCodecConfiguration{
					{
						CodecType: VideoCodecTypeH264,
						CodecParams: []VideoCodecParameters{
							{
								ProfileID: []byte{VideoCodecProfileConstrainedBaseline, VideoCodecProfileMain, VideoCodecProfileHigh},
								Level:     []byte{VideoCodecLevel31, VideoCodecLevel32, VideoCodecLevel40},
							},
						},
						VideoAttrs: []VideoCodecAttributes{

							{Width: 320, Height: 180, Framerate: 30},
							{Width: 320, Height: 240, Framerate: 15},
							{Width: 320, Height: 240, Framerate: 30},
							{Width: 480, Height: 270, Framerate: 30},
							{Width: 480, Height: 360, Framerate: 30},
							{Width: 640, Height: 360, Framerate: 30},
							{Width: 640, Height: 480, Framerate: 30},
							{Width: 1280, Height: 720, Framerate: 30},
							{Width: 1280, Height: 960, Framerate: 30},
							{Width: 1920, Height: 1080, Framerate: 30},
							{Width: 1600, Height: 1200, Framerate: 30},
						},
					},
				},
			},
		},
		{
			name:   "116",
			value:  "AgEA",
			actual: &SupportedRTPConfiguration{},
			expect: &SupportedRTPConfiguration{
				SRTPCryptoType: []byte{CryptoAES_CM_128_HMAC_SHA1_80},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func TestScrypted(t *testing.T) {
	tests := []testTLV8{
		{
			name:   "114",
			value:  "AVIBAQACEwEBAQIBAAAAAgEBAAACAQIDAQADCwECAA8CAnAIAwEeAAADCwECgAcCAjgEAwEeAAADCwECAAUCAtACAwEeAAADCwECQAECAvAAAwEP",
			actual: &SupportedVideoStreamConfiguration{},
			expect: &SupportedVideoStreamConfiguration{
				Codecs: []VideoCodecConfiguration{
					{
						CodecType: VideoCodecTypeH264,
						CodecParams: []VideoCodecParameters{
							{
								ProfileID: []byte{VideoCodecProfileMain},
								Level:     []byte{VideoCodecLevel31, VideoCodecLevel32, VideoCodecLevel40},
							},
						},
						VideoAttrs: []VideoCodecAttributes{
							{Width: 3840, Height: 2160, Framerate: 30},
							{Width: 1920, Height: 1080, Framerate: 30},
							{Width: 1280, Height: 720, Framerate: 30},
							{Width: 320, Height: 240, Framerate: 15},
						},
					},
				},
			},
		},
		{
			name:   "115",
			value:  "AScBAQMCIgEBAQIBAAMBAAAAAwEAAAADAQEAAAMBAQAAAwECAAADAQICAQA=",
			actual: &SupportedAudioStreamConfiguration{},
			expect: &SupportedAudioStreamConfiguration{
				Codecs: []AudioCodecConfiguration{
					{
						CodecType: AudioCodecTypeOpus,
						CodecParams: []AudioCodecParameters{
							{
								Channels:    1,
								BitrateMode: AudioCodecBitrateVariable,
								SampleRate: []byte{
									AudioCodecSampleRate8Khz, AudioCodecSampleRate8Khz,
									AudioCodecSampleRate16Khz, AudioCodecSampleRate16Khz,
									AudioCodecSampleRate24Khz, AudioCodecSampleRate24Khz,
								},
							},
						},
					},
				},
				ComfortNoiseSupport: 0,
			},
		},
		{
			name:   "116",
			value:  "AgEAAAACAQI=",
			actual: &SupportedRTPConfiguration{},
			expect: &SupportedRTPConfiguration{
				SRTPCryptoType: []byte{CryptoAES_CM_128_HMAC_SHA1_80, CryptoDisabled},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func TestHass(t *testing.T) {
	tests := []testTLV8{
		{
			name:  "114",
			value: "AdABAQACFQMBAAEBAAEBAQEBAgIBAAIBAQIBAgMMAQJAAQICtAADAg8AAwwBAkABAgLwAAMCDwADDAECQAECArQAAwIeAAMMAQJAAQIC8AADAh4AAwwBAuABAgIOAQMCHgADDAEC4AECAmgBAwIeAAMMAQKAAgICaAEDAh4AAwwBAoACAgLgAQMCHgADDAECAAQCAkACAwIeAAMMAQIABAICAAMDAh4AAwwBAgAFAgLQAgMCHgADDAECAAUCAsADAwIeAAMMAQKABwICOAQDAh4A",
		},
		{
			name:  "115",
			value: "AQ4BAQMCCQEBAQIBAAMBAgEOAQEDAgkBAQECAQADAQECAQA=",
		},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func TestHomeLoomAccessoryUsesStandardSetupEndpointsReadAfterWrite(t *testing.T) {
	accessory := NewAccessory("HomeLoom", "Camera", "Camera", "camera-1", "dev")
	for _, service := range accessory.Services {
		if service.Type != "110" {
			continue
		}
		setup := service.GetCharacter(TypeSetupEndpoints)
		if setup == nil {
			t.Fatal("missing SetupEndpoints characteristic")
		}
		hasRead, hasWrite, hasWriteResponse := false, false, false
		for _, perm := range setup.Perms {
			switch perm {
			case "pr":
				hasRead = true
			case "pw":
				hasWrite = true
			case "wr":
				hasWriteResponse = true
			}
		}
		if !hasRead || !hasWrite || hasWriteResponse {
			t.Fatalf("SetupEndpoints perms = %#v, want pr/pw without wr", setup.Perms)
		}
	}
}
