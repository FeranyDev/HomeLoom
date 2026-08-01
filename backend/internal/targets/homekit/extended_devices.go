package homekit

import (
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"go.uber.org/zap"
)

type extendedUpdate func(device.Device) uint64

type extendedValue func(device.Device) (any, bool)

func (b *accessoryBindings) addExtendedUpdate(deviceID string, current *characteristic.C, value extendedValue) {
	b.extendedUpdates[deviceID] = append(b.extendedUpdates[deviceID], func(item device.Device) uint64 {
		next, found := value(item)
		if !found {
			return 0
		}
		_, _ = current.SetValueRequest(next, nil)
		return 1
	})
}

func (b *accessoryBindings) updateExtended(item device.Device) uint64 {
	var pushes uint64
	for _, update := range b.extendedUpdates[item.ID] {
		pushes += update(item)
	}
	return pushes
}

func propertyValue(capabilityID, propertyID string, convert func(device.PropertyValue) (any, bool)) extendedValue {
	return func(item device.Device) (any, bool) {
		property, found := item.Property("main", capabilityID, propertyID)
		if !found {
			return nil, false
		}
		return convert(property.Value)
	}
}

func boolValue(value device.PropertyValue) (any, bool) {
	if value.Bool == nil {
		return nil, false
	}
	return *value.Bool, true
}

func boolIntValue(falseValue, trueValue int) func(device.PropertyValue) (any, bool) {
	return func(value device.PropertyValue) (any, bool) {
		if value.Bool == nil {
			return nil, false
		}
		if *value.Bool {
			return trueValue, true
		}
		return falseValue, true
	}
}

func numberValue(value device.PropertyValue) (any, bool) {
	if value.Number == nil {
		return nil, false
	}
	return *value.Number, true
}

func integerValue(value device.PropertyValue) (any, bool) {
	if value.Int == nil {
		return nil, false
	}
	return int(*value.Int), true
}

func enumIntValue(values map[string]int, fallback int) func(device.PropertyValue) (any, bool) {
	return func(value device.PropertyValue) (any, bool) {
		if value.String == nil {
			return nil, false
		}
		if mapped, found := values[*value.String]; found {
			return mapped, true
		}
		return fallback, true
	}
}

func extendedInfo(item device.Device) accessory.Info {
	return accessory.Info{Name: item.Name, SerialNumber: item.ID, Manufacturer: "HomeLoom", Model: string(item.Type), Firmware: "0.0.1"}
}

func (b *accessoryBindings) extendedAccessory(item device.Device, accessoryID uint64, category byte, primary *service.S) *accessory.A {
	a := accessory.New(extendedInfo(item), category)
	a.Id = accessoryID
	fault := characteristic.NewStatusFault()
	primary.AddC(fault.C)
	a.AddS(primary)
	b.faults[item.ID] = fault
	return a
}

func (b *accessoryBindings) addOptionalTamper(item device.Device, primary *service.S) {
	if _, found := item.Property("main", "security", "tampered"); !found {
		return
	}
	current := characteristic.NewStatusTampered()
	primary.AddC(current.C)
	b.tampered[item.ID] = current
}

