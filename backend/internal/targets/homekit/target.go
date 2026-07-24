package homekit

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	homekitqr "github.com/kradalby/homekit-qr"
)

type Config struct {
	ID            string
	Name          string
	Address       string
	Pin           string
	SetupID       string
	StorePath     string
	DeviceIDs     []string
	Devices       []domaintarget.VirtualDevice
	IdentityStore AccessoryIdentityStore
}

type AccessoryIdentityStore interface {
	HomeKitAccessoryAID(context.Context, string, string) (uint64, error)
	HomeKitIID(context.Context, string, string, string) (uint64, error)
}

type PairingInfo struct {
	Code     string
	SetupURI string
	QR       []byte
	Devices  []string
}

// ProjectionIssue records a device that could not be projected into HomeKit.
// The bridge may still start with the remaining accessories.
type ProjectionIssue struct {
	DeviceID   string
	DeviceName string
	DeviceType device.Type
	Stage      string
	Message    string
}

type Target struct {
	server                  *hap.Server
	logger                  *slog.Logger
	pin                     string
	id                      string
	pairing                 PairingInfo
	issueSnapshot           []ProjectionIssue
	publishedAccessoryCount int
	cancelSubscription      func()
}

type accessoryBindings struct {
	issues          []ProjectionIssue
	accessories     []*accessory.A
	switches        map[string]*characteristic.On
	temperatures    map[string]*characteristic.CurrentTemperature
	faults          map[string]*characteristic.StatusFault
	extraFaults     map[string][]*characteristic.StatusFault
	byDevice        map[string]*accessory.A
	outletInUse     map[string]*characteristic.OutletInUse
	humidities      map[string]*characteristic.CurrentRelativeHumidity
	contacts        map[string]*characteristic.ContactSensorState
	motions         map[string]*characteristic.MotionDetected
	actives         map[string]*characteristic.Active
	fanCurrent      map[string]*characteristic.CurrentFanState
	fanTargets      map[string]*characteristic.TargetFanState
	airCurrent      map[string]*characteristic.CurrentAirPurifierState
	airTargets      map[string]*characteristic.TargetAirPurifierState
	heaterCurrent   map[string]*characteristic.CurrentHeaterCoolerState
	heaterTargets   map[string]*characteristic.TargetHeaterCoolerState
	coolingTargets  map[string]*characteristic.CoolingThresholdTemperature
	heatingTargets  map[string]*characteristic.HeatingThresholdTemperature
	speeds          map[string]*characteristic.RotationSpeed
	filterLife      map[string]*characteristic.FilterLifeLevel
	filterChange    map[string]*characteristic.FilterChangeIndication
	filterResets    map[string]*characteristic.ResetFilterIndication
	positions       map[string]*characteristic.CurrentPosition
	positionTargets map[string]*characteristic.TargetPosition
	positionStates  map[string]*characteristic.PositionState
	brightness      map[string]*characteristic.Brightness
	colorTemps      map[string]*characteristic.ColorTemperature
	hues            map[string]*characteristic.Hue
	saturations     map[string]*characteristic.Saturation
	swingModes      map[string]*characteristic.SwingMode
	directions      map[string]*characteristic.RotationDirection
	controlLocks    map[string]*characteristic.LockPhysicalControls
	airQualities    map[string]*characteristic.AirQuality
	pm25            map[string]*characteristic.PM2_5Density
	voc             map[string]*characteristic.VOCDensity
	obstructions    map[string]*characteristic.ObstructionDetected
	batteryLevels   map[string]*characteristic.BatteryLevel
	lowBatteries    map[string]*characteristic.StatusLowBattery
	tampered        map[string]*characteristic.StatusTampered
	extendedUpdates map[string][]extendedUpdate
}

type accessoryRoute struct {
	sourceDeviceID   string
	sourceDeviceIDs  []string
	targetType       device.Type
	targetID         string
	consumerDeviceID string
}

