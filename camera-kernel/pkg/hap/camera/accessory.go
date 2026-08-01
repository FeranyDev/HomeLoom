package camera

import (
	"github.com/AlexxIT/go2rtc/pkg/hap"
	"github.com/AlexxIT/go2rtc/pkg/hap/tlv8"
)

const CameraStreamManagementCount = 8

func NewAccessory(manuf, model, name, serial, firmware string) *hap.Accessory {
	services := make([]*hap.Service, 0, CameraStreamManagementCount+2)
	services = append(services, hap.ServiceAccessoryInformation(manuf, model, name, serial, firmware))
	for range CameraStreamManagementCount {
		services = append(services, ServiceCameraRTPStreamManagement())
	}
	services = append(services, ServiceMicrophone())

	acc := &hap.Accessory{
		AID:      hap.DeviceAID,
		Services: services,
	}
	acc.InitIID()
	return acc
}

func ServiceMicrophone() *hap.Service {
	return &hap.Service{
		Type: "112", // 'Microphone'
		Characters: []*hap.Character{
			{
				Type:   "11A",
				Format: hap.FormatBool,
				Value:  false,
				Perms:  hap.EVPRPW,
				//Descr:  "Mute",
			},
			//{
			//	Type:   "119",
			//	Format: hap.FormatUInt8,
			//	Value:  100,
			//	Perms:  hap.EVPRPW,
			//	//Descr:    "Volume",
			//	//Unit:     hap.UnitPercentage,
			//	//MinValue: 0,
			//	//MaxValue: 100,
			//	//MinStep:  1,
			//},
		},
	}
}

func ServiceCameraRTPStreamManagement() *hap.Service {
	// HAP-NodeJS resets these readable control characteristics to explicit
	// protocol states. Leaving them as an empty string makes hap.Character omit
	// "value" from /accessories because of json:",omitempty"; current Apple
	// controllers then reject the Camera RTP database before SetupEndpoints.
	val117, _ := tlv8.MarshalBase64(initialSelectedStreamConfiguration{
		Control: initialSessionControl{Command: SessionCommandSuspend},
	})
	val118, _ := tlv8.MarshalBase64(initialSetupEndpointsResponse{
		Status: SetupEndpointsStatusError,
	})
	val120, _ := tlv8.MarshalBase64(StreamingStatus{
		Status: StreamingStatusAvailable,
	})
	val114, _ := tlv8.MarshalBase64(SupportedVideoStreamConfiguration{
		Codecs: []VideoCodecConfiguration{
			{
				CodecType: VideoCodecTypeH264,
				CodecParams: []VideoCodecParameters{
					{
						// Keep the capability database aligned with the
						// iOS-26-tested go2rtc v1.9.14 camera accessory. Apple
						// controllers use this database to decide whether to send
						// SelectedRTPStreamConfiguration at all.
						ProfileID: []byte{VideoCodecProfileMain},
						Level:     []byte{VideoCodecLevel31, VideoCodecLevel32, VideoCodecLevel40},
					},
				},
				VideoAttrs: []VideoCodecAttributes{
					{Width: 1920, Height: 1080, Framerate: 20},
					{Width: 1280, Height: 720, Framerate: 20},
					{Width: 960, Height: 540, Framerate: 15},
					{Width: 640, Height: 360, Framerate: 15},
					{Width: 320, Height: 240, Framerate: 15},
				},
			},
		},
	})
	// Scrypted supplies six logical Opus combinations, but HAP-NodeJS merges
	// legacy entries of the same codec type before encoding. The resulting wire
	// format is one Opus parameter block with repeated sample-rate entries.
	audioCodecs := []AudioCodecConfiguration{
		{
			CodecType: AudioCodecTypeOpus,
			CodecParams: []AudioCodecParameters{
				{
					Channels:    1,
					BitrateMode: AudioCodecBitrateVariable,
					SampleRate: []byte{
						AudioCodecSampleRate8Khz,
						AudioCodecSampleRate8Khz,
						AudioCodecSampleRate16Khz,
						AudioCodecSampleRate16Khz,
						AudioCodecSampleRate24Khz,
						AudioCodecSampleRate24Khz,
					},
				},
			},
		},
	}
	val115, _ := tlv8.MarshalBase64(SupportedAudioStreamConfiguration{
		Codecs:              audioCodecs,
		ComfortNoiseSupport: 0,
	})
	val116, _ := tlv8.MarshalBase64(SupportedRTPConfiguration{
		SRTPCryptoType: []byte{CryptoAES_CM_128_HMAC_SHA1_80},
	})

	service := &hap.Service{
		Type: "110", // 'CameraRTPStreamManagement'
		Characters: []*hap.Character{
			{
				Type:   TypeSelectedStreamConfiguration,
				Format: hap.FormatTLV8,
				Value:  val117,
				Perms:  hap.PRPW,
				//Descr:  "Selected RTP Stream Configuration",
			},
			{
				Type:   TypeSetupEndpoints,
				Format: hap.FormatTLV8,
				Value:  val118,
				// HAP uses a two-step exchange: write controller endpoints,
				// receive 204, then read this characteristic for the accessory
				// SRTP answer.
				Perms: hap.PRPW,
				//Descr:  "Setup Endpoints",
			},
			{
				Type:   TypeStreamingStatus,
				Format: hap.FormatTLV8,
				Value:  val120,
				Perms:  hap.EVPR,
				//Descr:  "Streaming Status",
			},
			{
				Type:   TypeSupportedVideoStreamConfiguration,
				Format: hap.FormatTLV8,
				Value:  val114,
				Perms:  hap.PR,
				//Descr:  "Supported Video Stream Configuration",
			},
			{
				Type:   TypeSupportedAudioStreamConfiguration,
				Format: hap.FormatTLV8,
				Value:  val115,
				Perms:  hap.PR,
				//Descr:  "Supported Audio Stream Configuration",
			},
			{
				Type:   TypeSupportedRTPConfiguration,
				Format: hap.FormatTLV8,
				Value:  val116,
				Perms:  hap.PR,
				//Descr:  "Supported RTP Configuration",
			},
			{
				Type:   "B0",
				Format: hap.FormatUInt8,
				Value:  1,
				Perms:  hap.EVPRPW,
				//Descr:    "Active",
				//MinValue: 0,
				//MaxValue: 1,
				//MinStep:  1,
				//ValidVal: []any{0, 1},
			},
		},
	}

	return service
}

// These deliberately small types marshal only the fields allowed in the idle
// values. Reusing SelectedStreamConfiguration or SetupEndpointsResponse would
// encode their zero-value session/address/key fields as additional TLVs.
type initialSelectedStreamConfiguration struct {
	Control initialSessionControl `tlv8:"1"`
}

type initialSessionControl struct {
	Command byte `tlv8:"2"`
}

type initialSetupEndpointsResponse struct {
	Status byte `tlv8:"2"`
}
