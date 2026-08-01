package camera

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/media"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
)

func testProfile() media.MediaProfile {
	return media.MediaProfile{
		SchemaVersion: media.SchemaVersion, ID: "main", Name: "Main",
		Width: 1920, Height: 1080, FPS: 25, VideoCodec: media.VideoCodecH264,
		AudioCodec: media.AudioCodecAAC, Bitrate: 2_000_000,
	}
}

func newTestProvider(t *testing.T, config Config) *Provider {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewProviderFromConfig(providerconfig.Config{
		ID: "camera-main", Type: ProviderType, Name: "Cameras", Enabled: true, Config: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestRTSPCameraProviderOwnsDeviceSourceAndAuthorization(t *testing.T) {
	provider := newTestProvider(t, Config{Cameras: []Entry{{
		ID: "front-door", Name: "Front Door", Driver: "rtsp", Profiles: []media.MediaProfile{testProfile()},
		RTSP: &RTSPConfig{Host: "192.0.2.10", Path: "/live", Username: "viewer", Password: "secret"},
	}}})
	devices, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(devices) != 1 {
		t.Fatalf("DiscoverDevices() = %#v, %v", devices, err)
	}
	if devices[0].ProviderID != "camera-main" || devices[0].ID != "front-door" {
		t.Fatalf("camera identity = %#v", devices[0])
	}
	if err := devices[0].ValidateStructure(); err != nil {
		t.Fatalf("camera device structure = %v", err)
	}
	sources, err := provider.DiscoverMediaSources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("DiscoverMediaSources() = %#v, %v", sources, err)
	}
	if sources[0].ProviderID != "camera-main" || sources[0].Protocol != media.ProtocolRTSP || sources[0].CredentialRef == "" {
		t.Fatalf("media source = %#v", sources[0])
	}
	if sources[0].ConnectionMode != media.StreamOnDemand {
		t.Fatalf("default connection mode = %q", sources[0].ConnectionMode)
	}
	response, err := provider.AcquireMediaAuthorization(context.Background(), media.AuthorizationRequest{
		SchemaVersion: media.SchemaVersion, RequestID: "request-1", WorkerID: "worker-1", WorkerInstanceID: "instance-1",
		DeviceID: "front-door", Protocol: media.ProtocolRTSP, Purpose: media.PurposePlayback, Attempt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Endpoint.Host != "192.0.2.10" || response.AuthType != media.AuthTypeBasic {
		t.Fatalf("authorization = %#v", response)
	}
	var secret map[string]string
	if err := json.Unmarshal(response.SecretMaterial, &secret); err != nil || secret["password"] != "secret" {
		t.Fatalf("secret material = %#v, %v", secret, err)
	}
}

func TestCameraProviderExposesControlBindingWithoutCredentials(t *testing.T) {
	provider := newTestProvider(t, Config{Cameras: []Entry{{
		ID: "front-door", Name: "Front Door", Driver: "rtsp", Profiles: []media.MediaProfile{testProfile()},
		RTSP:    &RTSPConfig{Host: "192.0.2.10", Path: "/live"},
		Control: &ControlBinding{ProviderRef: "xiaomi-hub", DeviceID: "xiaomi-camera"},
	}}})
	bindings := provider.CapabilityBindings()
	if len(bindings) != 1 || bindings[0].DeviceID != "front-door" ||
		bindings[0].ProviderID != "xiaomi-hub" || bindings[0].SourceDeviceID != "xiaomi-camera" {
		t.Fatalf("control bindings = %#v", bindings)
	}
	encoded, err := json.Marshal(provider.config.Cameras[0].Control)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "password", "did"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("control binding leaked protocol identity or credentials: %s", encoded)
		}
	}
}

func TestCameraProviderRejectsInvalidOrSelfControlBinding(t *testing.T) {
	for name, binding := range map[string]*ControlBinding{
		"invalid provider": {ProviderRef: "not valid", DeviceID: "camera"},
		"invalid device":   {ProviderRef: "xiaomi-hub", DeviceID: ""},
		"self provider":    {ProviderRef: "camera-main", DeviceID: "camera"},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(Config{Cameras: []Entry{{
				ID: "front-door", Name: "Front Door", Driver: "rtsp", Profiles: []media.MediaProfile{testProfile()},
				RTSP: &RTSPConfig{Host: "192.0.2.10", Path: "/live"}, Control: binding,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewProviderFromConfig(providerconfig.Config{
				ID: "camera-main", Type: ProviderType, Name: "Cameras", Config: raw,
			}); err == nil {
				t.Fatal("invalid control binding was accepted")
			}
		})
	}
}

func TestCameraProviderExposesConfiguredConnectionModes(t *testing.T) {
	for _, mode := range []media.StreamMode{media.StreamOnDemand, media.StreamPreload, media.StreamAlwaysOn} {
		provider := newTestProvider(t, Config{Cameras: []Entry{{
			ID: "camera-" + string(mode), Name: "Camera", Driver: "rtsp", ConnectionMode: mode,
			Profiles: []media.MediaProfile{testProfile()},
			RTSP:     &RTSPConfig{Host: "192.0.2.10", Path: "/live"},
		}}})
		sources, err := provider.DiscoverMediaSources(context.Background())
		if err != nil || len(sources) != 1 || sources[0].ConnectionMode != mode {
			t.Fatalf("mode %q source = %#v, %v", mode, sources, err)
		}
	}
}

func TestONVIFCameraProviderOwnsDiscoveryInputAndDigestAuthorization(t *testing.T) {
	provider := newTestProvider(t, Config{Cameras: []Entry{{
		ID: "garage", Name: "Garage", Driver: "onvif", Profiles: []media.MediaProfile{testProfile()},
		ONVIF: &ONVIFConfig{
			Host: "192.0.2.11", Profile: "profile-main", Username: "viewer", Password: "secret",
		},
	}}})
	sources, err := provider.DiscoverMediaSources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("DiscoverMediaSources() = %#v, %v", sources, err)
	}
	if sources[0].Protocol != media.ProtocolONVIF || sources[0].CredentialRef != "garage-onvif-auth" {
		t.Fatalf("ONVIF source = %#v", sources[0])
	}
	response, err := provider.AcquireMediaAuthorization(context.Background(), media.AuthorizationRequest{
		SchemaVersion: media.SchemaVersion, RequestID: "request-onvif", WorkerID: "worker-1", WorkerInstanceID: "instance-1",
		DeviceID: "garage", Protocol: media.ProtocolONVIF, Purpose: media.PurposePlayback, Attempt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Endpoint.Port != 80 || response.Endpoint.Path != "profile-main" || response.AuthType != media.AuthTypeDigest {
		t.Fatalf("ONVIF authorization = %#v", response)
	}
	var secret map[string]string
	if err := json.Unmarshal(response.SecretMaterial, &secret); err != nil ||
		secret["username"] != "viewer" || secret["password"] != "secret" {
		t.Fatalf("ONVIF secret material = %#v, %v", secret, err)
	}
}

func TestXiaomiCameraUsesIndependentEncryptedSessionConfig(t *testing.T) {
	provider := newTestProvider(t, Config{Cameras: []Entry{{
		ID: "xiaomi-camera", Name: "Xiaomi Camera", Driver: "xiaomi-miss", Profiles: []media.MediaProfile{testProfile()},
		Xiaomi: &XiaomiConfig{
			Region: "cn", UserID: "123", Ssecurity: "security", ServiceToken: "service",
			PassToken: "pass", DID: "987", Model: "chuangmi.camera.079ac1", LocalIP: "192.0.2.20",
		},
	}}})
	sources, err := provider.DiscoverMediaSources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("DiscoverMediaSources() = %#v, %v", sources, err)
	}
	if sources[0].ProviderID != "camera-main" || sources[0].ProviderDeviceID != "xiaomi-camera" || sources[0].Protocol != media.ProtocolXiaomiMISS {
		t.Fatalf("Xiaomi source ownership = %#v", sources[0])
	}
	if string(sources[0].SourceConfig) == "" || string(sources[0].SourceConfig) == "pass" {
		t.Fatalf("unexpected source config %s", sources[0].SourceConfig)
	}
	response, err := provider.AcquireMediaAuthorization(context.Background(), media.AuthorizationRequest{
		SchemaVersion: media.SchemaVersion, RequestID: "request-1", WorkerID: "worker-1", WorkerInstanceID: "instance-1",
		DeviceID: "xiaomi-camera", Protocol: media.ProtocolXiaomiMISS, Purpose: media.PurposePlayback, Attempt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Endpoint.Host != "192.0.2.20" || string(response.SecretMaterial) == "" {
		t.Fatalf("Xiaomi authorization = %#v", response)
	}
}

func TestXiaomiCameraCachesSignatureForWorkerKeyUntilProviderRestart(t *testing.T) {
	config := Config{Cameras: []Entry{{
		ID: "xiaomi-camera", Name: "Xiaomi Camera", Driver: "xiaomi-miss", Profiles: []media.MediaProfile{testProfile()},
		Xiaomi: &XiaomiConfig{
			Region: "cn", UserID: "123", Ssecurity: "security", ServiceToken: "service",
			PassToken: "pass", DID: "987", Model: "chuangmi.camera.079ac1", LocalIP: "192.0.2.20",
		},
	}}}
	cloudCalls := 0
	authorize := func(context.Context, xiaomi.CloudConfig, string, string) (xiaomi.MISSAuthorizationResult, error) {
		cloudCalls++
		return xiaomi.MISSAuthorizationResult{
			DevicePublic: strings.Repeat("c", 64), Sign: "signature-canary", Vendor: "cs2",
		}, nil
	}
	provider := newTestProvider(t, config)
	provider.xiaomiAuthorizeFn = authorize
	request := media.AuthorizationRequest{
		SchemaVersion: media.SchemaVersion, RequestID: "request-cache-1", WorkerID: "worker-1", WorkerInstanceID: "instance-1",
		DeviceID: "xiaomi-camera", Protocol: media.ProtocolXiaomiMISS, Purpose: media.PurposePlayback, Attempt: 1,
		ClientMaterial: json.RawMessage(`{"clientPublic":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`),
	}
	first, err := provider.AcquireMediaAuthorization(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.RequestID = "request-cache-2"
	second, err := provider.AcquireMediaAuthorization(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if cloudCalls != 1 || first.LeaseID == second.LeaseID {
		t.Fatalf("cloud calls=%d lease1=%q lease2=%q", cloudCalls, first.LeaseID, second.LeaseID)
	}
	for _, response := range []*media.AuthorizationResponse{first, second} {
		var secret map[string]string
		if err := json.Unmarshal(response.SecretMaterial, &secret); err != nil || secret["sign"] != "signature-canary" {
			t.Fatalf("cached signature response = %#v, %v", secret, err)
		}
		if _, exists := secret["kernelPassToken"]; exists {
			t.Fatalf("preauthorized lease leaked Xiaomi account token: %#v", secret)
		}
		if response.Reusable || response.MaxUses != 1 {
			t.Fatalf("cached signature changed single-use lease semantics: %#v", response)
		}
	}

	restarted := newTestProvider(t, config)
	restarted.xiaomiAuthorizeFn = authorize
	request.RequestID = "request-cache-3"
	if _, err := restarted.AcquireMediaAuthorization(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if cloudCalls != 2 {
		t.Fatalf("provider restart did not reacquire signature: cloud calls=%d", cloudCalls)
	}
}

func TestXiaomiCameraResolvesReferencedAccountCredentialsAtAuthorizationBoundary(t *testing.T) {
	raw, err := json.Marshal(Config{Cameras: []Entry{{
		ID: "xiaomi-referenced", Name: "Referenced Xiaomi Camera", Driver: "xiaomi-miss", Profiles: []media.MediaProfile{testProfile()},
		Xiaomi: &XiaomiConfig{
			CredentialProviderRef: "xiaomi-cloud", DID: "987", Model: "chuangmi.camera.079ac1", LocalIP: "192.0.2.21",
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	resolved := ""
	provider, err := NewProviderFromConfigWithXiaomiCredentialResolver(providerconfig.Config{
		ID: "camera-main", Type: ProviderType, Name: "Cameras", Enabled: true, Config: raw,
	}, func(id string) (xiaomi.CloudConfig, error) {
		resolved = id
		return xiaomi.CloudConfig{
			Region: "cn", UserID: "account-user", Ssecurity: "security",
			ServiceToken: "service", PassToken: "referenced-pass-token",
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.AcquireMediaAuthorization(context.Background(), media.AuthorizationRequest{
		SchemaVersion: media.SchemaVersion, RequestID: "request-ref", WorkerID: "worker-1", WorkerInstanceID: "instance-1",
		DeviceID: "xiaomi-referenced", Protocol: media.ProtocolXiaomiMISS, Purpose: media.PurposePlayback, Attempt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var secret struct {
		KernelPassToken string `json:"kernelPassToken"`
	}
	if err := json.Unmarshal(response.SecretMaterial, &secret); err != nil {
		t.Fatal(err)
	}
	if resolved != "xiaomi-cloud" || secret.KernelPassToken != "referenced-pass-token" {
		t.Fatalf("resolved provider = %q, secret = %#v", resolved, secret)
	}
	encoded, _ := json.Marshal(provider.config)
	if string(encoded) == "" || strings.Contains(string(encoded), "referenced-pass-token") {
		t.Fatalf("resolved secret was persisted in Camera Provider config: %s", encoded)
	}
}

func TestCameraProviderRejectsMixedDriverConfig(t *testing.T) {
	raw, _ := json.Marshal(Config{Cameras: []Entry{{
		ID: "mixed", Name: "Mixed", Driver: "rtsp", Profiles: []media.MediaProfile{testProfile()},
		RTSP:   &RTSPConfig{Host: "192.0.2.10", Path: "/live"},
		Xiaomi: &XiaomiConfig{},
	}}})
	if _, err := NewProviderFromConfig(providerconfig.Config{ID: "camera-main", Type: ProviderType, Name: "Cameras", Config: raw}); err == nil {
		t.Fatal("mixed driver config was accepted")
	}
}

func TestCameraProviderRejectsUnsupportedFourthDriver(t *testing.T) {
	raw, _ := json.Marshal(Config{Cameras: []Entry{{
		ID: "unsupported", Name: "Unsupported", Driver: "tapo", Profiles: []media.MediaProfile{testProfile()},
	}}})
	if _, err := NewProviderFromConfig(providerconfig.Config{ID: "camera-main", Type: ProviderType, Name: "Cameras", Config: raw}); err == nil {
		t.Fatal("unsupported camera driver was accepted")
	}
}

func TestCameraProviderRejectsUnsupportedConnectionMode(t *testing.T) {
	raw, _ := json.Marshal(Config{Cameras: []Entry{{
		ID: "camera", Name: "Camera", Driver: "rtsp", ConnectionMode: "aggressive",
		Profiles: []media.MediaProfile{testProfile()},
		RTSP:     &RTSPConfig{Host: "192.0.2.10", Path: "/live"},
	}}})
	if _, err := NewProviderFromConfig(providerconfig.Config{ID: "camera-main", Type: ProviderType, Name: "Cameras", Config: raw}); err == nil {
		t.Fatal("unsupported camera connection mode was accepted")
	}
}