func (b *accessoryBindings) newExtendedAccessory(item device.Device, accessoryID uint64, devices *application.DeviceService, logger *zap.Logger, route accessoryRoute, sourceDeviceID string) *accessory.A {
	write := func(capabilityID, propertyID string, value device.PropertyValue) error {
		return writeHomeKitProperty(devices, logger, sourceDeviceID, route, capabilityID, propertyID, value)
	}
	switch item.Type {
	case device.TypeIlluminanceSensor:
		primary := service.NewLightSensor()
		a := b.extendedAccessory(item, accessoryID, accessory.TypeSensor, primary.S)
		b.addExtendedUpdate(item.ID, primary.CurrentAmbientLightLevel.C, propertyValue("illuminance", "current-illuminance", numberValue))
		return a
	case device.TypeOccupancySensor:
		primary := service.NewOccupancySensor()
		b.addOptionalTamper(item, primary.S)
		a := b.extendedAccessory(item, accessoryID, accessory.TypeSensor, primary.S)
		b.addExtendedUpdate(item.ID, primary.OccupancyDetected.C, propertyValue("occupancy", "occupancy-detected", boolIntValue(characteristic.OccupancyDetectedOccupancyNotDetected, characteristic.OccupancyDetectedOccupancyDetected)))
		return a
	case device.TypeLeakSensor:
		primary := service.NewLeakSensor()
		b.addOptionalTamper(item, primary.S)
		a := b.extendedAccessory(item, accessoryID, accessory.TypeSensor, primary.S)
		b.addExtendedUpdate(item.ID, primary.LeakDetected.C, propertyValue("leak", "leak-detected", boolIntValue(characteristic.LeakDetectedLeakNotDetected, characteristic.LeakDetectedLeakDetected)))
		return a
	case device.TypeSmokeSensor:
		primary := service.NewSmokeSensor()
		b.addOptionalTamper(item, primary.S)
		a := b.extendedAccessory(item, accessoryID, accessory.TypeSensor, primary.S)
		b.addExtendedUpdate(item.ID, primary.SmokeDetected.C, propertyValue("smoke", "smoke-detected", boolIntValue(characteristic.SmokeDetectedSmokeNotDetected, characteristic.SmokeDetectedSmokeDetected)))
		return a
	case device.TypeCarbonMonoxideSensor:
		primary := service.NewCarbonMonoxideSensor()
		b.addOptionalTamper(item, primary.S)
		if _, found := item.Property("main", "carbon-monoxide", "current-level"); found {
			current := characteristic.NewCarbonMonoxideLevel()
			primary.AddC(current.C)
			b.addExtendedUpdate(item.ID, current.C, propertyValue("carbon-monoxide", "current-level", numberValue))
		}
		if _, found := item.Property("main", "carbon-monoxide", "peak-level"); found {
			current := characteristic.NewCarbonMonoxidePeakLevel()
			primary.AddC(current.C)
			b.addExtendedUpdate(item.ID, current.C, propertyValue("carbon-monoxide", "peak-level", numberValue))
		}
		a := b.extendedAccessory(item, accessoryID, accessory.TypeSensor, primary.S)
		b.addExtendedUpdate(item.ID, primary.CarbonMonoxideDetected.C, propertyValue("carbon-monoxide", "detected", boolIntValue(characteristic.CarbonMonoxideDetectedCOLevelsNormal, characteristic.CarbonMonoxideDetectedCOLevelsAbnormal)))
		return a
	case device.TypeCarbonDioxideSensor:
		primary := service.NewCarbonDioxideSensor()
		if _, found := item.Property("main", "carbon-dioxide", "current-level"); found {
			current := characteristic.NewCarbonDioxideLevel()
			primary.AddC(current.C)
			b.addExtendedUpdate(item.ID, current.C, propertyValue("carbon-dioxide", "current-level", numberValue))
		}
		if _, found := item.Property("main", "carbon-dioxide", "peak-level"); found {
			current := characteristic.NewCarbonDioxidePeakLevel()
			primary.AddC(current.C)
			b.addExtendedUpdate(item.ID, current.C, propertyValue("carbon-dioxide", "peak-level", numberValue))
		}
		a := b.extendedAccessory(item, accessoryID, accessory.TypeSensor, primary.S)
		b.addExtendedUpdate(item.ID, primary.CarbonDioxideDetected.C, propertyValue("carbon-dioxide", "detected", boolIntValue(characteristic.CarbonDioxideDetectedCO2LevelsNormal, characteristic.CarbonDioxideDetectedCO2LevelsAbnormal)))
		return a
	case device.TypeAirQualitySensor:
		return b.newAirQualityAccessory(item, accessoryID)
	case device.TypeThermostat:
		return b.newThermostatAccessory(item, accessoryID, write)
	case device.TypeHeaterCooler:
		return b.newHeaterCoolerAccessory(item, accessoryID, write)
	case device.TypeHumidifierDehumidifier:
		return b.newHumidifierAccessory(item, accessoryID, write)
	case device.TypeLock:
		return b.newLockAccessory(item, accessoryID, write)
	case device.TypeGarageDoor:
		return b.newGarageDoorAccessory(item, accessoryID, write)
	case device.TypeSecuritySystem:
		return b.newSecurityAccessory(item, accessoryID, write)
	case device.TypeValve:
		return b.newValveAccessory(item, accessoryID, write)
	case device.TypeSpeaker:
		return b.newSpeakerAccessory(item, accessoryID, write)
	default:
		return nil
	}
}

