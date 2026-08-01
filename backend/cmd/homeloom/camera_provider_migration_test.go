package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/media"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/persistence/gormstore"
	cameraprovider "github.com/feranydev/homeloom/backend/internal/providers/camera"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
)

type cameraMigrationStoreStub struct {
	saved   []providerconfig.Config
	sources []gormstore.MediaSource
	saveErr error
}

func (s *cameraMigrationStoreStub) SaveProvidersAtomically(_ context.Context, items ...providerconfig.Config) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, items...)
	return nil
}
func (s *cameraMigrationStoreStub) ListMediaSources(context.Context) ([]gormstore.MediaSource, error) {
	return append([]gormstore.MediaSource(nil), s.sources...), nil
}

func TestMigrateXiaomiCameraProviderSeparatesCameraFromAccount(t *testing.T) {
	profile := media.MediaProfile{
		SchemaVersion: media.SchemaVersion, ID: "main", Name: "Main", Width: 1920, Height: 1080,
		FPS: 25, VideoCodec: media.VideoCodecH265, AudioCodec: media.AudioCodecOpus, Bitrate: 2_000_000,
	}
	cloud := xiaomi.CloudConfig{
		Region: "cn", UserID: "42", Ssecurity: "security", ServiceToken: "service", PassToken: "pass",
		Devices: []xiaomi.DeviceConfig{
			{DID: "camera-did", ID: "camera-1", Name: "Camera", Type: device.TypeCamera, Model: "chuangmi.camera.079ac1", Media: &xiaomi.CameraMediaConfig{Protocol: media.ProtocolXiaomiMISS, Host: "192.0.2.20", AuthType: media.AuthTypeVendor, Subtype: "hd", Channel: 1, Profiles: []media.MediaProfile{profile}}},
			{DID: "light-did", ID: "light-1", Name: "Light", Type: device.TypeLightbulb},
		},
	}
	raw, _ := json.Marshal(cloud)
	store := &cameraMigrationStoreStub{}
	result, err := migrateXiaomiCameraProviders(context.Background(), []providerconfig.Config{{
		ID: "xiaomi-cloud", Type: xiaomi.XiaomiMIoTCloudProviderType, Name: "Xiaomi", Enabled: true, Config: raw,
	}}, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || len(store.saved) != 2 {
		t.Fatalf("migration result=%#v saved=%#v", result, store.saved)
	}
	var migratedCloud xiaomi.CloudConfig
	if err := json.Unmarshal(result[0].Config, &migratedCloud); err != nil {
		t.Fatal(err)
	}
	if len(migratedCloud.Devices) != 1 || migratedCloud.Devices[0].ID != "light-1" {
		t.Fatalf("remaining Xiaomi devices = %#v", migratedCloud.Devices)
	}
	if result[1].ID != "camera-xiaomi-cloud" || result[1].Type != cameraprovider.ProviderType {
		t.Fatalf("camera provider = %#v", result[1])
	}
	var cameras cameraprovider.Config
	if err := json.Unmarshal(result[1].Config, &cameras); err != nil {
		t.Fatal(err)
	}
	if len(cameras.Cameras) != 1 || cameras.Cameras[0].ID != "camera-1" || cameras.Cameras[0].Xiaomi.PassToken != "pass" {
		t.Fatalf("migrated cameras = %#v", cameras.Cameras)
	}
}

func TestMigrateXiaomiCameraProviderIsIdempotent(t *testing.T) {
	raw, _ := json.Marshal(xiaomi.CloudConfig{Devices: []xiaomi.DeviceConfig{}})
	store := &cameraMigrationStoreStub{}
	configs := []providerconfig.Config{
		{ID: "xiaomi-cloud", Type: xiaomi.XiaomiMIoTCloudProviderType, Name: "Xiaomi", Config: raw},
		{ID: "camera-xiaomi-cloud", Type: cameraprovider.ProviderType, Name: "Cameras", Config: json.RawMessage(`{"cameras":[]}`)},
	}
	result, err := migrateXiaomiCameraProviders(context.Background(), configs, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != len(configs) || len(store.saved) != 0 {
		t.Fatalf("idempotent migration result=%#v saved=%#v", result, store.saved)
	}
}

func TestMigrateXiaomiCameraProviderRepairsInterruptedMigration(t *testing.T) {
	profile := media.MediaProfile{
		SchemaVersion: media.SchemaVersion, ID: "main", Name: "Main", Width: 1920, Height: 1080,
		FPS: 25, VideoCodec: media.VideoCodecH264, AudioCodec: media.AudioCodecAAC,
	}
	camera := xiaomi.DeviceConfig{
		DID: "camera-did", ID: "camera-1", Name: "Camera", Type: device.TypeCamera, Model: "chuangmi.camera.079ac1",
		Media: &xiaomi.CameraMediaConfig{
			Protocol: media.ProtocolXiaomiMISS, Host: "192.0.2.20",
			AuthType: media.AuthTypeVendor, Subtype: "hd", Channel: 1, Profiles: []media.MediaProfile{profile},
		},
	}
	cloudRaw, _ := json.Marshal(xiaomi.CloudConfig{
		Region: "cn", Username: "user", Password: "password", UserID: "42", PassToken: "pass-token",
		Devices: []xiaomi.DeviceConfig{camera, {DID: "light-did", ID: "light-1", Type: device.TypeLightbulb}},
	})
	existingRaw, _ := json.Marshal(cameraprovider.Config{Cameras: []cameraprovider.Entry{{
		ID: "camera-1", Name: "Camera", Driver: "xiaomi-miss",
		Xiaomi: &cameraprovider.XiaomiConfig{
			Region: "cn", Username: "user", Password: "password",
			UserID: "42", PassToken: "pass-token",
			DID: "camera-did", Model: "chuangmi.camera.079ac1",
			LocalIP: "192.0.2.20", Subtype: "hd", Channel: 1,
		},
		Profiles: []media.MediaProfile{profile},
	}}})
	store := &cameraMigrationStoreStub{}
	result, err := migrateXiaomiCameraProviders(context.Background(), []providerconfig.Config{
		{ID: "xiaomi-cloud", Type: xiaomi.XiaomiMIoTCloudProviderType, Name: "Xiaomi", Config: cloudRaw},
		{ID: "camera-xiaomi-cloud", Type: cameraprovider.ProviderType, Name: "Cameras", Config: existingRaw},
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || len(store.saved) != 2 {
		t.Fatalf("repair result=%#v saved=%#v", result, store.saved)
	}
	var repairedCloud xiaomi.CloudConfig
	if err := json.Unmarshal(result[0].Config, &repairedCloud); err != nil {
		t.Fatal(err)
	}
	if len(repairedCloud.Devices) != 1 || repairedCloud.Devices[0].ID != "light-1" {
		t.Fatalf("remaining Xiaomi devices = %#v", repairedCloud.Devices)
	}
	var repairedCamera cameraprovider.Config
	if err := json.Unmarshal(result[1].Config, &repairedCamera); err != nil {
		t.Fatal(err)
	}
	if len(repairedCamera.Cameras) != 1 || repairedCamera.Cameras[0].ID != "camera-1" {
		t.Fatalf("camera provider was duplicated or lost: %#v", repairedCamera.Cameras)
	}
}

func TestMigrateXiaomiCameraProviderAtomicFailureLeavesResultUnchanged(t *testing.T) {
	cloudRaw, _ := json.Marshal(xiaomi.CloudConfig{Devices: []xiaomi.DeviceConfig{{
		DID: "camera-did", ID: "camera-1", Name: "Camera", Type: device.TypeCamera,
		Media: &xiaomi.CameraMediaConfig{
			Protocol: media.ProtocolRTSP, Host: "192.0.2.20", Port: 554, Path: "/live",
		},
	}}})
	configs := []providerconfig.Config{{
		ID: "xiaomi-cloud", Type: xiaomi.XiaomiMIoTCloudProviderType, Name: "Xiaomi", Config: cloudRaw,
	}}
	store := &cameraMigrationStoreStub{saveErr: errors.New("commit failed")}
	result, err := migrateXiaomiCameraProviders(context.Background(), configs, store)
	if err == nil {
		t.Fatal("migration accepted a failed atomic write")
	}
	if result != nil {
		t.Fatalf("failed migration returned a partial result: %#v", result)
	}
	if len(store.saved) != 0 {
		t.Fatalf("failed atomic write persisted partial providers: %#v", store.saved)
	}
	var original xiaomi.CloudConfig
	if err := json.Unmarshal(configs[0].Config, &original); err != nil {
		t.Fatal(err)
	}
	if len(original.Devices) != 1 || original.Devices[0].ID != "camera-1" {
		t.Fatalf("input configs were mutated after failure: %#v", original.Devices)
	}
}

func TestMigrateXiaomiCameraProviderRecoversOrphanedMediaSource(t *testing.T) {
	cloudRaw, _ := json.Marshal(xiaomi.CloudConfig{
		Region: "cn", UserID: "42", Ssecurity: "security", ServiceToken: "service", PassToken: "pass",
	})
	profiles, _ := json.Marshal([]media.MediaProfile{{
		SchemaVersion: media.SchemaVersion, ID: "main", Width: 1920, Height: 1080, FPS: 25,
		VideoCodec: media.VideoCodecH264, AudioCodec: media.AudioCodecAAC,
	}})
	store := &cameraMigrationStoreStub{sources: []gormstore.MediaSource{{
		DeviceID: "xiaomi-camera", ProviderID: "xiaomi-cloud", ProviderDeviceID: "xiaomi-camera",
		Protocol: string(media.ProtocolXiaomiMISS), ProfilesJSON: profiles,
		SourceConfigJSON: []byte(`{"did":"1178028045","model":"chuangmi.camera.079ac1","localIp":"192.0.2.20","subtype":"hd","channel":1}`),
		Enabled:          true,
	}}}
	result, err := migrateXiaomiCameraProviders(context.Background(), []providerconfig.Config{{
		ID: "xiaomi-cloud", Type: xiaomi.XiaomiMIoTCloudProviderType, Name: "Xiaomi", Enabled: true, Config: cloudRaw,
	}}, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[1].ID != "camera-xiaomi-cloud" {
		t.Fatalf("recovered providers = %#v", result)
	}
	var recovered cameraprovider.Config
	if err := json.Unmarshal(result[1].Config, &recovered); err != nil {
		t.Fatal(err)
	}
	if len(recovered.Cameras) != 1 || recovered.Cameras[0].Xiaomi == nil ||
		recovered.Cameras[0].Xiaomi.DID != "1178028045" ||
		recovered.Cameras[0].Xiaomi.PassToken != "pass" {
		t.Fatalf("recovered camera = %#v", recovered.Cameras)
	}
}
