package mapping

import "github.com/feranydev/homeloom/backend/internal/domain/device"

// MatterConsumerContracts declares the official Matter bridge device set using the
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
		{ConsumerID: "matter", DeviceType: device.TypeIlluminanceSensor, Parameters: []device.ConsumerParameterMapping{
			required("illuminance", "current-illuminance", "IlluminanceMeasurement.MeasuredValue"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypePressureSensor, Parameters: []device.ConsumerParameterMapping{
			required("pressure", "current-pressure", "PressureMeasurement.MeasuredValue"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeLeakSensor, Parameters: []device.ConsumerParameterMapping{
			required("leak", "leak-detected", "BooleanState.StateValue"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeSmokeSensor, Parameters: []device.ConsumerParameterMapping{
			required("smoke", "smoke-detected", "SmokeCoAlarm.SmokeState"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeCarbonMonoxideSensor, Parameters: []device.ConsumerParameterMapping{
			required("carbon-monoxide", "detected", "SmokeCoAlarm.CoState"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeAirQualitySensor, Parameters: []device.ConsumerParameterMapping{
			required("air-quality", "current-air-quality", "AirQuality.AirQuality"),
			optional("air-quality", "pm2.5-density", "Pm25ConcentrationMeasurement.MeasuredValue"),
			optional("air-quality", "pm10-density", "Pm10ConcentrationMeasurement.MeasuredValue"),
			optional("air-quality", "voc-density", "TotalVolatileOrganicCompoundsConcentrationMeasurement.MeasuredValue"),
			optional("air-quality", "carbon-dioxide-level", "CarbonDioxideConcentrationMeasurement.MeasuredValue"),
			optional("air-quality", "carbon-monoxide-level", "CarbonMonoxideConcentrationMeasurement.MeasuredValue"),
			optional("temperature", "current-temperature", "TemperatureMeasurement.MeasuredValue"),
			optional("humidity", "current-humidity", "RelativeHumidityMeasurement.MeasuredValue"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeValve, Parameters: []device.ConsumerParameterMapping{
			required("valve", "active", "ValveConfigurationAndControl.TargetState"),
			optional("valve", "in-use", "ValveConfigurationAndControl.CurrentState"),
			optional("valve", "position", "ValveConfigurationAndControl.CurrentLevel"),
			optional("valve", "set-duration", "ValveConfigurationAndControl.DefaultOpenDuration"),
			optional("valve", "remaining-duration", "ValveConfigurationAndControl.RemainingDuration"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypePump, Parameters: []device.ConsumerParameterMapping{
			required("pump", "active", "OnOff.OnOff"),
			optional("pump", "speed", "LevelControl.CurrentLevel"),
			optional("pressure", "current-pressure", "PressureMeasurement.MeasuredValue"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeAirPurifier, Parameters: []device.ConsumerParameterMapping{
			required("air-purifier", "active", "OnOff.OnOff"),
			optional("air-purifier", "target-state", "FanControl.FanMode"),
			optional("air-purifier", "rotation-speed", "FanControl.PercentSetting"),
			optional("air-purifier", "swing-mode", "FanControl.RockSetting"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeSpeaker, Parameters: []device.ConsumerParameterMapping{
			required("speaker", "mute", "OnOff.OnOff"),
			required("speaker", "volume", "LevelControl.CurrentLevel"),
		}},
		{ConsumerID: "matter", DeviceType: device.TypeTelevision, Parameters: []device.ConsumerParameterMapping{
			required("television", "active", "OnOff.OnOff"),
			optional("television", "current-media-state", "MediaPlayback.CurrentState"),
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
		{device.TypeValve, path("valve", "active"), "ValveConfigurationAndControl.Open", "打开阀门"},
		{device.TypeValve, path("valve", "active"), "ValveConfigurationAndControl.Close", "关闭阀门"},
		{device.TypePump, path("pump", "active"), "OnOff.On", "启动水泵"},
		{device.TypePump, path("pump", "active"), "OnOff.Off", "停止水泵"},
		{device.TypeAirPurifier, path("air-purifier", "active"), "OnOff.On", "启动净化器"},
		{device.TypeAirPurifier, path("air-purifier", "active"), "OnOff.Off", "停止净化器"},
		{device.TypeSpeaker, path("speaker", "mute"), "OnOff.On", "取消静音"},
		{device.TypeSpeaker, path("speaker", "mute"), "OnOff.Off", "静音"},
		{device.TypeTelevision, path("television", "target-media-state"), "MediaPlayback.Play", "播放"},
		{device.TypeTelevision, path("television", "target-media-state"), "MediaPlayback.Pause", "暂停"},
		{device.TypeTelevision, path("television", "target-media-state"), "MediaPlayback.Stop", "停止"},
		{device.TypeTelevision, path("television", "remote-key"), "KeypadInput.SendKey", "发送遥控按键"},
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