func (b *accessoryBindings) newAirQualityAccessory(item device.Device, accessoryID uint64) *accessory.A {
	primary := service.NewAirQualitySensor()
	a := b.extendedAccessory(item, accessoryID, accessory.TypeSensor, primary.S)
	b.addExtendedUpdate(item.ID, primary.AirQuality.C, propertyValue("air-quality", "current-air-quality", enumIntValue(map[string]int{
		"unknown": characteristic.AirQualityUnknown, "excellent": characteristic.AirQualityExcellent, "good": characteristic.AirQualityGood,
		"fair": characteristic.AirQualityFair, "inferior": characteristic.AirQualityInferior, "poor": characteristic.AirQualityPoor,
	}, characteristic.AirQualityUnknown)))
	type optionalFloat struct {
		propertyID string
		create     func() *characteristic.C
	}
	items := []optionalFloat{
		{"pm2.5-density", func() *characteristic.C { return characteristic.NewPM2_5Density().C }},
		{"pm10-density", func() *characteristic.C { return characteristic.NewPM10Density().C }},
		{"voc-density", func() *characteristic.C { return characteristic.NewVOCDensity().C }},
		{"carbon-dioxide-level", func() *characteristic.C { return characteristic.NewCarbonDioxideLevel().C }},
		{"nitrogen-dioxide-density", func() *characteristic.C { return characteristic.NewNitrogenDioxideDensity().C }},
		{"ozone-density", func() *characteristic.C { return characteristic.NewOzoneDensity().C }},
	}
	for _, optional := range items {
		if _, found := item.Property("main", "air-quality", optional.propertyID); !found {
			continue
		}
		current := optional.create()
		primary.AddC(current)
		b.addExtendedUpdate(item.ID, current, propertyValue("air-quality", optional.propertyID, numberValue))
	}
	return a
}

func (b *accessoryBindings) newThermostatAccessory(item device.Device, accessoryID uint64, write func(string, string, device.PropertyValue) error) *accessory.A {
	primary := service.NewThermostat()
	a := b.extendedAccessory(item, accessoryID, accessory.TypeThermostat, primary.S)
	primary.TargetHeatingCoolingState.OnSetRemoteValue(func(value int) error {
		return write("thermostat", "target-mode", device.EnumValue(thermostatTargetName(value)))
	})
	primary.TargetTemperature.OnSetRemoteValue(func(value float64) error {
		return write("temperature", "target-temperature", device.NumberValue(value))
	})
	b.addExtendedUpdate(item.ID, primary.CurrentHeatingCoolingState.C, propertyValue("thermostat", "current-state", enumIntValue(map[string]int{"off": characteristic.CurrentHeatingCoolingStateOff, "idle": characteristic.CurrentHeatingCoolingStateOff, "heating": characteristic.CurrentHeatingCoolingStateHeat, "cooling": characteristic.CurrentHeatingCoolingStateCool}, characteristic.CurrentHeatingCoolingStateOff)))
	b.addExtendedUpdate(item.ID, primary.TargetHeatingCoolingState.C, propertyValue("thermostat", "target-mode", enumIntValue(map[string]int{"off": characteristic.TargetHeatingCoolingStateOff, "heat": characteristic.TargetHeatingCoolingStateHeat, "cool": characteristic.TargetHeatingCoolingStateCool, "auto": characteristic.TargetHeatingCoolingStateAuto}, characteristic.TargetHeatingCoolingStateOff)))
	b.addExtendedUpdate(item.ID, primary.CurrentTemperature.C, propertyValue("temperature", "current-temperature", numberValue))
	b.addExtendedUpdate(item.ID, primary.TargetTemperature.C, propertyValue("temperature", "target-temperature", numberValue))
	if _, found := item.Property("main", "thermostat", "display-units"); found {
		primary.TemperatureDisplayUnits.OnSetRemoteValue(func(value int) error {
			return write("thermostat", "display-units", device.EnumValue(temperatureUnitName(value)))
		})
		b.addExtendedUpdate(item.ID, primary.TemperatureDisplayUnits.C, propertyValue("thermostat", "display-units", enumIntValue(map[string]int{"celsius": characteristic.TemperatureDisplayUnitsCelsius, "fahrenheit": characteristic.TemperatureDisplayUnitsFahrenheit}, characteristic.TemperatureDisplayUnitsCelsius)))
	}
	if _, found := item.Property("main", "temperature", "heating-threshold"); found {
		current := characteristic.NewHeatingThresholdTemperature()
		current.OnSetRemoteValue(func(value float64) error { return write("temperature", "heating-threshold", device.NumberValue(value)) })
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("temperature", "heating-threshold", numberValue))
	}
	if _, found := item.Property("main", "temperature", "cooling-threshold"); found {
		current := characteristic.NewCoolingThresholdTemperature()
		current.OnSetRemoteValue(func(value float64) error { return write("temperature", "cooling-threshold", device.NumberValue(value)) })
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("temperature", "cooling-threshold", numberValue))
	}
	if _, found := item.Property("main", "humidity", "current-humidity"); found {
		current := characteristic.NewCurrentRelativeHumidity()
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("humidity", "current-humidity", numberValue))
	}
	return a
}

