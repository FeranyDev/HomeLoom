package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

type providerBootstrapStore interface {
	ListProviders(context.Context) ([]providerconfig.Config, error)
	SaveProvider(context.Context, providerconfig.Config) error
}

// initializeAllVirtualModels expands only the untouched default Virtual
// Provider. Explicit device configuration is user-owned and is never replaced.
func initializeAllVirtualModels(ctx context.Context, store providerBootstrapStore) (bool, error) {
	providers, err := store.ListProviders(ctx)
	if err != nil {
		return false, fmt.Errorf("list providers for virtual model initialization: %w", err)
	}
	for _, item := range providers {
		if item.ID != "virtual-main" || item.Type != "virtual" {
			continue
		}
		config := map[string]json.RawMessage{}
		if len(item.Config) > 0 {
			if err := json.Unmarshal(item.Config, &config); err != nil {
				return false, fmt.Errorf("decode default virtual provider config: %w", err)
			}
		}
		if config == nil {
			config = map[string]json.RawMessage{}
		}
		if rawDevices, exists := config["devices"]; exists {
			var devices []json.RawMessage
			if string(rawDevices) != "null" {
				if err := json.Unmarshal(rawDevices, &devices); err != nil {
					return false, fmt.Errorf("decode default virtual provider devices: %w", err)
				}
			}
			if len(devices) > 0 {
				return false, nil
			}
		}
		devices, err := json.Marshal(virtual.AllModelDeviceConfigs())
		if err != nil {
			return false, fmt.Errorf("encode virtual model devices: %w", err)
		}
		config["devices"] = devices
		item.Config, err = json.Marshal(config)
		if err != nil {
			return false, fmt.Errorf("encode default virtual provider config: %w", err)
		}
		if err := store.SaveProvider(ctx, item); err != nil {
			return false, fmt.Errorf("save initialized virtual model devices: %w", err)
		}
		return true, nil
	}
	return false, nil
}
