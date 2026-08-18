package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/feranydev/homeloom/backend/internal/domain/logicaldevice"
)

var ErrLogicalDeviceNotFound = errors.New("logical device not found")

type LogicalDeviceStore interface {
	ListLogicalDevices(context.Context) ([]logicaldevice.Config, error)
	SaveLogicalDevice(context.Context, logicaldevice.Config) error
	DeleteLogicalDevice(context.Context, string) error
}

type LogicalDeviceRuntime interface {
	SetLogicalDevices([]logicaldevice.Config) error
	LogicalDevices() []logicaldevice.Config
	LogicalDeviceCandidates(context.Context) ([]logicaldevice.MatchCandidate, error)
	LogicalDeviceExplanations(string) ([]logicaldevice.RouteExplanation, bool)
}

// LogicalDeviceService coordinates durable configuration with the running
// Provider Manager. It has no knowledge of a Provider's credentials or state
// store internals; the runtime owns live route selection.
type LogicalDeviceService struct {
	store   LogicalDeviceStore
	runtime LogicalDeviceRuntime
	devices *DeviceService
}

func NewLogicalDeviceService(store LogicalDeviceStore, runtime LogicalDeviceRuntime, devices *DeviceService) *LogicalDeviceService {
	return &LogicalDeviceService{store: store, runtime: runtime, devices: devices}
}

func (s *LogicalDeviceService) Load(ctx context.Context) error {
	if s.store == nil || s.runtime == nil {
		return errors.New("logical device configuration is unavailable")
	}
	items, err := s.store.ListLogicalDevices(ctx)
	if err != nil {
		return err
	}
	return s.runtime.SetLogicalDevices(items)
}

func (s *LogicalDeviceService) List() []logicaldevice.Config {
	if s.runtime == nil {
		return nil
	}
	return s.runtime.LogicalDevices()
}

func (s *LogicalDeviceService) Save(ctx context.Context, item logicaldevice.Config) error {
	if s.store == nil || s.runtime == nil {
		return errors.New("logical device configuration is unavailable")
	}
	if err := item.Validate(); err != nil {
		return NewValidationError("invalid logical device", map[string]string{"logicalDevice": err.Error()})
	}
	previous := s.runtime.LogicalDevices()
	next := replaceLogicalDevice(previous, item)
	if err := s.runtime.SetLogicalDevices(next); err != nil {
		return NewValidationError("invalid logical device", map[string]string{"logicalDevice": err.Error()})
	}
	if err := s.store.SaveLogicalDevice(ctx, item); err != nil {
		_ = s.runtime.SetLogicalDevices(previous)
		return err
	}
	return s.refreshAndHideSources(ctx, item)
}

func (s *LogicalDeviceService) Delete(ctx context.Context, id string) error {
	if s.store == nil || s.runtime == nil {
		return errors.New("logical device configuration is unavailable")
	}
	previous := s.runtime.LogicalDevices()
	var removed *logicaldevice.Config
	next := make([]logicaldevice.Config, 0, len(previous))
	for index := range previous {
		if previous[index].ID == id {
			copy := previous[index].Clone()
			removed = &copy
			continue
		}
		next = append(next, previous[index])
	}
	if removed == nil {
		return ErrLogicalDeviceNotFound
	}
	if err := s.runtime.SetLogicalDevices(next); err != nil {
		return err
	}
	if err := s.store.DeleteLogicalDevice(ctx, id); err != nil {
		_ = s.runtime.SetLogicalDevices(previous)
		return fmt.Errorf("delete logical device: %w", err)
	}
	if s.devices != nil {
		if err := s.devices.RefreshDevices(ctx); err != nil {
			return err
		}
		s.devices.RemoveFromRegistry(id)
	}
	return nil
}

func (s *LogicalDeviceService) Candidates(ctx context.Context) ([]logicaldevice.MatchCandidate, error) {
	if s.runtime == nil {
		return nil, errors.New("logical device configuration is unavailable")
	}
	return s.runtime.LogicalDeviceCandidates(ctx)
}

func (s *LogicalDeviceService) Explanations(id string) ([]logicaldevice.RouteExplanation, error) {
	if s.runtime == nil {
		return nil, errors.New("logical device configuration is unavailable")
	}
	items, exists := s.runtime.LogicalDeviceExplanations(id)
	if !exists {
		return nil, ErrLogicalDeviceNotFound
	}
	return items, nil
}

func replaceLogicalDevice(items []logicaldevice.Config, item logicaldevice.Config) []logicaldevice.Config {
	result := make([]logicaldevice.Config, 0, len(items)+1)
	replaced := false
	for _, current := range items {
		if current.ID == item.ID {
			result = append(result, item.Clone())
			replaced = true
			continue
		}
		result = append(result, current)
	}
	if !replaced {
		result = append(result, item.Clone())
	}
	return result
}

func (s *LogicalDeviceService) refreshAndHideSources(ctx context.Context, item logicaldevice.Config) error {
	if s.devices == nil {
		return nil
	}
	if err := s.devices.RefreshDevices(ctx); err != nil {
		return err
	}
	for _, binding := range item.Bindings {
		s.devices.RemoveFromRegistry(binding.DeviceID)
	}
	return nil
}