func (b *accessoryBindings) newHeaterCoolerAccessory(item device.Device, accessoryID uint64, write func(string, string, device.PropertyValue) error) *accessory.A {
	primary := service.NewHeaterCooler()
	a := b.extendedAccessory(item, accessoryID, accessory.TypeAirConditioner, primary.S)
	primary.Active.OnSetRemoteValue(func(value int) error {
		return write("heater-cooler", "active", device.BoolValue(value == characteristic.ActiveActive))
	})
	primary.TargetHeaterCoolerState.OnSetRemoteValue(func(value int) error {
		return write("heater-cooler", "target-state", device.EnumValue(heaterCoolerTargetName(value)))
	})
	b.addExtendedUpdate(item.ID, primary.Active.C, propertyValue("heater-cooler", "active", boolIntValue(characteristic.ActiveInactive, characteristic.ActiveActive)))
	b.addExtendedUpdate(item.ID, primary.CurrentHeaterCoolerState.C, propertyValue("heater-cooler", "current-state", enumIntValue(map[string]int{"inactive": characteristic.CurrentHeaterCoolerStateInactive, "idle": characteristic.CurrentHeaterCoolerStateIdle, "heating": characteristic.CurrentHeaterCoolerStateHeating, "cooling": characteristic.CurrentHeaterCoolerStateCooling}, characteristic.CurrentHeaterCoolerStateInactive)))
	b.addExtendedUpdate(item.ID, primary.TargetHeaterCoolerState.C, propertyValue("heater-cooler", "target-state", enumIntValue(map[string]int{"auto": characteristic.TargetHeaterCoolerStateAuto, "heat": characteristic.TargetHeaterCoolerStateHeat, "cool": characteristic.TargetHeaterCoolerStateCool}, characteristic.TargetHeaterCoolerStateAuto)))
	b.addExtendedUpdate(item.ID, primary.CurrentTemperature.C, propertyValue("temperature", "current-temperature", numberValue))
	if _, found := item.Property("main", "temperature", "heating-threshold"); found {
		current := characteristic.NewHeatingThresholdTemperature()
		current.OnSetRemoteValue(func(value float64) error { return write("temperature", "heating-threshold", device.NumberValue(value)) })
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("temperature", "heating-threshold", numberValue))
	}
	if _, found := item.Property("main", "temperature", "cooling-threshold"); found {
		current := characteristic.NewCoolingThresholdTemperature()
		current.OnSetRemoteValue(func(value float64) error { return write("temperature", "cooling-threshold", device.NumberValue(value)) })
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("temperature", "cooling-threshold", numberValue))
	}
	if _, found := item.Property("main", "heater-cooler", "rotation-speed"); found {
		current := characteristic.NewRotationSpeed()
		current.OnSetRemoteValue(func(value float64) error { return write("heater-cooler", "rotation-speed", device.NumberValue(value)) })
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("heater-cooler", "rotation-speed", numberValue))
	}
	if _, found := item.Property("main", "heater-cooler", "swing-mode"); found {
		current := characteristic.NewSwingMode()
		current.OnSetRemoteValue(func(value int) error {
			return write("heater-cooler", "swing-mode", device.BoolValue(value == characteristic.SwingModeSwingEnabled))
		})
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("heater-cooler", "swing-mode", boolIntValue(characteristic.SwingModeSwingDisabled, characteristic.SwingModeSwingEnabled)))
	}
	if _, found := item.Property("main", "heater-cooler", "lock-physical-controls"); found {
		current := characteristic.NewLockPhysicalControls()
		current.OnSetRemoteValue(func(value int) error {
			return write("heater-cooler", "lock-physical-controls", device.BoolValue(value == characteristic.LockPhysicalControlsControlLockEnabled))
		})
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("heater-cooler", "lock-physical-controls", boolIntValue(characteristic.LockPhysicalControlsControlLockDisabled, characteristic.LockPhysicalControlsControlLockEnabled)))
	}
	return a
}

