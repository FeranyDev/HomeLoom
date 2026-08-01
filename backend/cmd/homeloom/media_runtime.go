package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/application"
	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/persistence/gormstore"
	cameraprovider "github.com/feranydev/homeloom/backend/internal/providers/camera"
)

type mediaReplayStore interface {
	ListMediaStreams(context.Context, string) ([]gormstore.MediaStream, error)
	GetMediaConfigVersion(context.Context) (gormstore.MediaConfigVersion, error)
}

type mediaReplayProvider struct {
	store mediaReplayStore
}

type mediaSourceDiscoverer interface {
	DiscoverMediaSources(context.Context) ([]domainmedia.MediaSourceDescriptor, error)
}

type mediaCatalogStore interface {
	SaveMediaSource(context.Context, gormstore.MediaSource) error
	ListMediaSources(context.Context) ([]gormstore.MediaSource, error)
	DeleteMediaSource(context.Context, string) error
	ListMediaStreams(context.Context, string) ([]gormstore.MediaStream, error)
	SaveMediaStreamVersioned(context.Context, gormstore.MediaStream) (gormstore.MediaConfigVersion, error)
	DeleteMediaStreamVersioned(context.Context, string) (gormstore.MediaConfigVersion, error)
}

type gormMediaStreamStore struct {
	store *gormstore.Store
}

func (s gormMediaStreamStore) ListMediaStreams(ctx context.Context) ([]domainmedia.StreamSpec, error) {
	rows, err := s.store.ListMediaStreams(ctx, "")
	if err != nil {
		return nil, err
	}
	result := make([]domainmedia.StreamSpec, 0, len(rows))
	for _, row := range rows {
		if row.Enabled {
			result = append(result, mediaStreamSpecFromRow(row))
		}
	}
	return result, nil
}

func (s gormMediaStreamStore) SaveMediaStream(ctx context.Context, item domainmedia.StreamSpec) (application.MediaConfigVersion, error) {
	version, err := s.store.SaveMediaStreamVersioned(ctx, mediaStreamRowFromSpec(item))
	return application.MediaConfigVersion{Generation: version.Generation, Revision: version.Revision}, err
}

func (s gormMediaStreamStore) DeleteMediaStream(ctx context.Context, id string) (domainmedia.StreamSpec, application.MediaConfigVersion, error) {
	row, found, err := s.store.GetMediaStream(ctx, id)
	if err != nil {
		return domainmedia.StreamSpec{}, application.MediaConfigVersion{}, err
	}
	if !found {
		return domainmedia.StreamSpec{}, application.MediaConfigVersion{}, application.ErrMediaStreamNotFound
	}
	version, err := s.store.DeleteMediaStreamVersioned(ctx, id)
	return mediaStreamSpecFromRow(row), application.MediaConfigVersion{
		Generation: version.Generation, Revision: version.Revision,
	}, err
}

func (s gormMediaStreamStore) MediaStreamReplay(ctx context.Context) (domainmedia.StreamReplay, error) {
	value, err := (mediaReplayProvider{store: s.store}).MediaReplay(ctx)
	if err != nil {
		return domainmedia.StreamReplay{}, err
	}
	return value.(domainmedia.StreamReplay), nil
}

func mediaStreamRowFromSpec(item domainmedia.StreamSpec) gormstore.MediaStream {
	return gormstore.MediaStream{
		ID: item.ID, DeviceID: item.DeviceID, Protocol: string(item.Protocol),
		CredentialRef: item.CredentialRef, Profile: item.Profile, Mode: string(item.Mode), AudioEnabled: item.Audio,
		TalkbackEnabled: item.Talkback, OptionsJSON: append([]byte(nil), item.Options...),
		Enabled: true,
	}
}

func mediaStreamSpecFromRow(row gormstore.MediaStream) domainmedia.StreamSpec {
	return domainmedia.StreamSpec{
		SchemaVersion: domainmedia.SchemaVersion,
		ID:            row.ID, DeviceID: row.DeviceID, Protocol: domainmedia.Protocol(row.Protocol),
		CredentialRef: row.CredentialRef, Profile: row.Profile, Mode: domainmedia.StreamMode(row.Mode),
		Audio: row.AudioEnabled, Talkback: row.TalkbackEnabled,
		Options: append([]byte(nil), row.OptionsJSON...),
	}
}