func newAccessoryBindings(items []device.Device, selected map[string]bool, accessoryIDs map[string]uint64, devices *application.DeviceService, logger *slog.Logger, routeMaps ...map[string]accessoryRoute) *accessoryBindings {
	routes := map[string]accessoryRoute{}
	if len(routeMaps) > 0 {
		routes = routeMaps[0]
	}
	sourceID := func(id string) string {
		if routes[id].sourceDeviceID != "" {
			return routes[id].sourceDeviceID
		}
		return id
	}
	bindings := &accessoryBindings{
		accessories: make([]*accessory.A, 0, len(items)), switches: make(map[string]*characteristic.On), temperatures: make(map[string]*characteristic.CurrentTemperature), faults: make(map[string]*characteristic.StatusFault), extraFaults: make(map[string][]*characteristic.StatusFault), byDevice: make(map[string]*accessory.A), outletInUse: make(map[string]*characteristic.OutletInUse), humidities: make(map[string]*characteristic.CurrentRelativeHumidity), contacts: make(map[string]*characteristic.ContactSensorState), motions: make(map[string]*characteristic.MotionDetected), actives: make(map[string]*characteristic.Active), fanCurrent: make(map[string]*characteristic.CurrentFanState), fanTargets: make(map[string]*characteristic.TargetFanState), airCurrent: make(map[string]*characteristic.CurrentAirPurifierState), airTargets: make(map[string]*characteristic.TargetAirPurifierState), heaterCurrent: make(map[string]*characteristic.CurrentHeaterCoolerState), heaterTargets: make(map[string]*characteristic.TargetHeaterCoolerState), coolingTargets: make(map[string]*characteristic.CoolingThresholdTemperature), heatingTargets: make(map[string]*characteristic.HeatingThresholdTemperature), speeds: make(map[string]*characteristic.RotationSpeed), filterLife: make(map[string]*characteristic.FilterLifeLevel), filterChange: make(map[string]*characteristic.FilterChangeIndication), filterResets: make(map[string]*characteristic.ResetFilterIndication), positions: make(map[string]*characteristic.CurrentPosition), positionTargets: make(map[string]*characteristic.TargetPosition), positionStates: make(map[string]*characteristic.PositionState),
		brightness: make(map[string]*characteristic.Brightness), colorTemps: make(map[string]*characteristic.ColorTemperature), hues: make(map[string]*characteristic.Hue), saturations: make(map[string]*characteristic.Saturation), swingModes: make(map[string]*characteristic.SwingMode), directions: make(map[string]*characteristic.RotationDirection), controlLocks: make(map[string]*characteristic.LockPhysicalControls), airQualities: make(map[string]*characteristic.AirQuality), pm25: make(map[string]*characteristic.PM2_5Density), voc: make(map[string]*characteristic.VOCDensity), obstructions: make(map[string]*characteristic.ObstructionDetected), batteryLevels: make(map[string]*characteristic.BatteryLevel), lowBatteries: make(map[string]*characteristic.StatusLowBattery), tampered: make(map[string]*characteristic.StatusTampered),
		extendedUpdates: make(map[string][]extendedUpdate),
	}
	for _, item := range items {
		if len(selected) > 0 && !selected[item.ID] {
			continue
		}
		consumerContract, supported := homeKitModelContract(item.Type)
		if !supported {
			issue := ProjectionIssue{
				DeviceID: item.ID, DeviceName: item.Name, DeviceType: item.Type,
				Stage: "unsupported-type", Message: fmt.Sprintf("HomeKit does not support device type %q", item.Type),
			}
			bindings.issues = append(bindings.issues, issue)
			logger.Error("device type is unsupported by HomeKit consumer", "device_id", item.ID, "device_type", item.Type)
			continue
		}
		if _, err := device.ProjectForConsumer(item, consumerContract); err != nil {
			issue := ProjectionIssue{
				DeviceID: item.ID, DeviceName: item.Name, DeviceType: item.Type,
				Stage: "consumer-contract", Message: err.Error(),
			}
			bindings.issues = append(bindings.issues, issue)
			logger.Error("device does not satisfy HomeKit consumer contract", "device_id", item.ID, "device_type", item.Type, "error", err)
			continue
		}
		info := accessory.Info{Name: item.Name, SerialNumber: item.ID, Manufacturer: "HomeLoom", Model: string(item.Type), Firmware: "0.0.1"}
		route := routes[item.ID]
		var created *accessory.A
		switch item.Type {
		case device.TypeSwitch:
			a := accessory.NewSwitch(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.Switch.AddC(fault.C)
			deviceID := sourceID(item.ID)
			a.Switch.On.OnSetRemoteValue(func(value bool) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "switch", "power", device.BoolValue(value))
			})
			bindings.switches[item.ID], bindings.faults[item.ID] = a.Switch.On, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeLightbulb:
			a := accessory.NewLightbulb(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.Lightbulb.AddC(fault.C)
			deviceID := sourceID(item.ID)
			a.Lightbulb.On.OnSetRemoteValue(func(value bool) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "switch", "power", device.BoolValue(value))
			})
			if _, found := item.Property("main", "light", "brightness"); found {
				current := characteristic.NewBrightness()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "light", "brightness", device.NumberValue(float64(value)))
				})
				a.Lightbulb.AddC(current.C)
				bindings.brightness[item.ID] = current
			}
			if property, found := item.Property("main", "light", "color-temperature"); found {
				current := characteristic.NewColorTemperature()
				if !configureColorTemperatureRange(current, property.Definition) {
					logger.Warn("device color temperature range does not overlap HomeKit", "device_id", item.ID, "min", property.Definition.Min, "max", property.Definition.Max)
				} else {
					current.OnSetRemoteValue(func(value int) error {
						return writeHomeKitProperty(devices, logger, deviceID, route, "light", "color-temperature", device.IntValue(int64(value)))
					})
					a.Lightbulb.AddC(current.C)
					bindings.colorTemps[item.ID] = current
				}
			}
			if _, found := item.Property("main", "light", "hue"); found {
				current := characteristic.NewHue()
				current.OnSetRemoteValue(func(value float64) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "light", "hue", device.NumberValue(value))
				})
				a.Lightbulb.AddC(current.C)
				bindings.hues[item.ID] = current
			}
			if _, found := item.Property("main", "light", "saturation"); found {
				current := characteristic.NewSaturation()
				current.OnSetRemoteValue(func(value float64) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "light", "saturation", device.NumberValue(value))
				})
				a.Lightbulb.AddC(current.C)
				bindings.saturations[item.ID] = current
			}
			bindings.switches[item.ID], bindings.faults[item.ID] = a.Lightbulb.On, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeOutlet:
			a := accessory.NewOutlet(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.Outlet.AddC(fault.C)
			deviceID := sourceID(item.ID)
			a.Outlet.On.OnSetRemoteValue(func(value bool) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "switch", "power", device.BoolValue(value))
			})
			bindings.switches[item.ID], bindings.outletInUse[item.ID], bindings.faults[item.ID] = a.Outlet.On, a.Outlet.OutletInUse, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeTemperatureSensor, device.TypeHumiditySensor:
			_, hasTemperature := item.Property("main", "temperature", "current-temperature")
			_, hasHumidity := item.Property("main", "humidity", "current-humidity")
			if !hasTemperature && !hasHumidity {
				continue
			}
			if hasTemperature {
				a := accessory.NewTemperatureSensor(info)
				a.A.Id = accessoryIDs[item.ID]
				fault := characteristic.NewStatusFault()
				a.TempSensor.AddC(fault.C)
				bindings.temperatures[item.ID], bindings.faults[item.ID] = a.TempSensor.CurrentTemperature, fault
				if hasHumidity {
					humidity := service.NewHumiditySensor()
					humidityFault := characteristic.NewStatusFault()
					humidity.AddC(humidityFault.C)
					a.A.AddS(humidity.S)
					bindings.humidities[item.ID] = humidity.CurrentRelativeHumidity
					bindings.extraFaults[item.ID] = append(bindings.extraFaults[item.ID], humidityFault)
				}
				bindings.accessories = append(bindings.accessories, a.A)
				created = a.A
			} else {
				a := accessory.New(info, accessory.TypeSensor)
				a.Id = accessoryIDs[item.ID]
				sensor := service.NewHumiditySensor()
				fault := characteristic.NewStatusFault()
				sensor.AddC(fault.C)
				a.AddS(sensor.S)
				bindings.humidities[item.ID], bindings.faults[item.ID] = sensor.CurrentRelativeHumidity, fault
				bindings.accessories = append(bindings.accessories, a)
				created = a
			}
		case device.TypeTemperatureHumiditySensor:
			a := accessory.NewTemperatureSensor(info)
			a.A.Id = accessoryIDs[item.ID]
			temperatureFault := characteristic.NewStatusFault()
			a.TempSensor.AddC(temperatureFault.C)
			humidity := service.NewHumiditySensor()
			humidityFault := characteristic.NewStatusFault()
			humidity.AddC(humidityFault.C)
			a.A.AddS(humidity.S)
			bindings.temperatures[item.ID], bindings.humidities[item.ID], bindings.faults[item.ID] = a.TempSensor.CurrentTemperature, humidity.CurrentRelativeHumidity, temperatureFault
			bindings.extraFaults[item.ID] = append(bindings.extraFaults[item.ID], humidityFault)
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeContactSensor:
			a := accessory.NewContactSensor(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.ContactSensor.AddC(fault.C)
			if _, found := item.Property("main", "security", "tampered"); found {
				current := characteristic.NewStatusTampered()
				a.ContactSensor.AddC(current.C)
				bindings.tampered[item.ID] = current
			}
			bindings.contacts[item.ID], bindings.faults[item.ID] = a.ContactSensor.ContactSensorState, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeMotionSensor:
			a := accessory.NewMotionSensor(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.MotionSensor.AddC(fault.C)
			if _, found := item.Property("main", "security", "tampered"); found {
				current := characteristic.NewStatusTampered()
				a.MotionSensor.AddC(current.C)
				bindings.tampered[item.ID] = current
			}
			bindings.motions[item.ID], bindings.faults[item.ID] = a.MotionSensor.MotionDetected, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeFan:
			a := accessory.New(info, accessory.TypeFan)
			a.Id = accessoryIDs[item.ID]
			fan := service.NewFanV2()
			current, target, speed := characteristic.NewCurrentFanState(), characteristic.NewTargetFanState(), characteristic.NewRotationSpeed()
			fault := characteristic.NewStatusFault()
			fan.AddC(current.C)
			fan.AddC(target.C)
			fan.AddC(speed.C)
			fan.AddC(fault.C)
			a.AddS(fan.S)
			deviceID := sourceID(item.ID)
			fan.Active.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "fan", "active", device.BoolValue(value == characteristic.ActiveActive))
			})
			target.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "fan", "target-state", device.EnumValue(fanTargetName(value)))
			})
			speed.OnSetRemoteValue(func(value float64) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "fan", "rotation-speed", device.NumberValue(value))
			})
			if _, found := item.Property("main", "fan", "swing-mode"); found {
				current := characteristic.NewSwingMode()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "fan", "swing-mode", device.BoolValue(value == characteristic.SwingModeSwingEnabled))
				})
				fan.AddC(current.C)
				bindings.swingModes[item.ID] = current
			}
			if _, found := item.Property("main", "fan", "rotation-direction"); found {
				current := characteristic.NewRotationDirection()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "fan", "rotation-direction", device.EnumValue(rotationDirectionName(value)))
				})
				fan.AddC(current.C)
				bindings.directions[item.ID] = current
			}
			if _, found := item.Property("main", "fan", "lock-physical-controls"); found {
				current := characteristic.NewLockPhysicalControls()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "fan", "lock-physical-controls", device.BoolValue(value == characteristic.LockPhysicalControlsControlLockEnabled))
				})
				fan.AddC(current.C)
				bindings.controlLocks[item.ID] = current
			}
			bindings.actives[item.ID], bindings.fanCurrent[item.ID], bindings.fanTargets[item.ID], bindings.speeds[item.ID], bindings.faults[item.ID] = fan.Active, current, target, speed, fault
			bindings.accessories = append(bindings.accessories, a)
			created = a
		case device.TypeAirPurifier:
			a := accessory.NewAirPurifier(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.AirPurifier.AddC(fault.C)
			deviceID := sourceID(item.ID)
			a.AirPurifier.Active.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "air-purifier", "active", device.BoolValue(value == characteristic.ActiveActive))
			})
			a.AirPurifier.TargetAirPurifierState.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "air-purifier", "target-state", device.EnumValue(airTargetName(value)))
			})
			if _, found := item.Property("main", "air-purifier", "rotation-speed"); found {
				speed := characteristic.NewRotationSpeed()
				speed.OnSetRemoteValue(func(value float64) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "air-purifier", "rotation-speed", device.NumberValue(value))
				})
				a.AirPurifier.AddC(speed.C)
				bindings.speeds[item.ID] = speed
			}
			if _, found := item.Property("main", "air-purifier", "swing-mode"); found {
				current := characteristic.NewSwingMode()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "air-purifier", "swing-mode", device.BoolValue(value == characteristic.SwingModeSwingEnabled))
				})
				a.AirPurifier.AddC(current.C)
				bindings.swingModes[item.ID] = current
			}
			if _, found := item.Property("main", "air-purifier", "lock-physical-controls"); found {
				current := characteristic.NewLockPhysicalControls()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "air-purifier", "lock-physical-controls", device.BoolValue(value == characteristic.LockPhysicalControlsControlLockEnabled))
				})
				a.AirPurifier.AddC(current.C)
				bindings.controlLocks[item.ID] = current
			}
			_, hasFilterLife := item.Property("main", "filter", "life-level")
			_, hasFilterChange := item.Property("main", "filter", "change-indication")
			hasFilterReset := false
			for _, endpoint := range item.Endpoints {
				if endpoint.ID != "main" {
					continue
				}
				for _, capability := range endpoint.Capabilities {
					if capability.ID != "filter" {
						continue
					}
					for _, command := range capability.Commands {
						if command.ID == "reset-filter" {
							hasFilterReset = true
						}
					}
				}
			}
			if hasFilterLife || hasFilterChange || hasFilterReset {
				filter := service.NewFilterMaintenance()
				filterFault := characteristic.NewStatusFault()
				filter.AddC(filterFault.C)
				if hasFilterLife {
					life := characteristic.NewFilterLifeLevel()
					filter.AddC(life.C)
					bindings.filterLife[item.ID] = life
				}
				if hasFilterChange {
					bindings.filterChange[item.ID] = filter.FilterChangeIndication
				}
				if hasFilterReset {
					reset := characteristic.NewResetFilterIndication()
					reset.OnSetRemoteValue(func(int) error {
						return executeHomeKitCommand(devices, logger, providersdk.CommandRequest{DeviceID: deviceID, EndpointID: "main", CapabilityID: "filter", CommandID: "reset-filter"})
					})
					filter.AddC(reset.C)
					bindings.filterResets[item.ID] = reset
				}
				a.AirPurifier.AddS(filter.S)
				a.A.AddS(filter.S)
				bindings.extraFaults[item.ID] = append(bindings.extraFaults[item.ID], filterFault)
			}
			if _, found := item.Property("main", "air-quality", "current-air-quality"); found {
				quality := service.NewAirQualitySensor()
				qualityFault := characteristic.NewStatusFault()
				quality.AddC(qualityFault.C)
				if _, present := item.Property("main", "air-quality", "pm2.5-density"); present {
					current := characteristic.NewPM2_5Density()
					quality.AddC(current.C)
					bindings.pm25[item.ID] = current
				}
				if _, present := item.Property("main", "air-quality", "voc-density"); present {
					current := characteristic.NewVOCDensity()
					quality.AddC(current.C)
					bindings.voc[item.ID] = current
				}
				a.AirPurifier.AddS(quality.S)
				a.A.AddS(quality.S)
				bindings.airQualities[item.ID] = quality.AirQuality
				bindings.extraFaults[item.ID] = append(bindings.extraFaults[item.ID], qualityFault)
			}
			bindings.actives[item.ID], bindings.airCurrent[item.ID], bindings.airTargets[item.ID], bindings.faults[item.ID] = a.AirPurifier.Active, a.AirPurifier.CurrentAirPurifierState, a.AirPurifier.TargetAirPurifierState, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeAirConditioner:
			a := accessory.New(info, accessory.TypeAirConditioner)
			a.Id = accessoryIDs[item.ID]
			heaterCooler := service.NewHeaterCooler()
			fault := characteristic.NewStatusFault()
			coolingTarget := characteristic.NewCoolingThresholdTemperature()
			heatingTarget := characteristic.NewHeatingThresholdTemperature()
			heaterCooler.AddC(fault.C)
			heaterCooler.AddC(coolingTarget.C)
			heaterCooler.AddC(heatingTarget.C)
			a.AddS(heaterCooler.S)
			deviceID := sourceID(item.ID)
			heaterCooler.Active.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "air-conditioner", "active", device.BoolValue(value == characteristic.ActiveActive))
			})
			heaterCooler.TargetHeaterCoolerState.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "air-conditioner", "target-mode", device.EnumValue(heaterCoolerTargetName(value)))
			})
			writeTargetTemperature := func(value float64) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "temperature", "target-temperature", device.NumberValue(value))
			}
			coolingTarget.OnSetRemoteValue(writeTargetTemperature)
			heatingTarget.OnSetRemoteValue(writeTargetTemperature)
			if _, found := item.Property("main", "air-conditioner", "rotation-speed"); found {
				speed := characteristic.NewRotationSpeed()
				speed.OnSetRemoteValue(func(value float64) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "air-conditioner", "rotation-speed", device.NumberValue(value))
				})
				heaterCooler.AddC(speed.C)
				bindings.speeds[item.ID] = speed
			}
			if _, found := item.Property("main", "air-conditioner", "vertical-swing"); found {
				swing := characteristic.NewSwingMode()
				swing.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "air-conditioner", "vertical-swing", device.BoolValue(value == characteristic.SwingModeSwingEnabled))
				})
				heaterCooler.AddC(swing.C)
				bindings.swingModes[item.ID] = swing
			}
			if _, found := item.Property("main", "air-conditioner", "display-units"); found {
				units := characteristic.NewTemperatureDisplayUnits()
				units.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, route, "air-conditioner", "display-units", device.EnumValue(temperatureUnitName(value)))
				})
				heaterCooler.AddC(units.C)
				bindings.addExtendedUpdate(item.ID, units.C, propertyValue("air-conditioner", "display-units", enumIntValue(map[string]int{"celsius": characteristic.TemperatureDisplayUnitsCelsius, "fahrenheit": characteristic.TemperatureDisplayUnitsFahrenheit}, characteristic.TemperatureDisplayUnitsCelsius)))
			}
			if _, found := item.Property("main", "humidity", "current-humidity"); found {
				humidity := service.NewHumiditySensor()
				humidityFault := characteristic.NewStatusFault()
				humidity.AddC(humidityFault.C)
				heaterCooler.AddS(humidity.S)
				a.AddS(humidity.S)
				bindings.humidities[item.ID] = humidity.CurrentRelativeHumidity
				bindings.extraFaults[item.ID] = append(bindings.extraFaults[item.ID], humidityFault)
			}
			_, hasFilterLife := item.Property("main", "filter", "life-level")
			_, hasFilterChange := item.Property("main", "filter", "change-indication")
			if hasFilterLife || hasFilterChange {
				filter := service.NewFilterMaintenance()
				filterFault := characteristic.NewStatusFault()
				filter.AddC(filterFault.C)
				heaterCooler.AddS(filter.S)
				a.AddS(filter.S)
				if hasFilterLife {
					life := characteristic.NewFilterLifeLevel()
					filter.AddC(life.C)
					bindings.filterLife[item.ID] = life
				}
				if hasFilterChange {
					bindings.filterChange[item.ID] = filter.FilterChangeIndication
				}
				bindings.extraFaults[item.ID] = append(bindings.extraFaults[item.ID], filterFault)
			}
			bindings.actives[item.ID], bindings.heaterCurrent[item.ID], bindings.heaterTargets[item.ID] = heaterCooler.Active, heaterCooler.CurrentHeaterCoolerState, heaterCooler.TargetHeaterCoolerState
			bindings.temperatures[item.ID], bindings.coolingTargets[item.ID], bindings.heatingTargets[item.ID], bindings.faults[item.ID] = heaterCooler.CurrentTemperature, coolingTarget, heatingTarget, fault
			bindings.accessories = append(bindings.accessories, a)
			created = a
		case device.TypeWindowCovering:
			a := accessory.NewWindowCovering(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.WindowCovering.AddC(fault.C)
			deviceID := sourceID(item.ID)
			a.WindowCovering.TargetPosition.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, route, "window-covering", "target-position", device.IntValue(int64(value)))
			})
			if _, found := item.Property("main", "window-covering", "obstruction-detected"); found {
				current := characteristic.NewObstructionDetected()
				a.WindowCovering.AddC(current.C)
				bindings.obstructions[item.ID] = current
			}
			bindings.positions[item.ID], bindings.positionTargets[item.ID], bindings.positionStates[item.ID], bindings.faults[item.ID] = a.WindowCovering.CurrentPosition, a.WindowCovering.TargetPosition, a.WindowCovering.PositionState, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		default:
			created = bindings.newExtendedAccessory(item, accessoryIDs[item.ID], devices, logger, route, sourceID(item.ID))
			if created != nil {
				bindings.accessories = append(bindings.accessories, created)
			}
		}
		if created != nil {
			if _, found := item.Property("main", "battery", "level"); found {
				battery := service.NewBatteryService()
				_ = battery.ChargingState.SetValue(characteristic.ChargingStateNotChargeable)
				created.AddS(battery.S)
				bindings.batteryLevels[item.ID] = battery.BatteryLevel
				bindings.lowBatteries[item.ID] = battery.StatusLowBattery
			}
			configureAccessoryNumericRanges(created, item, logger)
			bindings.byDevice[item.ID] = created
			bindings.update(item)
		}
	}
	return bindings
}