func (b *accessoryBindings) newHumidifierAccessory(item device.Device, accessoryID uint64, write func(string, string, device.PropertyValue) error) *accessory.A {
	primary := service.NewHumidifierDehumidifier()
	a := b.extendedAccessory(item, accessoryID, accessory.TypeHumidifier, primary.S)
	primary.Active.OnSetRemoteValue(func(value int) error {
		return write("humidifier-dehumidifier", "active", device.BoolValue(value == characteristic.ActiveActive))
	})
	primary.TargetHumidifierDehumidifierState.OnSetRemoteValue(func(value int) error {
		return write("humidifier-dehumidifier", "target-state", device.EnumValue(humidifierTargetName(value)))
	})
	b.addExtendedUpdate(item.ID, primary.Active.C, propertyValue("humidifier-dehumidifier", "active", boolIntValue(characteristic.ActiveInactive, characteristic.ActiveActive)))
	b.addExtendedUpdate(item.ID, primary.CurrentHumidifierDehumidifierState.C, propertyValue("humidifier-dehumidifier", "current-state", enumIntValue(map[string]int{"inactive": characteristic.CurrentHumidifierDehumidifierStateInactive, "idle": characteristic.CurrentHumidifierDehumidifierStateIdle, "humidifying": characteristic.CurrentHumidifierDehumidifierStateHumidifying, "dehumidifying": characteristic.CurrentHumidifierDehumidifierStateDehumidifying}, characteristic.CurrentHumidifierDehumidifierStateInactive)))
	b.addExtendedUpdate(item.ID, primary.TargetHumidifierDehumidifierState.C, propertyValue("humidifier-dehumidifier", "target-state", enumIntValue(map[string]int{"auto": characteristic.TargetHumidifierDehumidifierStateHumidifierOrDehumidifier, "humidify": characteristic.TargetHumidifierDehumidifierStateHumidifier, "dehumidify": characteristic.TargetHumidifierDehumidifierStateDehumidifier}, characteristic.TargetHumidifierDehumidifierStateHumidifierOrDehumidifier)))
	b.addExtendedUpdate(item.ID, primary.CurrentRelativeHumidity.C, propertyValue("humidity", "current-humidity", numberValue))
	writeHumidity := func(value float64) error { return write("humidity", "target-humidity", device.NumberValue(value)) }
	humidifierTarget := characteristic.NewRelativeHumidityHumidifierThreshold()
	dehumidifierTarget := characteristic.NewRelativeHumidityDehumidifierThreshold()
	humidifierTarget.OnSetRemoteValue(writeHumidity)
	dehumidifierTarget.OnSetRemoteValue(writeHumidity)
	primary.AddC(humidifierTarget.C)
	primary.AddC(dehumidifierTarget.C)
	b.addExtendedUpdate(item.ID, humidifierTarget.C, propertyValue("humidity", "target-humidity", numberValue))
	b.addExtendedUpdate(item.ID, dehumidifierTarget.C, propertyValue("humidity", "target-humidity", numberValue))
	if _, found := item.Property("main", "humidifier-dehumidifier", "water-level"); found {
		current := characteristic.NewWaterLevel()
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("humidifier-dehumidifier", "water-level", numberValue))
	}
	if _, found := item.Property("main", "humidifier-dehumidifier", "lock-physical-controls"); found {
		current := characteristic.NewLockPhysicalControls()
		current.OnSetRemoteValue(func(value int) error {
			return write("humidifier-dehumidifier", "lock-physical-controls", device.BoolValue(value == characteristic.LockPhysicalControlsControlLockEnabled))
		})
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("humidifier-dehumidifier", "lock-physical-controls", boolIntValue(characteristic.LockPhysicalControlsControlLockDisabled, characteristic.LockPhysicalControlsControlLockEnabled)))
	}
	return a
}

