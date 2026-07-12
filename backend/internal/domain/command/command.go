package command

import "time"

type Status string

const (
	StatusQueued    Status = "queued"
	StatusSent      Status = "sent"
	StatusAccepted  Status = "accepted"
	StatusConfirmed Status = "confirmed"
	StatusRejected  Status = "rejected"
	StatusTimeout   Status = "timeout"
)

type Command struct {
	ID         string    `json:"id"`
	DeviceID   string    `json:"deviceId"`
	PropertyID string    `json:"propertyId"`
	BoolValue  *bool     `json:"boolValue,omitempty"`
	Status     Status    `json:"status"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Deadline   time.Time `json:"deadline"`
}