func (b *accessoryBindings) update(item device.Device) uint64 {
	fault, exists := b.faults[item.ID]
	if !exists {
		return 0
	}
	var pushes uint64
	if !item.IsOnline() {
		_ = fault.SetValue(characteristic.StatusFaultGeneralFault)
		pushes = 1
		for _, extra := range b.extraFaults[item.ID] {
			_ = extra.SetValue(characteristic.StatusFaultGeneralFault)
			pushes++
		}
		return pushes
	}
	_ = fault.SetValue(characteristic.StatusFaultNoFault)
	pushes++
	for _, extra := range b.extraFaults[item.ID] {
		_ = extra.SetValue(characteristic.StatusFaultNoFault)
		pushes++
	}
	if current, ok := b.switches[item.ID]; ok {
		if property, found := item.Property("main", "switch", "power"); found && property.Value.Bool != nil {
			current.SetValue(*property.Value.Bool)
			pushes++
		}
	}
	if current, ok := b.outletInUse[item.ID]; ok {
		if property, found := item.Property("main", "outlet", "in-use"); found && property.Value.Bool != nil {
			current.SetValue(*property.Value.Bool)
			pushes++
		} else if property, found := item.Property("main", "switch", "power"); found && property.Value.Bool != nil {
			current.SetValue(*property.Value.Bool)
			pushes++
		}
	}
	if current, ok := b.brightness[item.ID]; ok {
		if property, found := item.Property("main", "light", "brightness"); found && property.Value.Number != nil {
			_ = current.SetValue(int(*property.Value.Number))
			pushes++
		}
	}
	if current, ok := b.hues[item.ID]; ok {
		if property, found := item.Property("main", "light", "hue"); found && property.Value.Number != nil {
			current.SetValue(*property.Value.Number)
			pushes++
		}
	}
	if current, ok := b.saturations[item.ID]; ok {
		if property, found := item.Property("main", "light", "saturation"); found && property.Value.Number != nil {
			current.SetValue(*property.Value.Number)
			pushes++
		}
	}
	if current, ok := b.colorTemps[item.ID]; ok {
		if property, found := item.Property("main", "light", "color-temperature"); found && property.Value.Int != nil {
			value := int(*property.Value.Int)
			value = max(current.MinValue(), min(current.MaxValue(), value))
			if current.SetValue(value) == nil {
				pushes++
			}
		}
	}
	if current, ok := b.temperatures[item.ID]; ok {
		property, found := item.Property("main", "temperature", "current-temperature")
		if (!found || property.Value.Number == nil) && item.Type == device.TypeAirConditioner {
			property, found = item.Property("main", "temperature", "target-temperature")
		}
		if found && property.Value.Number != nil {
			value := *property.Value.Number
			if value < 0 {
				value = 0
			}
			if value > 100 {
				value = 100
			}
			current.SetValue(value)
			pushes++
		}
	}
	if current, ok := b.humidities[item.ID]; ok {
		if property, found := item.Property("main", "humidity", "current-humidity"); found && property.Value.Number != nil {
			current.SetValue(*property.Value.Number)
			pushes++
		}
	}
	if current, ok := b.contacts[item.ID]; ok {
		if property, found := item.Property("main", "contact", "contact-detected"); found && property.Value.Bool != nil {
			value := characteristic.ContactSensorStateContactNotDetected
			if *property.Value.Bool {
				value = characteristic.ContactSensorStateContactDetected
			}
			_ = current.SetValue(value)
			pushes++
		}
	}
	if current, ok := b.motions[item.ID]; ok {
		if property, found := item.Property("main", "motion", "motion-detected"); found && property.Value.Bool != nil {
			current.SetValue(*property.Value.Bool)
			pushes++
		}
	}
	if current, ok := b.batteryLevels[item.ID]; ok {
		if property, found := item.Property("main", "battery", "level"); found && property.Value.Int != nil {
			_ = current.SetValue(int(*property.Value.Int))
			pushes++
		}
	}
	if current, ok := b.lowBatteries[item.ID]; ok {
		value := characteristic.StatusLowBatteryBatteryLevelNormal
		if property, found := item.Property("main", "battery", "low"); found && property.Value.Bool != nil && *property.Value.Bool {
			value = characteristic.StatusLowBatteryBatteryLevelLow
		}
		_ = current.SetValue(value)
		pushes++
	}
	if current, ok := b.tampered[item.ID]; ok {
		value := characteristic.StatusTamperedNotTampered
		if property, found := item.Property("main", "security", "tampered"); found && property.Value.Bool != nil && *property.Value.Bool {
			value = characteristic.StatusTamperedTampered
		}
		_ = current.SetValue(value)
		pushes++
	}
	if current, ok := b.actives[item.ID]; ok {
		capability := "fan"
		switch item.Type {
		case device.TypeAirPurifier:
			capability = "air-purifier"
		case device.TypeAirConditioner:
			capability = "air-conditioner"
		}
		if property, found := item.Property("main", capability, "active"); found && property.Value.Bool != nil {
			value := characteristic.ActiveInactive
			if *property.Value.Bool {
				value = characteristic.ActiveActive
			}
			_ = current.SetValue(value)
			pushes++
		}
	}
	if current, ok := b.speeds[item.ID]; ok {
		capability := "fan"
		switch item.Type {
		case device.TypeAirPurifier:
			capability = "air-purifier"
		case device.TypeAirConditioner:
			capability = "air-conditioner"
		}
		if property, found := item.Property("main", capability, "rotation-speed"); found && property.Value.Number != nil {
			current.SetValue(*property.Value.Number)
			pushes++
		}
	}
	if current, ok := b.swingModes[item.ID]; ok {
		capability := "fan"
		propertyID := "swing-mode"
		switch item.Type {
		case device.TypeAirPurifier:
			capability = "air-purifier"
		case device.TypeAirConditioner:
			capability, propertyID = "air-conditioner", "vertical-swing"
		}
		if property, found := item.Property("main", capability, propertyID); found && property.Value.Bool != nil {
			value := characteristic.SwingModeSwingDisabled
			if *property.Value.Bool {
				value = characteristic.SwingModeSwingEnabled
			}
			_ = current.SetValue(value)
			pushes++
		}
	}
	if current, ok := b.directions[item.ID]; ok {
		if value, found := enumProperty(item, "fan", "rotation-direction"); found {
			_ = current.SetValue(rotationDirectionValue(value))
			pushes++
		}
	}
	if current, ok := b.controlLocks[item.ID]; ok {
		capability := "fan"
		if item.Type == device.TypeAirPurifier {
			capability = "air-purifier"
		}
		if property, found := item.Property("main", capability, "lock-physical-controls"); found && property.Value.Bool != nil {
			value := characteristic.LockPhysicalControlsControlLockDisabled
			if *property.Value.Bool {
				value = characteristic.LockPhysicalControlsControlLockEnabled
			}
			_ = current.SetValue(value)
			pushes++
		}
	}
	if current, ok := b.fanCurrent[item.ID]; ok {
		if value, found := enumProperty(item, "fan", "current-state"); found {
			_ = current.SetValue(fanCurrentValue(value))
			pushes++
		}
	}
	if current, ok := b.fanTargets[item.ID]; ok {
		if value, found := enumProperty(item, "fan", "target-state"); found {
			_ = current.SetValue(fanTargetValue(value))
			pushes++
		}
	}
	if current, ok := b.airCurrent[item.ID]; ok {
		if value, found := enumProperty(item, "air-purifier", "current-state"); found {
			_ = current.SetValue(airCurrentValue(value))
			pushes++
		}
	}
	if current, ok := b.airTargets[item.ID]; ok {
		if value, found := enumProperty(item, "air-purifier", "target-state"); found {
			_ = current.SetValue(airTargetValue(value))
			pushes++
		}
	}
	if current, ok := b.heaterTargets[item.ID]; ok {
		if value, found := enumProperty(item, "air-conditioner", "target-mode"); found {
			_ = current.SetValue(heaterCoolerTargetValue(value))
			pushes++
		}
	}
	if current, ok := b.heaterCurrent[item.ID]; ok {
		value, found := enumProperty(item, "air-conditioner", "current-state")
		if !found {
			value = derivedHeaterCoolerState(item)
		}
		_ = current.SetValue(heaterCoolerCurrentValue(value))
		pushes++
	}
	if property, found := item.Property("main", "temperature", "target-temperature"); found && property.Value.Number != nil {
		if current, ok := b.coolingTargets[item.ID]; ok {
			current.SetValue(*property.Value.Number)
			pushes++
		}
		if current, ok := b.heatingTargets[item.ID]; ok {
			current.SetValue(*property.Value.Number)
			pushes++
		}
	}
	if current, ok := b.filterLife[item.ID]; ok {
		if property, found := item.Property("main", "filter", "life-level"); found && property.Value.Number != nil {
			current.SetValue(*property.Value.Number)
			pushes++
		}
	}
	if current, ok := b.filterChange[item.ID]; ok {
		if property, found := item.Property("main", "filter", "change-indication"); found && property.Value.Bool != nil {
			value := characteristic.FilterChangeIndicationFilterOK
			if *property.Value.Bool {
				value = characteristic.FilterChangeIndicationChangeFilter
			}
			_ = current.SetValue(value)
			pushes++
		}
	}
	if current, ok := b.airQualities[item.ID]; ok {
		if value, found := enumProperty(item, "air-quality", "current-air-quality"); found {
			_ = current.SetValue(airQualityValue(value))
			pushes++
		}
	}
	if current, ok := b.pm25[item.ID]; ok {
		if property, found := item.Property("main", "air-quality", "pm2.5-density"); found && property.Value.Number != nil {
			current.SetValue(*property.Value.Number)
			pushes++
		}
	}
	if current, ok := b.voc[item.ID]; ok {
		if property, found := item.Property("main", "air-quality", "voc-density"); found && property.Value.Number != nil {
			current.SetValue(*property.Value.Number)
			pushes++
		}
	}
	if current, ok := b.positions[item.ID]; ok {
		if property, found := item.Property("main", "window-covering", "current-position"); found && property.Value.Int != nil {
			_ = current.SetValue(int(*property.Value.Int))
			pushes++
		}
		if property, found := item.Property("main", "window-covering", "target-position"); found && property.Value.Int != nil {
			_ = b.positionTargets[item.ID].SetValue(int(*property.Value.Int))
			pushes++
		}
		if value, found := enumProperty(item, "window-covering", "position-state"); found {
			_ = b.positionStates[item.ID].SetValue(positionStateValue(value))
			pushes++
		}
	}
	if current, ok := b.obstructions[item.ID]; ok {
		if property, found := item.Property("main", "window-covering", "obstruction-detected"); found && property.Value.Bool != nil {
			current.SetValue(*property.Value.Bool)
			pushes++
		}
	}
	pushes += b.updateExtended(item)
	return pushes
}

