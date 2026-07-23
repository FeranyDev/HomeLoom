package target

import (
	"errors"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

var (
	ErrMatterEndpointIDsExhausted = errors.New("Matter endpoint IDs are exhausted")
	ErrMatterDeviceTypeChange     = errors.New("Matter endpoint device type change requires explicit confirmation")
)

type MatterRuntimeValue struct {
	TargetID  string
	Key       string
	Value     []byte
	Sensitive bool
	UpdatedAt time.Time
}

type MatterEndpointIdentity struct {
	TargetID         string
	ConsumerDeviceID string
	EndpointID       uint16
	DeviceType       device.Type
	Tombstone        bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
