package audit

import "time"

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
)

// Event contains operation metadata only. Request bodies and configuration
// values are deliberately excluded so credentials cannot enter the audit log.
type Event struct {
	ID            int64     `json:"id"`
	CorrelationID string    `json:"correlationId"`
	Actor         string    `json:"actor"`
	Action        string    `json:"action"`
	ResourceType  string    `json:"resourceType"`
	ResourceID    string    `json:"resourceId,omitempty"`
	Method        string    `json:"method"`
	Route         string    `json:"route"`
	Status        int       `json:"status"`
	Outcome       Outcome   `json:"outcome"`
	CreatedAt     time.Time `json:"createdAt"`
}