func configureColorTemperatureRange(current *characteristic.ColorTemperature, definition device.PropertyDefinition) bool {
	minimum, maximum := current.MinValue(), current.MaxValue()
	if definition.Min != nil {
		minimum = max(minimum, int(math.Ceil(*definition.Min)))
	}
	if definition.Max != nil {
		maximum = min(maximum, int(math.Floor(*definition.Max)))
	}
	if minimum > maximum {
		return false
	}
	current.SetMinValue(minimum)
	current.SetMaxValue(maximum)
	_ = current.SetValue(minimum)
	return true
}

func writeHomeKitProperty(devices *application.DeviceService, logger *slog.Logger, deviceID string, route accessoryRoute, capabilityID, propertyID string, value device.PropertyValue) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error
	if route.targetID != "" && route.consumerDeviceID != "" {
		sourceIDs := route.sourceDeviceIDs
		if len(sourceIDs) == 0 {
			sourceIDs = []string{deviceID}
		}
		_, _, err = devices.ExecuteConsumerPropertySourcesInstance(ctx, "homekit", route.targetID, route.consumerDeviceID, route.targetType, sourceIDs, "main", capabilityID, propertyID, value)
	} else {
		_, _, err = devices.ExecuteConsumerProperty(ctx, "homekit", deviceID, "main", capabilityID, propertyID, value)
	}
	if err != nil {
		logger.Error("HomeKit property write failed", "device_id", deviceID, "capability_id", capabilityID, "property_id", propertyID, "error", err)
		return err
	}
	return nil
}

