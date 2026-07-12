package provider

import (
	"context"
	"errors"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

var (
	ErrDeviceNotFound      = errors.New("device not found")
	ErrPropertyUnsupported = errors.New("property unsupported")
)

type Manifest struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Capabilities struct {
	Discovery     bool `json:"discovery"`
	PropertyRead  bool `json:"propertyRead"`
	PropertyWrite bool `json:"propertyWrite"`
	Events        bool `json:"events"`
}

type Provider interface {
	Manifest() Manifest
	Capabilities() Capabilities
	Initialize(context.Context) error
	Close(context.Context) error
}

type Discoverer interface {
	DiscoverDevices(context.Context) ([]device.Device, error)
}

type PropertyWriteRequest struct {
	DeviceID     string
	EndpointID   string
	CapabilityID string
	PropertyID   string
	Value        device.PropertyValue
}

type PropertyWriter interface {
	WriteProperty(context.Context, PropertyWriteRequest) (device.Device, error)
}

type EventSubscriber interface {
	Subscribe(func(device.Device)) func()
}

type RuntimeInfo struct {
	Manifest     Manifest     `json:"manifest"`
	Capabilities Capabilities `json:"capabilities"`
	Status       string       `json:"status"`
	Error        string       `json:"error,omitempty"`
}

type Inspector interface {
	ProviderInfos() []RuntimeInfo
}
