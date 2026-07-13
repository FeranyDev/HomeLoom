package application

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

const (
	commandTimeoutSetting = "command_timeout_seconds"
	commandHistorySetting = "command_history_limit"
)

type SettingsStore interface {
	GetSetting(context.Context, string) (string, bool, error)
	SaveSettings(context.Context, map[string]string) error
}

type RuntimeSettings struct {
	CommandTimeoutSeconds int `json:"commandTimeoutSeconds"`
	CommandHistoryLimit   int `json:"commandHistoryLimit"`
}

type SettingsService struct {
	mu      sync.RWMutex
	current RuntimeSettings
	store   SettingsStore
	devices *DeviceService
}

func NewSettingsService(ctx context.Context, store SettingsStore, devices *DeviceService) (*SettingsService, error) {
	current := RuntimeSettings{CommandTimeoutSeconds: 5, CommandHistoryLimit: 1000}
	missing := make(map[string]string)
	if value, exists, err := store.GetSetting(ctx, commandTimeoutSetting); err != nil {
		return nil, err
	} else if exists {
		seconds, parseErr := strconv.Atoi(value)
		if parseErr != nil || seconds < 1 || seconds > 300 {
			return nil, fmt.Errorf("invalid stored command timeout %q", value)
		}
		current.CommandTimeoutSeconds = seconds
	} else {
		missing[commandTimeoutSetting] = strconv.Itoa(current.CommandTimeoutSeconds)
	}
	if value, exists, err := store.GetSetting(ctx, commandHistorySetting); err != nil {
		return nil, err
	} else if exists {
		limit, parseErr := strconv.Atoi(value)
		if parseErr != nil || limit < 100 || limit > 10_000 {
			return nil, fmt.Errorf("invalid stored command history limit %q", value)
		}
		current.CommandHistoryLimit = limit
	} else {
		missing[commandHistorySetting] = strconv.Itoa(current.CommandHistoryLimit)
	}
	if len(missing) > 0 {
		if err := store.SaveSettings(ctx, missing); err != nil {
			return nil, err
		}
	}
	service := &SettingsService{current: current, store: store, devices: devices}
	devices.SetCommandTimeout(time.Duration(current.CommandTimeoutSeconds) * time.Second)
	devices.SetCommandHistoryLimit(current.CommandHistoryLimit)
	return service, nil
}

func (s *SettingsService) Get() RuntimeSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *SettingsService) Save(ctx context.Context, next RuntimeSettings) (RuntimeSettings, error) {
	if next.CommandTimeoutSeconds < 1 || next.CommandTimeoutSeconds > 300 {
		return RuntimeSettings{}, NewValidationError("invalid runtime settings", map[string]string{"commandTimeoutSeconds": "must be between 1 and 300"})
	}
	if next.CommandHistoryLimit < 100 || next.CommandHistoryLimit > 10_000 {
		return RuntimeSettings{}, NewValidationError("invalid runtime settings", map[string]string{"commandHistoryLimit": "must be between 100 and 10000"})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.SaveSettings(ctx, map[string]string{commandTimeoutSetting: strconv.Itoa(next.CommandTimeoutSeconds), commandHistorySetting: strconv.Itoa(next.CommandHistoryLimit)}); err != nil {
		return RuntimeSettings{}, err
	}
	s.devices.SetCommandTimeout(time.Duration(next.CommandTimeoutSeconds) * time.Second)
	s.devices.SetCommandHistoryLimit(next.CommandHistoryLimit)
	s.current = next
	return next, nil
}
