package state

import (
	"encoding/json"
	"time"
)

type ValueKind string
type Source string
type Quality string
type UnavailableReason string

const (
	KindBool   ValueKind = "bool"
	KindInt    ValueKind = "int"
	KindNumber ValueKind = "number"
	KindString ValueKind = "string"
	KindEnum   ValueKind = "enum"

	SourceReported        Source = "reported"
	SourcePolled          Source = "polled"
	SourceOptimistic      Source = "optimistic"
	SourcePersistentCache Source = "persistent-cache"
	SourceUnknown         Source = "unknown"

	QualityConfirmed  Quality = "confirmed"
	QualityReported   Quality = "reported"
	QualityPolled     Quality = "polled"
	QualityOptimistic Quality = "optimistic"
	QualityStale      Quality = "stale"
	QualityUnknown    Quality = "unknown"

	UnavailableNeverReported          UnavailableReason = "never-reported"
	UnavailableDeviceOffline          UnavailableReason = "device-offline"
	UnavailableAvailabilityUnknown    UnavailableReason = "availability-unknown"
	UnavailableDisabled               UnavailableReason = "disabled"
	UnavailableRemoved                UnavailableReason = "removed"
	UnavailableExpired                UnavailableReason = "expired"
	UnavailableStale                  UnavailableReason = "stale"
	UnavailableCommandUnconfirmed     UnavailableReason = "command-unconfirmed"
	UnavailableControlProviderOffline UnavailableReason = "control-provider-offline"
)

type Value struct {
	Kind   ValueKind `json:"kind"`
	Bool   *bool     `json:"bool,omitempty"`
	Int    *int64    `json:"int,omitempty"`
	Number *float64  `json:"number,omitempty"`
	String *string   `json:"string,omitempty"`
}

func BoolValue(value bool) Value      { return Value{Kind: KindBool, Bool: &value} }
func IntValue(value int64) Value      { return Value{Kind: KindInt, Int: &value} }
func NumberValue(value float64) Value { return Value{Kind: KindNumber, Number: &value} }
func StringValue(value string) Value  { return Value{Kind: KindString, String: &value} }
func EnumValue(value string) Value    { return Value{Kind: KindEnum, String: &value} }

type Key struct {
	DeviceID     string `json:"deviceId"`
	EndpointID   string `json:"endpointId"`
	CapabilityID string `json:"capabilityId"`
	PropertyID   string `json:"propertyId"`
}

type StateValue struct {
	Key               Key               `json:"key"`
	Value             Value             `json:"-"`
	ProviderID        string            `json:"providerId"`
	Source            Source            `json:"source"`
	ObservedAt        time.Time         `json:"observedAt"`
	ReceivedAt        time.Time         `json:"receivedAt"`
	ExpiresAt         time.Time         `json:"expiresAt,omitzero"`
	Sequence          uint64            `json:"sequence"`
	Version           uint64            `json:"version"`
	Quality           Quality           `json:"quality"`
	Known             bool              `json:"known"`
	Available         bool              `json:"available"`
	UnavailableReason UnavailableReason `json:"unavailableReason,omitempty"`
	TraceID           string            `json:"traceId,omitempty"`
	PendingCommandID  string            `json:"pendingCommandId,omitempty"`
}

func (s StateValue) MarshalJSON() ([]byte, error) {
	type alias StateValue
	var value any
	if s.Known {
		value = s.Value
	}
	return json.Marshal(struct {
		alias
		Value any `json:"value"`
	}{alias: alias(s), Value: value})
}
