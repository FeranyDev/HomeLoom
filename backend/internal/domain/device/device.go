package device

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"
)

type Type string
type ValueType string
type Availability string
type ParameterLevel string
type RuntimeMode string

const (
	SchemaVersion = 1

	TypeSwitch               Type = "switch"
	TypeSinglePropertySensor Type = "single-property-sensor"
	// Deprecated sensor types are retained only for reading legacy Provider
	// configurations and snapshots. NormalizeModelParameters migrates them to
	// TypeSinglePropertySensor before they enter the unified registry.
	TypeTemperatureSensor         Type = "temperature-sensor"
	TypeTemperatureHumiditySensor Type = "temperature-humidity-sensor"
	TypeLightbulb                 Type = "lightbulb"
	TypeOutlet                    Type = "outlet"
	TypeHumiditySensor            Type = "humidity-sensor"
	TypeContactSensor             Type = "contact-sensor"
	TypeMotionSensor              Type = "motion-sensor"
	TypeFan                       Type = "fan"
	TypeAirPurifier               Type = "air-purifier"
	TypeWindowCovering            Type = "window-covering"
	TypeIlluminanceSensor         Type = "illuminance-sensor"
	TypeOccupancySensor           Type = "occupancy-sensor"
	TypeLeakSensor                Type = "leak-sensor"
	TypeSmokeSensor               Type = "smoke-sensor"
	TypeCarbonMonoxideSensor      Type = "carbon-monoxide-sensor"
	TypeCarbonDioxideSensor       Type = "carbon-dioxide-sensor"
	TypeAirQualitySensor          Type = "air-quality-sensor"
	TypeThermostat                Type = "thermostat"
	TypeAirConditioner            Type = "air-conditioner"
	TypeHeaterCooler              Type = "heater-cooler"
	TypeHumidifierDehumidifier    Type = "humidifier-dehumidifier"
	TypeLock                      Type = "lock"
	TypeGarageDoor                Type = "garage-door"
	TypeSecuritySystem            Type = "security-system"
	TypeValve                     Type = "valve"
	TypeSpeaker                   Type = "speaker"
	TypeRobotVacuum               Type = "robot-vacuum"
)

const (
	ParameterRequired ParameterLevel = "required"
	ParameterOptional ParameterLevel = "optional"
	ParameterCustom   ParameterLevel = "custom"
)

const (
	AvailabilityOnline  Availability = "online"
	AvailabilityOffline Availability = "offline"
	AvailabilityUnknown Availability = "unknown"
)

const (
	RuntimeModePending RuntimeMode = "pending"
	RuntimeModeLocal   RuntimeMode = "local"
	RuntimeModeCloud   RuntimeMode = "cloud"
)

var stableIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)

var ErrInvalidModel = errors.New("invalid device model")

const (
	ValueTypeBool   ValueType = "bool"
	ValueTypeInt    ValueType = "int"
	ValueTypeNumber ValueType = "number"
	ValueTypeString ValueType = "string"
	ValueTypeEnum   ValueType = "enum"
)

type PropertyValue struct {
	Type   ValueType `json:"type"`
	Bool   *bool     `json:"bool,omitempty"`
	Int    *int64    `json:"int,omitempty"`
	Number *float64  `json:"number,omitempty"`
	String *string   `json:"string,omitempty"`
}

func BoolValue(value bool) PropertyValue { return PropertyValue{Type: ValueTypeBool, Bool: &value} }
func IntValue(value int64) PropertyValue { return PropertyValue{Type: ValueTypeInt, Int: &value} }
func NumberValue(value float64) PropertyValue {
	return PropertyValue{Type: ValueTypeNumber, Number: &value}
}
func StringValue(value string) PropertyValue {
	return PropertyValue{Type: ValueTypeString, String: &value}
}
func EnumValue(value string) PropertyValue {
	return PropertyValue{Type: ValueTypeEnum, String: &value}
}

type PropertyDefinition struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Type              ValueType      `json:"type"`
	ParameterLevel    ParameterLevel `json:"parameterLevel,omitempty"`
	Unit              string         `json:"unit,omitempty"`
	Readable          bool           `json:"readable"`
	Writable          bool           `json:"writable"`
	Notifiable        bool           `json:"notifiable"`
	Min               *float64       `json:"min,omitempty"`
	Max               *float64       `json:"max,omitempty"`
	Step              *float64       `json:"step,omitempty"`
	Enum              []string       `json:"enum,omitempty"`
	StaleAfterSeconds int            `json:"staleAfterSeconds,omitempty"`
}

