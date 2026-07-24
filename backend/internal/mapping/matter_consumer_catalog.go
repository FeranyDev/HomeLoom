package mapping

import "github.com/feranydev/homeloom/backend/internal/domain/device"

// MatterConsumerContracts declares the first bridge device set using the
// canonical Matter Cluster.Attribute vocabulary. Runtime-specific scaling
// (for example Celsius to centi-degrees and percent to percent100ths) belongs
// to the Matter adapter, not the unified model.
func MatterConsumerContracts() []device.ConsumerModelContract {
	path := func(capabilityID, propertyID string) device.ParameterPath {
		return device.ParameterPath{EndpointID: "main", CapabilityID: capabilityID, PropertyID: propertyID}
	}
	required := func(capabilityID, propertyID, target string) device.ConsumerParameterMapping {
		return device.ConsumerParameterMapping{Source: path(capabilityID, propertyID), Target: target, Level: device.ParameterRequired}
	}
	optional := func(capabilityID, propertyID, target string) device.ConsumerParameterMapping {
		return device.ConsumerParameterMapping{Source: path(capabilityID, propertyID), Target: target, Level: device.ParameterOptional}
	}

	return []device.ConsumerModelContract{
		{ConsumerID: "matter", DeviceType: device.TypeSwitch, Parameters: []device.ConsumerParameterMapping{
			required("switch", "power", "OnOff.OnOff"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeOutlet, Parameters: []device.ConsumerParameterMapping{
			required("switch", "power", "OnOff.OnOff"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeLightbulb, Parameters: []device.ConsumerParameterMapping{
			required("switch", "power", "OnOff.OnOff"),
			optional("light", "brightness", "LevelControl.CurrentLevel"),
			optional("light", "color-temperature", "ColorControl.ColorTemperatureMireds"),
			optional("light", "hue", "ColorControl.CurrentHue"),
			optional("light", "saturation", "ColorControl.CurrentSaturation"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeTemperatureSensor, Parameters: []device.ConsumerParameterMapping{
			required("temperature", "current-temperature", "TemperatureMeasurement.MeasuredValue"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeHumiditySensor, Parameters: []device.ConsumerParameterMapping{
			required("humidity", "current-humidity", "RelativeHumidityMeasurement.MeasuredValue"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeContactSensor, Parameters: []device.ConsumerParameterMapping{
			required("contact", "contact-detected", "BooleanState.StateValue"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeMotionSensor, Parameters: []device.ConsumerParameterMapping{
			required("motion", "motion-detected", "OccupancySensing.Occupancy"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeOccupancySensor, Parameters: []device.ConsumerParameterMapping{
			required("occupancy", "occupancy-detected", "OccupancySensing.Occupancy"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeWindowCovering, Parameters: []device.ConsumerParameterMapping{
			required("window-covering", "current-position", "WindowCovering.CurrentPositionLiftPercent100ths"),
			required("window-covering", "target-position", "WindowCovering.TargetPositionLiftPercent100ths"),
			required("window-covering", "position-state", "WindowCovering.OperationalStatus"),
			optional("window-covering", "obstruction-detected", "WindowCovering.SafetyStatus"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeFan, Parameters: []device.ConsumerParameterMapping{
			required("fan", "active", "OnOff.OnOff"),
			optional("fan", "target-state", "FanControl.FanMode"),
			optional("fan", "rotation-speed", "FanControl.PercentSetting"),
			optional("fan", "swing-mode", "FanControl.RockSetting"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeThermostat, Parameters: []device.ConsumerParameterMapping{
			required("thermostat", "current-state", "Thermostat.ThermostatRunningState"),
			required("thermostat", "target-mode", "Thermostat.SystemMode"),
			required("temperature", "current-temperature", "Thermostat.LocalTemperature"),
			required("temperature", "target-temperature", "Thermostat.OccupiedHeatingSetpoint"),
			optional("temperature", "cooling-threshold", "Thermostat.OccupiedCoolingSetpoint"),
			optional("thermostat", "display-units", "ThermostatUserInterfaceConfiguration.TemperatureDisplayMode"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeLock, Parameters: []device.ConsumerParameterMapping{
			required("lock", "current-state", "DoorLock.LockState"),
			optional("lock", "door-open", "DoorLock.DoorState"),
		}},
	}
}

type matterCommandDefinition struct {
	deviceType device.Type
	modelPath  device.ParameterPath
	id         string
	name       string
}

func matterCommandProperties(contracts []device.ConsumerModelContract) []ConsumerProperty {
	path := func(capabilityID, propertyID string) device.ParameterPath {
		return device.ParameterPath{EndpointID: "main", CapabilityID: capabilityID, PropertyID: propertyID}
	}
	definitions := []matterCommandDefinition{
		{device.TypeSwitch, path("switch", "power"), "OnOff.On", "开启"},
		{device.TypeSwitch, path("switch", "power"), "OnOff.Off", "关闭"},
		{device.TypeOutlet, path("switch", "power"), "OnOff.On", "开启"},
		{device.TypeOutlet, path("switch", "power"), "OnOff.Off", "关闭"},
		{device.TypeLightbulb, path("switch", "power"), "OnOff.On", "开启"},
		{device.TypeLightbulb, path("switch", "power"), "OnOff.Off", "关闭"},
		{device.TypeLightbulb, path("light", "brightness"), "LevelControl.MoveToLevel", "移动到亮度"},
		{device.TypeWindowCovering, path("window-covering", "target-position"), "WindowCovering.GoToLiftPercentage", "移动窗帘"},
		{device.TypeWindowCovering, path("window-covering", "target-position"), "WindowCovering.StopMotion", "停止窗帘"},
		{device.TypeLock, path("lock", "target-state"), "DoorLock.LockDoor", "上锁"},
		{device.TypeLock, path("lock", "target-state"), "DoorLock.UnlockDoor", "解锁"},
	}
	result := make([]ConsumerProperty, 0, len(definitions))
	for _, definition := range definitions {
		model, ok := device.ModelContractFor(definition.deviceType)
		if !ok {
			continue
		}
		var parameter device.ModelParameter
		for _, current := range model.Parameters {
			if current.Path.Key() == definition.modelPath.Key() {
				parameter = current
				break
			}
		}
		cluster, element := splitConsumerPath(definition.id)
		result = append(result, ConsumerProperty{
			ID: definition.id, Name: definition.name, OriginalName: definition.id,
			Cluster: cluster, Element: element, Kind: "command",
			DeviceType: definition.deviceType, DefaultModelPath: definition.modelPath,
			Level: device.ParameterOptional, Type: parameter.Type, Unit: parameter.Unit,
			Min: parameter.Min, Max: parameter.Max, Step: parameter.Step,
			Enum:     append([]string(nil), parameter.Enum...),
			Readable: false, Writable: true, Notifiable: false,
		})
	}
	return result
}
