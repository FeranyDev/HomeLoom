package mapping

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

var targetScopeID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type BindingStage string

const (
	StageProvider BindingStage = "provider"
	StageConsumer BindingStage = "consumer"
)

// Binding is one side of the two-stage mapping graph. Provider bindings route
// an exact raw Provider property into the unified model. Consumer bindings
// route one unified-model property into an exact Consumer property. ProfileID
// is optional; an empty value means an identity conversion.
//
// EndpointID/CapabilityID/PropertyID retain their original JSON names and are
// the Provider source path. Model* fields always address the three-level
// unified model. Existing schema-v1 bindings without stage/model fields are
// treated as Provider identity-path bindings.
type Binding struct {
	ID                string       `json:"id"`
	Stage             BindingStage `json:"stage"`
	ProfileID         string       `json:"profileId,omitempty"`
	ProviderID        string       `json:"providerId,omitempty"`
	DeviceID          string       `json:"deviceId,omitempty"`
	EndpointID        string       `json:"endpointId,omitempty"`
	CapabilityID      string       `json:"capabilityId,omitempty"`
	PropertyID        string       `json:"propertyId,omitempty"`
	DeviceType        device.Type  `json:"deviceType,omitempty"`
	ModelEndpointID   string       `json:"modelEndpointId"`
	ModelCapabilityID string       `json:"modelCapabilityId"`
	ModelPropertyID   string       `json:"modelPropertyId"`
	ConsumerID        string       `json:"consumerId,omitempty"`
	TargetID          string       `json:"targetId,omitempty"`
	ConsumerDeviceID  string       `json:"consumerDeviceId,omitempty"`
	ConsumerProperty  string       `json:"consumerProperty,omitempty"`
	Enabled           bool         `json:"enabled"`
}

func (b Binding) EffectiveStage() BindingStage {
	if b.Stage == "" {
		return StageProvider
	}
	return b.Stage
}

func (b Binding) SourcePath() device.ParameterPath {
	return device.ParameterPath{EndpointID: b.EndpointID, CapabilityID: b.CapabilityID, PropertyID: b.PropertyID}
}

func (b Binding) ModelPath() device.ParameterPath {
	path := device.ParameterPath{EndpointID: b.ModelEndpointID, CapabilityID: b.ModelCapabilityID, PropertyID: b.ModelPropertyID}
	if b.EffectiveStage() == StageProvider && path.EndpointID == "" && path.CapabilityID == "" && path.PropertyID == "" {
		return b.SourcePath()
	}
	return path
}

// Key identifies the source side of a route. Provider source properties may
// fan out to multiple unified-model properties; Consumer targets remain
// unique inside their target virtual-device scope.
func (b Binding) Key() string {
	if b.EffectiveStage() == StageConsumer {
		return strings.Join([]string{string(StageConsumer), b.ProviderID, b.DeviceID, b.TargetID, b.ConsumerDeviceID, b.ConsumerID, b.ConsumerProperty}, "\x00")
	}
	return strings.Join([]string{string(StageProvider), b.ProviderID, b.DeviceID, b.EndpointID, b.CapabilityID, b.PropertyID}, "\x00")
}

// ModelKey identifies the unified-model side and is used for reverse Provider
// writes. It intentionally includes the concrete Provider device identity.
func (b Binding) ModelKey() string {
	path := b.ModelPath()
	return strings.Join([]string{string(StageProvider), b.ProviderID, b.DeviceID, path.EndpointID, path.CapabilityID, path.PropertyID}, "\x00")
}

func ValidateBinding(b Binding) error {
	fields := make(map[string]string)
	if !device.ValidStableID(b.ID) {
		fields["binding.id"] = "must be a stable lowercase identifier"
	}
	if b.ProfileID != "" && !device.ValidStableID(b.ProfileID) {
		fields["binding.profileId"] = "must be empty or a stable lowercase identifier"
	}
	stage := b.EffectiveStage()
	if stage != StageProvider && stage != StageConsumer {
		fields["binding.stage"] = "must be provider or consumer"
	}
	model := b.ModelPath()
	for name, value := range map[string]string{
		"binding.modelEndpointId":   model.EndpointID,
		"binding.modelCapabilityId": model.CapabilityID,
		"binding.modelPropertyId":   model.PropertyID,
	} {
		if !device.ValidStableID(value) {
			fields[name] = "must be a stable lowercase identifier"
		}
	}
	if stage == StageProvider {
		for name, value := range map[string]string{
			"binding.providerId": b.ProviderID, "binding.deviceId": b.DeviceID,
			"binding.endpointId": b.EndpointID, "binding.capabilityId": b.CapabilityID,
			"binding.propertyId": b.PropertyID,
		} {
			if !device.ValidStableID(value) {
				fields[name] = "must be a stable lowercase identifier"
			}
		}
		if b.DeviceType != "" {
			if !device.ValidStableID(string(b.DeviceType)) {
				fields["binding.deviceType"] = "must be a stable lowercase identifier"
			}
		}
	} else if stage == StageConsumer {
		if !device.ValidStableID(b.ProviderID) {
			fields["binding.providerId"] = "must be a stable lowercase identifier"
		}
		if !device.ValidStableID(b.DeviceID) {
			fields["binding.deviceId"] = "must be a stable lowercase identifier"
		}
		if !device.ValidStableID(b.ConsumerID) {
			fields["binding.consumerId"] = "must be a stable lowercase identifier"
		}
		if (b.TargetID == "") != (b.ConsumerDeviceID == "") {
			fields["binding.targetId"] = "targetId and consumerDeviceId must be provided together"
		}
		if b.TargetID != "" && !targetScopeID.MatchString(b.TargetID) {
			fields["binding.targetId"] = "may contain only letters, numbers, underscores and hyphens"
		}
		if b.ConsumerDeviceID != "" && !targetScopeID.MatchString(b.ConsumerDeviceID) {
			fields["binding.consumerDeviceId"] = "may contain only letters, numbers, underscores and hyphens"
		}
		if !device.ValidStableID(string(b.DeviceType)) {
			fields["binding.deviceType"] = "must be a stable lowercase identifier"
		}
		if strings.TrimSpace(b.ConsumerProperty) == "" || len(b.ConsumerProperty) > 160 {
			fields["binding.consumerProperty"] = "must be between 1 and 160 characters"
		}
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func BindingPath(b Binding) string {
	if b.EffectiveStage() == StageConsumer {
		return fmt.Sprintf("%s/%s/%s -> %s/%s/%s/%s", b.ProviderID, b.DeviceID, b.ModelPath(), b.TargetID, b.ConsumerDeviceID, b.ConsumerID, b.ConsumerProperty)
	}
	return fmt.Sprintf("%s/%s/%s -> %s", b.ProviderID, b.DeviceID, b.SourcePath(), b.ModelPath())
}
