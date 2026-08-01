package mapping

import (
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

type ConsumerProperty struct {
	ID               string                `json:"id"`
	Name             string                `json:"name"`
	OriginalName     string                `json:"originalName,omitempty"`
	Cluster          string                `json:"cluster,omitempty"`
	Element          string                `json:"element,omitempty"`
	Kind             string                `json:"kind,omitempty"`
	DeviceType       device.Type           `json:"deviceType"`
	DefaultModelPath device.ParameterPath  `json:"defaultModelPath"`
	Level            device.ParameterLevel `json:"level"`
	Type             device.ValueType      `json:"type"`
	Unit             string                `json:"unit,omitempty"`
	Min              *float64              `json:"min,omitempty"`
	Max              *float64              `json:"max,omitempty"`
	Step             *float64              `json:"step,omitempty"`
	Enum             []string              `json:"enum,omitempty"`
	Readable         bool                  `json:"readable"`
	Writable         bool                  `json:"writable"`
	Notifiable       bool                  `json:"notifiable"`
}

type ConsumerCatalog struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Properties []ConsumerProperty `json:"properties"`
}

type ConsumerAdapter struct {
	Catalog   ConsumerCatalog
	Contracts []device.ConsumerModelContract
}

func BuiltInConsumerCatalogs() []ConsumerCatalog {
	adapters := BuiltInConsumerAdapters()
	result := make([]ConsumerCatalog, 0, len(adapters))
	for _, adapter := range adapters {
		result = append(result, adapter.Catalog)
	}
	return result
}

// BuiltInConsumerAdapters is the registry boundary between the unified model
// and concrete Target implementations. Adding a Consumer does not require
// application services to know its protocol or property vocabulary.
func BuiltInConsumerAdapters() []ConsumerAdapter {
	homeKitContracts := HomeKitConsumerContracts()
	matterContracts := MatterConsumerContracts()
	matterCatalog := consumerCatalog("matter", "Matter", matterContracts)
	matterCatalog.Properties = append(matterCatalog.Properties, matterCommandProperties(matterContracts)...)
	return []ConsumerAdapter{
		{Catalog: consumerCatalog("homekit", "Apple Home / HomeKit", homeKitContracts), Contracts: homeKitContracts},
		{Catalog: matterCatalog, Contracts: matterContracts},
	}
}

func consumerCatalog(id, name string, contracts []device.ConsumerModelContract) ConsumerCatalog {
	properties := make([]ConsumerProperty, 0, 48)
	for _, contract := range contracts {
		model, _ := device.ModelContractFor(contract.DeviceType)
		definitions := make(map[string]device.ModelParameter, len(model.Parameters))
		for _, parameter := range model.Parameters {
			definitions[parameter.Path.Key()] = parameter
		}
		for _, item := range contract.Parameters {
			modelPath := item.ModelPath()
			parameter := definitions[modelPath.Key()]
			cluster, element := splitConsumerPath(item.Target)
			propertyName := item.Target
			if id == "matter" && parameter.Name != "" {
				propertyName = parameter.Name
			}
			properties = append(properties, ConsumerProperty{
				ID: item.Target, Name: propertyName, OriginalName: item.Target,
				Cluster: cluster, Element: element, Kind: "attribute", DeviceType: contract.DeviceType,
				DefaultModelPath: modelPath, Level: item.Level, Type: parameter.Type,
				Unit: parameter.Unit, Min: parameter.Min, Max: parameter.Max, Step: parameter.Step,
				Enum:     append([]string(nil), parameter.Enum...),
				Readable: parameter.Readable, Writable: parameter.Writable, Notifiable: parameter.Notifiable,
			})
			if id == "homekit" {
				applyHomeKitNumericConstraints(&properties[len(properties)-1])
			}
		}
	}
	return ConsumerCatalog{ID: id, Name: name, Properties: properties}
}

type numericConstraints struct {
	min  float64
	max  float64
	step float64
}

