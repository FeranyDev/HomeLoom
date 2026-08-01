package media

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validProfile() MediaProfile {
	return MediaProfile{
		SchemaVersion: SchemaVersion,
		ID:            "main",
		Name:          "Main",
		Width:         1920,
		Height:        1080,
		FPS:           25,
		VideoCodec:    VideoCodecH264,
		AudioCodec:    AudioCodecAAC,
		Bitrate:       2_000_000,
	}
}

func validSource() MediaSource {
	return MediaSource{
		SchemaVersion:    SchemaVersion,
		DeviceID:         "camera-1",
		ProviderID:       "onvif-main",
		ProviderDeviceID: "native-camera-1",
		Protocol:         ProtocolRTSP,
		CredentialRef:    "media-credential-1",
		Profiles:         []MediaProfile{validProfile()},
		SourceConfig:     json.RawMessage(`{"host":"192.0.2.10","port":554,"path":"/live"}`),
		Revision:         1,
		Enabled:          true,
	}
}

func validStream() StreamSpec {
	return StreamSpec{
		SchemaVersion: SchemaVersion,
		ID:            "camera_camera-1",
		DeviceID:      "camera-1",
		Protocol:      ProtocolRTSP,
		CredentialRef: "media-credential-1",
		Profile:       "main",
		Mode:          StreamPreload,
		Audio:         true,
		Talkback:      true,
		Options:       json.RawMessage(`{"transport":"tcp"}`),
	}
}

func TestMediaSourceValidate(t *testing.T) {
	source := validSource()
	if err := source.Validate(); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}

	tests := map[string]func(*MediaSource){
		"schema":             func(value *MediaSource) { value.SchemaVersion++ },
		"protocol":           func(value *MediaSource) { value.Protocol = "unknown" },
		"revision":           func(value *MediaSource) { value.Revision = 0 },
		"provider device id": func(value *MediaSource) { value.ProviderDeviceID = "" },
		"profile codec":      func(value *MediaSource) { value.Profiles[0].VideoCodec = "mpeg2" },
		"duplicate profile":  func(value *MediaSource) { value.Profiles = append(value.Profiles, value.Profiles[0]) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := validSource()
			mutate(&value)
			if err := value.Validate(); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}

func TestCameraProtocolScopeIsFrozenToThreeInputs(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolRTSP, ProtocolONVIF, ProtocolXiaomiMISS} {
		if !protocol.Valid() {
			t.Fatalf("scoped protocol %q was rejected", protocol)
		}
	}
	for _, protocol := range []Protocol{"tapo", "homekit-camera", "webrtc", "rtmp", "hls"} {
		if protocol.Valid() {
			t.Fatalf("out-of-scope protocol %q was accepted", protocol)
		}
	}
}

func TestStreamSpecValidate(t *testing.T) {
	stream := validStream()
	if err := stream.Validate(); err != nil {
		t.Fatalf("valid stream rejected: %v", err)
	}
	stream.Mode = "eager"
	if err := stream.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("invalid mode error = %v", err)
	}
	stream = validStream()
	stream.Audio = false
	if err := stream.Validate(); err == nil || !strings.Contains(err.Error(), "talkback requires audio") {
		t.Fatalf("talkback validation error = %v", err)
	}
}

func TestStreamReplayAndMutationRevisionContract(t *testing.T) {
	replay := StreamReplay{
		SchemaVersion: SchemaVersion,
		Generation:    3,
		Revision:      11,
		Streams:       []StreamSpec{validStream()},
	}
	if err := replay.Validate(); err != nil {
		t.Fatalf("valid replay rejected: %v", err)
	}
	replay.Generation = 0
	if err := replay.Validate(); err == nil || !strings.Contains(err.Error(), "generation and revision") {
		t.Fatalf("zero generation error = %v", err)
	}

	mutation := StreamMutation{
		SchemaVersion: SchemaVersion,
		Generation:    3,
		Revision:      12,
		Action:        MutationUpsert,
		Spec:          validStream(),
	}
	if err := mutation.Validate(); err != nil {
		t.Fatalf("valid mutation rejected: %v", err)
	}
	for name, state := range map[string]struct {
		generation uint64
		revision   uint64
		want       MutationDecision
	}{
		"next":           {generation: 3, revision: 11, want: MutationApply},
		"duplicate":      {generation: 3, revision: 12, want: MutationDuplicate},
		"older revision": {generation: 3, revision: 13, want: MutationDuplicate},
		"old generation": {generation: 4, revision: 1, want: MutationStale},
		"new generation": {generation: 2, revision: 99, want: MutationNeedReplay},
		"revision gap":   {generation: 3, revision: 9, want: MutationNeedReplay},
		"uninitialized":  {generation: 0, revision: 0, want: MutationNeedReplay},
	} {
		t.Run(name, func(t *testing.T) {
			if got := mutation.Decision(state.generation, state.revision); got != state.want {
				t.Fatalf("Decision(%d, %d) = %q, want %q", state.generation, state.revision, got, state.want)
			}
		})
	}

	mutation.Action = "replace"
	if err := mutation.Validate(); err == nil || !strings.Contains(err.Error(), "mutation action") {
		t.Fatalf("invalid mutation action error = %v", err)
	}
}

func TestStreamOperationUsesStrictActionEnum(t *testing.T) {
	operation := StreamOperation{
		SchemaVersion: SchemaVersion,
		RequestID:     "operation-1",
		StreamID:      "camera_camera-1",
		Action:        OperationSnapshot,
	}
	if err := operation.Validate(); err != nil {
		t.Fatalf("valid operation rejected: %v", err)
	}
	operation.Action = "configure"
	if err := operation.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("invalid operation error = %v", err)
	}
}

