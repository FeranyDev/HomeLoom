package application

import (
	"context"
	"encoding/json"
	"time"

	"github.com/feranydev/homeloom/backend/internal/eventbus"
	statestore "github.com/feranydev/homeloom/backend/internal/state"
)

const stateCheckpointSetting = "state_checkpoint_v1"

// StateCheckpointStore is intentionally narrow so the optional runtime cache
// can use the existing system-settings persistence without introducing a new
// database table or making a durable state cache mandatory for every caller.
type StateCheckpointStore interface {
	GetSetting(context.Context, string) (string, bool, error)
	SaveSetting(context.Context, string, string) error
}

// StateCheckpointConfig enables periodic persistence of last-known state. An
// interval of zero disables it. Deployments should use a low-frequency value
// (at least one minute) because the cache is a restart aid, not an event log.
type StateCheckpointConfig struct {
	Store    StateCheckpointStore
	Interval time.Duration
}

// DeviceServiceOptions carries optional runtime behavior while retaining the
// established NewDeviceService(provider, dependencies...) constructor.
type DeviceServiceOptions struct {
	EventQueue             eventbus.Config
	StatePriority          statestore.PriorityResolver
	StateCheckpoint        StateCheckpointConfig
	PropertyReadbackDelays []time.Duration
}

func deviceServiceOptionsFrom(dependencies []any) DeviceServiceOptions {
	for _, dependency := range dependencies {
		switch value := dependency.(type) {
		case DeviceServiceOptions:
			return value
		case *DeviceServiceOptions:
			if value != nil {
				return *value
			}
		}
	}
	return DeviceServiceOptions{}
}

type stateCheckpoint struct {
	store    StateCheckpointStore
	interval time.Duration
}

func newStateCheckpoint(config StateCheckpointConfig) stateCheckpoint {
	if config.Store == nil || config.Interval <= 0 {
		return stateCheckpoint{}
	}
	return stateCheckpoint{store: config.Store, interval: config.Interval}
}

func (checkpoint stateCheckpoint) ticker() *time.Ticker {
	if checkpoint.store == nil || checkpoint.interval <= 0 {
		return nil
	}
	return time.NewTicker(checkpoint.interval)
}

func checkpointChan(ticker *time.Ticker) <-chan time.Time {
	if ticker == nil {
		return nil
	}
	return ticker.C
}

func (s *DeviceService) restoreStateCheckpoint(ctx context.Context) {
	if s.checkpoint.store == nil {
		return
	}
	payload, exists, err := s.checkpoint.store.GetSetting(ctx, stateCheckpointSetting)
	if err != nil {
		s.metrics.stateCheckpointErrors.Add(1)
		return
	}
	if !exists || payload == "" {
		return
	}
	var checkpoint statestore.Checkpoint
	if err := json.Unmarshal([]byte(payload), &checkpoint); err != nil || !s.states.RestoreCheckpoint(checkpoint) {
		s.metrics.stateCheckpointErrors.Add(1)
		return
	}
	s.metrics.statesRestored.Add(uint64(len(checkpoint.Values)))
}

func (s *DeviceService) saveStateCheckpoint(ctx context.Context) {
	if s.checkpoint.store == nil {
		return
	}
	payload, err := json.Marshal(s.states.Checkpoint())
	if err != nil {
		s.metrics.stateCheckpointErrors.Add(1)
		return
	}
	if err := s.checkpoint.store.SaveSetting(ctx, stateCheckpointSetting, string(payload)); err != nil {
		s.metrics.stateCheckpointErrors.Add(1)
		return
	}
	s.metrics.stateCheckpoints.Add(1)
}