func (b *accessoryBindings) newLockAccessory(item device.Device, accessoryID uint64, write func(string, string, device.PropertyValue) error) *accessory.A {
	primary := service.NewLockMechanism()
	b.addOptionalTamper(item, primary.S)
	a := b.extendedAccessory(item, accessoryID, accessory.TypeDoorLock, primary.S)
	primary.LockTargetState.OnSetRemoteValue(func(value int) error { return write("lock", "target-state", device.EnumValue(lockTargetName(value))) })
	b.addExtendedUpdate(item.ID, primary.LockCurrentState.C, propertyValue("lock", "current-state", enumIntValue(map[string]int{"unsecured": characteristic.LockCurrentStateUnsecured, "secured": characteristic.LockCurrentStateSecured, "jammed": characteristic.LockCurrentStateJammed, "unknown": characteristic.LockCurrentStateUnknown}, characteristic.LockCurrentStateUnknown)))
	b.addExtendedUpdate(item.ID, primary.LockTargetState.C, propertyValue("lock", "target-state", enumIntValue(map[string]int{"unsecured": characteristic.LockTargetStateUnsecured, "secured": characteristic.LockTargetStateSecured}, characteristic.LockTargetStateUnsecured)))
	return a
}

func (b *accessoryBindings) newGarageDoorAccessory(item device.Device, accessoryID uint64, write func(string, string, device.PropertyValue) error) *accessory.A {
	primary := service.NewGarageDoorOpener()
	a := b.extendedAccessory(item, accessoryID, accessory.TypeGarageDoorOpener, primary.S)
	primary.TargetDoorState.OnSetRemoteValue(func(value int) error {
		return write("garage-door", "target-state", device.EnumValue(garageTargetName(value)))
	})
	b.addExtendedUpdate(item.ID, primary.CurrentDoorState.C, propertyValue("garage-door", "current-state", enumIntValue(map[string]int{"open": characteristic.CurrentDoorStateOpen, "closed": characteristic.CurrentDoorStateClosed, "opening": characteristic.CurrentDoorStateOpening, "closing": characteristic.CurrentDoorStateClosing, "stopped": characteristic.CurrentDoorStateStopped, "unknown": characteristic.CurrentDoorStateStopped}, characteristic.CurrentDoorStateStopped)))
	b.addExtendedUpdate(item.ID, primary.TargetDoorState.C, propertyValue("garage-door", "target-state", enumIntValue(map[string]int{"open": characteristic.TargetDoorStateOpen, "closed": characteristic.TargetDoorStateClosed}, characteristic.TargetDoorStateClosed)))
	b.addExtendedUpdate(item.ID, primary.ObstructionDetected.C, propertyValue("garage-door", "obstruction-detected", boolValue))
	return a
}