func executeHomeKitCommand(devices *application.DeviceService, logger *slog.Logger, request providersdk.CommandRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := devices.ExecuteCommand(ctx, request); err != nil {
		logger.Error("HomeKit command failed", "device_id", request.DeviceID, "command_id", request.CommandID, "error", err)
		return err
	}
	return nil
}

func enumProperty(item device.Device, capabilityID, propertyID string) (string, bool) {
	property, ok := item.Property("main", capabilityID, propertyID)
	if !ok || property.Value.String == nil {
		return "", false
	}
	return *property.Value.String, true
}
func rotationDirectionName(value int) string {
	if value == characteristic.RotationDirectionCounterclockwise {
		return "counter-clockwise"
	}
	return "clockwise"
}
func rotationDirectionValue(value string) int {
	if value == "counter-clockwise" {
		return characteristic.RotationDirectionCounterclockwise
	}
	return characteristic.RotationDirectionClockwise
}
func airQualityValue(value string) int {
	switch value {
	case "excellent":
		return characteristic.AirQualityExcellent
	case "good":
		return characteristic.AirQualityGood
	case "fair":
		return characteristic.AirQualityFair
	case "inferior":
		return characteristic.AirQualityInferior
	case "poor":
		return characteristic.AirQualityPoor
	default:
		return characteristic.AirQualityUnknown
	}
}
func fanTargetName(value int) string {
	if value == characteristic.TargetFanStateAuto {
		return "auto"
	}
	return "manual"
}
func fanTargetValue(value string) int {
	if value == "auto" {
		return characteristic.TargetFanStateAuto
	}
	return characteristic.TargetFanStateManual
}
func fanCurrentValue(value string) int {
	if value == "blowing-air" {
		return characteristic.CurrentFanStateBlowingAir
	}
	if value == "idle" {
		return characteristic.CurrentFanStateIdle
	}
	return characteristic.CurrentFanStateInactive
}
func airTargetName(value int) string {
	if value == characteristic.TargetAirPurifierStateAuto {
		return "auto"
	}
	return "manual"
}
func airTargetValue(value string) int {
	if value == "auto" {
		return characteristic.TargetAirPurifierStateAuto
	}
	return characteristic.TargetAirPurifierStateManual
}
func airCurrentValue(value string) int {
	if value == "purifying-air" {
		return characteristic.CurrentAirPurifierStatePurifyingAir
	}
	if value == "idle" {
		return characteristic.CurrentAirPurifierStateIdle
	}
	return characteristic.CurrentAirPurifierStateInactive
}
func heaterCoolerTargetName(value int) string {
	switch value {
	case characteristic.TargetHeaterCoolerStateHeat:
		return "heat"
	case characteristic.TargetHeaterCoolerStateCool:
		return "cool"
	default:
		return "auto"
	}
}
func heaterCoolerTargetValue(value string) int {
	switch value {
	case "heat":
		return characteristic.TargetHeaterCoolerStateHeat
	case "cool":
		return characteristic.TargetHeaterCoolerStateCool
	default:
		return characteristic.TargetHeaterCoolerStateAuto
	}
}
func heaterCoolerCurrentValue(value string) int {
	switch value {
	case "heating":
		return characteristic.CurrentHeaterCoolerStateHeating
	case "cooling":
		return characteristic.CurrentHeaterCoolerStateCooling
	case "idle", "drying", "fan-only":
		return characteristic.CurrentHeaterCoolerStateIdle
	default:
		return characteristic.CurrentHeaterCoolerStateInactive
	}
}
func derivedHeaterCoolerState(item device.Device) string {
	if active, found := item.Property("main", "air-conditioner", "active"); !found || active.Value.Bool == nil || !*active.Value.Bool {
		return "off"
	}
	if mode, found := enumProperty(item, "air-conditioner", "target-mode"); found {
		switch mode {
		case "heat":
			return "heating"
		case "cool":
			return "cooling"
		}
	}
	return "idle"
}
func positionStateValue(value string) int {
	if value == "increasing" {
		return characteristic.PositionStateIncreasing
	}
	if value == "decreasing" {
		return characteristic.PositionStateDecreasing
	}
	return characteristic.PositionStateStopped
}

