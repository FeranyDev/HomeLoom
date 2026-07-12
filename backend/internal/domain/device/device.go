package device

import "time"

type Type string

const (
	TypeSwitch            Type = "switch"
	TypeTemperatureSensor Type = "temperature-sensor"
)

type State struct {
	Power       *bool    `json:"power,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type Device struct {
	ID           string    `json:"id"`
	ProviderID   string    `json:"providerId"`
	Name         string    `json:"name"`
	Type         Type      `json:"type"`
	Online       bool      `json:"online"`
	State        State     `json:"state"`
	LastUpdateAt time.Time `json:"lastUpdateAt"`
}
