package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/media"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

const cameraPasswordCanary = "camera-password-secret-canary"

func cameraProfile(audio media.AudioCodec) media.MediaProfile {
	return media.MediaProfile{
		SchemaVersion: media.SchemaVersion,
		ID:            "main",
		Name:          "Main",
		Width:         1920,
		Height:        1080,
		FPS:           25,
		VideoCodec:    media.VideoCodecH264,
		AudioCodec:    audio,
		Bitrate:       2_000_000,
	}
}

func cameraDeviceConfig(id, did string) DeviceConfig {
	return DeviceConfig{
		DID:  did,
		ID:   id,
		Name: "Camera",
		Type: device.TypeCamera,
		Media: &CameraMediaConfig{
			Protocol: media.ProtocolRTSP,
			Port:     8554,
			Path:     "/live/main",
			AuthType: media.AuthTypeDigest,
			Username: "camera-user",
			Password: cameraPasswordCanary,
			Profiles: []media.MediaProfile{cameraProfile(media.AudioCodecAAC)},
		},
	}
}

func nativeCameraDeviceConfig(id, did string) DeviceConfig {
	return DeviceConfig{
		DID:   did,
		ID:    id,
		Name:  "Native Camera",
		Type:  device.TypeCamera,
		Model: "isa.camera.hlc7",
		Media: &CameraMediaConfig{
			Protocol: media.ProtocolXiaomiMISS,
			Host:     "192.0.2.20",
			AuthType: media.AuthTypeVendor,
			Subtype:  "hd",
			Channel:  1,
			Profiles: []media.MediaProfile{cameraProfile(media.AudioCodecAAC)},
		},
	}
}

type fakeXiaomiMISSMediaCloud struct {
	authorization xiaomiMISSAuthorization
	err           error
	did           string
	clientPublic  string
}

func (*fakeXiaomiMISSMediaCloud) Login(context.Context) error { return nil }
func (*fakeXiaomiMISSMediaCloud) DeviceList(context.Context) ([]HubDevice, error) {
	return nil, nil
}
func (*fakeXiaomiMISSMediaCloud) GetProperties(context.Context, []cloudProperty) ([]cloudProperty, error) {
	return nil, nil
}
func (*fakeXiaomiMISSMediaCloud) SetProperties(context.Context, []cloudProperty) ([]cloudProperty, error) {
	return nil, nil
}
func (*fakeXiaomiMISSMediaCloud) Action(context.Context, cloudAction) error { return nil }
func (f *fakeXiaomiMISSMediaCloud) AcquireMISSAuthorization(_ context.Context, did, clientPublic string) (xiaomiMISSAuthorization, error) {
	f.did, f.clientPublic = did, clientPublic
	return f.authorization, f.err
}

func cameraAuthorizationRequest(deviceID string, purpose media.AuthorizationPurpose) media.AuthorizationRequest {
	return media.AuthorizationRequest{
		SchemaVersion:    media.SchemaVersion,
		RequestID:        "request-1",
		WorkerID:         "worker-1",
		WorkerInstanceID: "worker-instance-1",
		DeviceID:         deviceID,
		Protocol:         media.ProtocolRTSP,
		Purpose:          purpose,
		Attempt:          1,
	}
}

