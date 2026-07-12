package state

import "time"

type ValueKind string
type Source string
type Quality string

const (
	KindBool   ValueKind = "bool"
	KindNumber ValueKind = "number"

	SourceReported        Source = "reported"
	SourcePolled          Source = "polled"
	SourceOptimistic      Source = "optimistic"
	SourcePersistentCache Source = "persistent-cache"

	QualityConfirmed  Quality = "confirmed"
	QualityReported   Quality = "reported"
	QualityPolled     Quality = "polled"
	QualityOptimistic Quality = "optimistic"
	QualityStale      Quality = "stale"
	QualityUnknown    Quality = "unknown"
)

type Value struct {
	Kind   ValueKind `json:"kind"`
	Bool   *bool     `json:"bool,omitempty"`
	Number *float64  `json:"number,omitempty"`
}

func BoolValue(value bool) Value      { return Value{Kind: KindBool, Bool: &value} }
func NumberValue(value float64) Value { return Value{Kind: KindNumber, Number: &value} }

type Key struct {
	DeviceID     string `json:"deviceId"`
	EndpointID   string `json:"endpointId"`
	CapabilityID string `json:"capabilityId"`
	PropertyID   string `json:"propertyId"`
}

type StateValue struct {
	Key              Key       `json:"key"`
	Value            Value     `json:"value"`
	ProviderID       string    `json:"providerId"`
	Source           Source    `json:"source"`
	ObservedAt       time.Time `json:"observedAt"`
	ReceivedAt       time.Time `json:"receivedAt"`
	ExpiresAt        time.Time `json:"expiresAt,omitempty"`
	Sequence         uint64    `json:"sequence"`
	Version          uint64    `json:"version"`
	Quality          Quality   `json:"quality"`
	PendingCommandID string    `json:"pendingCommandId,omitempty"`
}
