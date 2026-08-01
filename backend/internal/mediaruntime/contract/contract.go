// Package contract defines Core's in-process versioned media state.
package contract

import (
	"encoding/json"
	"time"
)

// StreamSpec mirrors the Core domain contract. Protocol-specific Options are
// a tagged JSON object and may only contain logical configuration.
type StreamSpec struct {
	SchemaVersion int             `json:"schemaVersion"`
	ID            string          `json:"id"`
	DeviceID      string          `json:"deviceId"`
	Protocol      string          `json:"protocol"`
	CredentialRef string          `json:"credentialRef,omitempty"`
	Profile       string          `json:"profile"`
	Mode          string          `json:"mode"`
	Audio         bool            `json:"audio"`
	Talkback      bool            `json:"talkback"`
	Options       json.RawMessage `json:"options,omitempty"`
}

type ReplayParams struct {
	SchemaVersion int          `json:"schemaVersion"`
	Generation    uint64       `json:"generation"`
	Revision      uint64       `json:"revision"`
	Streams       []StreamSpec `json:"streams"`
}

type UpsertParams struct {
	SchemaVersion int        `json:"schemaVersion"`
	Generation    uint64     `json:"generation"`
	Revision      uint64     `json:"revision"`
	Stream        StreamSpec `json:"stream"`
}

type DeleteParams struct {
	SchemaVersion int    `json:"schemaVersion"`
	Generation    uint64 `json:"generation"`
	Revision      uint64 `json:"revision"`
	StreamID      string `json:"streamId"`
}

type ApplyResult struct {
	Applied    bool   `json:"applied"`
	Generation uint64 `json:"generation"`
	Revision   uint64 `json:"revision"`
}

// AuthorizationRequest and AuthorizationResponse mirror backend/domain/media.
// SecretMaterial is intentionally only a transient JSON value in memory.
type AuthorizationRequest struct {
	SchemaVersion  int             `json:"schemaVersion"`
	RequestID      string          `json:"requestId"`
	DeviceID       string          `json:"deviceId"`
	Protocol       string          `json:"protocol"`
	Purpose        string          `json:"purpose"`
	Attempt        int             `json:"attempt"`
	ClientMaterial json.RawMessage `json:"clientMaterial,omitempty"`
	SessionOffer   []byte          `json:"sessionOffer,omitempty"`
}

type EndpointSpec struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Path     string `json:"path,omitempty"`
}

type AuthorizationResponse struct {
	SchemaVersion  int             `json:"schemaVersion"`
	LeaseID        string          `json:"leaseId"`
	ExpiresAt      time.Time       `json:"expiresAt"`
	Endpoint       EndpointSpec    `json:"endpoint"`
	AuthType       string          `json:"authType"`
	PublicMaterial json.RawMessage `json:"publicMaterial,omitempty"`
	SecretMaterial json.RawMessage `json:"secretMaterial,omitempty"`
	SessionAnswer  []byte          `json:"sessionAnswer,omitempty"`
	Reusable       bool            `json:"reusable"`
	MaxUses        int             `json:"maxUses"`
}