func TestXiaomiCameraMediaDiscoveryAndRefreshNeverExposeCredentials(t *testing.T) {
	configured := cameraDeviceConfig("xiaomi-camera-1", "did-camera-1")
	config := Config{Devices: []DeviceConfig{configured}}
	provider, err := newProvider("xiaomi-main", "Xiaomi", config, func() hubClient { return &fakeHub{} })
	if err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.directory = []HubDevice{{DID: configured.DID, LocalIP: "192.0.2.10"}}
	provider.mu.Unlock()

	sources, err := provider.DiscoverMediaSources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("DiscoverMediaSources() = %#v, %v", sources, err)
	}
	source := sources[0]
	if source.DeviceID != configured.ID || source.ProviderID != "xiaomi-main" ||
		source.ProviderDeviceID != configured.DID || source.CredentialRef != "xiaomi-camera-1-rtsp-auth" {
		t.Fatalf("discovered source = %#v", source)
	}
	var logical rtspSourceConfig
	if err := media.DecodeStrictConfig(source.SourceConfig, &logical); err != nil {
		t.Fatal(err)
	}
	if logical.Host != "192.0.2.10" || logical.Port != 8554 || logical.Path != "/live/main" {
		t.Fatalf("logical source config = %#v", logical)
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), cameraPasswordCanary) || strings.Contains(string(encoded), "camera-user") {
		t.Fatalf("media discovery exposed credentials: %s", encoded)
	}

	provider.mu.Lock()
	provider.directory[0].LocalIP = "192.0.2.11"
	provider.mu.Unlock()
	refreshed, err := provider.RefreshMediaSource(context.Background(), configured.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(refreshed.SourceConfig, &logical); err != nil || logical.Host != "192.0.2.11" {
		t.Fatalf("refreshed source = %#v, logical=%#v, err=%v", refreshed, logical, err)
	}
}

func TestXiaomiCloudProviderDiscoversConfiguredCameraMedia(t *testing.T) {
	configured := cameraDeviceConfig("xiaomi-cloud-camera-1", "cloud-camera-1")
	configured.Media.Host = "camera.lan"
	provider, err := newCloudProvider(
		"xiaomi-miot-cloud-main",
		"Xiaomi Cloud",
		CloudConfig{Devices: []DeviceConfig{configured}},
		func() miotCloudClient { return nil },
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := provider.DiscoverMediaSources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("DiscoverMediaSources() = %#v, %v", sources, err)
	}
	if sources[0].ProviderID != "xiaomi-miot-cloud-main" || !strings.Contains(string(sources[0].SourceConfig), "camera.lan") {
		t.Fatalf("cloud media source = %#v", sources[0])
	}
}

