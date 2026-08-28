package catalog

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type DeviceInput struct {
	ID, ProviderID, Name, Model string
	DeviceID                    string
	UIID                        int
	Type                        string
	HomeID, HomeName            string
	RoomID, RoomName            string
	Params                      map[string]any
	Online                      bool
	Channels                    int
	RuntimeMode                 string
	StateTransport              string
}

func BuildDevice(input DeviceInput) (device.Device, error) {
	if input.ID == "" || input.ProviderID == "" || input.Name == "" {
		return device.Device{}, fmt.Errorf("Sonoff device id, provider id and name are required")
	}
	if !device.ValidStableID(input.ID) || !device.ValidStableID(input.ProviderID) {
		return device.Device{}, fmt.Errorf("invalid Sonoff stable device identity")
	}
	if input.Params == nil {
		input.Params = map[string]any{}
	}
	if input.Channels < 1 {
		input.Channels = 1
	}
	// The eWeLink directory is authoritative for the device protocol. Provider
	// configurations historically defaulted channels to 1, so a multi-channel
	// device must still expand when its live state exposes a switches array.
	if switches, ok := input.Params["switches"].([]any); ok && len(switches) > input.Channels {
		input.Channels = len(switches)
	}
	dtypeValue := inferType(input)
	mode := device.RuntimeMode(input.RuntimeMode)
	if mode == "" {
		mode = device.RuntimeModePending
	}
	transport := device.StateTransport(input.StateTransport)
	if transport == "" {
		transport = device.StateTransportPending
	}
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: input.ID, ProviderID: input.ProviderID, Name: input.Name, Type: dtypeValue, HomeID: input.HomeID, HomeName: input.HomeName, RoomID: input.RoomID, RoomName: input.RoomName, RuntimeMode: mode, StateTransport: transport, Sequence: 1, LastUpdateAt: time.Now().UTC()}
	item.SetOnline(input.Online)
	item.Endpoints = []device.Endpoint{{ID: "main", Name: input.Name, Type: "sonoff"}}
	if input.Channels > 1 && isSwitchType(input.UIID, dtypeValue, input.Params) {
		item.Endpoints = make([]device.Endpoint, 0, input.Channels)
		for index := 0; index < input.Channels; index++ {
			item.Endpoints = append(item.Endpoints, device.Endpoint{ID: "channel-" + strconv.Itoa(index), Name: fmt.Sprintf("%s %d", input.Name, index+1), Type: "sonoff-channel"})
		}
	}
	for endpointIndex := range item.Endpoints {
		item.Endpoints[endpointIndex].Capabilities = capabilitiesFor(input, dtypeValue, endpointIndex)
	}
	item, err := ApplyParams(item, input.Params)
	if err != nil {
		return device.Device{}, err
	}
	setPropertyStateTransport(&item, transport)
	return item, nil
}

func setPropertyStateTransport(item *device.Device, transport device.StateTransport) {
	for endpointIndex := range item.Endpoints {
		for capabilityIndex := range item.Endpoints[endpointIndex].Capabilities {
			for propertyIndex := range item.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties {
				item.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties[propertyIndex].StateTransport = transport
			}
		}
	}
}

func ApplyParams(base device.Device, params map[string]any) (device.Device, error) {
	for endpointIndex := range base.Endpoints {
		endpoint := &base.Endpoints[endpointIndex]
		for capabilityIndex := range endpoint.Capabilities {
			capability := &endpoint.Capabilities[capabilityIndex]
			for propertyIndex := range capability.Properties {
				property := &capability.Properties[propertyIndex]
				if raw, ok := paramForProperty(params, property.Definition.ID, endpointIndex); ok {
					if value, valid := valueForDefinition(property.Definition, raw); valid {
						property.Value = value
					}
				}
			}
		}
	}
	addUnknownParams(&base, params)
	base.LastUpdateAt = time.Now().UTC()
	return base, nil
}

