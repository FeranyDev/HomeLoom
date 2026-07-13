package homekit

import "github.com/feranydev/homeloom/backend/internal/domain/device"

func homeKitModelContract(deviceType device.Type) (device.ConsumerModelContract, bool) {
	path := func(capabilityID, propertyID string) device.ParameterPath {
		return device.ParameterPath{EndpointID: "main", CapabilityID: capabilityID, PropertyID: propertyID}
	}
	required := func(capabilityID, propertyID, target string) device.ConsumerParameterMapping {
		return device.ConsumerParameterMapping{Source: path(capabilityID, propertyID), Target: target, Level: device.ParameterRequired}
	}
	optional := func(capabilityID, propertyID, target string) device.ConsumerParameterMapping {
		return device.ConsumerParameterMapping{Source: path(capabilityID, propertyID), Target: target, Level: device.ParameterOptional}
	}
	contract := device.ConsumerModelContract{ConsumerID: "homekit", DeviceType: deviceType}
	switch deviceType {
	case device.TypeSwitch:
		contract.Parameters = []device.ConsumerParameterMapping{required("switch", "power", "Switch.On")}
	case device.TypeLightbulb:
		contract.Parameters = []device.ConsumerParameterMapping{
			required("switch", "power", "Lightbulb.On"),
			optional("light", "brightness", "Lightbulb.Brightness"),
			optional("light", "color-temperature", "Lightbulb.ColorTemperature"),
			optional("light", "hue", "Lightbulb.Hue"),
			optional("light", "saturation", "Lightbulb.Saturation"),
		}
	case device.TypeOutlet:
		contract.Parameters = []device.ConsumerParameterMapping{required("switch", "power", "Outlet.On"), optional("outlet", "in-use", "Outlet.OutletInUse")}
	case device.TypeTemperatureSensor:
		contract.Parameters = append([]device.ConsumerParameterMapping{required("temperature", "current-temperature", "TemperatureSensor.CurrentTemperature")}, homeKitSensorStatusMappings(optional)...)
	case device.TypeHumiditySensor:
		contract.Parameters = append([]device.ConsumerParameterMapping{required("humidity", "current-humidity", "HumiditySensor.CurrentRelativeHumidity")}, homeKitSensorStatusMappings(optional)...)
	case device.TypeContactSensor:
		contract.Parameters = append([]device.ConsumerParameterMapping{required("contact", "contact-detected", "ContactSensor.ContactSensorState")}, homeKitSensorStatusMappings(optional)...)
	case device.TypeMotionSensor:
		contract.Parameters = append([]device.ConsumerParameterMapping{required("motion", "motion-detected", "MotionSensor.MotionDetected")}, homeKitSensorStatusMappings(optional)...)
	case device.TypeFan:
		contract.Parameters = []device.ConsumerParameterMapping{
			required("fan", "active", "FanV2.Active"), required("fan", "current-state", "FanV2.CurrentFanState"),
			required("fan", "target-state", "FanV2.TargetFanState"), required("fan", "rotation-speed", "FanV2.RotationSpeed"),
			optional("fan", "swing-mode", "FanV2.SwingMode"), optional("fan", "rotation-direction", "FanV2.RotationDirection"),
			optional("fan", "lock-physical-controls", "FanV2.LockPhysicalControls"),
		}
	case device.TypeAirPurifier:
		contract.Parameters = []device.ConsumerParameterMapping{
			required("air-purifier", "active", "AirPurifier.Active"), required("air-purifier", "current-state", "AirPurifier.CurrentAirPurifierState"),
			required("air-purifier", "target-state", "AirPurifier.TargetAirPurifierState"), required("air-purifier", "rotation-speed", "AirPurifier.RotationSpeed"),
			optional("air-purifier", "swing-mode", "AirPurifier.SwingMode"), optional("air-purifier", "lock-physical-controls", "AirPurifier.LockPhysicalControls"),
			optional("air-quality", "current-air-quality", "AirQualitySensor.AirQuality"), optional("air-quality", "pm2.5-density", "AirQualitySensor.PM2.5Density"),
			optional("air-quality", "voc-density", "AirQualitySensor.VOCDensity"), required("filter", "life-level", "FilterMaintenance.FilterLifeLevel"),
			required("filter", "change-indication", "FilterMaintenance.FilterChangeIndication"),
		}
	case device.TypeWindowCovering:
		contract.Parameters = []device.ConsumerParameterMapping{
			required("window-covering", "current-position", "WindowCovering.CurrentPosition"),
			required("window-covering", "target-position", "WindowCovering.TargetPosition"),
			required("window-covering", "position-state", "WindowCovering.PositionState"),
			optional("window-covering", "obstruction-detected", "WindowCovering.ObstructionDetected"),
		}
	default:
		return device.ConsumerModelContract{}, false
	}
	return contract, true
}

func homeKitSensorStatusMappings(optional func(string, string, string) device.ConsumerParameterMapping) []device.ConsumerParameterMapping {
	return []device.ConsumerParameterMapping{
		optional("battery", "level", "BatteryService.BatteryLevel"),
		optional("battery", "low", "BatteryService.StatusLowBattery"),
		optional("security", "tampered", "PrimaryService.StatusTampered"),
	}
}
