package application

import (
	"context"
	"errors"
	"fmt"

	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
)

var (
	ErrMCPDeviceNotFound   = errors.New("MCP device not found")
	ErrMCPPropertyNotFound = errors.New("MCP property not found")
)

type MCPConfigStore interface {
	GetMCPDeviceConfig(context.Context, string) (domainmcp.DeviceConfig, bool, error)
	SaveMCPDeviceConfig(context.Context, domainmcp.DeviceConfig) error
	ListMCPPropertyConfigs(context.Context, string) ([]domainmcp.PropertyConfig, error)
	GetMCPPropertyConfig(context.Context, domainmcp.PropertyPath) (domainmcp.PropertyConfig, bool, error)
	SaveMCPPropertyConfig(context.Context, domainmcp.PropertyConfig) error
	DeleteMCPPropertyConfig(context.Context, domainmcp.PropertyPath) error
}

type MCPConfigService struct {
	store   MCPConfigStore
	devices *DeviceService
}

func NewMCPConfigService(store MCPConfigStore, devices *DeviceService) *MCPConfigService {
	return &MCPConfigService{store: store, devices: devices}
}

func (s *MCPConfigService) Device(ctx context.Context, id string) (domainmcp.DeviceConfig, error) {
	if !s.hasDevice(id) {
		return domainmcp.DeviceConfig{}, ErrMCPDeviceNotFound
	}
	config, found, err := s.store.GetMCPDeviceConfig(ctx, id)
	if err != nil {
		return domainmcp.DeviceConfig{}, err
	}
	if !found {
		return domainmcp.DeviceConfig{DeviceID: id, DefaultAccess: domainmcp.AccessHidden}, nil
	}
	return config.Normalize(), nil
}

func (s *MCPConfigService) SaveDevice(ctx context.Context, config domainmcp.DeviceConfig) (domainmcp.DeviceConfig, error) {
	config = config.Normalize()
	if err := config.Validate(); err != nil {
		return domainmcp.DeviceConfig{}, err
	}
	if !s.hasDevice(config.DeviceID) {
		return domainmcp.DeviceConfig{}, ErrMCPDeviceNotFound
	}
	if err := s.store.SaveMCPDeviceConfig(ctx, config); err != nil {
		return domainmcp.DeviceConfig{}, err
	}
	return config, nil
}

func (s *MCPConfigService) Properties(ctx context.Context, deviceID string) ([]domainmcp.PropertyConfig, error) {
	if !s.hasDevice(deviceID) {
		return nil, ErrMCPDeviceNotFound
	}
	return s.store.ListMCPPropertyConfigs(ctx, deviceID)
}

func (s *MCPConfigService) SaveProperty(ctx context.Context, config domainmcp.PropertyConfig) (domainmcp.PropertyConfig, error) {
	config = config.Normalize()
	if err := config.Validate(); err != nil {
		return domainmcp.PropertyConfig{}, err
	}
	if !s.hasProperty(config.PropertyPath) {
		return domainmcp.PropertyConfig{}, ErrMCPPropertyNotFound
	}
	if config.AllowUnattendedAI {
		deviceConfig, err := s.Device(ctx, config.DeviceID)
		if err != nil {
			return domainmcp.PropertyConfig{}, err
		}
		if domainmcp.Effective(deviceConfig, config).EffectiveAccess != domainmcp.AccessConfirm {
			return domainmcp.PropertyConfig{}, fmt.Errorf("%w: unattended AI requires confirm access", domainmcp.ErrInvalidConfig)
		}
	}
	if err := s.store.SaveMCPPropertyConfig(ctx, config); err != nil {
		return domainmcp.PropertyConfig{}, err
	}
	return config, nil
}

func (s *MCPConfigService) DeleteProperty(ctx context.Context, path domainmcp.PropertyPath) error {
	if err := path.Validate(); err != nil {
		return err
	}
	if !s.hasProperty(path) {
		return ErrMCPPropertyNotFound
	}
	return s.store.DeleteMCPPropertyConfig(ctx, path)
}

func (s *MCPConfigService) EffectiveProperty(ctx context.Context, path domainmcp.PropertyPath) (domainmcp.EffectivePropertyConfig, error) {
	if !s.hasProperty(path) {
		return domainmcp.EffectivePropertyConfig{}, ErrMCPPropertyNotFound
	}
	deviceConfig, err := s.Device(ctx, path.DeviceID)
	if err != nil {
		return domainmcp.EffectivePropertyConfig{}, err
	}
	propertyConfig, found, err := s.store.GetMCPPropertyConfig(ctx, path)
	if err != nil {
		return domainmcp.EffectivePropertyConfig{}, err
	}
	if !found {
		propertyConfig = domainmcp.PropertyConfig{PropertyPath: path, Access: domainmcp.AccessInherit}
	}
	return domainmcp.Effective(deviceConfig, propertyConfig), nil
}

func (s *MCPConfigService) hasDevice(id string) bool {
	items, _ := s.devices.List(context.Background())
	for _, item := range items {
		if item.ID == id && !item.Removed {
			return true
		}
	}
	return false
}

func (s *MCPConfigService) hasProperty(path domainmcp.PropertyPath) bool {
	items, _ := s.devices.List(context.Background())
	for _, item := range items {
		if item.ID == path.DeviceID && !item.Removed {
			_, found := item.Property(path.EndpointID, path.CapabilityID, path.PropertyID)
			return found
		}
	}
	return false
}