func assignPersistentIIDs(ctx context.Context, targetID, deviceID string, a *accessory.A, store AccessoryIdentityStore) error {
	serviceOccurrences := make(map[string]int)
	for _, service := range a.Ss {
		serviceOccurrences[service.Type]++
		serviceKey := fmt.Sprintf("service:%s:%d", service.Type, serviceOccurrences[service.Type])
		iid, err := store.HomeKitIID(ctx, targetID, deviceID, serviceKey)
		if err != nil {
			return err
		}
		service.Id = iid
		characteristicOccurrences := make(map[string]int)
		for _, current := range service.Cs {
			characteristicOccurrences[current.Type]++
			key := fmt.Sprintf("%s/characteristic:%s:%d", serviceKey, current.Type, characteristicOccurrences[current.Type])
			iid, err := store.HomeKitIID(ctx, targetID, deviceID, key)
			if err != nil {
				return err
			}
			current.Id = iid
		}
	}
	return nil
}

func New(ctx context.Context, config Config, devices *application.DeviceService, logger *slog.Logger) (*Target, error) {
	sourceItems, err := devices.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	projectedSources := make(map[string]device.Device, len(sourceItems))
	rawSources := make(map[string]device.Device, len(sourceItems))
	var projectionIssues []ProjectionIssue
	for index := range sourceItems {
		rawSources[sourceItems[index].ID] = sourceItems[index]
		projected, projectErr := devices.ProjectForConsumer("homekit", sourceItems[index])
		if projectErr != nil {
			issue := ProjectionIssue{
				DeviceID: sourceItems[index].ID, DeviceName: sourceItems[index].Name, DeviceType: sourceItems[index].Type,
				Stage: "consumer-projection", Message: projectErr.Error(),
			}
			projectionIssues = append(projectionIssues, issue)
			logger.Error("HomeKit consumer projection failed", "device_id", sourceItems[index].ID, "error", projectErr)
			continue
		}
		projectedSources[sourceItems[index].ID] = projected
	}

	items := make([]device.Device, 0)
	routes := make(map[string]accessoryRoute)
	selected := make(map[string]bool, len(config.DeviceIDs))
	if len(config.Devices) > 0 {
		for _, virtual := range config.Devices {
			if !virtual.Enabled {
				continue
			}
			sourceIDs := virtual.SourceDeviceIDs()
			for _, sourceID := range sourceIDs {
				if _, ok := rawSources[sourceID]; !ok {
					return nil, fmt.Errorf("target virtual device %q references missing unified device %q", virtual.ID, sourceID)
				}
			}
			targetType := virtual.Type
			if targetType == "" {
				targetType = rawSources[virtual.SourceDeviceID].Type
			}
			source, projectErr := devices.ProjectSourcesForConsumerInstance("homekit", config.ID, virtual.ID, targetType, sourceIDs)
			if projectErr != nil {
				return nil, fmt.Errorf("project target virtual device %q: %w", virtual.ID, projectErr)
			}
			routes[virtual.ID] = accessoryRoute{sourceDeviceID: virtual.SourceDeviceID, sourceDeviceIDs: sourceIDs, targetType: targetType, targetID: config.ID, consumerDeviceID: virtual.ID}
			source.ID, source.Name = virtual.ID, virtual.Name
			items = append(items, source)
		}
	} else {
		for _, id := range config.DeviceIDs {
			selected[id] = true
		}
		for _, source := range projectedSources {
			items = append(items, source)
		}
	}
	accessoryIDs := make(map[string]uint64)
	if config.IdentityStore != nil {
		for _, item := range items {
			if len(selected) > 0 && !selected[item.ID] {
				continue
			}
			aid, err := config.IdentityStore.HomeKitAccessoryAID(ctx, config.ID, item.ID)
			if err != nil {
				return nil, fmt.Errorf("allocate HomeKit AID for %q: %w", item.ID, err)
			}
			accessoryIDs[item.ID] = aid
		}
	}
	bridge := accessory.NewBridge(accessory.Info{
		Name: config.Name, SerialNumber: "homeloom-" + config.ID,
		Manufacturer: "HomeLoom", Model: "HomeLoom Demo", Firmware: "0.0.1",
	})
	bindings := newAccessoryBindings(items, selected, accessoryIDs, devices, logger, routes)
	issues := append([]ProjectionIssue{}, projectionIssues...)
	issues = append(issues, bindings.issues...)
	if config.IdentityStore != nil {
		for deviceID, current := range bindings.byDevice {
			if err := assignPersistentIIDs(ctx, config.ID, deviceID, current, config.IdentityStore); err != nil {
				return nil, fmt.Errorf("allocate HomeKit IIDs for %q: %w", deviceID, err)
			}
		}
	}

	identityStore, err := newSecureFSStore(config.StorePath)
	if err != nil {
		return nil, err
	}
	server, err := hap.NewServer(identityStore, bridge.A, bindings.accessories...)
	if err != nil {
		return nil, fmt.Errorf("create HAP server: %w", err)
	}
	server.Addr = config.Address
	server.Pin = config.Pin
	server.SetupId = config.SetupID

	qrConfig := homekitqr.QRCodeConfig{SetupURIConfig: homekitqr.SetupURIConfig{
		Category: homekitqr.CategoryBridge, Flag: 2, PairingCode: config.Pin, SetupID: config.SetupID,
	}, Size: 320}
	setupURI, err := homekitqr.ComposeSetupURI(qrConfig.SetupURIConfig)
	if err != nil {
		return nil, fmt.Errorf("compose HomeKit setup URI: %w", err)
	}
	qr, err := homekitqr.GenerateQRPNG(qrConfig)
	if err != nil {
		return nil, fmt.Errorf("generate HomeKit QR code: %w", err)
	}

	target := &Target{
		server: server, logger: logger, pin: config.Pin, id: config.ID,
		pairing:                 PairingInfo{Code: formatPin(config.Pin), SetupURI: setupURI, QR: qr, Devices: virtualDeviceIDs(config, items)},
		issueSnapshot:           append([]ProjectionIssue(nil), issues...),
		publishedAccessoryCount: len(bindings.accessories),
	}
	target.cancelSubscription = devices.Subscribe(func(item device.Device) {
		if len(config.Devices) == 0 {
			projected, projectErr := devices.ProjectForConsumer("homekit", item)
			if projectErr != nil {
				logger.Error("HomeKit consumer projection failed", "device_id", item.ID, "error", projectErr)
				return
			}
			devices.RecordHomeKitPushes(bindings.update(projected))
			return
		}
		for _, virtual := range config.Devices {
			if !virtual.Enabled || !containsSourceDeviceID(virtual.SourceDeviceIDs(), item.ID) {
				continue
			}
			targetType := virtual.Type
			if targetType == "" {
				targetType = rawSources[virtual.SourceDeviceID].Type
			}
			scoped, scopedErr := devices.ProjectSourcesForConsumerInstance("homekit", config.ID, virtual.ID, targetType, virtual.SourceDeviceIDs())
			if scopedErr != nil {
				logger.Error("HomeKit scoped consumer projection failed", "target_id", config.ID, "consumer_device_id", virtual.ID, "device_id", item.ID, "error", scopedErr)
				continue
			}
			scoped.ID, scoped.Name = virtual.ID, virtual.Name
			devices.RecordHomeKitPushes(bindings.update(scoped))
		}
	})
	return target, nil
}

