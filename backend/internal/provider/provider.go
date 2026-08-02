package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/media"
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

// ConnectionTester is an optional capability for Providers whose Initialize
// method can succeed while retaining an offline, recoverable device catalog.
// TestConnection must be safe before Initialize, perform a live check, and
// return an error when the configured connection cannot reach any device.
type ConnectionTester interface {
	TestConnection(context.Context) error
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

// DiscoveryCandidate is a transient device identity returned by a Provider
// network scan. It is intentionally separate from device.Device: a scan is
// used while editing a Provider and must not publish or mutate the configured
// runtime device catalog.
type DiscoveryCandidate struct {
	ID       string            `json:"id,omitempty"`
	Provider string            `json:"providerType"`
	Name     string            `json:"name"`
	Host     string            `json:"host"`
	Port     int               `json:"port"`
	MAC      string            `json:"mac"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// DiscoveryScanner is an optional Provider capability for transient LAN or
// protocol discovery. It must not be implemented by forwarding to
// DiscoverDevices, which represents the already configured runtime catalog.
type DiscoveryScanner interface {
	Scan(context.Context) ([]DiscoveryCandidate, error)
}

// HiddenDeviceSource marks Provider-owned identities that exist only as
// internal capability sources. The runtime keeps their routes available for
// delegated reads, writes and commands, but never publishes them as separate
// Device Center cards.
type HiddenDeviceSource interface {
	HiddenDeviceIDs() []string
}

// CapabilityBinding joins the non-media capabilities of a source Device to a
// canonical Device owned by another Provider. The binding contains identities
// only: credentials and protocol-specific identifiers remain in the source
// Provider.
type CapabilityBinding struct {
	DeviceID       string
	ProviderID     string
	SourceDeviceID string
}

// CapabilityBindingSource is intentionally provider-agnostic. It lets the
// runtime compose a Camera with a control Provider without making either
// Provider import the other's implementation.
type CapabilityBindingSource interface {
	CapabilityBindings() []CapabilityBinding
}

// CapabilityAvailability reports reachability of a delegated capability
// independently from the canonical Device. It is used when media remains
// online while an auxiliary control Provider is unavailable.
type CapabilityAvailability struct {
	ProviderID   string
	DeviceID     string
	EndpointID   string
	CapabilityID string
	Available    bool
}

type CapabilityAvailabilityReporter interface {
	CapabilityAvailabilities() []CapabilityAvailability
}

type CapabilityAvailabilitySubscriber interface {
	SubscribeCapabilityAvailability(func(CapabilityAvailability)) func()
}

// MediaSourceDiscoverer optionally exposes media extensions for camera Devices.
// The standard Device remains the authority for identity, room, availability,
// commands, and events; a discovered source only describes how media is
// acquired.
type MediaSourceDiscoverer interface {
	DiscoverMediaSources(context.Context) ([]media.MediaSourceDescriptor, error)
}

// MediaSourceRefresher refreshes the protocol-specific media extension for one
// already discovered Device without creating a parallel camera identity.
type MediaSourceRefresher interface {
	RefreshMediaSource(context.Context, string) (*media.MediaSourceDescriptor, error)
}

// MediaAuthorizer is the optional short-lived authorization capability for a
// Provider. Durable credentials stay owned by the Core/Provider configuration;
// implementations must never return them in AuthorizationResponse.
type MediaAuthorizer interface {
	AcquireMediaAuthorization(context.Context, media.AuthorizationRequest) (*media.AuthorizationResponse, error)
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

// PropertyInterest identifies a Provider-native property that is actively
// consumed by the mapping graph. Providers may use this hint to avoid polling
// every field in a complete source catalog merely to keep its mapping UI
// populated. An empty ProviderID is only meaningful inside a concrete
// Provider; aggregators use it to route interests to the owning instance.
type PropertyInterest struct {
	ProviderID   string
	DeviceID     string
	EndpointID   string
	CapabilityID string
	PropertyID   string
}

// PropertyInterestSetter receives the complete current interest set. Calls
// replace previous interests and must not restart Provider transports.
type PropertyInterestSetter interface {
	SetPropertyInterests([]PropertyInterest)
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