func EncodePropertyCommand(item device.Device, request providersdk.PropertyWriteRequest) (string, map[string]any, error) {
	if _, ok := item.Property(request.EndpointID, request.CapabilityID, request.PropertyID); !ok || !request.Value.HasSinglePayload() {
		return "", nil, providersdk.ErrPropertyUnsupported
	}
	value := scalarValue(request.Value)
	switch request.CapabilityID {
	case "switch":
		if request.PropertyID == "power" {
			return "switch", map[string]any{"switch": onOff(value)}, nil
		}
		if strings.HasPrefix(request.PropertyID, "power-") {
			index, err := strconv.Atoi(strings.TrimPrefix(request.PropertyID, "power-"))
			if err != nil {
				return "", nil, providersdk.ErrPropertyInvalid
			}
			return "switches", map[string]any{"switches": []map[string]any{{"outlet": index, "switch": onOff(value)}}}, nil
		}
	case "light":
		key := map[string]string{"power": "switch", "brightness": "bright", "color-temperature": "colorTemp", "hue": "hue", "saturation": "sat"}[request.PropertyID]
		if key != "" {
			if request.PropertyID == "power" {
				value = onOff(value)
			}
			return "light", map[string]any{key: value}, nil
		}
	case "fan":
		key := map[string]string{"active": "switch", "speed-level": "speed"}[request.PropertyID]
		if key != "" {
			if request.PropertyID == "active" {
				value = onOff(value)
			}
			return "fan", map[string]any{key: value}, nil
		}
	case "window-covering":
		if request.PropertyID == "target-position" {
			return "cover", map[string]any{"location": value}, nil
		}
	}
	return "", nil, providersdk.ErrPropertyUnsupported
}

func inferType(input DeviceInput) device.Type {
	if input.Type != "" {
		return device.Type(input.Type)
	}
	switch input.UIID {
	case 15, 181:
		return device.TypeTemperatureHumiditySensor
	case 32, 190:
		return device.TypeOutlet
	case 34:
		return device.TypeFan
	case 36, 77, 136:
		return device.TypeLightbulb
	case 102:
		return device.TypeContactSensor
	case 126:
		if _, ok := input.Params["motorTurn"]; ok {
			return device.TypeWindowCovering
		}
		return device.TypeSwitch
	case 173, 177:
		return device.TypeMotionSensor
	default:
		return device.TypeSwitch
	}
}

func capabilitiesFor(input DeviceInput, dtype device.Type, endpointIndex int) []device.Capability {
	params := input.Params
	capabilities := make([]device.Capability, 0, 6)
	if isSwitchType(input.UIID, dtype, params) {
		propertyID := "power"
		if input.Channels > 1 {
			propertyID += "-" + strconv.Itoa(endpointIndex)
		}
		capabilities = append(capabilities, device.Capability{ID: "switch", Type: "switch", Properties: []device.Property{propertyBool(propertyID, "开关", true)}})
	}
	if dtype == device.TypeLightbulb {
		capabilities = append(capabilities, device.Capability{ID: "light", Type: "light", Properties: []device.Property{propertyBool("power", "开关", true), propertyNumber("brightness", "亮度", "percent", true, 0, 100), propertyInt("color-temperature", "色温", "mired", true, 0, 1000), propertyNumber("hue", "色相", "degree", true, 0, 360), propertyNumber("saturation", "饱和度", "percent", true, 0, 100)}})
	}
	if dtype == device.TypeFan {
		capabilities = append(capabilities, device.Capability{ID: "fan", Type: "fan", Properties: []device.Property{propertyBool("active", "运行", true), propertyInt("speed-level", "风速档位", "", true, 0, 3)}})
	}
	if hasAny(params, "current", "currentWatts", "power", "voltage", "energy", "electricity") || dtype == device.TypeOutlet {
		capabilities = append(capabilities, device.Capability{ID: "electrical", Type: "electrical", Properties: []device.Property{propertyNumber("current-power", "当前功率", "watt", false), propertyNumber("voltage", "电压", "volt", false), propertyNumber("current", "电流", "ampere", false), propertyNumber("energy", "累计电量", "kilowatt-hour", false)}})
	}
	if dtype == device.TypeTemperatureHumiditySensor || hasAny(params, "temperature", "currentTemperature", "temp") {
		capabilities = append(capabilities, device.Capability{ID: "temperature", Type: "temperature", Properties: []device.Property{propertyNumber("current-temperature", "当前温度", "celsius", false)}})
	}
	if dtype == device.TypeTemperatureHumiditySensor || hasAny(params, "humidity", "currentHumidity", "hum") {
		capabilities = append(capabilities, device.Capability{ID: "humidity", Type: "humidity", Properties: []device.Property{propertyNumber("current-humidity", "当前湿度", "percent", false, 0, 100)}})
	}
	if dtype == device.TypeContactSensor || hasAny(params, "contact", "door", "doorcontact") {
		capabilities = append(capabilities, device.Capability{ID: "contact", Type: "contact-sensor", Properties: []device.Property{propertyBool("contact-detected", "接触状态", false)}})
	}
	if dtype == device.TypeMotionSensor || hasAny(params, "motion", "pir", "presence") {
		capabilities = append(capabilities, device.Capability{ID: "motion", Type: "motion-sensor", Properties: []device.Property{propertyBool("motion-detected", "活动状态", false)}})
	}
	if dtype == device.TypeWindowCovering || hasAny(params, "location", "position", "currentPosition") {
		capabilities = append(capabilities, device.Capability{ID: "window-covering", Type: "window-covering", Properties: []device.Property{propertyNumber("target-position", "目标位置", "percent", true, 0, 100), propertyNumber("current-position", "当前位置", "percent", false, 0, 100)}})
	}
	if hasAny(params, "battery", "batteryLevel") {
		capabilities = append(capabilities, device.Capability{ID: "battery", Type: "battery", Properties: []device.Property{propertyNumber("level", "电池电量", "percent", false, 0, 100)}})
	}
	if hasAny(params, "rssi", "signalStrength", "RSSI") {
		capabilities = append(capabilities, device.Capability{ID: "signal", Type: "signal", Properties: []device.Property{propertyNumber("rssi", "信号强度", "dBm", false, -120, 0)}})
	}
	return capabilities
}