func (p mediaReplayProvider) MediaReplay(ctx context.Context) (any, error) {
	rows, err := p.store.ListMediaStreams(ctx, "")
	if err != nil {
		return nil, err
	}
	version, err := p.store.GetMediaConfigVersion(ctx)
	if err != nil {
		return nil, err
	}
	streams := make([]domainmedia.StreamSpec, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		streams = append(streams, mediaStreamSpecFromRow(row))
	}
	replay := domainmedia.StreamReplay{
		SchemaVersion: domainmedia.SchemaVersion,
		Generation:    version.Generation, Revision: version.Revision, Streams: streams,
	}
	if err := replay.Validate(); err != nil {
		return nil, fmt.Errorf("build media replay: %w", err)
	}
	return replay, nil
}

// reconcileDiscoveredMedia persists Camera Provider-owned source descriptors
// and creates one stable preview stream per newly discovered camera. Preview
// streams never publish to an external ecosystem; Camera Targets opt in by
// updating publisher desired state separately.
func reconcileDiscoveredMedia(ctx context.Context, discoverer mediaSourceDiscoverer, store mediaCatalogStore, providers []providerconfig.Config) error {
	if discoverer == nil || store == nil {
		return nil
	}
	sources, err := discoverer.DiscoverMediaSources(ctx)
	if err != nil {
		return err
	}
	discovered := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if !source.Enabled {
			continue
		}
		discovered[source.DeviceID] = struct{}{}
		profiles, err := json.Marshal(source.Profiles)
		if err != nil {
			return fmt.Errorf("encode media source %q profiles: %w", source.DeviceID, err)
		}
		if err := store.SaveMediaSource(ctx, gormstore.MediaSource{
			DeviceID: source.DeviceID, ProviderID: source.ProviderID,
			ProviderDeviceID: source.ProviderDeviceID, Protocol: string(source.Protocol),
			CredentialRef: source.CredentialRef, ProfilesJSON: profiles,
			SourceConfigJSON: append([]byte(nil), source.SourceConfig...),
			Revision:         source.Revision, Enabled: source.Enabled,
		}); err != nil {
			return fmt.Errorf("save media source %q: %w", source.DeviceID, err)
		}
		existing, err := store.ListMediaStreams(ctx, source.DeviceID)
		if err != nil {
			return fmt.Errorf("list media streams for %q: %w", source.DeviceID, err)
		}
		desiredMode := source.ConnectionMode
		if desiredMode == "" {
			desiredMode = domainmedia.StreamOnDemand
		}
		for index := range existing {
			changed := false
			if isLegacyAutomaticPublisher(existing[index].OptionsJSON) {
				// This exact marker was emitted only by the pre-Target Xiaomi
				// vertical slice. It is safe to migrate to preview-only desired
				// state now that camera publication is explicit.
				existing[index].OptionsJSON = json.RawMessage(`{"publisher":"none"}`)
				changed = true
			}
			if existing[index].Mode != string(desiredMode) {
				existing[index].Mode = string(desiredMode)
				changed = true
			}
			if changed {
				if _, err := store.SaveMediaStreamVersioned(ctx, existing[index]); err != nil {
					return fmt.Errorf("update camera stream policy %q: %w", existing[index].ID, err)
				}
			}
		}
		if len(existing) > 0 || len(source.Profiles) == 0 {
			continue
		}
		profile := source.Profiles[0]
		options := json.RawMessage(`{"publisher":"none"}`)
		stream := domainmedia.StreamSpec{
			SchemaVersion: domainmedia.SchemaVersion,
			ID:            defaultCameraStreamID(source.DeviceID),
			DeviceID:      source.DeviceID,
			Protocol:      source.Protocol,
			CredentialRef: source.CredentialRef,
			Profile:       profile.ID,
			Mode:          desiredMode,
			Audio:         profile.AudioCodec != domainmedia.AudioCodecNone,
			Talkback:      false,
			Options:       options,
		}
		if err := stream.Validate(); err != nil {
			return fmt.Errorf("build default stream for %q: %w", source.DeviceID, err)
		}
		if _, err := store.SaveMediaStreamVersioned(ctx, mediaStreamRowFromSpec(stream)); err != nil {
			return fmt.Errorf("save default stream for %q: %w", source.DeviceID, err)
		}
	}
	knownProviders := make(map[string]struct{}, len(providers))
	cameraProviders := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		knownProviders[provider.ID] = struct{}{}
		if provider.Type == cameraprovider.ProviderType {
			cameraProviders[provider.ID] = struct{}{}
		}
	}
	persisted, err := store.ListMediaSources(ctx)
	if err != nil {
		return fmt.Errorf("list persisted media sources: %w", err)
	}
	for _, source := range persisted {
		if _, exists := discovered[source.DeviceID]; exists {
			continue
		}
		_, providerExists := knownProviders[source.ProviderID]
		_, cameraOwned := cameraProviders[source.ProviderID]
		if providerExists && !cameraOwned {
			continue
		}
		streams, err := store.ListMediaStreams(ctx, source.DeviceID)
		if err != nil {
			return fmt.Errorf("list stale media streams for %q: %w", source.DeviceID, err)
		}
		for _, stream := range streams {
			if _, err := store.DeleteMediaStreamVersioned(ctx, stream.ID); err != nil {
				return fmt.Errorf("delete stale media stream %q: %w", stream.ID, err)
			}
		}
		if err := store.DeleteMediaSource(ctx, source.DeviceID); err != nil {
			return fmt.Errorf("delete stale media source %q: %w", source.DeviceID, err)
		}
	}
	return nil
}

