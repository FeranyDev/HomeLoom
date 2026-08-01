package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/persistence/gormstore"
)

type mediaSourceDiscovererStub struct {
	sources []domainmedia.MediaSourceDescriptor
}

func (s mediaSourceDiscovererStub) DiscoverMediaSources(context.Context) ([]domainmedia.MediaSourceDescriptor, error) {
	return append([]domainmedia.MediaSourceDescriptor(nil), s.sources...), nil
}

type mediaCatalogStoreStub struct {
	sources map[string]gormstore.MediaSource
	streams map[string][]gormstore.MediaStream
	version gormstore.MediaConfigVersion
}

func newMediaCatalogStoreStub() *mediaCatalogStoreStub {
	return &mediaCatalogStoreStub{
		sources: make(map[string]gormstore.MediaSource),
		streams: make(map[string][]gormstore.MediaStream),
		version: gormstore.MediaConfigVersion{Generation: 1, Revision: 1},
	}
}

func (s *mediaCatalogStoreStub) SaveMediaSource(_ context.Context, item gormstore.MediaSource) error {
	s.sources[item.DeviceID] = item
	return nil
}

func (s *mediaCatalogStoreStub) ListMediaSources(context.Context) ([]gormstore.MediaSource, error) {
	result := make([]gormstore.MediaSource, 0, len(s.sources))
	for _, source := range s.sources {
		result = append(result, source)
	}
	return result, nil
}

func (s *mediaCatalogStoreStub) DeleteMediaSource(_ context.Context, deviceID string) error {
	delete(s.sources, deviceID)
	return nil
}

func (s *mediaCatalogStoreStub) ListMediaStreams(_ context.Context, deviceID string) ([]gormstore.MediaStream, error) {
	return append([]gormstore.MediaStream(nil), s.streams[deviceID]...), nil
}

func (s *mediaCatalogStoreStub) SaveMediaStreamVersioned(_ context.Context, item gormstore.MediaStream) (gormstore.MediaConfigVersion, error) {
	s.version.Revision++
	for index := range s.streams[item.DeviceID] {
		if s.streams[item.DeviceID][index].ID == item.ID {
			s.streams[item.DeviceID][index] = item
			return s.version, nil
		}
	}
	s.streams[item.DeviceID] = append(s.streams[item.DeviceID], item)
	return s.version, nil
}

func (s *mediaCatalogStoreStub) DeleteMediaStreamVersioned(_ context.Context, streamID string) (gormstore.MediaConfigVersion, error) {
	s.version.Revision++
	for deviceID, streams := range s.streams {
		for index, stream := range streams {
			if stream.ID == streamID {
				s.streams[deviceID] = append(streams[:index], streams[index+1:]...)
				return s.version, nil
			}
		}
	}
	return s.version, nil
}

func cameraProviderConfigs() []providerconfig.Config {
	return []providerconfig.Config{{ID: "camera-main", Type: "camera", Enabled: true}}
}

type mediaReplayStoreStub struct {
	streams []gormstore.MediaStream
	version gormstore.MediaConfigVersion
}

func (s mediaReplayStoreStub) ListMediaStreams(context.Context, string) ([]gormstore.MediaStream, error) {
	return append([]gormstore.MediaStream(nil), s.streams...), nil
}

func (s mediaReplayStoreStub) GetMediaConfigVersion(context.Context) (gormstore.MediaConfigVersion, error) {
	if s.version.Generation == 0 {
		return gormstore.MediaConfigVersion{Generation: 1, Revision: 1}, nil
	}
	return s.version, nil
}