func propertyBool(id, name string, writable bool) device.Property {
	return device.Property{Definition: device.PropertyDefinition{ID: id, Name: name, Type: device.ValueTypeBool, ParameterLevel: device.ParameterOptional, Readable: true, Writable: writable, Notifiable: true}, Value: device.BoolValue(false)}
}

func propertyNumber(id, name, unit string, writable bool, bounds ...float64) device.Property {
	definition := device.PropertyDefinition{ID: id, Name: name, Type: device.ValueTypeNumber, ParameterLevel: device.ParameterOptional, Unit: unit, Readable: true, Writable: writable, Notifiable: true}
	if len(bounds) >= 2 {
		definition.Min, definition.Max = &bounds[0], &bounds[1]
	}
	return device.Property{Definition: definition, Value: device.NumberValue(0)}
}

func propertyInt(id, name, unit string, writable bool, bounds ...float64) device.Property {
	definition := device.PropertyDefinition{ID: id, Name: name, Type: device.ValueTypeInt, ParameterLevel: device.ParameterOptional, Unit: unit, Readable: true, Writable: writable, Notifiable: true}
	if len(bounds) >= 2 {
		definition.Min, definition.Max = &bounds[0], &bounds[1]
	}
	return device.Property{Definition: definition, Value: device.IntValue(0)}
}

func paramForProperty(params map[string]any, propertyID string, endpoint int) (any, bool) {
	baseID := propertyID
	multiChannelPower := strings.HasPrefix(baseID, "power-")
	if multiChannelPower {
		baseID = "power"
	}
	// Multi-channel snapshots can contain both a legacy top-level switch and
	// the per-outlet switches array. The per-outlet value is the only accurate
	// source for channel endpoints and must win over the legacy field.
	if multiChannelPower {
		if raw, ok := switchParamAt(params, endpoint); ok {
			return raw, true
		}
	}
	keys := map[string][]string{
		"power": {"switch", "power"}, "brightness": {"bright", "brightness", "bright_value"},
		"color-temperature": {"colorTemp", "colorTemperature"}, "hue": {"hue"}, "saturation": {"sat", "saturation"},
		"active": {"switch", "active"}, "speed-level": {"speed", "fanSpeed"},
		"current-power": {"power", "currentPower", "currentWatts"}, "voltage": {"voltage"}, "current": {"current"}, "energy": {"energy", "electricity"},
		"current-temperature": {"temperature", "currentTemperature", "temp"}, "current-humidity": {"humidity", "currentHumidity", "hum"},
		"contact-detected": {"contact", "doorcontact", "door"}, "motion-detected": {"motion", "pir", "presence"},
		"target-position": {"location", "position"}, "current-position": {"currentPosition", "position"},
		"level": {"battery", "batteryLevel"}, "rssi": {"rssi", "RSSI", "signalStrength"},
	}
	for _, key := range keys[baseID] {
		if raw, ok := params[key]; ok {
			return raw, true
		}
	}
	if raw, ok := switchParamAt(params, endpoint); ok {
		return raw, true
	}
	return nil, false
}

func switchParamAt(params map[string]any, endpoint int) (any, bool) {
	switches, ok := params["switches"].([]any)
	if !ok || endpoint < 0 || endpoint >= len(switches) {
		return nil, false
	}
	object, ok := switches[endpoint].(map[string]any)
	if !ok {
		return nil, false
	}
	value, exists := object["switch"]
	return value, exists
}

