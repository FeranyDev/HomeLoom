package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

// LiveReconfigurer lets a running provider adopt a compatible replacement
// configuration without tearing down its network session. Providers with a
// configurable child-device catalog must implement this contract so catalog
// additions and mapping edits never reconnect the transport. Returning
// handled false asks the runtime to perform a close-before-open replacement;
// the manager never runs two connections for the same Provider ID.
// Implementations must leave the current provider unchanged on error.
type LiveReconfigurer interface {
	Reconfigure(context.Context, Provider) (handled bool, err error)
}

// CredentialStatus describes renewable Provider credentials without exposing
// their values. RefreshAt is the earliest safe renewal time across tokens and
// certificates.
type CredentialStatus struct {
	Managed              bool      `json:"managed"`
	RefreshAt            time.Time `json:"refreshAt,omitempty"`
	TokenExpiresAt       time.Time `json:"tokenExpiresAt,omitempty"`
	CertificateExpiresAt time.Time `json:"certificateExpiresAt,omitempty"`
}

// CredentialMaintainer is implemented by Providers whose durable credentials
// can be renewed without user interaction. RenewCredentials returns a complete
// replacement config document; persistence and runtime reconciliation remain
// the application layer's responsibility.
type CredentialMaintainer interface {
	CredentialStatus(time.Time) (CredentialStatus, error)
	RenewCredentials(context.Context) (json.RawMessage, error)
}

type Discoverer interface {
	DiscoverDevices(context.Context) ([]device.Device, error)
}

// SourceCatalogMetadata describes how completely a Provider can enumerate the
// native capabilities of one device. A Provider must not claim Complete when
// the catalog only mirrors user-configured mappings.
type SourceCatalogMetadata struct {
	Complete  bool                         `json:"complete"`
	Source    string                       `json:"source"`
	SpecType  string                       `json:"specType,omitempty"`
	Model     string                       `json:"model,omitempty"`
	FetchedAt time.Time                    `json:"fetchedAt,omitempty"`
	Error     string                       `json:"error,omitempty"`
	Values    map[string]SourceValueStatus `json:"values,omitempty"`
}

type SourceValueStatus struct {
	Known      bool      `json:"known"`
	Available  bool      `json:"available"`
	ObservedAt time.Time `json:"observedAt,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func SourceValueKey(endpointID, capabilityID, propertyID string) string {
	return strings.Join([]string{endpointID, capabilityID, propertyID}, "/")
}

func SnapshotValueStatuses(item device.Device) map[string]SourceValueStatus {
	result := make(map[string]SourceValueStatus)
	for _, endpoint := range item.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				result[SourceValueKey(endpoint.ID, capability.ID, property.Definition.ID)] = SourceValueStatus{Known: true, Available: item.IsOnline(), ObservedAt: item.LastUpdateAt}
			}
		}
	}
	return result
}

// SourceCatalogDevice keeps the normal device shape so existing clients can
// traverse Endpoint/Capability/Property while adding an explicit provenance
// and completeness contract.
type SourceCatalogDevice struct {
	device.Device
	Catalog SourceCatalogMetadata `json:"catalog"`
}

// SourceCataloger exposes native Provider attributes before configurable
// Provider → unified-model projection.
type SourceCataloger interface {
	SourceCatalog(context.Context) ([]SourceCatalogDevice, error)
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

// DeviceEvent is a transient Provider occurrence. Unlike a Device snapshot it
// has no durable current value and must not be projected into the state store.
type DeviceEvent struct {
	ProviderID   string          `json:"providerId"`
	DeviceID     string          `json:"deviceId"`
	EndpointID   string          `json:"endpointId"`
	CapabilityID string          `json:"capabilityId"`
	EventID      string          `json:"eventId"`
	Name         string          `json:"name,omitempty"`
	Payload      json.RawMessage `json:"payload"`
	ObservedAt   time.Time       `json:"observedAt"`
	Sequence     uint64          `json:"sequence"`
}

type DeviceEventSubscriber interface {
	SubscribeDeviceEvents(func(DeviceEvent)) func()
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
	Diagnostics    map[string]string `json:"diagnostics,omitempty"`
}

type Inspector interface {
	ProviderInfos() []RuntimeInfo
}

type MetricsReporter interface {
	ProviderMetrics() map[string]uint64
}

// DiagnosticsReporter exposes sanitized runtime details that cannot be
// represented as counters. Implementations must never include credentials,
// broker passwords, subscription payloads, or other secrets.
type DiagnosticsReporter interface {
	ProviderDiagnostics() map[string]string
}
