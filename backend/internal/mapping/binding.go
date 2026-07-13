package mapping

import (
	"fmt"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

// Binding attaches one mapping profile to one exact provider property path.
// Exact paths keep the first runtime implementation deterministic; broader
// capability and target matching can be layered on without changing this shape.
type Binding struct {
	ID           string `json:"id"`
	ProfileID    string `json:"profileId"`
	ProviderID   string `json:"providerId"`
	DeviceID     string `json:"deviceId"`
	EndpointID   string `json:"endpointId"`
	CapabilityID string `json:"capabilityId"`
	PropertyID   string `json:"propertyId"`
	Enabled      bool   `json:"enabled"`
}

func (b Binding) Key() string {
	return strings.Join([]string{b.ProviderID, b.DeviceID, b.EndpointID, b.CapabilityID, b.PropertyID}, "\x00")
}

func ValidateBinding(b Binding) error {
	fields := make(map[string]string)
	for name, value := range map[string]string{
		"binding.id": b.ID, "binding.profileId": b.ProfileID, "binding.providerId": b.ProviderID,
		"binding.deviceId": b.DeviceID, "binding.endpointId": b.EndpointID,
		"binding.capabilityId": b.CapabilityID, "binding.propertyId": b.PropertyID,
	} {
		if !device.ValidStableID(value) {
			fields[name] = "must be a stable lowercase identifier"
		}
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func BindingPath(b Binding) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", b.ProviderID, b.DeviceID, b.EndpointID, b.CapabilityID, b.PropertyID)
}