// migrateLegacyCameraPublisherRuntimeDir moves HomeKit publisher state out of
// the historical cache path when operators adopt the durable default. Existing
// pairings and device identities are preserved so Apple Home does not require
// reconfiguration after an upgrade.
func migrateLegacyCameraPublisherRuntimeDir(runtimeDir string) (int, error) {
	destination, err := filepath.Abs(strings.TrimSpace(runtimeDir))
	if err != nil {
		return 0, fmt.Errorf("resolve media publisher runtime directory: %w", err)
	}
	legacyCandidates := []string{
		".cache/media-worker/publishers",
		filepath.Join("backend", ".cache", "media-worker", "publishers"),
	}
	moved := 0
	for _, candidate := range legacyCandidates {
		source, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if source == destination {
			continue
		}
		entries, err := os.ReadDir(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return moved, fmt.Errorf("inspect legacy media publisher runtime %q: %w", source, err)
		}
		if err := os.MkdirAll(destination, 0o700); err != nil {
			return moved, fmt.Errorf("create durable media publisher runtime directory: %w", err)
		}
		if err := os.Chmod(destination, 0o700); err != nil {
			return moved, fmt.Errorf("secure durable media publisher runtime directory: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			from := filepath.Join(source, entry.Name())
			to := filepath.Join(destination, entry.Name())
			if filepath.Dir(from) != source || filepath.Dir(to) != destination {
				return moved, fmt.Errorf("unsafe legacy publisher migration path %q -> %q", from, to)
			}
			if _, err := os.Lstat(to); err == nil {
				// Destination already owns this stream identity; leave both alone.
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return moved, fmt.Errorf("inspect durable publisher directory %q: %w", to, err)
			}
			if err := os.Rename(from, to); err != nil {
				return moved, fmt.Errorf("migrate publisher directory %q: %w", entry.Name(), err)
			}
			moved++
		}
	}
	return moved, nil
}

// pruneOrphanedCameraPublisherDirectories removes publisher state only when its
// stream no longer exists in the authoritative media catalog. A Camera Target
// can be disabled while its stream remains available in Device Center, so
// publisher directories belonging to any persisted stream are retained.
func pruneOrphanedCameraPublisherDirectories(ctx context.Context, runtimeDir string, store mediaReplayStore) (int, error) {
	if store == nil || strings.TrimSpace(runtimeDir) == "" {
		return 0, nil
	}
	root, err := filepath.Abs(runtimeDir)
	if err != nil {
		return 0, fmt.Errorf("resolve media publisher runtime directory: %w", err)
	}
	if root == filepath.Dir(root) {
		return 0, errors.New("refusing to prune filesystem root as media publisher runtime directory")
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("list media publisher runtime directory: %w", err)
	}
	streams, err := store.ListMediaStreams(ctx, "")
	if err != nil {
		return 0, fmt.Errorf("list authoritative media streams before publisher cleanup: %w", err)
	}
	retained := make(map[string]struct{}, len(streams))
	for _, stream := range streams {
		retained[stream.ID] = struct{}{}
	}
	removed := 0
	for _, entry := range entries {
		// Publisher stream IDs are path-safe and Camera Provider default IDs
		// use this prefix. Skip files, symlinks, and unrelated runtime content.
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "camera-") {
			continue
		}
		if _, exists := retained[entry.Name()]; exists {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		if filepath.Dir(directory) != root {
			return removed, fmt.Errorf("unsafe orphaned publisher directory %q", directory)
		}
		if err := os.RemoveAll(directory); err != nil {
			return removed, fmt.Errorf("remove orphaned publisher directory %q: %w", entry.Name(), err)
		}
		removed++
	}
	return removed, nil
}

func isLegacyAutomaticPublisher(raw []byte) bool {
	var options struct {
		Publisher   string `json:"publisher"`
		Independent bool   `json:"independent"`
	}
	if err := json.Unmarshal(raw, &options); err != nil {
		return false
	}
	return options.Publisher == "apple-home" && options.Independent
}

func defaultCameraStreamID(deviceID string) string {
	sum := sha256.Sum256([]byte(deviceID))
	return "camera-" + hex.EncodeToString(sum[:8])
}
