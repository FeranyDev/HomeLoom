package target

import "github.com/feranydev/homeloom/backend/internal/domain/device"

// VirtualDevice is a Consumer-side device owned by one Target instance. It
// keeps HomeKit/Matter identity separate from the unified source device.
type VirtualDevice struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Type           device.Type `json:"type"`
	SourceDeviceID string      `json:"sourceDeviceId"`
	Enabled        bool        `json:"enabled"`
}

type Config struct {
	ID        string
	Type      string
	Name      string
	Enabled   bool
	Address   string
	Pin       string
	SetupID   string
	StorePath string
	DeviceIDs []string
	Devices   []VirtualDevice
}