var homeKitNumericConstraints = map[string]numericConstraints{
	"Lightbulb.Brightness":                           {0, 100, 1},
	"Lightbulb.ColorTemperature":                     {140, 500, 1},
	"Lightbulb.Hue":                                  {0, 360, 1},
	"Lightbulb.Saturation":                           {0, 100, 1},
	"TemperatureSensor.CurrentTemperature":           {0, 100, 0.1},
	"HumiditySensor.CurrentRelativeHumidity":         {0, 100, 1},
	"BatteryService.BatteryLevel":                    {0, 100, 1},
	"FanV2.RotationSpeed":                            {0, 100, 1},
	"AirPurifier.RotationSpeed":                      {0, 100, 1},
	"AirQualitySensor.PM2.5Density":                  {0, 1000, 1},
	"AirQualitySensor.PM10Density":                   {0, 1000, 1},
	"AirQualitySensor.VOCDensity":                    {0, 1000, 1},
	"AirQualitySensor.CarbonDioxideLevel":            {0, 100000, 1},
	"AirQualitySensor.NitrogenDioxideDensity":        {0, 1000, 1},
	"AirQualitySensor.OzoneDensity":                  {0, 1000, 1},
	"FilterMaintenance.FilterLifeLevel":              {0, 100, 1},
	"WindowCovering.CurrentPosition":                 {0, 100, 1},
	"WindowCovering.TargetPosition":                  {0, 100, 1},
	"LightSensor.CurrentAmbientLightLevel":           {0.0001, 100000, 0},
	"CarbonMonoxideSensor.CarbonMonoxideLevel":       {0, 100, 0},
	"CarbonMonoxideSensor.CarbonMonoxidePeakLevel":   {0, 100, 0},
	"CarbonDioxideSensor.CarbonDioxideLevel":         {0, 100000, 0},
	"CarbonDioxideSensor.CarbonDioxidePeakLevel":     {0, 100000, 0},
	"Thermostat.CurrentTemperature":                  {0, 100, 0.1},
	"Thermostat.TargetTemperature":                   {10, 38, 0.1},
	"Thermostat.HeatingThresholdTemperature":         {0, 25, 0.1},
	"Thermostat.CoolingThresholdTemperature":         {10, 35, 0.1},
	"Thermostat.CurrentRelativeHumidity":             {0, 100, 1},
	"HeaterCooler.CurrentTemperature":                {0, 100, 0.1},
	"HeaterCooler.TargetTemperature":                 {0, 35, 0.1},
	"HeaterCooler.HeatingThresholdTemperature":       {0, 25, 0.1},
	"HeaterCooler.CoolingThresholdTemperature":       {10, 35, 0.1},
	"HeaterCooler.RotationSpeed":                     {0, 100, 1},
	"HumidifierDehumidifier.CurrentRelativeHumidity": {0, 100, 1},
	"HumidifierDehumidifier.TargetHumidity":          {0, 100, 1},
	"HumidifierDehumidifier.WaterLevel":              {0, 100, 1},
	"Valve.SetDuration":                              {0, 3600, 1},
	"Valve.RemainingDuration":                        {0, 3600, 1},
	"Speaker.Volume":                                 {0, 100, 1},
}

func applyHomeKitNumericConstraints(property *ConsumerProperty) {
	constraints, found := homeKitNumericConstraints[property.ID]
	if !found {
		return
	}
	property.Min, property.Max = consumerFloatPointer(constraints.min), consumerFloatPointer(constraints.max)
	property.Step = nil
	if constraints.step > 0 {
		property.Step = consumerFloatPointer(constraints.step)
	}
}

func consumerFloatPointer(value float64) *float64 {
	return &value
}

func splitConsumerPath(value string) (string, string) {
	cluster, element, found := strings.Cut(value, ".")
	if !found {
		return "", value
	}
	return cluster, element
}

func ConsumerContract(consumerID string, deviceType device.Type) (device.ConsumerModelContract, bool) {
	for _, adapter := range BuiltInConsumerAdapters() {
		if adapter.Catalog.ID != consumerID {
			continue
		}
		for _, contract := range adapter.Contracts {
			if contract.DeviceType == deviceType {
				return contract, true
			}
		}
	}
	return device.ConsumerModelContract{}, false
}

