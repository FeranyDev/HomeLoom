package application

import (
	"context"
	"errors"
	"sort"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
)

var ErrMCPAccessDenied = errors.New("MCP access is not enabled for this resource")

// MCPToolService is the single read-model boundary used by both the MCP HTTP
// endpoint and the Agent. It redacts every device/property that has not been
// deliberately exposed in MCP configuration.
type MCPToolService struct {
	devices *DeviceService
	configs *MCPConfigService
}

func NewMCPToolService(devices *DeviceService, configs *MCPConfigService) *MCPToolService {
	return &MCPToolService{devices: devices, configs: configs}
}

type MCPDevice struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Type       device.Type   `json:"type"`
	HomeName   string        `json:"homeName,omitempty"`
	RoomName   string        `json:"roomName,omitempty"`
	Online     bool          `json:"online"`
	UsageNote  string        `json:"usageNote,omitempty"`
	Properties []MCPProperty `json:"properties"`
}

type MCPProperty struct {
	domainmcp.PropertyPath
	Name       string           `json:"name"`
	Type       device.ValueType `json:"type"`
	Unit       string           `json:"unit,omitempty"`
	Readable   bool             `json:"readable"`
	Writable   bool             `json:"writable"`
	Access     domainmcp.Access `json:"access"`
	UsageNote  string           `json:"usageNote,omitempty"`
}

type MCPState struct {
	MCPDevice
	States []domainstate.StateValue `json:"states"`
}

func (s *MCPToolService) ListDevices(ctx context.Context) ([]MCPDevice, error) {
	items, err := s.devices.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]MCPDevice, 0, len(items))
	for _, item := range items {
		if item.Removed || item.Disabled {
			continue
		}
		entry, visible, err := s.exposeDevice(ctx, item)
		if err != nil {
			return nil, err
		}
		if visible {
			result = append(result, entry)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}

func (s *MCPToolService) DeviceState(ctx context.Context, deviceID string) (MCPState, error) {
	items, err := s.devices.List(ctx)
	if err != nil {
		return MCPState{}, err
	}
	for _, item := range items {
		if item.ID != deviceID || item.Removed || item.Disabled {
			continue
		}
		entry, visible, exposeErr := s.exposeDevice(ctx, item)
		if exposeErr != nil {
			return MCPState{}, exposeErr
		}
		if !visible {
			return MCPState{}, ErrMCPAccessDenied
		}
		allowed := make(map[domainmcp.PropertyPath]struct{}, len(entry.Properties))
		for _, property := range entry.Properties {
			allowed[property.PropertyPath] = struct{}{}
		}
		states := make([]domainstate.StateValue, 0)
		for _, state := range s.devices.States(deviceID) {
			path := domainmcp.PropertyPath{DeviceID: state.Key.DeviceID, EndpointID: state.Key.EndpointID, CapabilityID: state.Key.CapabilityID, PropertyID: state.Key.PropertyID}
			if _, ok := allowed[path]; ok {
				states = append(states, state)
			}
		}
		return MCPState{MCPDevice: entry, States: states}, nil
	}
	return MCPState{}, ErrMCPDeviceNotFound
}

func (s *MCPToolService) PropertyAccess(ctx context.Context, path domainmcp.PropertyPath) (domainmcp.EffectivePropertyConfig, error) {
	effective, err := s.configs.EffectiveProperty(ctx, path)
	if err != nil {
		return domainmcp.EffectivePropertyConfig{}, err
	}
	if effective.EffectiveAccess == domainmcp.AccessHidden {
		return domainmcp.EffectivePropertyConfig{}, ErrMCPAccessDenied
	}
	return effective, nil
}

func (s *MCPToolService) exposeDevice(ctx context.Context, item device.Device) (MCPDevice, bool, error) {
	config, err := s.configs.Device(ctx, item.ID)
	if err != nil {
		return MCPDevice{}, false, err
	}
	if !config.Enabled {
		return MCPDevice{}, false, nil
	}
	overrides, err := s.configs.Properties(ctx, item.ID)
	if err != nil {
		return MCPDevice{}, false, err
	}
	overrideByPath := make(map[domainmcp.PropertyPath]domainmcp.PropertyConfig, len(overrides))
	for _, override := range overrides {
		overrideByPath[override.PropertyPath] = override
	}
	properties := make([]MCPProperty, 0)
	for _, endpoint := range item.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				path := domainmcp.PropertyPath{DeviceID: item.ID, EndpointID: endpoint.ID, CapabilityID: capability.ID, PropertyID: property.Definition.ID}
				override, exists := overrideByPath[path]
				if !exists {
					override = domainmcp.PropertyConfig{PropertyPath: path, Access: domainmcp.AccessInherit}
				}
				effective := domainmcp.Effective(config, override)
				if effective.EffectiveAccess == domainmcp.AccessHidden {
					continue
				}
				properties = append(properties, MCPProperty{
					PropertyPath: path, Name: property.Definition.Name, Type: property.Definition.Type, Unit: property.Definition.Unit,
					Readable: property.Definition.Readable, Writable: property.Definition.Writable, Access: effective.EffectiveAccess, UsageNote: effective.UsageNote,
				})
			}
		}
	}
	if len(properties) == 0 {
		return MCPDevice{}, false, nil
	}
	sort.Slice(properties, func(left, right int) bool {
		leftPath, rightPath := properties[left].PropertyPath, properties[right].PropertyPath
		return leftPath.EndpointID+"\x00"+leftPath.CapabilityID+"\x00"+leftPath.PropertyID < rightPath.EndpointID+"\x00"+rightPath.CapabilityID+"\x00"+rightPath.PropertyID
	})
	return MCPDevice{ID: item.ID, Name: item.Name, Type: item.Type, HomeName: item.HomeName, RoomName: item.RoomName, Online: item.IsOnline(), UsageNote: config.UsageNote, Properties: properties}, true, nil
}