type Property struct {
	Definition PropertyDefinition `json:"definition"`
	Value      PropertyValue      `json:"value"`
}

type Capability struct {
	ID         string              `json:"id"`
	Type       string              `json:"type"`
	Properties []Property          `json:"properties"`
	Commands   []CommandDefinition `json:"commands,omitempty"`
	Events     []EventDefinition   `json:"events,omitempty"`
}

type CommandParameter struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Type     ValueType `json:"type"`
	Required bool      `json:"required"`
}

type CommandDefinition struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Idempotent bool               `json:"idempotent"`
	Parameters []CommandParameter `json:"parameters,omitempty"`
}

type EventDefinition struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Payload ValueType `json:"payload"`
}

type Endpoint struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Type         string       `json:"type"`
	Capabilities []Capability `json:"capabilities"`
}

type Device struct {
	SchemaVersion int          `json:"schemaVersion"`
	ID            string       `json:"id"`
	ProviderID    string       `json:"providerId"`
	Name          string       `json:"name"`
	Type          Type         `json:"type"`
	Availability  Availability `json:"availability"`
	// Online is retained as a compatibility projection for schema v1 clients.
	Online       bool        `json:"online"`
	Sequence     uint64      `json:"sequence,omitempty"`
	Disabled     bool        `json:"disabled,omitempty"`
	Removed      bool        `json:"removed,omitempty"`
	RuntimeMode  RuntimeMode `json:"runtimeMode,omitempty"`
	Endpoints    []Endpoint  `json:"endpoints"`
	LastUpdateAt time.Time   `json:"lastUpdateAt"`
}

func ValidStableID(value string) bool { return stableIDPattern.MatchString(value) }

func (d Device) EffectiveAvailability() Availability {
	switch d.Availability {
	case AvailabilityOnline, AvailabilityOffline, AvailabilityUnknown:
		return d.Availability
	default:
		if d.Online {
			return AvailabilityOnline
		}
		return AvailabilityOffline
	}
}

func (d Device) IsOnline() bool { return d.EffectiveAvailability() == AvailabilityOnline }

func (d *Device) SetAvailability(value Availability) {
	d.Availability = value
	d.Online = value == AvailabilityOnline
}

func (d *Device) SetOnline(online bool) {
	if online {
		d.SetAvailability(AvailabilityOnline)
	} else {
		d.SetAvailability(AvailabilityOffline)
	}
}

func (d *Device) NormalizeAvailability() { d.SetAvailability(d.EffectiveAvailability()) }