func TestLogicalConfigurationRejectsSensitiveMaterial(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"password":        json.RawMessage(`{"password":"secret-canary"}`),
		"nested token":    json.RawMessage(`{"auth":{"access_token":"secret-canary"}}`),
		"private key":     json.RawMessage(`{"clientPrivateKey":"secret-canary"}`),
		"URI user info":   json.RawMessage(`{"url":"rtsp://user:secret-canary@camera/live"}`),
		"URI query token": json.RawMessage(`{"url":"https://camera/live?access_token=secret-canary"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateLogicalConfig(raw); err == nil || !strings.Contains(err.Error(), "sensitive") && !strings.Contains(err.Error(), "user information") {
				t.Fatalf("sensitive logical configuration accepted: %v", err)
			}
		})
	}

	safe := json.RawMessage(`{"credentialRef":"credential-1","clientPublic":"base64-public","endpoint":{"host":"camera.local","port":554}}`)
	if err := ValidateLogicalConfig(safe); err != nil {
		t.Fatalf("reference-only logical configuration rejected: %v", err)
	}

	source := validSource()
	source.SourceConfig = json.RawMessage(`{"refreshToken":"secret-canary"}`)
	if err := source.Validate(); err == nil {
		t.Fatal("MediaSource accepted a plaintext token")
	}
	stream := validStream()
	stream.Options = json.RawMessage(`{"turnPassword":"secret-canary"}`)
	if err := stream.Validate(); err == nil {
		t.Fatal("StreamSpec accepted a plaintext password")
	}
}

func TestDecodeStrictConfigRejectsUnknownFields(t *testing.T) {
	type rtspConfig struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	var decoded rtspConfig
	if err := DecodeStrictConfig(json.RawMessage(`{"host":"camera.local","port":554}`), &decoded); err != nil {
		t.Fatalf("strict config rejected: %v", err)
	}
	if decoded.Host != "camera.local" || decoded.Port != 554 {
		t.Fatalf("decoded config = %#v", decoded)
	}
	if err := DecodeStrictConfig(json.RawMessage(`{"host":"camera.local","port":554,"unknown":true}`), &decoded); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
	if err := DecodeStrictConfig(json.RawMessage(`{"host":"camera.local","password":"secret-canary"}`), &decoded); err == nil || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("secret field error = %v", err)
	}
}

func TestAuthorizationContracts(t *testing.T) {
	now := time.Now().UTC()
	request := AuthorizationRequest{
		SchemaVersion:    SchemaVersion,
		RequestID:        "request-1",
		WorkerID:         "worker-1",
		WorkerInstanceID: "worker-instance-1",
		DeviceID:         "camera-1",
		Protocol:         ProtocolXiaomiMISS,
		Purpose:          PurposePlayback,
		Attempt:          1,
		ClientMaterial:   json.RawMessage(`{"clientPublic":"base64-public"}`),
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid authorization request rejected: %v", err)
	}
	request.ClientMaterial = json.RawMessage(`{"sharedKey":"secret-canary"}`)
	if err := request.Validate(); err == nil {
		t.Fatal("authorization request accepted private session material")
	}

	response := AuthorizationResponse{
		SchemaVersion:  SchemaVersion,
		LeaseID:        "lease-1",
		ExpiresAt:      now.Add(time.Minute),
		Endpoint:       EndpointSpec{Protocol: ProtocolXiaomiMISS, Host: "camera.local", Port: 443},
		AuthType:       AuthTypeSession,
		PublicMaterial: json.RawMessage(`{"serverPublic":"base64-public"}`),
		SecretMaterial: json.RawMessage(`{"sessionKey":"short-lived-secret"}`),
		MaxUses:        1,
	}
	if err := response.ValidateAt(now); err != nil {
		t.Fatalf("valid authorization response rejected: %v", err)
	}
	response.MaxUses = 2
	if err := response.ValidateAt(now); err == nil || !strings.Contains(err.Error(), "exactly one use") {
		t.Fatalf("non-reusable lease validation error = %v", err)
	}
}

func TestEndpointSpecAllowsPortlessXiaomiP2PButNotRTSP(t *testing.T) {
	if err := (EndpointSpec{Protocol: ProtocolXiaomiMISS, Host: "192.168.1.20"}).Validate(); err != nil {
		t.Fatalf("portless Xiaomi endpoint = %v", err)
	}
	if err := (EndpointSpec{Protocol: ProtocolRTSP, Host: "192.168.1.20"}).Validate(); err == nil {
		t.Fatal("portless RTSP endpoint was accepted")
	}
}

func TestSessionReportValidate(t *testing.T) {
	connected := time.Now().UTC()
	report := SessionReport{
		SchemaVersion:    SchemaVersion,
		LeaseID:          "lease-1",
		WorkerID:         "worker-1",
		WorkerInstanceID: "worker-instance-1",
		DeviceID:         "camera-1",
		Result:           SessionNetworkFailed,
		ErrorCode:        "dial-timeout",
		ConnectedAt:      connected,
		EndedAt:          connected.Add(time.Second),
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("valid session report rejected: %v", err)
	}
	report.Result = "unknown"
	if err := report.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("invalid result error = %v", err)
	}
	report.Result = SessionConnected
	report.EndedAt = connected.Add(-time.Second)
	if err := report.Validate(); err == nil || !strings.Contains(err.Error(), "precedes") {
		t.Fatalf("invalid session timestamps error = %v", err)
	}
}