func TestXiaomiCameraAuthorizationReturnsOnlyBrokeredRTSPSecret(t *testing.T) {
	configured := cameraDeviceConfig("xiaomi-camera-1", "did-camera-1")
	configured.Media.Host = "camera.lan"
	provider, err := newProvider("xiaomi-main", "Xiaomi", Config{Devices: []DeviceConfig{configured}}, func() hubClient { return &fakeHub{} })
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	response, err := provider.AcquireMediaAuthorization(
		context.Background(),
		cameraAuthorizationRequest(configured.ID, media.PurposePlayback),
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.AuthType != media.AuthTypeDigest || response.Endpoint.Host != "camera.lan" ||
		response.Endpoint.Path != "/live/main" || response.MaxUses != 1 || response.Reusable ||
		!response.ExpiresAt.After(before) || response.ExpiresAt.After(before.Add(2*time.Minute)) {
		t.Fatalf("authorization response = %#v", response)
	}
	var secret rtspSecretMaterial
	if err := json.Unmarshal(response.SecretMaterial, &secret); err != nil {
		t.Fatal(err)
	}
	if secret.Username != "camera-user" || secret.Password != cameraPasswordCanary {
		t.Fatalf("brokered RTSP material = %#v", secret)
	}
	if strings.Contains(string(response.PublicMaterial), cameraPasswordCanary) ||
		strings.Contains(string(response.SessionAnswer), cameraPasswordCanary) {
		t.Fatal("authorization copied the RTSP password into public material")
	}
}

func TestXiaomiCameraAuthorizationAndDiscoveryRejectUnsupportedDevices(t *testing.T) {
	camera := cameraDeviceConfig("xiaomi-camera-1", "did-camera-1")
	camera.Media.Host = "camera.lan"
	camera.Media.Profiles[0].AudioCodec = media.AudioCodecNone
	nonCamera := DeviceConfig{DID: "switch-1", ID: "xiaomi-switch-1", Name: "Switch", Type: device.Type("vendor-device")}
	provider, err := newProvider(
		"xiaomi-main",
		"Xiaomi",
		Config{Devices: []DeviceConfig{camera, nonCamera}},
		func() hubClient { return &fakeHub{} },
	)
	if err != nil {
		t.Fatal(err)
	}

	sources, err := provider.DiscoverMediaSources(context.Background())
	if err != nil || len(sources) != 1 || sources[0].DeviceID != camera.ID {
		t.Fatalf("DiscoverMediaSources() = %#v, %v", sources, err)
	}
	if _, err := provider.RefreshMediaSource(context.Background(), nonCamera.ID); !errors.Is(err, providersdk.ErrPropertyUnsupported) {
		t.Fatalf("non-camera refresh error = %v", err)
	}
	if _, err := provider.RefreshMediaSource(context.Background(), "missing"); !errors.Is(err, providersdk.ErrDeviceNotFound) {
		t.Fatalf("missing refresh error = %v", err)
	}
	if _, err := provider.AcquireMediaAuthorization(
		context.Background(),
		cameraAuthorizationRequest(camera.ID, media.PurposeTalkback),
	); !errors.Is(err, providersdk.ErrCommandUnsupported) {
		t.Fatalf("unsupported talkback error = %v", err)
	}
}

func TestXiaomiCameraMediaConfigurationValidation(t *testing.T) {
	valid := cameraDeviceConfig("xiaomi-camera-1", "did-camera-1")
	applyCameraMediaDefaults(&valid)
	if err := validateCameraMediaConfig(valid); err != nil {
		t.Fatalf("valid media config rejected: %v", err)
	}

	tests := map[string]func(*DeviceConfig){
		"non camera": func(item *DeviceConfig) { item.Type = device.TypeSwitch },
		"protocol":   func(item *DeviceConfig) { item.Media.Protocol = media.ProtocolXiaomiMISS },
		"host URI":   func(item *DeviceConfig) { item.Media.Host = "rtsp://user:password@camera" },
		"path query": func(item *DeviceConfig) { item.Media.Path = "/live?token=secret" },
		"password":   func(item *DeviceConfig) { item.Media.Username = "" },
		"profiles":   func(item *DeviceConfig) { item.Media.Profiles = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			item := cameraDeviceConfig("xiaomi-camera-1", "did-camera-1")
			mutate(&item)
			err := validateCameraMediaConfig(item)
			if err == nil {
				t.Fatal("invalid media config accepted")
			}
			if strings.Contains(err.Error(), cameraPasswordCanary) {
				t.Fatalf("validation error exposed password: %v", err)
			}
		})
	}
}

func TestXiaomiCameraMediaDefaultsKeepSecretsOutOfLogicalSource(t *testing.T) {
	item := cameraDeviceConfig("xiaomi-camera-1", "did-camera-1")
	item.Media.Protocol = ""
	item.Media.Port = 0
	item.Media.AuthType = ""
	item.Media.Profiles[0].SchemaVersion = 0
	applyCameraMediaDefaults(&item)
	if item.Media.Protocol != media.ProtocolRTSP || item.Media.Port != 554 ||
		item.Media.AuthType != media.AuthTypeBasic || item.Media.Profiles[0].SchemaVersion != media.SchemaVersion {
		t.Fatalf("camera media defaults = %#v", item.Media)
	}
	source, err := buildCameraMediaSource("xiaomi-main", item, []HubDevice{{DID: item.DID, LocalIP: "192.0.2.10"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source.SourceConfig), cameraPasswordCanary) {
		t.Fatalf("logical source contains password: %s", source.SourceConfig)
	}
}

func TestXiaomiNativeCameraSourceIsSecretFreeAndStructured(t *testing.T) {
	item := nativeCameraDeviceConfig("xiaomi-camera-native", "native-did-1")
	source, err := buildCameraMediaSource("xiaomi-miot-cloud-main", item, nil)
	if err != nil {
		t.Fatal(err)
	}
	if source.Protocol != media.ProtocolXiaomiMISS || source.CredentialRef != "xiaomi-camera-native-xiaomi-account" {
		t.Fatalf("native source = %#v", source)
	}
	var config xiaomiNativeSourceConfig
	if err := media.DecodeStrictConfig(source.SourceConfig, &config); err != nil {
		t.Fatal(err)
	}
	if config.DID != item.DID || config.Model != item.Model || config.LocalIP != item.Media.Host ||
		config.Subtype != "hd" || config.Channel != 1 {
		t.Fatalf("native source config = %#v", config)
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"passToken", "serviceToken", "accessToken", cameraPasswordCanary} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("native source leaked %s: %s", forbidden, encoded)
		}
	}
}