func (d Device) ValidateStructure() error {
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema version %d", ErrInvalidModel, d.SchemaVersion)
	}
	if !ValidStableID(d.ID) || !ValidStableID(d.ProviderID) {
		return fmt.Errorf("%w: invalid device or provider id", ErrInvalidModel)
	}
	if d.Availability != "" && d.Availability != AvailabilityOnline && d.Availability != AvailabilityOffline && d.Availability != AvailabilityUnknown {
		return fmt.Errorf("%w: invalid availability %q", ErrInvalidModel, d.Availability)
	}
	if d.Availability != "" && d.Online != (d.Availability == AvailabilityOnline) {
		return fmt.Errorf("%w: online compatibility projection conflicts with availability", ErrInvalidModel)
	}
	if d.RuntimeMode != "" && d.RuntimeMode != RuntimeModePending && d.RuntimeMode != RuntimeModeLocal && d.RuntimeMode != RuntimeModeCloud {
		return fmt.Errorf("%w: invalid runtime mode %q", ErrInvalidModel, d.RuntimeMode)
	}
	if (d.Disabled || d.Removed) && d.IsOnline() {
		return fmt.Errorf("%w: disabled or removed device cannot be online", ErrInvalidModel)
	}
	endpointIDs := make(map[string]struct{}, len(d.Endpoints))
	for _, endpoint := range d.Endpoints {
		if !ValidStableID(endpoint.ID) {
			return fmt.Errorf("%w: invalid endpoint id %q", ErrInvalidModel, endpoint.ID)
		}
		if _, duplicate := endpointIDs[endpoint.ID]; duplicate {
			return fmt.Errorf("%w: duplicate endpoint id %q", ErrInvalidModel, endpoint.ID)
		}
		endpointIDs[endpoint.ID] = struct{}{}
		capabilityIDs := make(map[string]struct{}, len(endpoint.Capabilities))
		for _, capability := range endpoint.Capabilities {
			if !ValidStableID(capability.ID) {
				return fmt.Errorf("%w: invalid capability id %q", ErrInvalidModel, capability.ID)
			}
			if _, duplicate := capabilityIDs[capability.ID]; duplicate {
				return fmt.Errorf("%w: duplicate capability id %q", ErrInvalidModel, capability.ID)
			}
			capabilityIDs[capability.ID] = struct{}{}
			propertyIDs := make(map[string]struct{}, len(capability.Properties))
			for _, property := range capability.Properties {
				if !ValidStableID(property.Definition.ID) {
					return fmt.Errorf("%w: invalid property id %q", ErrInvalidModel, property.Definition.ID)
				}
				if _, duplicate := propertyIDs[property.Definition.ID]; duplicate {
					return fmt.Errorf("%w: duplicate property id %q", ErrInvalidModel, property.Definition.ID)
				}
				propertyIDs[property.Definition.ID] = struct{}{}
				if err := property.Validate(); err != nil {
					return fmt.Errorf("%w: %s: %v", ErrInvalidModel, property.Definition.ID, err)
				}
			}
			definitionIDs := make(map[string]struct{}, len(capability.Commands)+len(capability.Events))
			for _, command := range capability.Commands {
				if err := claimStableID(definitionIDs, command.ID, "command"); err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidModel, err)
				}
				parameterIDs := make(map[string]struct{}, len(command.Parameters))
				for _, parameter := range command.Parameters {
					if err := claimStableID(parameterIDs, parameter.ID, "command parameter"); err != nil {
						return fmt.Errorf("%w: %v", ErrInvalidModel, err)
					}
					if !validValueType(parameter.Type) {
						return fmt.Errorf("%w: unsupported command parameter type %q", ErrInvalidModel, parameter.Type)
					}
				}
			}
			for _, event := range capability.Events {
				if err := claimStableID(definitionIDs, event.ID, "event"); err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidModel, err)
				}
				if !validValueType(event.Payload) {
					return fmt.Errorf("%w: unsupported event payload type %q", ErrInvalidModel, event.Payload)
				}
			}
		}
	}
	return nil
}

// Validate applies both the transport-safe structural contract and the
// unified publisher contract. Provider managers use ValidateStructure before
// configurable mapping; the Core uses Validate after Provider → model routing.
func (d Device) Validate() error {
	if err := d.ValidateStructure(); err != nil {
		return err
	}
	if err := validatePublisherModel(d); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidModel, err)
	}
	return nil
}

func claimStableID(seen map[string]struct{}, id, kind string) error {
	if !ValidStableID(id) {
		return fmt.Errorf("invalid %s id %q", kind, id)
	}
	if _, duplicate := seen[id]; duplicate {
		return fmt.Errorf("duplicate %s id %q", kind, id)
	}
	seen[id] = struct{}{}
	return nil
}

func validValueType(value ValueType) bool {
	return value == ValueTypeBool || value == ValueTypeInt || value == ValueTypeNumber || value == ValueTypeString || value == ValueTypeEnum
}

