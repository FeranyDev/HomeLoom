package audit

import "time"

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
)

// Detail is an allow-listed, human-readable part of an audited operation.
// It must never contain credentials, configuration payloads, pairing codes, or
// other secrets.
type Detail struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Event contains operation metadata and a small set of allow-listed details.
// Request bodies and configuration values are deliberately excluded so
// credentials cannot enter the audit log.
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
	Details       []Detail  `json:"details,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}
