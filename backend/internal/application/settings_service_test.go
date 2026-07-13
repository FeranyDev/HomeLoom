package application_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

type settingsStore struct{ values map[string]string }

func (s *settingsStore) GetSetting(_ context.Context, key string) (string, bool, error) {
	value, exists := s.values[key]
	return value, exists, nil
}
func (s *settingsStore) SaveSettings(_ context.Context, values map[string]string) error {
	for key, value := range values {
		s.values[key] = value
	}
	return nil
}

func TestSettingsServiceLoadsPersistsAndAppliesCommandTimeout(t *testing.T) {
	store := &settingsStore{values: map[string]string{"command_timeout_seconds": "9", "command_history_limit": "500"}}
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	service, err := application.NewSettingsService(context.Background(), store, devices)
	if err != nil || service.Get().CommandTimeoutSeconds != 9 {
		t.Fatalf("settings = %#v, %v", service, err)
	}
	updated, err := service.Save(context.Background(), application.RuntimeSettings{CommandTimeoutSeconds: 12, CommandHistoryLimit: 750})
	if err != nil || updated.CommandTimeoutSeconds != 12 || updated.CommandHistoryLimit != 750 || store.values["command_timeout_seconds"] != strconv.Itoa(12) || store.values["command_history_limit"] != "750" {
		t.Fatalf("updated = %#v, store = %#v, error = %v", updated, store.values, err)
	}
	_, command, err := devices.ExecutePower(context.Background(), "virtual-switch-1", true)
	if err != nil {
		t.Fatal(err)
	}
	deadline := command.Deadline.Sub(command.CreatedAt)
	if deadline < 11900*time.Millisecond || deadline > 12100*time.Millisecond {
		t.Fatalf("command deadline = %s", deadline)
	}
	if _, err := service.Save(context.Background(), application.RuntimeSettings{CommandTimeoutSeconds: 0, CommandHistoryLimit: 1000}); err == nil {
		t.Fatal("invalid timeout was accepted")
	}
}
