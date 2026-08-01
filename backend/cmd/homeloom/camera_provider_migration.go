package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/media"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/persistence/gormstore"
	cameraprovider "github.com/feranydev/homeloom/backend/internal/providers/camera"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
)

type cameraProviderMigrationStore interface {
	SaveProvidersAtomically(context.Context, ...providerconfig.Config) error
	ListMediaSources(context.Context) ([]gormstore.MediaSource, error)
}

// migrateXiaomiCameraProviders moves only configured camera devices out of
// Xiaomi MIoT Cloud. Account credentials are copied into the Camera Provider
// as an independent encrypted session; ordinary MIoT devices remain owned by
// the account Provider. Every account/camera-provider pair is updated in one
// transaction, and an existing Camera Provider is reconciled so migrations
// interrupted by older versions converge on the next startup.
func migrateXiaomiCameraProviders(ctx context.Context, configs []providerconfig.Config, store cameraProviderMigrationStore) ([]providerconfig.Config, error) {
	mediaSources, err := store.ListMediaSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list legacy Xiaomi media sources: %w", err)
	}
	result := append([]providerconfig.Config(nil), configs...)
	providerIndexes := make(map[string]int, len(configs))
	for index, item := range configs {
		providerIndexes[item.ID] = index
	}
	originalCount := len(result)
	for index := 0; index < originalCount; index++ {
		account := result[index]
		if account.Type != xiaomi.XiaomiMIoTCloudProviderType {
			continue
		}
		cameraProviderID := "camera-" + account.ID
		var cloud xiaomi.CloudConfig
		if err := json.Unmarshal(account.Config, &cloud); err != nil {
			return nil, fmt.Errorf("decode Xiaomi camera migration config %q: %w", account.ID, err)
		}
		cameraConfig := providerconfig.Config{
			ID: cameraProviderID, Type: cameraprovider.ProviderType,
			Name: account.Name + " · 摄像头", Enabled: account.Enabled,
		}
		entries := make([]cameraprovider.Entry, 0)
		_, cameraProviderExists := providerIndexes[cameraProviderID]
		if cameraIndex, found := providerIndexes[cameraProviderID]; found {
			cameraConfig = result[cameraIndex]
			if cameraConfig.Type != cameraprovider.ProviderType {
				return nil, fmt.Errorf("camera provider migration %q conflicts with provider type %q", cameraProviderID, cameraConfig.Type)
			}
			var existingConfig cameraprovider.Config
			if err := json.Unmarshal(cameraConfig.Config, &existingConfig); err != nil {
				return nil, fmt.Errorf("decode existing camera provider %q: %w", cameraProviderID, err)
			}
			entries = append(entries, existingConfig.Cameras...)
		}
		entryIDs := make(map[string]struct{}, len(entries))
		for _, entry := range entries {
			entryIDs[entry.ID] = struct{}{}
		}
		cameraChanged := false
		accountChanged := false
		remaining := make([]xiaomi.DeviceConfig, 0, len(cloud.Devices))
		for _, configured := range cloud.Devices {
			entry, migratable := xiaomiCameraMigrationEntry(cloud, configured)
			if !migratable {
				remaining = append(remaining, configured)
				continue
			}
			accountChanged = true
			if _, exists := entryIDs[entry.ID]; !exists {
				entries = append(entries, entry)
				entryIDs[entry.ID] = struct{}{}
				cameraChanged = true
			}
		}
		// Recover a previously interrupted migration. Older builds could remove
		// the camera from CloudConfig.Devices before the independent Provider
		// survived, while leaving the non-secret MediaSource and Stream rows.
		for _, source := range mediaSources {
			if source.ProviderID != account.ID || source.Protocol != string(media.ProtocolXiaomiMISS) || !source.Enabled {
				continue
			}
			if _, exists := entryIDs[source.DeviceID]; exists {
				continue
			}
			var profiles []media.MediaProfile
			if err := json.Unmarshal(source.ProfilesJSON, &profiles); err != nil {
				return nil, fmt.Errorf("decode legacy camera profiles %q: %w", source.DeviceID, err)
			}
			var sourceConfig struct {
				DID     string `json:"did"`
				Model   string `json:"model"`
				LocalIP string `json:"localIp"`
				Subtype string `json:"subtype"`
				Channel int    `json:"channel"`
			}
			if err := json.Unmarshal(source.SourceConfigJSON, &sourceConfig); err != nil {
				return nil, fmt.Errorf("decode legacy camera source %q: %w", source.DeviceID, err)
			}
			entry := cameraprovider.Entry{
				ID: source.DeviceID, Name: source.DeviceID, Driver: "xiaomi-miss", Profiles: profiles,
				Xiaomi: &cameraprovider.XiaomiConfig{
					Region: cloud.Region, Username: cloud.Username, Password: cloud.Password, UserID: cloud.UserID,
					Ssecurity: cloud.Ssecurity, ServiceToken: cloud.ServiceToken, PassToken: cloud.PassToken,
					DID: sourceConfig.DID, Model: sourceConfig.Model, LocalIP: sourceConfig.LocalIP,
					Subtype: sourceConfig.Subtype, Channel: sourceConfig.Channel,
					RequestTimeoutSec: cloud.RequestTimeoutSec,
				},
			}
			entries = append(entries, entry)
			entryIDs[entry.ID] = struct{}{}
			cameraChanged = true
		}
		if len(entries) == 0 {
			continue
		}
		if !cameraProviderExists {
			cameraChanged = true
		}
		if !cameraChanged && !accountChanged {
			continue
		}
		cameraRaw, err := json.Marshal(cameraprovider.Config{Cameras: entries})
		if err != nil {
			return nil, fmt.Errorf("encode camera provider migration %q: %w", account.ID, err)
		}
		cameraConfig.Config = cameraRaw
		if _, err := cameraprovider.NewProviderFromConfig(cameraConfig); err != nil {
			return nil, fmt.Errorf("validate camera provider migration %q: %w", account.ID, err)
		}
		cloud.Devices = remaining
		accountRaw, err := json.Marshal(cloud)
		if err != nil {
			return nil, fmt.Errorf("encode migrated Xiaomi provider %q: %w", account.ID, err)
		}
		account.Config = accountRaw
		if err := store.SaveProvidersAtomically(ctx, cameraConfig, account); err != nil {
			return nil, fmt.Errorf("save migrated provider pair %q: %w", account.ID, err)
		}
		result[index] = account
		if cameraIndex, found := providerIndexes[cameraProviderID]; found {
			result[cameraIndex] = cameraConfig
		} else {
			providerIndexes[cameraProviderID] = len(result)
			result = append(result, cameraConfig)
		}
	}
	return result, nil
}

