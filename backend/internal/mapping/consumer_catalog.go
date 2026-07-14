package mapping

import "github.com/feranydev/homeloom/backend/internal/domain/device"

type ConsumerProperty struct {
	ID               string                `json:"id"`
	Name             string                `json:"name"`
	DeviceType       device.Type           `json:"deviceType"`
	DefaultModelPath device.ParameterPath  `json:"defaultModelPath"`
	Level            device.ParameterLevel `json:"level"`
	Type             device.ValueType      `json:"type"`
	Readable         bool                  `json:"readable"`
	Writable         bool                  `json:"writable"`
	Notifiable       bool                  `json:"notifiable"`
}

type ConsumerCatalog struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Properties []ConsumerProperty `json:"properties"`
}

func BuiltInConsumerCatalogs() []ConsumerCatalog {
	contracts := HomeKitConsumerContracts()
	properties := make([]ConsumerProperty, 0, 48)
	for _, contract := range contracts {
		model, _ := device.ModelContractFor(contract.DeviceType)
		definitions := make(map[string]device.ModelParameter, len(model.Parameters))
		for _, parameter := range model.Parameters {
			definitions[parameter.Path.Key()] = parameter
		}
		for _, item := range contract.Parameters {
			parameter := definitions[item.Source.Key()]
			properties = append(properties, ConsumerProperty{
				ID: item.Target, Name: item.Target, DeviceType: contract.DeviceType,
				DefaultModelPath: item.Source, Level: item.Level, Type: parameter.Type,
				Readable: parameter.Readable, Writable: parameter.Writable, Notifiable: parameter.Notifiable,
			})
		}
	}
	return []ConsumerCatalog{{ID: "homekit", Name: "Apple Home / HomeKit", Properties: properties}}
}

func HomeKitConsumerContracts() []device.ConsumerModelContract {
	path := func(capabilityID, propertyID string) device.ParameterPath {
		return device.ParameterPath{EndpointID: "main", CapabilityID: capabilityID, PropertyID: propertyID}
	}
	required := func(deviceType device.Type, capabilityID, propertyID, target string) device.ConsumerParameterMapping {
		return device.ConsumerParameterMapping{Source: path(capabilityID, propertyID), Target: target, Level: device.ParameterRequired}
	}
	optional := func(deviceType device.Type, capabilityID, propertyID, target string) device.ConsumerParameterMapping {
		return device.ConsumerParameterMapping{Source: path(capabilityID, propertyID), Target: target, Level: device.ParameterOptional}
	}
	sensorStatus := func(deviceType device.Type) []device.ConsumerParameterMapping {
		return []device.ConsumerParameterMapping{
			optional(deviceType, "battery", "level", "BatteryService.BatteryLevel"),
			optional(deviceType, "battery", "low", "BatteryService.StatusLowBattery"),
			optional(deviceType, "security", "tampered", "PrimaryService.StatusTampered"),
		}
	}
	contracts := []device.ConsumerModelContract{
		{ConsumerID: "homekit", DeviceType: device.TypeSwitch, Parameters: []device.ConsumerParameterMapping{required(device.TypeSwitch, "switch", "power", "Switch.On")}},
		{ConsumerID: "homekit", DeviceType: device.TypeLightbulb, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeLightbulb, "switch", "power", "Lightbulb.On"), optional(device.TypeLightbulb, "light", "brightness", "Lightbulb.Brightness"), optional(device.TypeLightbulb, "light", "color-temperature", "Lightbulb.ColorTemperature"), optional(device.TypeLightbulb, "light", "hue", "Lightbulb.Hue"), optional(device.TypeLightbulb, "light", "saturation", "Lightbulb.Saturation"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeOutlet, Parameters: []device.ConsumerParameterMapping{required(device.TypeOutlet, "switch", "power", "Outlet.On"), optional(device.TypeOutlet, "outlet", "in-use", "Outlet.OutletInUse")}},
		{ConsumerID: "homekit", DeviceType: device.TypeTemperatureSensor, Parameters: append([]device.ConsumerParameterMapping{required(device.TypeTemperatureSensor, "temperature", "current-temperature", "TemperatureSensor.CurrentTemperature")}, sensorStatus(device.TypeTemperatureSensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeHumiditySensor, Parameters: append([]device.ConsumerParameterMapping{required(device.TypeHumiditySensor, "humidity", "current-humidity", "HumiditySensor.CurrentRelativeHumidity")}, sensorStatus(device.TypeHumiditySensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeContactSensor, Parameters: append([]device.ConsumerParameterMapping{required(device.TypeContactSensor, "contact", "contact-detected", "ContactSensor.ContactSensorState")}, sensorStatus(device.TypeContactSensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeMotionSensor, Parameters: append([]device.ConsumerParameterMapping{required(device.TypeMotionSensor, "motion", "motion-detected", "MotionSensor.MotionDetected")}, sensorStatus(device.TypeMotionSensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeFan, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeFan, "fan", "active", "FanV2.Active"), required(device.TypeFan, "fan", "current-state", "FanV2.CurrentFanState"), required(device.TypeFan, "fan", "target-state", "FanV2.TargetFanState"), required(device.TypeFan, "fan", "rotation-speed", "FanV2.RotationSpeed"), optional(device.TypeFan, "fan", "swing-mode", "FanV2.SwingMode"), optional(device.TypeFan, "fan", "rotation-direction", "FanV2.RotationDirection"), optional(device.TypeFan, "fan", "lock-physical-controls", "FanV2.LockPhysicalControls"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeAirPurifier, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeAirPurifier, "air-purifier", "active", "AirPurifier.Active"), required(device.TypeAirPurifier, "air-purifier", "current-state", "AirPurifier.CurrentAirPurifierState"), required(device.TypeAirPurifier, "air-purifier", "target-state", "AirPurifier.TargetAirPurifierState"), required(device.TypeAirPurifier, "air-purifier", "rotation-speed", "AirPurifier.RotationSpeed"), optional(device.TypeAirPurifier, "air-purifier", "swing-mode", "AirPurifier.SwingMode"), optional(device.TypeAirPurifier, "air-purifier", "lock-physical-controls", "AirPurifier.LockPhysicalControls"), optional(device.TypeAirPurifier, "air-quality", "current-air-quality", "AirQualitySensor.AirQuality"), optional(device.TypeAirPurifier, "air-quality", "pm2.5-density", "AirQualitySensor.PM2.5Density"), optional(device.TypeAirPurifier, "air-quality", "voc-density", "AirQualitySensor.VOCDensity"), required(device.TypeAirPurifier, "filter", "life-level", "FilterMaintenance.FilterLifeLevel"), required(device.TypeAirPurifier, "filter", "change-indication", "FilterMaintenance.FilterChangeIndication"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeWindowCovering, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeWindowCovering, "window-covering", "current-position", "WindowCovering.CurrentPosition"), required(device.TypeWindowCovering, "window-covering", "target-position", "WindowCovering.TargetPosition"), required(device.TypeWindowCovering, "window-covering", "position-state", "WindowCovering.PositionState"), optional(device.TypeWindowCovering, "window-covering", "obstruction-detected", "WindowCovering.ObstructionDetected"),
		}},
	}
	return contracts
}

func HomeKitConsumerContract(deviceType device.Type) (device.ConsumerModelContract, bool) {
	for _, contract := range HomeKitConsumerContracts() {
		if contract.DeviceType == deviceType {
			return contract, true
		}
	}
	return device.ConsumerModelContract{}, false
}