func TestMediaReplayProviderBuildsEnabledSnapshot(t *testing.T) {
	provider := mediaReplayProvider{store: mediaReplayStoreStub{
		version: gormstore.MediaConfigVersion{Generation: 4, Revision: 8},
		streams: []gormstore.MediaStream{
			{ID: "stream-a", DeviceID: "camera-a", Protocol: "rtsp", CredentialRef: "credential-a", Profile: "main", Mode: "on_demand", Revision: 8, Enabled: true, OptionsJSON: []byte(`{}`)},
			{ID: "stream-disabled", DeviceID: "camera-b", Protocol: "rtsp", Profile: "main", Mode: "preload", Revision: 7, Enabled: false, OptionsJSON: []byte(`{}`)},
		},
	}}
	value, err := provider.MediaReplay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	replay, ok := value.(domainmedia.StreamReplay)
	if !ok {
		t.Fatalf("replay type = %T", value)
	}
	if replay.Generation != 4 || replay.Revision != 8 || len(replay.Streams) != 1 || replay.Streams[0].ID != "stream-a" {
		t.Fatalf("replay = %#v", replay)
	}
	if replay.Streams[0].CredentialRef != "credential-a" {
		t.Fatalf("credential ref was not replayed: %#v", replay.Streams[0])
	}
}

func TestMediaReplayProviderDefaultsEmptyVersion(t *testing.T) {
	value, err := (mediaReplayProvider{store: mediaReplayStoreStub{}}).MediaReplay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	replay := value.(domainmedia.StreamReplay)
	if replay.Generation != 1 || replay.Revision != 1 || len(replay.Streams) != 0 {
		t.Fatalf("replay = %#v", replay)
	}
}