func TestXiaomiNativeCameraAuthorizationUsesPassTokenBoundary(t *testing.T) {
	item := nativeCameraDeviceConfig("xiaomi-camera-native", "native-did-1")
	config := CloudConfig{
		Region: "cn", UserID: "account-1", Ssecurity: "session-security",
		ServiceToken: "miot-service-token", PassToken: "distinct-pass-token",
		Devices: []DeviceConfig{item},
	}
	cloud := &fakeXiaomiMISSMediaCloud{authorization: xiaomiMISSAuthorization{
		DevicePublic: "device-public",
		Sign:         "short-lived-sign",
		Vendor:       "cs2",
		UID:          "p2p-uid",
	}}
	provider, err := newCloudProvider("xiaomi-miot-cloud-main", "Xiaomi Cloud", config, func() miotCloudClient { return cloud }, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.client = cloud
	provider.mu.Unlock()

	request := cameraAuthorizationRequest(item.ID, media.PurposePlayback)
	request.Protocol = media.ProtocolXiaomiMISS
	request.ClientMaterial = json.RawMessage(`{"clientPublic":"04aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	response, err := provider.AcquireMediaAuthorization(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if cloud.did != item.DID || cloud.clientPublic != "04aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("MISS cloud request = did %q clientPublic %q", cloud.did, cloud.clientPublic)
	}
	if response.AuthType != media.AuthTypeVendor || response.Endpoint.Protocol != media.ProtocolXiaomiMISS ||
		response.Endpoint.Host != item.Media.Host || response.Endpoint.Port != 0 {
		t.Fatalf("MISS response = %#v", response)
	}
	var public xiaomiMISSPublicMaterial
	var secret xiaomiMISSSecretMaterial
	if err := json.Unmarshal(response.PublicMaterial, &public); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(response.SecretMaterial, &secret); err != nil {
		t.Fatal(err)
	}
	if public.UserID != config.UserID || public.Region != config.Region || public.DID != item.DID ||
		public.Model != item.Model || public.LocalIP != item.Media.Host || public.Subtype != item.Media.Subtype ||
		public.Channel != item.Media.Channel || public.DevicePublic != "device-public" ||
		public.Vendor != "cs2" || public.UID != "p2p-uid" ||
		secret.KernelPassToken != config.PassToken || secret.Sign != "short-lived-sign" {
		t.Fatalf("MISS material = public %#v secret %#v", public, secret)
	}
	publicEncoded, _ := json.Marshal(public)
	if strings.Contains(string(publicEncoded), config.PassToken) ||
		strings.Contains(string(response.SessionAnswer), config.PassToken) ||
		strings.Contains(response.Endpoint.Host, config.PassToken) {
		t.Fatalf("MISS passToken escaped secret material: public=%s response=%#v", publicEncoded, response)
	}
	encoded, _ := json.Marshal(response)
	if strings.Contains(string(encoded), config.ServiceToken) || strings.Contains(string(encoded), config.Ssecurity) {
		t.Fatalf("MISS response leaked unrelated account credentials: %s", encoded)
	}
}

func TestXiaomiNativeCameraKernelAuthorizationNeedsNoClientPublic(t *testing.T) {
	item := nativeCameraDeviceConfig("xiaomi-camera-kernel", "native-did-kernel")
	config := CloudConfig{
		Region: "de", UserID: "account-kernel", Ssecurity: "session-security",
		ServiceToken: "miot-service-token", PassToken: "kernel-pass-token-canary",
		Devices: []DeviceConfig{item},
	}
	provider, err := newCloudProvider(
		"xiaomi-miot-cloud-main",
		"Xiaomi Cloud",
		config,
		func() miotCloudClient { return nil },
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	request := cameraAuthorizationRequest(item.ID, media.PurposePlayback)
	request.Protocol = media.ProtocolXiaomiMISS
	response, err := provider.AcquireMediaAuthorization(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var public xiaomiMISSPublicMaterial
	var secret xiaomiMISSSecretMaterial
	if err := json.Unmarshal(response.PublicMaterial, &public); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(response.SecretMaterial, &secret); err != nil {
		t.Fatal(err)
	}
	if public.UserID != config.UserID || public.Region != config.Region || public.DID != item.DID ||
		public.Model != item.Model || public.LocalIP != item.Media.Host || public.Subtype != "hd" ||
		public.Channel != 1 || public.DevicePublic != "" || public.Vendor != "" {
		t.Fatalf("kernel public material = %#v", public)
	}
	if secret.KernelPassToken != config.PassToken || secret.Sign != "" {
		t.Fatalf("kernel secret material = %#v", secret)
	}
	source, err := buildCameraMediaSource("xiaomi-miot-cloud-main", item, nil)
	if err != nil {
		t.Fatal(err)
	}
	sourceJSON, _ := json.Marshal(source)
	if strings.Contains(string(sourceJSON), config.PassToken) ||
		strings.Contains(string(response.PublicMaterial), config.PassToken) {
		t.Fatalf("kernel passToken escaped secret lease material: source=%s public=%s", sourceJSON, response.PublicMaterial)
	}
}

func TestXiaomiNativeCameraNeverTreatsServiceOrOAuthTokenAsPassToken(t *testing.T) {
	item := nativeCameraDeviceConfig("xiaomi-camera-native", "native-did-1")
	cloud := &fakeXiaomiMISSMediaCloud{}
	provider, err := newCloudProvider(
		"xiaomi-miot-cloud-main",
		"Xiaomi Cloud",
		CloudConfig{
			Region: "cn", UserID: "account-1", Ssecurity: "security",
			ServiceToken: "must-not-be-used-as-pass-token", Devices: []DeviceConfig{item},
		},
		func() miotCloudClient { return cloud },
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.client = cloud
	provider.mu.Unlock()
	request := cameraAuthorizationRequest(item.ID, media.PurposePlayback)
	request.Protocol = media.ProtocolXiaomiMISS
	request.ClientMaterial = json.RawMessage(`{"clientPublic":"04aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	if _, err := provider.AcquireMediaAuthorization(context.Background(), request); err == nil || !strings.Contains(err.Error(), "passToken") {
		t.Fatalf("missing passToken error = %v", err)
	}
	if cloud.did != "" {
		t.Fatal("MISS authorization was attempted without a distinct passToken")
	}

	central, err := newProvider(
		"xiaomi-main",
		"Xiaomi",
		Config{
			OAuth:   &OAuthConfig{AccessToken: "must-not-be-used-as-pass-token"},
			Devices: []DeviceConfig{item},
		},
		func() hubClient { return &fakeHub{} },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := central.AcquireMediaAuthorization(context.Background(), request); err == nil || !strings.Contains(err.Error(), "passToken") {
		t.Fatalf("OAuth token boundary error = %v", err)
	}
}

func TestXiaomiNativeMediaConfigRequiresPersistedPassToken(t *testing.T) {
	item := nativeCameraDeviceConfig("xiaomi-camera-native", "native-did-1")
	config := CloudConfig{
		Region: "cn", UserID: "account-1", Ssecurity: "security", ServiceToken: "service",
		Devices: []DeviceConfig{item}, PollIntervalSec: 30, RequestTimeoutSec: 15,
	}
	config.Devices[0].ConnectionMode = cloudConnectionAuto
	if err := config.validate(); err == nil || !strings.Contains(err.Error(), "passToken") ||
		strings.Contains(err.Error(), config.ServiceToken) {
		t.Fatalf("missing passToken validation error = %v", err)
	}
	config.PassToken = "distinct-pass-token"
	if err := config.validate(); err != nil {
		t.Fatalf("native media config with passToken rejected: %v", err)
	}
}