func (b *accessoryBindings) newSecurityAccessory(item device.Device, accessoryID uint64, write func(string, string, device.PropertyValue) error) *accessory.A {
	primary := service.NewSecuritySystem()
	b.addOptionalTamper(item, primary.S)
	a := b.extendedAccessory(item, accessoryID, accessory.TypeSecuritySystem, primary.S)
	primary.SecuritySystemTargetState.OnSetRemoteValue(func(value int) error {
		return write("security-system", "target-state", device.EnumValue(securityTargetName(value)))
	})
	b.addExtendedUpdate(item.ID, primary.SecuritySystemCurrentState.C, propertyValue("security-system", "current-state", enumIntValue(map[string]int{"stay-arm": characteristic.SecuritySystemCurrentStateStayArm, "away-arm": characteristic.SecuritySystemCurrentStateAwayArm, "night-arm": characteristic.SecuritySystemCurrentStateNightArm, "disarmed": characteristic.SecuritySystemCurrentStateDisarmed, "triggered": characteristic.SecuritySystemCurrentStateAlarmTriggered}, characteristic.SecuritySystemCurrentStateDisarmed)))
	b.addExtendedUpdate(item.ID, primary.SecuritySystemTargetState.C, propertyValue("security-system", "target-state", enumIntValue(map[string]int{"stay-arm": characteristic.SecuritySystemTargetStateStayArm, "away-arm": characteristic.SecuritySystemTargetStateAwayArm, "night-arm": characteristic.SecuritySystemTargetStateNightArm, "disarmed": characteristic.SecuritySystemTargetStateDisarm}, characteristic.SecuritySystemTargetStateDisarm)))
	if _, found := item.Property("main", "security-system", "alarm-type"); found {
		alarm := characteristic.NewSecuritySystemAlarmType()
		primary.AddC(alarm.C)
		b.addExtendedUpdate(item.ID, alarm.C, propertyValue("security-system", "alarm-type", enumIntValue(map[string]int{"none": 0}, 1)))
	}
	return a
}

func (b *accessoryBindings) newValveAccessory(item device.Device, accessoryID uint64, write func(string, string, device.PropertyValue) error) *accessory.A {
	category := accessory.TypeOther
	if valveType, found := enumProperty(item, "valve", "valve-type"); found {
		switch valveType {
		case "irrigation":
			category = accessory.TypeSprinkler
		case "shower":
			category = accessory.TypeShowerSystem
		case "faucet":
			category = accessory.TypeFaucet
		}
	}
	primary := service.NewValve()
	a := b.extendedAccessory(item, accessoryID, category, primary.S)
	primary.Active.OnSetRemoteValue(func(value int) error {
		return write("valve", "active", device.BoolValue(value == characteristic.ActiveActive))
	})
	b.addExtendedUpdate(item.ID, primary.Active.C, propertyValue("valve", "active", boolIntValue(characteristic.ActiveInactive, characteristic.ActiveActive)))
	b.addExtendedUpdate(item.ID, primary.InUse.C, propertyValue("valve", "in-use", boolIntValue(characteristic.InUseNotInUse, characteristic.InUseInUse)))
	b.addExtendedUpdate(item.ID, primary.ValveType.C, propertyValue("valve", "valve-type", enumIntValue(map[string]int{"generic": characteristic.ValveTypeGenericValve, "irrigation": characteristic.ValveTypeIrrigation, "shower": characteristic.ValveTypeShowerHead, "faucet": characteristic.ValveTypeWaterFaucet}, characteristic.ValveTypeGenericValve)))
	if _, found := item.Property("main", "valve", "set-duration"); found {
		current := characteristic.NewSetDuration()
		current.OnSetRemoteValue(func(value int) error { return write("valve", "set-duration", device.IntValue(int64(value))) })
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("valve", "set-duration", integerValue))
	}
	if _, found := item.Property("main", "valve", "remaining-duration"); found {
		current := characteristic.NewRemainingDuration()
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("valve", "remaining-duration", integerValue))
	}
	return a
}

