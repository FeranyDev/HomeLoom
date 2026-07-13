package homekit

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
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

type Target struct {
	server             *hap.Server
	logger             *slog.Logger
	pin                string
	id                 string
	pairing            PairingInfo
	cancelSubscription func()
}

type accessoryBindings struct {
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
}

func newAccessoryBindings(items []device.Device, selected map[string]bool, accessoryIDs map[string]uint64, devices *application.DeviceService, logger *slog.Logger) *accessoryBindings {
	bindings := &accessoryBindings{
		accessories: make([]*accessory.A, 0, len(items)), switches: make(map[string]*characteristic.On), temperatures: make(map[string]*characteristic.CurrentTemperature), faults: make(map[string]*characteristic.StatusFault), extraFaults: make(map[string][]*characteristic.StatusFault), byDevice: make(map[string]*accessory.A), outletInUse: make(map[string]*characteristic.OutletInUse), humidities: make(map[string]*characteristic.CurrentRelativeHumidity), contacts: make(map[string]*characteristic.ContactSensorState), motions: make(map[string]*characteristic.MotionDetected), actives: make(map[string]*characteristic.Active), fanCurrent: make(map[string]*characteristic.CurrentFanState), fanTargets: make(map[string]*characteristic.TargetFanState), airCurrent: make(map[string]*characteristic.CurrentAirPurifierState), airTargets: make(map[string]*characteristic.TargetAirPurifierState), speeds: make(map[string]*characteristic.RotationSpeed), filterLife: make(map[string]*characteristic.FilterLifeLevel), filterChange: make(map[string]*characteristic.FilterChangeIndication), filterResets: make(map[string]*characteristic.ResetFilterIndication), positions: make(map[string]*characteristic.CurrentPosition), positionTargets: make(map[string]*characteristic.TargetPosition), positionStates: make(map[string]*characteristic.PositionState),
		brightness: make(map[string]*characteristic.Brightness), colorTemps: make(map[string]*characteristic.ColorTemperature), hues: make(map[string]*characteristic.Hue), saturations: make(map[string]*characteristic.Saturation), swingModes: make(map[string]*characteristic.SwingMode), directions: make(map[string]*characteristic.RotationDirection), controlLocks: make(map[string]*characteristic.LockPhysicalControls), airQualities: make(map[string]*characteristic.AirQuality), pm25: make(map[string]*characteristic.PM2_5Density), voc: make(map[string]*characteristic.VOCDensity), obstructions: make(map[string]*characteristic.ObstructionDetected), batteryLevels: make(map[string]*characteristic.BatteryLevel), lowBatteries: make(map[string]*characteristic.StatusLowBattery), tampered: make(map[string]*characteristic.StatusTampered),
	}
	for _, item := range items {
		if len(selected) > 0 && !selected[item.ID] {
			continue
		}
		consumerContract, supported := homeKitModelContract(item.Type)
		if !supported {
			continue
		}
		if _, err := device.ProjectForConsumer(item, consumerContract); err != nil {
			logger.Error("device does not satisfy HomeKit consumer contract", "device_id", item.ID, "device_type", item.Type, "error", err)
			continue
		}
		info := accessory.Info{Name: item.Name, SerialNumber: item.ID, Manufacturer: "HomeLoom", Model: string(item.Type), Firmware: "0.0.1"}
		var created *accessory.A
		switch item.Type {
		case device.TypeSwitch:
			a := accessory.NewSwitch(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.Switch.AddC(fault.C)
			deviceID := item.ID
			a.Switch.On.OnSetRemoteValue(func(value bool) error {
				return writeHomeKitProperty(devices, logger, deviceID, "switch", "power", device.BoolValue(value))
			})
			bindings.switches[item.ID], bindings.faults[item.ID] = a.Switch.On, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeLightbulb:
			a := accessory.NewLightbulb(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.Lightbulb.AddC(fault.C)
			deviceID := item.ID
			a.Lightbulb.On.OnSetRemoteValue(func(value bool) error {
				return writeHomeKitProperty(devices, logger, deviceID, "switch", "power", device.BoolValue(value))
			})
			if _, found := item.Property("main", "light", "brightness"); found {
				current := characteristic.NewBrightness()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, "light", "brightness", device.NumberValue(float64(value)))
				})
				a.Lightbulb.AddC(current.C)
				bindings.brightness[item.ID] = current
			}
			if _, found := item.Property("main", "light", "color-temperature"); found {
				current := characteristic.NewColorTemperature()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, "light", "color-temperature", device.IntValue(int64(value)))
				})
				a.Lightbulb.AddC(current.C)
				bindings.colorTemps[item.ID] = current
			}
			if _, found := item.Property("main", "light", "hue"); found {
				current := characteristic.NewHue()
				current.OnSetRemoteValue(func(value float64) error {
					return writeHomeKitProperty(devices, logger, deviceID, "light", "hue", device.NumberValue(value))
				})
				a.Lightbulb.AddC(current.C)
				bindings.hues[item.ID] = current
			}
			if _, found := item.Property("main", "light", "saturation"); found {
				current := characteristic.NewSaturation()
				current.OnSetRemoteValue(func(value float64) error {
					return writeHomeKitProperty(devices, logger, deviceID, "light", "saturation", device.NumberValue(value))
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
			deviceID := item.ID
			a.Outlet.On.OnSetRemoteValue(func(value bool) error {
				return writeHomeKitProperty(devices, logger, deviceID, "switch", "power", device.BoolValue(value))
			})
			bindings.switches[item.ID], bindings.outletInUse[item.ID], bindings.faults[item.ID] = a.Outlet.On, a.Outlet.OutletInUse, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeTemperatureSensor:
			a := accessory.NewTemperatureSensor(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.TempSensor.AddC(fault.C)
			if _, found := item.Property("main", "security", "tampered"); found {
				current := characteristic.NewStatusTampered()
				a.TempSensor.AddC(current.C)
				bindings.tampered[item.ID] = current
			}
			bindings.temperatures[item.ID], bindings.faults[item.ID] = a.TempSensor.CurrentTemperature, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeHumiditySensor:
			a := accessory.New(info, accessory.TypeSensor)
			a.Id = accessoryIDs[item.ID]
			sensor := service.NewHumiditySensor()
			fault := characteristic.NewStatusFault()
			sensor.AddC(fault.C)
			if _, found := item.Property("main", "security", "tampered"); found {
				current := characteristic.NewStatusTampered()
				sensor.AddC(current.C)
				bindings.tampered[item.ID] = current
			}
			a.AddS(sensor.S)
			bindings.humidities[item.ID], bindings.faults[item.ID] = sensor.CurrentRelativeHumidity, fault
			bindings.accessories = append(bindings.accessories, a)
			created = a
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
			deviceID := item.ID
			fan.Active.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, "fan", "active", device.BoolValue(value == characteristic.ActiveActive))
			})
			target.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, "fan", "target-state", device.EnumValue(fanTargetName(value)))
			})
			speed.OnSetRemoteValue(func(value float64) error {
				return writeHomeKitProperty(devices, logger, deviceID, "fan", "rotation-speed", device.NumberValue(value))
			})
			if _, found := item.Property("main", "fan", "swing-mode"); found {
				current := characteristic.NewSwingMode()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, "fan", "swing-mode", device.BoolValue(value == characteristic.SwingModeSwingEnabled))
				})
				fan.AddC(current.C)
				bindings.swingModes[item.ID] = current
			}
			if _, found := item.Property("main", "fan", "rotation-direction"); found {
				current := characteristic.NewRotationDirection()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, "fan", "rotation-direction", device.EnumValue(rotationDirectionName(value)))
				})
				fan.AddC(current.C)
				bindings.directions[item.ID] = current
			}
			if _, found := item.Property("main", "fan", "lock-physical-controls"); found {
				current := characteristic.NewLockPhysicalControls()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, "fan", "lock-physical-controls", device.BoolValue(value == characteristic.LockPhysicalControlsControlLockEnabled))
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
			fault, speed := characteristic.NewStatusFault(), characteristic.NewRotationSpeed()
			a.AirPurifier.AddC(fault.C)
			a.AirPurifier.AddC(speed.C)
			filter := service.NewFilterMaintenance()
			life, reset := characteristic.NewFilterLifeLevel(), characteristic.NewResetFilterIndication()
			filterFault := characteristic.NewStatusFault()
			filter.AddC(life.C)
			filter.AddC(reset.C)
			filter.AddC(filterFault.C)
			a.AirPurifier.AddS(filter.S)
			a.A.AddS(filter.S)
			deviceID := item.ID
			a.AirPurifier.Active.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, "air-purifier", "active", device.BoolValue(value == characteristic.ActiveActive))
			})
			a.AirPurifier.TargetAirPurifierState.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, "air-purifier", "target-state", device.EnumValue(airTargetName(value)))
			})
			speed.OnSetRemoteValue(func(value float64) error {
				return writeHomeKitProperty(devices, logger, deviceID, "air-purifier", "rotation-speed", device.NumberValue(value))
			})
			if _, found := item.Property("main", "air-purifier", "swing-mode"); found {
				current := characteristic.NewSwingMode()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, "air-purifier", "swing-mode", device.BoolValue(value == characteristic.SwingModeSwingEnabled))
				})
				a.AirPurifier.AddC(current.C)
				bindings.swingModes[item.ID] = current
			}
			if _, found := item.Property("main", "air-purifier", "lock-physical-controls"); found {
				current := characteristic.NewLockPhysicalControls()
				current.OnSetRemoteValue(func(value int) error {
					return writeHomeKitProperty(devices, logger, deviceID, "air-purifier", "lock-physical-controls", device.BoolValue(value == characteristic.LockPhysicalControlsControlLockEnabled))
				})
				a.AirPurifier.AddC(current.C)
				bindings.controlLocks[item.ID] = current
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
			reset.OnSetRemoteValue(func(int) error {
				return executeHomeKitCommand(devices, logger, providersdk.CommandRequest{DeviceID: deviceID, EndpointID: "main", CapabilityID: "filter", CommandID: "reset-filter"})
			})
			bindings.actives[item.ID], bindings.airCurrent[item.ID], bindings.airTargets[item.ID], bindings.speeds[item.ID], bindings.filterLife[item.ID], bindings.filterChange[item.ID], bindings.faults[item.ID] = a.AirPurifier.Active, a.AirPurifier.CurrentAirPurifierState, a.AirPurifier.TargetAirPurifierState, speed, life, filter.FilterChangeIndication, fault
			bindings.extraFaults[item.ID] = append(bindings.extraFaults[item.ID], filterFault)
			bindings.filterResets[item.ID] = reset
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeWindowCovering:
			a := accessory.NewWindowCovering(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.WindowCovering.AddC(fault.C)
			deviceID := item.ID
			a.WindowCovering.TargetPosition.OnSetRemoteValue(func(value int) error {
				return writeHomeKitProperty(devices, logger, deviceID, "window-covering", "target-position", device.IntValue(int64(value)))
			})
			if _, found := item.Property("main", "window-covering", "obstruction-detected"); found {
				current := characteristic.NewObstructionDetected()
				a.WindowCovering.AddC(current.C)
				bindings.obstructions[item.ID] = current
			}
			bindings.positions[item.ID], bindings.positionTargets[item.ID], bindings.positionStates[item.ID], bindings.faults[item.ID] = a.WindowCovering.CurrentPosition, a.WindowCovering.TargetPosition, a.WindowCovering.PositionState, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		}
		if created != nil {
			if _, found := item.Property("main", "battery", "level"); found {
				battery := service.NewBatteryService()
				_ = battery.ChargingState.SetValue(characteristic.ChargingStateNotChargeable)
				created.AddS(battery.S)
				bindings.batteryLevels[item.ID] = battery.BatteryLevel
				bindings.lowBatteries[item.ID] = battery.StatusLowBattery
			}
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
			_ = current.SetValue(int(*property.Value.Int))
			pushes++
		}
	}
	if current, ok := b.temperatures[item.ID]; ok {
		if property, found := item.Property("main", "temperature", "current-temperature"); found && property.Value.Number != nil {
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
		if item.Type == device.TypeAirPurifier {
			capability = "air-purifier"
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
		if item.Type == device.TypeAirPurifier {
			capability = "air-purifier"
		}
		if property, found := item.Property("main", capability, "rotation-speed"); found && property.Value.Number != nil {
			current.SetValue(*property.Value.Number)
			pushes++
		}
	}
	if current, ok := b.swingModes[item.ID]; ok {
		capability := "fan"
		if item.Type == device.TypeAirPurifier {
			capability = "air-purifier"
		}
		if property, found := item.Property("main", capability, "swing-mode"); found && property.Value.Bool != nil {
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
	return pushes
}

func writeHomeKitProperty(devices *application.DeviceService, logger *slog.Logger, deviceID, capabilityID, propertyID string, value device.PropertyValue) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := devices.ExecuteProperty(ctx, deviceID, "main", capabilityID, propertyID, value); err != nil {
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
	items, err := devices.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}

	selected := make(map[string]bool, len(config.DeviceIDs))
	for _, id := range config.DeviceIDs {
		selected[id] = true
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
	bindings := newAccessoryBindings(items, selected, accessoryIDs, devices, logger)
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
		pairing: PairingInfo{Code: formatPin(config.Pin), SetupURI: setupURI, QR: qr, Devices: append([]string(nil), config.DeviceIDs...)},
	}
	target.cancelSubscription = devices.Subscribe(func(item device.Device) {
		devices.RecordHomeKitPushes(bindings.update(item))
	})
	return target, nil
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