func TestPruneOrphanedCameraPublisherDirectoriesRetainsCatalogStreams(t *testing.T) {
	runtimeDir := t.TempDir()
	active := filepath.Join(runtimeDir, "camera-active")
	orphaned := filepath.Join(runtimeDir, "camera-orphaned")
	unrelated := filepath.Join(runtimeDir, "go2rtc")
	for _, directory := range []string{active, orphaned, unrelated} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(orphaned, "pairings.json"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := pruneOrphanedCameraPublisherDirectories(context.Background(), runtimeDir, mediaReplayStoreStub{
		streams: []gormstore.MediaStream{{ID: "camera-active", DeviceID: "camera-1", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed directories = %d", removed)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active publisher was removed: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated runtime directory was removed: %v", err)
	}
	if _, err := os.Stat(orphaned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphaned publisher still exists: %v", err)
	}
}

func TestReconcileDiscoveredMediaCreatesPreviewOnlyCameraStream(t *testing.T) {
	store := newMediaCatalogStoreStub()
	source := domainmedia.MediaSourceDescriptor{
		SchemaVersion: domainmedia.SchemaVersion,
		DeviceID:      "xiaomi-camera-1", ProviderID: "camera-main", ProviderDeviceID: "xiaomi-camera-1",
		Protocol: domainmedia.ProtocolXiaomiMISS,
		Profiles: []domainmedia.MediaProfile{{
			SchemaVersion: domainmedia.SchemaVersion,
			ID:            "main", Width: 1920, Height: 1080, FPS: 30,
			VideoCodec: domainmedia.VideoCodecH264, AudioCodec: domainmedia.AudioCodecAAC,
		}},
		SourceConfig: json.RawMessage(`{"did":"123456","model":"isa.camera.hlc7","region":"cn"}`),
		Revision:     1, Enabled: true,
	}
	err := reconcileDiscoveredMedia(context.Background(), mediaSourceDiscovererStub{sources: []domainmedia.MediaSourceDescriptor{source}}, store, cameraProviderConfigs())
	if err != nil {
		t.Fatal(err)
	}
	if store.sources[source.DeviceID].ProviderID != "camera-main" {
		t.Fatalf("persisted source = %#v", store.sources[source.DeviceID])
	}
	streams := store.streams[source.DeviceID]
	if len(streams) != 1 || streams[0].Protocol != string(domainmedia.ProtocolXiaomiMISS) ||
		streams[0].Mode != string(domainmedia.StreamOnDemand) ||
		string(streams[0].OptionsJSON) != `{"publisher":"none"}` {
		t.Fatalf("default preview-only stream = %#v", streams)
	}

	if err := reconcileDiscoveredMedia(context.Background(), mediaSourceDiscovererStub{sources: []domainmedia.MediaSourceDescriptor{source}}, store, cameraProviderConfigs()); err != nil {
		t.Fatal(err)
	}
	if len(store.streams[source.DeviceID]) != 1 {
		t.Fatalf("reconcile duplicated camera stream: %#v", store.streams[source.DeviceID])
	}
}

func TestReconcileDiscoveredMediaAppliesChangedConnectionModeToExistingStream(t *testing.T) {
	store := newMediaCatalogStoreStub()
	store.streams["camera-1"] = []gormstore.MediaStream{{
		ID: "camera-stream", DeviceID: "camera-1", Protocol: string(domainmedia.ProtocolRTSP),
		Profile: "main", Mode: string(domainmedia.StreamOnDemand), Enabled: true,
		OptionsJSON: json.RawMessage(`{"publisher":"none"}`),
	}}
	source := domainmedia.MediaSourceDescriptor{
		SchemaVersion: domainmedia.SchemaVersion, DeviceID: "camera-1", ProviderID: "camera-main",
		ProviderDeviceID: "camera-1", Protocol: domainmedia.ProtocolRTSP,
		ConnectionMode: domainmedia.StreamAlwaysOn,
		Profiles: []domainmedia.MediaProfile{{
			SchemaVersion: domainmedia.SchemaVersion, ID: "main", Width: 1920, Height: 1080, FPS: 25,
			VideoCodec: domainmedia.VideoCodecH264, AudioCodec: domainmedia.AudioCodecAAC,
		}},
		SourceConfig: json.RawMessage(`{"host":"192.0.2.10","port":554,"path":"/live"}`),
		Revision:     1, Enabled: true,
	}
	if err := reconcileDiscoveredMedia(context.Background(), mediaSourceDiscovererStub{sources: []domainmedia.MediaSourceDescriptor{source}}, store, cameraProviderConfigs()); err != nil {
		t.Fatal(err)
	}
	if got := store.streams["camera-1"][0].Mode; got != string(domainmedia.StreamAlwaysOn) {
		t.Fatalf("updated stream mode = %q", got)
	}
}

func TestDefaultCameraStreamIDIsStableAndSecretFree(t *testing.T) {
	first := defaultCameraStreamID("xiaomi-camera-1")
	if first != defaultCameraStreamID("xiaomi-camera-1") || first == defaultCameraStreamID("xiaomi-camera-2") {
		t.Fatalf("default stream ids are not stable and unique: %q", first)
	}
	if len(first) > 128 {
		t.Fatalf("default stream id too long: %q", first)
	}
}

func TestReconcileDiscoveredMediaMigratesLegacyAutomaticPublisher(t *testing.T) {
	store := newMediaCatalogStoreStub()
	store.streams["camera-1"] = []gormstore.MediaStream{{
		ID: "camera-legacy", DeviceID: "camera-1", Protocol: string(domainmedia.ProtocolXiaomiMISS),
		Profile: "main", Mode: string(domainmedia.StreamOnDemand), Enabled: true,
		OptionsJSON: json.RawMessage(`{"publisher":"apple-home","independent":true}`),
	}}
	source := domainmedia.MediaSourceDescriptor{
		SchemaVersion: domainmedia.SchemaVersion, DeviceID: "camera-1", ProviderID: "camera-main",
		ProviderDeviceID: "camera-1", Protocol: domainmedia.ProtocolXiaomiMISS,
		Profiles: []domainmedia.MediaProfile{{
			SchemaVersion: domainmedia.SchemaVersion, ID: "main", Width: 1920, Height: 1080, FPS: 25,
			VideoCodec: domainmedia.VideoCodecH264, AudioCodec: domainmedia.AudioCodecNone,
		}},
		SourceConfig: json.RawMessage(`{"did":"1","model":"camera","localIp":"192.0.2.10"}`),
		Revision:     1, Enabled: true,
	}
	if err := reconcileDiscoveredMedia(context.Background(), mediaSourceDiscovererStub{sources: []domainmedia.MediaSourceDescriptor{source}}, store, cameraProviderConfigs()); err != nil {
		t.Fatal(err)
	}
	if got := string(store.streams["camera-1"][0].OptionsJSON); got != `{"publisher":"none"}` {
		t.Fatalf("legacy publisher options = %s", got)
	}
}

func TestReconcileDiscoveredMediaMigratesFormattedLegacyPublisherJSON(t *testing.T) {
	source := domainmedia.MediaSourceDescriptor{
		SchemaVersion: domainmedia.SchemaVersion, DeviceID: "camera-formatted", ProviderID: "camera-main",
		ProviderDeviceID: "camera-formatted", Protocol: domainmedia.ProtocolXiaomiMISS,
		Profiles: []domainmedia.MediaProfile{{
			SchemaVersion: domainmedia.SchemaVersion, ID: "main", Width: 1920, Height: 1080, FPS: 25,
			VideoCodec: domainmedia.VideoCodecH264, AudioCodec: domainmedia.AudioCodecAAC,
		}},
		SourceConfig: json.RawMessage(`{"did":"1","model":"camera","localIp":"192.0.2.10"}`),
		Revision:     1, Enabled: true,
	}
	store := newMediaCatalogStoreStub()
	store.streams[source.DeviceID] = []gormstore.MediaStream{{
		ID: "legacy-camera", DeviceID: source.DeviceID, Protocol: string(source.Protocol),
		Profile: "main", Mode: string(domainmedia.StreamOnDemand), AudioEnabled: true, Enabled: true,
		OptionsJSON: json.RawMessage(`{"publisher": "apple-home", "independent": true}`),
	}}
	if err := reconcileDiscoveredMedia(context.Background(), mediaSourceDiscovererStub{sources: []domainmedia.MediaSourceDescriptor{source}}, store, cameraProviderConfigs()); err != nil {
		t.Fatal(err)
	}
	if got := string(store.streams[source.DeviceID][0].OptionsJSON); got != `{"publisher":"none"}` {
		t.Fatalf("formatted legacy publisher options = %s", got)
	}
}

func TestReconcileDiscoveredMediaDeletesRemovedCameraChild(t *testing.T) {
	store := newMediaCatalogStoreStub()
	store.sources["removed-camera"] = gormstore.MediaSource{
		DeviceID: "removed-camera", ProviderID: "camera-main", ProviderDeviceID: "removed-camera",
		Protocol: string(domainmedia.ProtocolRTSP), Revision: 1, Enabled: true,
	}
	store.streams["removed-camera"] = []gormstore.MediaStream{{
		ID: "removed-stream", DeviceID: "removed-camera", Protocol: string(domainmedia.ProtocolRTSP),
		Profile: "main", Mode: string(domainmedia.StreamOnDemand), Enabled: true, OptionsJSON: json.RawMessage(`{}`),
	}}
	if err := reconcileDiscoveredMedia(context.Background(), mediaSourceDiscovererStub{}, store, cameraProviderConfigs()); err != nil {
		t.Fatal(err)
	}
	if len(store.sources) != 0 || len(store.streams["removed-camera"]) != 0 {
		t.Fatalf("stale media state was retained: sources=%#v streams=%#v", store.sources, store.streams)
	}
}

func TestMigrateLegacyCameraPublisherRuntimeDirMovesPairings(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, ".cache", "media-worker", "publishers")
	durable := filepath.Join(root, "data", "media", "publishers")
	streamDir := filepath.Join(legacy, "camera-main")
	if err := os.MkdirAll(streamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(streamDir, "homekit-identity.json"), []byte(`{"schemaVersion":1,"pin":"111-22-333"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(streamDir, "go2rtc.yaml"), []byte("homekit:\n  camera-main:\n    pairings:\n      - client_id=controller\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWD) }()

	moved, err := migrateLegacyCameraPublisherRuntimeDir("./data/media/publishers")
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("moved = %d", moved)
	}
	if _, err := os.Stat(filepath.Join(durable, "camera-main", "go2rtc.yaml")); err != nil {
		t.Fatalf("durable pairing config missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "camera-main")); !os.IsNotExist(err) {
		t.Fatalf("legacy directory still present: %v", err)
	}
}