// ConsumerModelSupport distinguishes an unknown Consumer adapter from a known
// adapter that intentionally does not implement a unified model.
func ConsumerModelSupport(consumerID string, deviceType device.Type) (known, supported bool) {
	for _, adapter := range BuiltInConsumerAdapters() {
		if adapter.Catalog.ID != consumerID {
			continue
		}
		for _, contract := range adapter.Contracts {
			if contract.DeviceType == deviceType {
				return true, true
			}
		}
		return true, false
	}
	return false, false
}

func FindConsumerProperty(consumerID string, deviceType device.Type, propertyID string) (ConsumerProperty, bool) {
	for _, catalog := range BuiltInConsumerCatalogs() {
		if catalog.ID != consumerID {
			continue
		}
		for _, property := range catalog.Properties {
			if property.DeviceType == deviceType && property.ID == propertyID {
				return property, true
			}
		}
	}
	return ConsumerProperty{}, false
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
	batteryStatus := func(deviceType device.Type) []device.ConsumerParameterMapping {
		return []device.ConsumerParameterMapping{
			optional(deviceType, "battery", "level", "BatteryService.BatteryLevel"),
			optional(deviceType, "battery", "low", "BatteryService.StatusLowBattery"),
		}
	}
	contracts := []device.ConsumerModelContract{
		{ConsumerID: "homekit", DeviceType: device.TypeSwitch, Parameters: []device.ConsumerParameterMapping{required(device.TypeSwitch, "switch", "power", "Switch.On")}},
		{ConsumerID: "homekit", DeviceType: device.TypeLightbulb, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeLightbulb, "switch", "power", "Lightbulb.On"), optional(device.TypeLightbulb, "light", "brightness", "Lightbulb.Brightness"), optional(device.TypeLightbulb, "light", "color-temperature", "Lightbulb.ColorTemperature"), optional(device.TypeLightbulb, "light", "hue", "Lightbulb.Hue"), optional(device.TypeLightbulb, "light", "saturation", "Lightbulb.Saturation"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeOutlet, Parameters: []device.ConsumerParameterMapping{required(device.TypeOutlet, "switch", "power", "Outlet.On"), optional(device.TypeOutlet, "outlet", "in-use", "Outlet.OutletInUse")}},
		{ConsumerID: "homekit", DeviceType: device.TypeTemperatureSensor, Parameters: append([]device.ConsumerParameterMapping{
			required(device.TypeTemperatureSensor, "temperature", "current-temperature", "TemperatureSensor.CurrentTemperature"),
		}, batteryStatus(device.TypeTemperatureSensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeHumiditySensor, Parameters: append([]device.ConsumerParameterMapping{
			required(device.TypeHumiditySensor, "humidity", "current-humidity", "HumiditySensor.CurrentRelativeHumidity"),
		}, batteryStatus(device.TypeHumiditySensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeTemperatureHumiditySensor, Parameters: append([]device.ConsumerParameterMapping{
			required(device.TypeTemperatureHumiditySensor, "temperature", "current-temperature", "TemperatureSensor.CurrentTemperature"),
			required(device.TypeTemperatureHumiditySensor, "humidity", "current-humidity", "HumiditySensor.CurrentRelativeHumidity"),
		}, batteryStatus(device.TypeTemperatureHumiditySensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeContactSensor, Parameters: append([]device.ConsumerParameterMapping{required(device.TypeContactSensor, "contact", "contact-detected", "ContactSensor.ContactSensorState")}, sensorStatus(device.TypeContactSensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeMotionSensor, Parameters: append([]device.ConsumerParameterMapping{required(device.TypeMotionSensor, "motion", "motion-detected", "MotionSensor.MotionDetected")}, sensorStatus(device.TypeMotionSensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeFan, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeFan, "fan", "active", "FanV2.Active"), required(device.TypeFan, "fan", "current-state", "FanV2.CurrentFanState"), required(device.TypeFan, "fan", "target-state", "FanV2.TargetFanState"), required(device.TypeFan, "fan", "rotation-speed", "FanV2.RotationSpeed"), optional(device.TypeFan, "fan", "swing-mode", "FanV2.SwingMode"), optional(device.TypeFan, "fan", "rotation-direction", "FanV2.RotationDirection"), optional(device.TypeFan, "fan", "lock-physical-controls", "FanV2.LockPhysicalControls"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeAirPurifier, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeAirPurifier, "air-purifier", "active", "AirPurifier.Active"), required(device.TypeAirPurifier, "air-purifier", "current-state", "AirPurifier.CurrentAirPurifierState"), optional(device.TypeAirPurifier, "air-purifier", "target-state", "AirPurifier.TargetAirPurifierState"), optional(device.TypeAirPurifier, "air-purifier", "rotation-speed", "AirPurifier.RotationSpeed"), optional(device.TypeAirPurifier, "air-purifier", "swing-mode", "AirPurifier.SwingMode"), optional(device.TypeAirPurifier, "air-purifier", "lock-physical-controls", "AirPurifier.LockPhysicalControls"), optional(device.TypeAirPurifier, "air-quality", "current-air-quality", "AirQualitySensor.AirQuality"), optional(device.TypeAirPurifier, "air-quality", "pm2.5-density", "AirQualitySensor.PM2.5Density"), optional(device.TypeAirPurifier, "air-quality", "voc-density", "AirQualitySensor.VOCDensity"), optional(device.TypeAirPurifier, "filter", "life-level", "FilterMaintenance.FilterLifeLevel"), optional(device.TypeAirPurifier, "filter", "change-indication", "FilterMaintenance.FilterChangeIndication"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeWindowCovering, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeWindowCovering, "window-covering", "current-position", "WindowCovering.CurrentPosition"), required(device.TypeWindowCovering, "window-covering", "target-position", "WindowCovering.TargetPosition"), required(device.TypeWindowCovering, "window-covering", "position-state", "WindowCovering.PositionState"), optional(device.TypeWindowCovering, "window-covering", "obstruction-detected", "WindowCovering.ObstructionDetected"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeIlluminanceSensor, Parameters: append([]device.ConsumerParameterMapping{
			required(device.TypeIlluminanceSensor, "illuminance", "current-illuminance", "LightSensor.CurrentAmbientLightLevel"),
		}, batteryStatus(device.TypeIlluminanceSensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeOccupancySensor, Parameters: append([]device.ConsumerParameterMapping{
			required(device.TypeOccupancySensor, "occupancy", "occupancy-detected", "OccupancySensor.OccupancyDetected"),
		}, sensorStatus(device.TypeOccupancySensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeLeakSensor, Parameters: append([]device.ConsumerParameterMapping{
			required(device.TypeLeakSensor, "leak", "leak-detected", "LeakSensor.LeakDetected"),
		}, sensorStatus(device.TypeLeakSensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeSmokeSensor, Parameters: append([]device.ConsumerParameterMapping{
			required(device.TypeSmokeSensor, "smoke", "smoke-detected", "SmokeSensor.SmokeDetected"),
		}, sensorStatus(device.TypeSmokeSensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeCarbonMonoxideSensor, Parameters: append([]device.ConsumerParameterMapping{
			required(device.TypeCarbonMonoxideSensor, "carbon-monoxide", "detected", "CarbonMonoxideSensor.CarbonMonoxideDetected"),
			optional(device.TypeCarbonMonoxideSensor, "carbon-monoxide", "current-level", "CarbonMonoxideSensor.CarbonMonoxideLevel"),
			optional(device.TypeCarbonMonoxideSensor, "carbon-monoxide", "peak-level", "CarbonMonoxideSensor.CarbonMonoxidePeakLevel"),
		}, sensorStatus(device.TypeCarbonMonoxideSensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeCarbonDioxideSensor, Parameters: append([]device.ConsumerParameterMapping{
			required(device.TypeCarbonDioxideSensor, "carbon-dioxide", "detected", "CarbonDioxideSensor.CarbonDioxideDetected"),
			optional(device.TypeCarbonDioxideSensor, "carbon-dioxide", "current-level", "CarbonDioxideSensor.CarbonDioxideLevel"),
			optional(device.TypeCarbonDioxideSensor, "carbon-dioxide", "peak-level", "CarbonDioxideSensor.CarbonDioxidePeakLevel"),
		}, batteryStatus(device.TypeCarbonDioxideSensor)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeAirQualitySensor, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeAirQualitySensor, "air-quality", "current-air-quality", "AirQualitySensor.AirQuality"),
			optional(device.TypeAirQualitySensor, "air-quality", "pm2.5-density", "AirQualitySensor.PM2.5Density"),
			optional(device.TypeAirQualitySensor, "air-quality", "pm10-density", "AirQualitySensor.PM10Density"),
			optional(device.TypeAirQualitySensor, "air-quality", "voc-density", "AirQualitySensor.VOCDensity"),
			optional(device.TypeAirQualitySensor, "air-quality", "carbon-dioxide-level", "AirQualitySensor.CarbonDioxideLevel"),
			optional(device.TypeAirQualitySensor, "air-quality", "nitrogen-dioxide-density", "AirQualitySensor.NitrogenDioxideDensity"),
			optional(device.TypeAirQualitySensor, "air-quality", "ozone-density", "AirQualitySensor.OzoneDensity"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeThermostat, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeThermostat, "thermostat", "current-state", "Thermostat.CurrentHeatingCoolingState"),
			required(device.TypeThermostat, "thermostat", "target-mode", "Thermostat.TargetHeatingCoolingState"),
			required(device.TypeThermostat, "temperature", "current-temperature", "Thermostat.CurrentTemperature"),
			required(device.TypeThermostat, "temperature", "target-temperature", "Thermostat.TargetTemperature"),
			optional(device.TypeThermostat, "temperature", "heating-threshold", "Thermostat.HeatingThresholdTemperature"),
			optional(device.TypeThermostat, "temperature", "cooling-threshold", "Thermostat.CoolingThresholdTemperature"),
			optional(device.TypeThermostat, "humidity", "current-humidity", "Thermostat.CurrentRelativeHumidity"),
			optional(device.TypeThermostat, "thermostat", "display-units", "Thermostat.TemperatureDisplayUnits"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeAirConditioner, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeAirConditioner, "air-conditioner", "active", "HeaterCooler.Active"),
			optional(device.TypeAirConditioner, "air-conditioner", "current-state", "HeaterCooler.CurrentHeaterCoolerState"),
			required(device.TypeAirConditioner, "air-conditioner", "target-mode", "HeaterCooler.TargetHeaterCoolerState"),
			optional(device.TypeAirConditioner, "temperature", "current-temperature", "HeaterCooler.CurrentTemperature"),
			required(device.TypeAirConditioner, "temperature", "target-temperature", "HeaterCooler.TargetTemperature"),
			optional(device.TypeAirConditioner, "air-conditioner", "rotation-speed", "HeaterCooler.RotationSpeed"),
			optional(device.TypeAirConditioner, "air-conditioner", "vertical-swing", "HeaterCooler.SwingMode"),
			optional(device.TypeAirConditioner, "humidity", "current-humidity", "HumiditySensor.CurrentRelativeHumidity"),
			optional(device.TypeAirConditioner, "air-conditioner", "display-units", "HeaterCooler.TemperatureDisplayUnits"),
			optional(device.TypeAirConditioner, "filter", "life-level", "FilterMaintenance.FilterLifeLevel"),
			optional(device.TypeAirConditioner, "filter", "change-indication", "FilterMaintenance.FilterChangeIndication"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeHeaterCooler, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeHeaterCooler, "heater-cooler", "active", "HeaterCooler.Active"),
			required(device.TypeHeaterCooler, "heater-cooler", "current-state", "HeaterCooler.CurrentHeaterCoolerState"),
			required(device.TypeHeaterCooler, "heater-cooler", "target-state", "HeaterCooler.TargetHeaterCoolerState"),
			required(device.TypeHeaterCooler, "temperature", "current-temperature", "HeaterCooler.CurrentTemperature"),
			optional(device.TypeHeaterCooler, "temperature", "heating-threshold", "HeaterCooler.HeatingThresholdTemperature"),
			optional(device.TypeHeaterCooler, "temperature", "cooling-threshold", "HeaterCooler.CoolingThresholdTemperature"),
			optional(device.TypeHeaterCooler, "heater-cooler", "rotation-speed", "HeaterCooler.RotationSpeed"),
			optional(device.TypeHeaterCooler, "heater-cooler", "swing-mode", "HeaterCooler.SwingMode"),
			optional(device.TypeHeaterCooler, "heater-cooler", "lock-physical-controls", "HeaterCooler.LockPhysicalControls"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeHumidifierDehumidifier, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeHumidifierDehumidifier, "humidifier-dehumidifier", "active", "HumidifierDehumidifier.Active"),
			required(device.TypeHumidifierDehumidifier, "humidifier-dehumidifier", "current-state", "HumidifierDehumidifier.CurrentState"),
			required(device.TypeHumidifierDehumidifier, "humidifier-dehumidifier", "target-state", "HumidifierDehumidifier.TargetState"),
			required(device.TypeHumidifierDehumidifier, "humidity", "current-humidity", "HumidifierDehumidifier.CurrentRelativeHumidity"),
			required(device.TypeHumidifierDehumidifier, "humidity", "target-humidity", "HumidifierDehumidifier.TargetHumidity"),
			optional(device.TypeHumidifierDehumidifier, "humidifier-dehumidifier", "water-level", "HumidifierDehumidifier.WaterLevel"),
			optional(device.TypeHumidifierDehumidifier, "humidifier-dehumidifier", "lock-physical-controls", "HumidifierDehumidifier.LockPhysicalControls"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeLock, Parameters: append([]device.ConsumerParameterMapping{
			required(device.TypeLock, "lock", "current-state", "LockMechanism.LockCurrentState"),
			required(device.TypeLock, "lock", "target-state", "LockMechanism.LockTargetState"),
		}, sensorStatus(device.TypeLock)...)},
		{ConsumerID: "homekit", DeviceType: device.TypeGarageDoor, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeGarageDoor, "garage-door", "current-state", "GarageDoorOpener.CurrentDoorState"),
			required(device.TypeGarageDoor, "garage-door", "target-state", "GarageDoorOpener.TargetDoorState"),
			optional(device.TypeGarageDoor, "garage-door", "obstruction-detected", "GarageDoorOpener.ObstructionDetected"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeSecuritySystem, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeSecuritySystem, "security-system", "current-state", "SecuritySystem.CurrentState"),
			required(device.TypeSecuritySystem, "security-system", "target-state", "SecuritySystem.TargetState"),
			optional(device.TypeSecuritySystem, "security-system", "alarm-type", "SecuritySystem.AlarmType"),
			optional(device.TypeSecuritySystem, "security", "tampered", "SecuritySystem.StatusTampered"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeValve, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeValve, "valve", "active", "Valve.Active"),
			required(device.TypeValve, "valve", "in-use", "Valve.InUse"),
			required(device.TypeValve, "valve", "valve-type", "Valve.ValveType"),
			optional(device.TypeValve, "valve", "set-duration", "Valve.SetDuration"),
			optional(device.TypeValve, "valve", "remaining-duration", "Valve.RemainingDuration"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeSpeaker, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeSpeaker, "speaker", "active", "Speaker.Active"),
			required(device.TypeSpeaker, "speaker", "volume", "Speaker.Volume"),
			required(device.TypeSpeaker, "speaker", "mute", "Speaker.Mute"),
			optional(device.TypeSpeaker, "speaker", "current-media-state", "Speaker.CurrentMediaState"),
			optional(device.TypeSpeaker, "speaker", "target-media-state", "Speaker.TargetMediaState"),
		}},
		{ConsumerID: "homekit", DeviceType: device.TypeTelevision, Parameters: []device.ConsumerParameterMapping{
			required(device.TypeTelevision, "television", "active", "Television.Active"),
			required(device.TypeTelevision, "television", "volume", "Television.Volume"),
			required(device.TypeTelevision, "television", "target-media-state", "Television.TargetMediaState"),
			required(device.TypeTelevision, "television", "remote-key", "Television.RemoteKey"),
			optional(device.TypeTelevision, "television", "current-media-state", "Television.CurrentMediaState"),
		}},
	}
	return contracts
}

func HomeKitConsumerContract(deviceType device.Type) (device.ConsumerModelContract, bool) {
	return ConsumerContract("homekit", deviceType)
}
