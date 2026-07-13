package command

import "time"

import "github.com/feranydev/homeloom/backend/internal/domain/device"

type Status string

const (
	StatusQueued     Status = "queued"
	StatusSent       Status = "sent"
	StatusAccepted   Status = "accepted"
	StatusConfirmed  Status = "confirmed"
	StatusRejected   Status = "rejected"
	StatusTimeout    Status = "timeout"
	StatusSuperseded Status = "superseded"
)

type Command struct {
	ID           string               `json:"id"`
	DeviceID     string               `json:"deviceId"`
	EndpointID   string               `json:"endpointId"`
	CapabilityID string               `json:"capabilityId"`
	PropertyID   string               `json:"propertyId"`
	Expected     device.PropertyValue `json:"expected"`
	Status       Status               `json:"status"`
	Error        string               `json:"error,omitempty"`
	CreatedAt    time.Time            `json:"createdAt"`
	UpdatedAt    time.Time            `json:"updatedAt"`
	Deadline     time.Time            `json:"deadline"`
}