func containsSourceDeviceID(ids []string, wanted string) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

func virtualDeviceIDs(config Config, items []device.Device) []string {
	if len(config.Devices) == 0 {
		return append([]string(nil), config.DeviceIDs...)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result
}

func CheckAddressAvailable(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("HomeKit address %q is unavailable: %w", address, err)
	}
	if err := listener.Close(); err != nil {
		return fmt.Errorf("release HomeKit address %q probe: %w", address, err)
	}
	return nil
}

func (t *Target) ID() string { return t.id }

func (t *Target) PairingInfo() PairingInfo { return t.pairing }

func (t *Target) Issues() []ProjectionIssue {
	if t == nil || len(t.issueSnapshot) == 0 {
		return nil
	}
	return append([]ProjectionIssue(nil), t.issueSnapshot...)
}

func (t *Target) PublishedAccessoryCount() int {
	if t == nil {
		return 0
	}
	return t.publishedAccessoryCount
}

func (t *Target) IsPaired() bool { return t.server.IsPaired() }

func (t *Target) Start(ctx context.Context) error {
	t.logger.Info("HomeKit target started", "address", t.server.Addr, "pairing_pin", formatPin(t.pin))
	err := t.server.ListenAndServe(ctx)
	t.cancelSubscription()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func formatPin(pin string) string {
	if len(pin) != 8 {
		return "invalid"
	}
	return pin[:3] + "-" + pin[3:5] + "-" + pin[5:]
}