func xiaomiCameraMigrationEntry(cloud xiaomi.CloudConfig, configured xiaomi.DeviceConfig) (cameraprovider.Entry, bool) {
	if configured.Type != device.TypeCamera || configured.Media == nil {
		return cameraprovider.Entry{}, false
	}
	entry := cameraprovider.Entry{
		ID: configured.ID, Name: configured.Name, HomeID: configured.HomeID, Home: configured.Home,
		RoomID: configured.RoomID, Room: configured.Room,
		Profiles: append([]media.MediaProfile(nil), configured.Media.Profiles...),
	}
	switch configured.Media.Protocol {
	case media.ProtocolXiaomiMISS:
		entry.Driver = "xiaomi-miss"
		entry.Xiaomi = &cameraprovider.XiaomiConfig{
			Region: cloud.Region, Username: cloud.Username, Password: cloud.Password, UserID: cloud.UserID,
			Ssecurity: cloud.Ssecurity, ServiceToken: cloud.ServiceToken, PassToken: cloud.PassToken,
			DID: configured.DID, Model: configured.Model, LocalIP: configured.Media.Host,
			Subtype: configured.Media.Subtype, Channel: configured.Media.Channel,
			RequestTimeoutSec: cloud.RequestTimeoutSec,
		}
	case media.ProtocolRTSP:
		entry.Driver = "rtsp"
		entry.RTSP = &cameraprovider.RTSPConfig{
			Host: configured.Media.Host, Port: configured.Media.Port, Path: configured.Media.Path,
			AuthType: configured.Media.AuthType, Username: configured.Media.Username, Password: configured.Media.Password,
		}
	default:
		return cameraprovider.Entry{}, false
	}
	return entry, true
}
