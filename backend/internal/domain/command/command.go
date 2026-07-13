package command

import (
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

type Status string
type Kind string
type Outcome string

const (
	KindProperty Kind = "property"
	KindAction   Kind = "action"

	StatusQueued     Status = "queued"
	StatusSent       Status = "sent"
	StatusAccepted   Status = "accepted"
	StatusConfirmed  Status = "confirmed"
	StatusRejected   Status = "rejected"
	StatusTimeout    Status = "timeout"
	StatusSuperseded Status = "superseded"

	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
	OutcomeUnknown   Outcome = "unknown"
)

type Command struct {
	ID             string                          `json:"id"`
	Kind           Kind                            `json:"kind"`
	DeviceID       string                          `json:"deviceId"`
	EndpointID     string                          `json:"endpointId"`
	CapabilityID   string                          `json:"capabilityId"`
	PropertyID     string                          `json:"propertyId,omitempty"`
	CommandID      string                          `json:"commandId,omitempty"`
	Expected       device.PropertyValue            `json:"expected,omitempty"`
	Parameters     map[string]device.PropertyValue `json:"parameters,omitempty"`
	IdempotencyKey string                          `json:"idempotencyKey,omitempty"`
	CorrelationID  string                          `json:"correlationId,omitempty"`
	Coalesced      uint64                          `json:"coalescedRequests,omitempty"`
	Status         Status                          `json:"status"`
	Outcome        Outcome                         `json:"outcome,omitempty"`
	Error          string                          `json:"error,omitempty"`
	CreatedAt      time.Time                       `json:"createdAt"`
	UpdatedAt      time.Time                       `json:"updatedAt"`
	Deadline       time.Time                       `json:"deadline"`
}