func (b *accessoryBindings) newSpeakerAccessory(item device.Device, accessoryID uint64, write func(string, string, device.PropertyValue) error) *accessory.A {
	primary := service.NewSpeaker()
	active := characteristic.NewActive()
	volume := characteristic.NewVolume()
	fault := characteristic.NewStatusFault()
	primary.AddC(active.C)
	primary.AddC(volume.C)
	primary.AddC(fault.C)
	a := accessory.New(extendedInfo(item), accessory.TypeOther)
	a.Id = accessoryID
	a.AddS(primary.S)
	b.faults[item.ID] = fault
	active.OnSetRemoteValue(func(value int) error {
		return write("speaker", "active", device.BoolValue(value == characteristic.ActiveActive))
	})
	volume.OnSetRemoteValue(func(value int) error { return write("speaker", "volume", device.NumberValue(float64(value))) })
	primary.Mute.OnSetRemoteValue(func(value bool) error { return write("speaker", "mute", device.BoolValue(value)) })
	b.addExtendedUpdate(item.ID, active.C, propertyValue("speaker", "active", boolIntValue(characteristic.ActiveInactive, characteristic.ActiveActive)))
	b.addExtendedUpdate(item.ID, volume.C, propertyValue("speaker", "volume", func(value device.PropertyValue) (any, bool) {
		if value.Number == nil {
			return nil, false
		}
		return int(*value.Number), true
	}))
	b.addExtendedUpdate(item.ID, primary.Mute.C, propertyValue("speaker", "mute", boolValue))
	if _, found := item.Property("main", "speaker", "current-media-state"); found {
		current := characteristic.NewCurrentMediaState()
		primary.AddC(current.C)
		b.addExtendedUpdate(item.ID, current.C, propertyValue("speaker", "current-media-state", enumIntValue(map[string]int{"playing": characteristic.CurrentMediaStatePlay, "paused": characteristic.CurrentMediaStatePause, "stopped": characteristic.CurrentMediaStateStop, "loading": characteristic.CurrentMediaStateUnknown, "interrupted": characteristic.CurrentMediaStateUnknown}, characteristic.CurrentMediaStateUnknown)))
	}
	if _, found := item.Property("main", "speaker", "target-media-state"); found {
		target := characteristic.NewTargetMediaState()
		target.OnSetRemoteValue(func(value int) error {
			return write("speaker", "target-media-state", device.EnumValue(mediaTargetName(value)))
		})
		primary.AddC(target.C)
		b.addExtendedUpdate(item.ID, target.C, propertyValue("speaker", "target-media-state", enumIntValue(map[string]int{"play": characteristic.TargetMediaStatePlay, "pause": characteristic.TargetMediaStatePause, "stop": characteristic.TargetMediaStateStop}, characteristic.TargetMediaStateStop)))
	}
	return a
}

func thermostatTargetName(value int) string {
	switch value {
	case characteristic.TargetHeatingCoolingStateHeat:
		return "heat"
	case characteristic.TargetHeatingCoolingStateCool:
		return "cool"
	case characteristic.TargetHeatingCoolingStateAuto:
		return "auto"
	default:
		return "off"
	}
}

func temperatureUnitName(value int) string {
	if value == characteristic.TemperatureDisplayUnitsFahrenheit {
		return "fahrenheit"
	}
	return "celsius"
}

func humidifierTargetName(value int) string {
	switch value {
	case characteristic.TargetHumidifierDehumidifierStateHumidifier:
		return "humidify"
	case characteristic.TargetHumidifierDehumidifierStateDehumidifier:
		return "dehumidify"
	default:
		return "auto"
	}
}

func lockTargetName(value int) string {
	if value == characteristic.LockTargetStateSecured {
		return "secured"
	}
	return "unsecured"
}

func garageTargetName(value int) string {
	if value == characteristic.TargetDoorStateClosed {
		return "closed"
	}
	return "open"
}

func securityTargetName(value int) string {
	switch value {
	case characteristic.SecuritySystemTargetStateStayArm:
		return "stay-arm"
	case characteristic.SecuritySystemTargetStateAwayArm:
		return "away-arm"
	case characteristic.SecuritySystemTargetStateNightArm:
		return "night-arm"
	default:
		return "disarmed"
	}
}

func mediaTargetName(value int) string {
	switch value {
	case characteristic.TargetMediaStatePlay:
		return "play"
	case characteristic.TargetMediaStatePause:
		return "pause"
	default:
		return "stop"
	}
}