func (p Property) Validate() error {
	value := p.Value
	if p.Definition.ParameterLevel != "" && p.Definition.ParameterLevel != ParameterRequired && p.Definition.ParameterLevel != ParameterOptional && p.Definition.ParameterLevel != ParameterCustom {
		return fmt.Errorf("unsupported parameter level %q", p.Definition.ParameterLevel)
	}
	if p.Definition.Min != nil && p.Definition.Max != nil && *p.Definition.Min > *p.Definition.Max {
		return errors.New("minimum exceeds maximum")
	}
	if p.Definition.Step != nil && *p.Definition.Step <= 0 {
		return errors.New("step must be positive")
	}
	if p.Definition.Type == ValueTypeEnum && len(p.Definition.Enum) == 0 {
		return errors.New("enum options are required")
	}
	if p.Definition.Type == ValueTypeInt {
		for name, constraint := range map[string]*float64{"minimum": p.Definition.Min, "maximum": p.Definition.Max, "step": p.Definition.Step} {
			if constraint != nil && math.Trunc(*constraint) != *constraint {
				return fmt.Errorf("int %s must be an integer", name)
			}
		}
	}
	if value.Type != p.Definition.Type {
		return fmt.Errorf("value type %q does not match definition %q", value.Type, p.Definition.Type)
	}
	pointers := 0
	if value.Bool != nil {
		pointers++
	}
	if value.Int != nil {
		pointers++
	}
	if value.Number != nil {
		pointers++
	}
	if value.String != nil {
		pointers++
	}
	if pointers != 1 {
		return errors.New("value must contain exactly one typed payload")
	}
	switch value.Type {
	case ValueTypeBool:
		if value.Bool == nil {
			return errors.New("bool payload is missing")
		}
	case ValueTypeInt:
		if value.Int == nil {
			return errors.New("int payload is missing")
		}
		if p.Definition.Min != nil && float64(*value.Int) < *p.Definition.Min {
			return errors.New("int is below minimum")
		}
		if p.Definition.Max != nil && float64(*value.Int) > *p.Definition.Max {
			return errors.New("int is above maximum")
		}
	case ValueTypeNumber:
		if value.Number == nil {
			return errors.New("number payload is missing")
		}
		if p.Definition.Min != nil && *value.Number < *p.Definition.Min {
			return errors.New("number is below minimum")
		}
		if p.Definition.Max != nil && *value.Number > *p.Definition.Max {
			return errors.New("number is above maximum")
		}
	case ValueTypeString:
		if value.String == nil {
			return errors.New("string payload is missing")
		}
	case ValueTypeEnum:
		if value.String == nil {
			return errors.New("enum payload is missing")
		}
		matched := false
		for _, option := range p.Definition.Enum {
			if option == *value.String {
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("enum value is not declared")
		}
	default:
		return fmt.Errorf("unsupported value type %q", value.Type)
	}
	return nil
}

func (d Device) Property(endpointID, capabilityID, propertyID string) (Property, bool) {
	for _, endpoint := range d.Endpoints {
		if endpoint.ID != endpointID {
			continue
		}
		for _, capability := range endpoint.Capabilities {
			if capability.ID != capabilityID {
				continue
			}
			for _, property := range capability.Properties {
				if property.Definition.ID == propertyID {
					return property, true
				}
			}
		}
	}
	return Property{}, false
}

func (d *Device) SetProperty(endpointID, capabilityID, propertyID string, value PropertyValue) bool {
	for endpointIndex := range d.Endpoints {
		endpoint := &d.Endpoints[endpointIndex]
		if endpoint.ID != endpointID {
			continue
		}
		for capabilityIndex := range endpoint.Capabilities {
			capability := &endpoint.Capabilities[capabilityIndex]
			if capability.ID != capabilityID {
				continue
			}
			for propertyIndex := range capability.Properties {
				property := &capability.Properties[propertyIndex]
				if property.Definition.ID == propertyID {
					property.Value = value
					return true
				}
			}
		}
	}
	return false
}

func (d Device) Clone() Device {
	clone := d
	clone.Endpoints = make([]Endpoint, len(d.Endpoints))
	for endpointIndex, endpoint := range d.Endpoints {
		clone.Endpoints[endpointIndex] = endpoint
		clone.Endpoints[endpointIndex].Capabilities = make([]Capability, len(endpoint.Capabilities))
		for capabilityIndex, capability := range endpoint.Capabilities {
			cloneCapability := capability
			cloneCapability.Properties = make([]Property, len(capability.Properties))
			for propertyIndex, property := range capability.Properties {
				cloneProperty := property
				cloneProperty.Definition.Enum = append([]string(nil), property.Definition.Enum...)
				if property.Value.Bool != nil {
					value := *property.Value.Bool
					cloneProperty.Value.Bool = &value
				}
				if property.Value.Int != nil {
					value := *property.Value.Int
					cloneProperty.Value.Int = &value
				}
				if property.Value.Number != nil {
					value := *property.Value.Number
					cloneProperty.Value.Number = &value
				}
				if property.Value.String != nil {
					value := *property.Value.String
					cloneProperty.Value.String = &value
				}
				cloneCapability.Properties[propertyIndex] = cloneProperty
			}
			cloneCapability.Commands = make([]CommandDefinition, len(capability.Commands))
			for commandIndex, command := range capability.Commands {
				cloneCapability.Commands[commandIndex] = command
				cloneCapability.Commands[commandIndex].Parameters = append([]CommandParameter(nil), command.Parameters...)
			}
			cloneCapability.Events = append([]EventDefinition(nil), capability.Events...)
			clone.Endpoints[endpointIndex].Capabilities[capabilityIndex] = cloneCapability
		}
	}
	return clone
}
