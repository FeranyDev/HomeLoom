package device

import "time"

type Type string
type ValueType string

const (
	TypeSwitch            Type = "switch"
	TypeTemperatureSensor Type = "temperature-sensor"
)

const (
	ValueTypeBool   ValueType = "bool"
	ValueTypeNumber ValueType = "number"
	ValueTypeString ValueType = "string"
	ValueTypeEnum   ValueType = "enum"
)

type PropertyValue struct {
	Type   ValueType `json:"type"`
	Bool   *bool     `json:"bool,omitempty"`
	Number *float64  `json:"number,omitempty"`
	String *string   `json:"string,omitempty"`
}

func BoolValue(value bool) PropertyValue { return PropertyValue{Type: ValueTypeBool, Bool: &value} }
func NumberValue(value float64) PropertyValue {
	return PropertyValue{Type: ValueTypeNumber, Number: &value}
}

type PropertyDefinition struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Type              ValueType `json:"type"`
	Unit              string    `json:"unit,omitempty"`
	Readable          bool      `json:"readable"`
	Writable          bool      `json:"writable"`
	Notifiable        bool      `json:"notifiable"`
	Min               *float64  `json:"min,omitempty"`
	Max               *float64  `json:"max,omitempty"`
	Step              *float64  `json:"step,omitempty"`
	Enum              []string  `json:"enum,omitempty"`
	StaleAfterSeconds int       `json:"staleAfterSeconds,omitempty"`
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

type State struct {
	Power       *bool    `json:"power,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type Device struct {
	ID           string     `json:"id"`
	ProviderID   string     `json:"providerId"`
	Name         string     `json:"name"`
	Type         Type       `json:"type"`
	Online       bool       `json:"online"`
	State        State      `json:"state"`
	Endpoints    []Endpoint `json:"endpoints"`
	LastUpdateAt time.Time  `json:"lastUpdateAt"`
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
