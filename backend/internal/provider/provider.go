package provider

import (
	"context"
	"errors"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

var (
	ErrDeviceNotFound      = errors.New("device not found")
	ErrPropertyUnsupported = errors.New("property unsupported")
	ErrPropertyInvalid     = errors.New("invalid property value")
	ErrWriteRejected       = errors.New("provider rejected write")
	ErrSimulationInvalid   = errors.New("invalid simulation request")
	ErrProviderUnavailable = errors.New("provider unavailable")
	ErrCommandUnsupported  = errors.New("command unsupported")
	ErrCommandInvalid      = errors.New("invalid command request")
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
	Commands      bool `json:"commands"`
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

type PropertyReadRequest struct {
	DeviceID     string
	EndpointID   string
	CapabilityID string
	PropertyID   string
}

type PropertyReader interface {
	ReadProperty(context.Context, PropertyReadRequest) (device.Property, error)
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

type CommandRequest struct {
	DeviceID       string
	EndpointID     string
	CapabilityID   string
	CommandID      string
	Parameters     map[string]device.PropertyValue
	IdempotencyKey string
}

type CommandExecutor interface {
	ExecuteCommand(context.Context, CommandRequest) (device.Device, error)
}

type EventSubscriber interface {
	Subscribe(func(device.Device)) func()
}

// SimulationRequest changes ephemeral provider state. It is intended for
// development providers and is never persisted as desired configuration.
type SimulationRequest struct {
	DeviceID     string
	Online       *bool
	Availability *device.Availability
	Properties   []PropertyWriteRequest
	Sequence     *uint64
	Repeat       int
}

type Simulator interface {
	Simulate(context.Context, SimulationRequest) (device.Device, error)
}

type RuntimeInfo struct {
	Manifest       Manifest          `json:"manifest"`
	Capabilities   Capabilities      `json:"capabilities"`
	Status         string            `json:"status"`
	Error          string            `json:"error,omitempty"`
	RetryCount     int               `json:"retryCount"`
	NextRetryAt    *time.Time        `json:"nextRetryAt,omitempty"`
	TransitionedAt time.Time         `json:"transitionedAt"`
	Metrics        map[string]uint64 `json:"metrics,omitempty"`
}

type Inspector interface {
	ProviderInfos() []RuntimeInfo
}

type MetricsReporter interface {
	ProviderMetrics() map[string]uint64
}
