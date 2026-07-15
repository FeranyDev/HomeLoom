package virtual

import (
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

// contractDevice materializes the complete built-in contract for model types
// that do not require bespoke virtual behavior. This keeps the virtual
// Provider useful as the model catalog grows and makes every optional field
// visible to mapping configuration.
func contractDevice(id, providerID, name string, deviceType device.Type, online bool) (device.Device, error) {
	contract, ok := device.ModelContractFor(deviceType)
	if !ok {
		return device.Device{}, fmt.Errorf("unsupported unified model %q", deviceType)
	}
	capabilityIndexes := make(map[string]int)
	endpoint := device.Endpoint{ID: "main", Name: "主端点", Type: string(deviceType)}
	for _, parameter := range contract.Parameters {
		if parameter.Path.EndpointID != "main" {
			return device.Device{}, fmt.Errorf("virtual contract endpoint %q is unsupported", parameter.Path.EndpointID)
		}
		index, exists := capabilityIndexes[parameter.Path.CapabilityID]
		if !exists {
			index = len(endpoint.Capabilities)
			capabilityIndexes[parameter.Path.CapabilityID] = index
			endpoint.Capabilities = append(endpoint.Capabilities, device.Capability{ID: parameter.Path.CapabilityID, Type: parameter.Path.CapabilityID})
		}
		definition := device.PropertyDefinition{
			ID: parameter.Path.PropertyID, Name: parameter.Name, Type: parameter.Type,
			ParameterLevel: parameter.Level, Unit: parameter.Unit, Readable: parameter.Readable,
			Writable: parameter.Writable, Notifiable: parameter.Notifiable, Min: parameter.Min,
			Max: parameter.Max, Step: parameter.Step, Enum: append([]string(nil), parameter.Enum...),
			StaleAfterSeconds: parameter.StaleAfterSeconds,
		}
		if definition.StaleAfterSeconds == 0 {
			definition.StaleAfterSeconds = 30
		}
		value, err := virtualParameterValue(parameter)
		if err != nil {
			return device.Device{}, err
		}
		endpoint.Capabilities[index].Properties = append(endpoint.Capabilities[index].Properties, device.Property{Definition: definition, Value: value})
	}
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: id, ProviderID: providerID, Name: name, Type: deviceType, Sequence: 1, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{endpoint}}
	item.SetOnline(online)
	if err := item.NormalizeModelParameters(); err != nil {
		return device.Device{}, err
	}
	return item, nil
}

func virtualParameterValue(parameter device.ModelParameter) (device.PropertyValue, error) {
	path := parameter.Path.CapabilityID + "/" + parameter.Path.PropertyID
	switch parameter.Type {
	case device.ValueTypeBool:
		return device.BoolValue(false), nil
	case device.ValueTypeString:
		return device.StringValue(""), nil
	case device.ValueTypeEnum:
		if len(parameter.Enum) == 0 {
			return device.PropertyValue{}, fmt.Errorf("enum parameter %s has no values", parameter.Path)
		}
		return device.EnumValue(parameter.Enum[0]), nil
	case device.ValueTypeInt:
		value := int64(0)
		if parameter.Path.CapabilityID == "battery" && parameter.Path.PropertyID == "level" {
			value = 88
		} else if parameter.Min != nil && *parameter.Min > 0 {
			value = int64(*parameter.Min)
		}
		return device.IntValue(value), nil
	case device.ValueTypeNumber:
		value := 0.0
		switch path {
		case "illuminance/current-illuminance":
			value = 320
		case "temperature/current-temperature":
			value = 23.5
		case "temperature/target-temperature":
			value = 22
		case "temperature/heating-threshold":
			value = 20
		case "temperature/cooling-threshold":
			value = 26
		case "humidity/current-humidity":
			value = 48
		case "humidity/target-humidity":
			value = 50
		case "speaker/volume":
			value = 35
		case "air-conditioner/rotation-speed":
			value = 40
		case "humidifier-dehumidifier/water-level":
			value = 75
		case "filter/life-level":
			value = 82
		case "robot-vacuum/fan-speed":
			value = 50
		}
		if parameter.Min != nil && value < *parameter.Min {
			value = *parameter.Min
		}
		if parameter.Max != nil && value > *parameter.Max {
			value = *parameter.Max
		}
		return device.NumberValue(value), nil
	default:
		return device.PropertyValue{}, fmt.Errorf("unsupported parameter type %q", parameter.Type)
	}
}