func valueForDefinition(definition device.PropertyDefinition, raw any) (device.PropertyValue, bool) {
	switch definition.Type {
	case device.ValueTypeBool:
		switch value := raw.(type) {
		case bool:
			return device.BoolValue(value), true
		case string:
			return device.BoolValue(strings.EqualFold(value, "on") || strings.EqualFold(value, "true")), true
		}
	case device.ValueTypeInt:
		if value, ok := number(raw); ok {
			return device.IntValue(int64(value)), true
		}
	case device.ValueTypeNumber:
		if value, ok := number(raw); ok {
			return device.NumberValue(value), true
		}
	case device.ValueTypeString, device.ValueTypeEnum:
		if value, ok := raw.(string); ok {
			if definition.Type == device.ValueTypeEnum {
				return device.EnumValue(value), true
			}
			return device.StringValue(value), true
		}
	}
	return device.PropertyValue{}, false
}

func addUnknownParams(item *device.Device, params map[string]any) {
	known := map[string]bool{"switch": true, "switches": true, "power": true, "bright": true, "brightness": true, "bright_value": true, "colorTemp": true, "colorTemperature": true, "hue": true, "sat": true, "saturation": true, "speed": true, "fanSpeed": true, "current": true, "currentPower": true, "currentWatts": true, "voltage": true, "energy": true, "electricity": true, "temperature": true, "currentTemperature": true, "temp": true, "humidity": true, "currentHumidity": true, "hum": true, "contact": true, "doorcontact": true, "door": true, "motion": true, "pir": true, "presence": true, "location": true, "position": true, "currentPosition": true, "battery": true, "batteryLevel": true, "rssi": true, "RSSI": true, "signalStrength": true}
	properties := make([]device.Property, 0)
	for key, raw := range params {
		if known[key] {
			continue
		}
		definition, value, ok := customProperty(key, raw)
		if ok {
			properties = append(properties, device.Property{Definition: definition, Value: value})
		}
	}
	if len(properties) > 0 && len(item.Endpoints) > 0 {
		item.Endpoints[0].Capabilities = append(item.Endpoints[0].Capabilities, device.Capability{ID: "sonoff-raw", Type: "sonoff-raw", Properties: properties})
	}
}

func customProperty(key string, raw any) (device.PropertyDefinition, device.PropertyValue, bool) {
	id := stableID(key)
	switch value := raw.(type) {
	case bool:
		return device.PropertyDefinition{ID: id, Name: key, Type: device.ValueTypeBool, ParameterLevel: device.ParameterCustom, Readable: true, Notifiable: true}, device.BoolValue(value), true
	case string:
		return device.PropertyDefinition{ID: id, Name: key, Type: device.ValueTypeString, ParameterLevel: device.ParameterCustom, Readable: true, Notifiable: true}, device.StringValue(value), true
	case float64:
		return device.PropertyDefinition{ID: id, Name: key, Type: device.ValueTypeNumber, ParameterLevel: device.ParameterCustom, Readable: true, Notifiable: true}, device.NumberValue(value), true
	case int:
		return device.PropertyDefinition{ID: id, Name: key, Type: device.ValueTypeNumber, ParameterLevel: device.ParameterCustom, Readable: true, Notifiable: true}, device.NumberValue(float64(value)), true
	}
	return device.PropertyDefinition{}, device.PropertyValue{}, false
}

func stableID(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			builder.WriteRune(r)
		} else if builder.Len() > 0 {
			builder.WriteByte('-')
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "value"
	}
	return result
}

func number(raw any) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case jsonNumber:
		parsed, err := strconv.ParseFloat(string(value), 64)
		return parsed, err == nil
	}
	return 0, false
}

type jsonNumber string

func hasAny(params map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := params[key]; ok {
			return true
		}
	}
	return false
}

func isSwitchType(uiid int, dtype device.Type, params map[string]any) bool {
	return dtype == device.TypeSwitch || dtype == device.TypeOutlet || uiid == 1 || uiid == 2 || uiid == 4 || uiid == 7 || uiid == 8 || uiid == 9 || uiid == 126 || hasAny(params, "switch", "switches", "power")
}

func scalarValue(value device.PropertyValue) any {
	switch value.Type {
	case device.ValueTypeBool:
		return *value.Bool
	case device.ValueTypeInt:
		return *value.Int
	case device.ValueTypeNumber:
		return *value.Number
	case device.ValueTypeEnum, device.ValueTypeString:
		return *value.String
	}
	return nil
}

func onOff(value any) string {
	if enabled, ok := value.(bool); ok && enabled {
		return "on"
	}
	return "off"
}
