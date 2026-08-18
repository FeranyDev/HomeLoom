package application_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
	"github.com/feranydev/homeloom/backend/internal/eventbus"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	statestore "github.com/feranydev/homeloom/backend/internal/state"
)

type checkpointStore struct{ values map[string]string }

func (s *checkpointStore) GetSetting(_ context.Context, key string) (string, bool, error) {
	value, exists := s.values[key]
	return value, exists, nil
}

func (s *checkpointStore) SaveSetting(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func TestDeviceServiceAppliesEventQueueOptions(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider(), application.DeviceServiceOptions{
		EventQueue: eventbus.Config{ShardCount: 2, QueueSize: 3},
	})
	defer service.Close()
	if got := service.Metrics().EventQueueCapacity; got != 6 {
		t.Fatalf("EventQueueCapacity = %d, want 6", got)
	}
}

func TestDeviceServiceRestoresCheckpointAsStaleAndPersistsOnClose(t *testing.T) {
	key := domainstate.Key{DeviceID: "cached-sensor", EndpointID: "main", CapabilityID: "temperature", PropertyID: "value"}
	checkpoint := statestore.Checkpoint{Version: statestore.CheckpointVersion, Values: []domainstate.StateValue{{
		Key: key, ProviderID: "cloud", Source: domainstate.SourceReported, Value: domainstate.NumberValue(21.5),
		ObservedAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(), Quality: domainstate.QualityReported, Known: true, Version: 4,
	}}}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	store := &checkpointStore{values: map[string]string{"state_checkpoint_v1": string(payload)}}
	service := application.NewDeviceService(virtual.NewProvider(), application.DeviceServiceOptions{
		StateCheckpoint: application.StateCheckpointConfig{Store: store, Interval: time.Hour},
	})
	values := service.States(key.DeviceID)
	if len(values) != 1 || values[0].Quality != domainstate.QualityStale || values[0].Available || values[0].Source != domainstate.SourcePersistentCache || values[0].Value.Number == nil || *values[0].Value.Number != 21.5 {
		t.Fatalf("restored values = %#v", values)
	}
	if metrics := service.Metrics(); metrics.StatesRestoredFromCache != 1 {
		t.Fatalf("metrics = %#v", metrics)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	var saved statestore.Checkpoint
	if err := json.Unmarshal([]byte(store.values["state_checkpoint_v1"]), &saved); err != nil || len(saved.Values) < 1 || saved.Version != statestore.CheckpointVersion {
		t.Fatalf("saved checkpoint = %#v, error = %v", saved, err)
	}
}
