package providermanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type mediaProviderStub struct {
	id          string
	device      device.Device
	source      domainmedia.MediaSourceDescriptor
	response    domainmedia.AuthorizationResponse
	discoverErr error
}

func (p *mediaProviderStub) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "camera", Name: p.id, Version: "1"}
}

func (p *mediaProviderStub) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true}
}

func (*mediaProviderStub) Initialize(context.Context) error { return nil }
func (*mediaProviderStub) Close(context.Context) error      { return nil }

func (p *mediaProviderStub) DiscoverDevices(context.Context) ([]device.Device, error) {
	return []device.Device{p.device.Clone()}, nil
}

func (p *mediaProviderStub) DiscoverMediaSources(context.Context) ([]domainmedia.MediaSourceDescriptor, error) {
	if p.discoverErr != nil {
		return nil, p.discoverErr
	}
	return []domainmedia.MediaSourceDescriptor{p.source}, nil
}

func (p *mediaProviderStub) AcquireMediaAuthorization(context.Context, domainmedia.AuthorizationRequest) (*domainmedia.AuthorizationResponse, error) {
	response := p.response
	return &response, nil
}

func newMediaProviderStub() *mediaProviderStub {
	camera := device.Device{
		SchemaVersion: device.SchemaVersion,
		ID:            "camera-1", ProviderID: "xiaomi-main", Name: "Camera", Type: device.TypeCamera,
		Availability: device.AvailabilityOnline, Online: true,
	}
	return &mediaProviderStub{
		id:     "xiaomi-main",
		device: camera,
		source: domainmedia.MediaSourceDescriptor{
			SchemaVersion: domainmedia.SchemaVersion,
			DeviceID:      camera.ID, ProviderID: "xiaomi-main", ProviderDeviceID: "123456",
			Protocol: domainmedia.ProtocolXiaomiMISS,
			Profiles: []domainmedia.MediaProfile{{
				SchemaVersion: domainmedia.SchemaVersion, ID: "main",
				Width: 1920, Height: 1080, FPS: 30,
				VideoCodec: domainmedia.VideoCodecH264, AudioCodec: domainmedia.AudioCodecAAC,
			}},
			SourceConfig: []byte(`{"model":"isa.camera.hlc7","region":"cn"}`),
			Revision:     1, Enabled: true,
		},
		response: domainmedia.AuthorizationResponse{
			SchemaVersion: domainmedia.SchemaVersion,
			LeaseID:       "lease-1", ExpiresAt: time.Now().Add(time.Minute),
			Endpoint: domainmedia.EndpointSpec{
				Protocol: domainmedia.ProtocolXiaomiMISS, Host: "192.168.1.20", Port: 0,
			},
			AuthType:       domainmedia.AuthTypeVendor,
			PublicMaterial: []byte(`{"did":"123456","model":"isa.camera.hlc7","region":"cn"}`),
			SecretMaterial: []byte(`{"passToken":"opaque"}`),
			MaxUses:        1,
		},
	}
}

func TestManagerAggregatesMediaSourcesAndRoutesAuthorization(t *testing.T) {
	provider := newMediaProviderStub()
	manager, err := New(provider)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	sources, err := manager.DiscoverMediaSources(context.Background())
	if err != nil || len(sources) != 1 || sources[0].DeviceID != "camera-1" {
		t.Fatalf("DiscoverMediaSources() = %#v, %v", sources, err)
	}
	request := domainmedia.AuthorizationRequest{
		SchemaVersion: domainmedia.SchemaVersion,
		RequestID:     "request-1", WorkerID: "worker-1", WorkerInstanceID: "instance-1",
		DeviceID: "camera-1", Protocol: domainmedia.ProtocolXiaomiMISS,
		Purpose: domainmedia.PurposePlayback, Attempt: 1,
	}
	response, err := manager.AcquireMediaAuthorization(context.Background(), request)
	if err != nil || response.LeaseID != "lease-1" {
		t.Fatalf("AcquireMediaAuthorization() = %#v, %v", response, err)
	}
}

func TestManagerRejectsDuplicateCameraMediaOwnership(t *testing.T) {
	first := newMediaProviderStub()
	second := newMediaProviderStub()
	second.id = "camera-secondary"
	second.device.ProviderID = second.id
	second.source.ProviderID = second.id
	manager, err := New(first, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DiscoverMediaSources(context.Background()); err == nil {
		t.Fatal("DiscoverMediaSources() accepted duplicate camera ownership")
	}
}

func TestManagerRejectsInvalidMediaSourceProviderIdentity(t *testing.T) {
	provider := newMediaProviderStub()
	provider.source.ProviderID = "other"
	manager, err := New(provider)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DiscoverMediaSources(context.Background()); err == nil {
		t.Fatal("DiscoverMediaSources() accepted a mismatched provider identity")
	}
}

func TestManagerMediaAuthorizationRequiresKnownRoute(t *testing.T) {
	manager, err := New(newMediaProviderStub())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = manager.AcquireMediaAuthorization(context.Background(), domainmedia.AuthorizationRequest{
		SchemaVersion: domainmedia.SchemaVersion,
		RequestID:     "request-1", WorkerID: "worker-1", WorkerInstanceID: "instance-1",
		DeviceID: "missing-camera", Protocol: domainmedia.ProtocolRTSP,
		Purpose: domainmedia.PurposePlayback, Attempt: 1,
	})
	if !errors.Is(err, providersdk.ErrDeviceNotFound) {
		t.Fatalf("AcquireMediaAuthorization() error = %v", err)
	}
}
