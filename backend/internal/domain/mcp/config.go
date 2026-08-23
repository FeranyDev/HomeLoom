package mcp

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

const MaxUsageNoteRunes = 1000

type Access string

const (
	// AccessHidden prevents a device or property from being disclosed to MCP.
	AccessHidden Access = "hidden"
	// AccessRead permits state inspection but never a device mutation.
	AccessRead Access = "read"
	// AccessConfirm permits an Agent to propose an action. A human approval is
	// still required before the command reaches a Provider.
	AccessConfirm Access = "confirm"
	// AccessInherit is valid only on a property and uses the device default.
	AccessInherit Access = "inherit"
)

var ErrInvalidConfig = errors.New("invalid MCP configuration")

type PropertyPath struct {
	DeviceID     string `json:"deviceId"`
	EndpointID   string `json:"endpointId"`
	CapabilityID string `json:"capabilityId"`
	PropertyID   string `json:"propertyId"`
}

func (p PropertyPath) Validate() error {
	for _, value := range []string{p.DeviceID, p.EndpointID, p.CapabilityID, p.PropertyID} {
		if !device.ValidStableID(value) {
			return fmt.Errorf("%w: invalid property path", ErrInvalidConfig)
		}
	}
	return nil
}

type DeviceConfig struct {
	DeviceID      string `json:"deviceId"`
	Enabled       bool   `json:"enabled"`
	UsageNote     string `json:"usageNote"`
	DefaultAccess Access `json:"defaultAccess"`
}

func (c DeviceConfig) Normalize() DeviceConfig {
	c.DeviceID = strings.TrimSpace(c.DeviceID)
	c.UsageNote = strings.TrimSpace(c.UsageNote)
	if c.DefaultAccess == "" {
		c.DefaultAccess = AccessHidden
	}
	return c
}

func (c DeviceConfig) Validate() error {
	c = c.Normalize()
	if !device.ValidStableID(c.DeviceID) {
		return fmt.Errorf("%w: invalid device id", ErrInvalidConfig)
	}
	if c.DefaultAccess != AccessHidden && c.DefaultAccess != AccessRead && c.DefaultAccess != AccessConfirm {
		return fmt.Errorf("%w: invalid device default access", ErrInvalidConfig)
	}
	if utf8.RuneCountInString(c.UsageNote) > MaxUsageNoteRunes {
		return fmt.Errorf("%w: usage note exceeds %d characters", ErrInvalidConfig, MaxUsageNoteRunes)
	}
	return nil
}

type PropertyConfig struct {
	PropertyPath
	UsageNote string `json:"usageNote"`
	Access    Access `json:"access"`
}

func (c PropertyConfig) Normalize() PropertyConfig {
	c.DeviceID = strings.TrimSpace(c.DeviceID)
	c.EndpointID = strings.TrimSpace(c.EndpointID)
	c.CapabilityID = strings.TrimSpace(c.CapabilityID)
	c.PropertyID = strings.TrimSpace(c.PropertyID)
	c.UsageNote = strings.TrimSpace(c.UsageNote)
	if c.Access == "" {
		c.Access = AccessInherit
	}
	return c
}

func (c PropertyConfig) Validate() error {
	c = c.Normalize()
	if err := c.PropertyPath.Validate(); err != nil {
		return err
	}
	if c.Access != AccessInherit && c.Access != AccessHidden && c.Access != AccessRead && c.Access != AccessConfirm {
		return fmt.Errorf("%w: invalid property access", ErrInvalidConfig)
	}
	if utf8.RuneCountInString(c.UsageNote) > MaxUsageNoteRunes {
		return fmt.Errorf("%w: usage note exceeds %d characters", ErrInvalidConfig, MaxUsageNoteRunes)
	}
	return nil
}

type EffectivePropertyConfig struct {
	PropertyConfig
	Enabled         bool   `json:"enabled"`
	EffectiveAccess Access `json:"effectiveAccess"`
}

func Effective(deviceConfig DeviceConfig, propertyConfig PropertyConfig) EffectivePropertyConfig {
	deviceConfig = deviceConfig.Normalize()
	propertyConfig = propertyConfig.Normalize()
	access := propertyConfig.Access
	if access == AccessInherit {
		access = deviceConfig.DefaultAccess
	}
	if !deviceConfig.Enabled {
		access = AccessHidden
	}
	return EffectivePropertyConfig{PropertyConfig: propertyConfig, Enabled: deviceConfig.Enabled, EffectiveAccess: access}
}
