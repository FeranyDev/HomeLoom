package homekit

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
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
	accessories  []*accessory.A
	switches     map[string]*characteristic.On
	temperatures map[string]*characteristic.CurrentTemperature
	faults       map[string]*characteristic.StatusFault
	byDevice     map[string]*accessory.A
	outletInUse  map[string]*characteristic.OutletInUse
	humidities   map[string]*characteristic.CurrentRelativeHumidity
	contacts     map[string]*characteristic.ContactSensorState
	motions      map[string]*characteristic.MotionDetected
}

func newAccessoryBindings(items []device.Device, selected map[string]bool, accessoryIDs map[string]uint64, devices *application.DeviceService, logger *slog.Logger) *accessoryBindings {
	bindings := &accessoryBindings{accessories: make([]*accessory.A, 0, len(items)), switches: make(map[string]*characteristic.On), temperatures: make(map[string]*characteristic.CurrentTemperature), faults: make(map[string]*characteristic.StatusFault), byDevice: make(map[string]*accessory.A), outletInUse: make(map[string]*characteristic.OutletInUse), humidities: make(map[string]*characteristic.CurrentRelativeHumidity), contacts: make(map[string]*characteristic.ContactSensorState), motions: make(map[string]*characteristic.MotionDetected)}
	for _, item := range items {
		if len(selected) > 0 && !selected[item.ID] {
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
			a.Switch.On.OnValueRemoteUpdate(func(value bool) {
				if _, _, err := devices.ExecutePower(context.Background(), deviceID, value); err != nil {
					logger.Error("HomeKit command failed", "device_id", deviceID, "error", err)
				}
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
			a.Lightbulb.On.OnValueRemoteUpdate(func(value bool) {
				if _, _, err := devices.ExecutePower(context.Background(), deviceID, value); err != nil {
					logger.Error("HomeKit command failed", "device_id", deviceID, "error", err)
				}
			})
			bindings.switches[item.ID], bindings.faults[item.ID] = a.Lightbulb.On, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeOutlet:
			a := accessory.NewOutlet(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.Outlet.AddC(fault.C)
			deviceID := item.ID
			a.Outlet.On.OnValueRemoteUpdate(func(value bool) {
				if _, _, err := devices.ExecutePower(context.Background(), deviceID, value); err != nil {
					logger.Error("HomeKit command failed", "device_id", deviceID, "error", err)
				}
			})
			bindings.switches[item.ID], bindings.outletInUse[item.ID], bindings.faults[item.ID] = a.Outlet.On, a.Outlet.OutletInUse, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeTemperatureSensor:
			a := accessory.NewTemperatureSensor(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.TempSensor.AddC(fault.C)
			bindings.temperatures[item.ID], bindings.faults[item.ID] = a.TempSensor.CurrentTemperature, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeHumiditySensor:
			a := accessory.New(info, accessory.TypeSensor)
			a.Id = accessoryIDs[item.ID]
			sensor := service.NewHumiditySensor()
			fault := characteristic.NewStatusFault()
			sensor.AddC(fault.C)
			a.AddS(sensor.S)
			bindings.humidities[item.ID], bindings.faults[item.ID] = sensor.CurrentRelativeHumidity, fault
			bindings.accessories = append(bindings.accessories, a)
			created = a
		case device.TypeContactSensor:
			a := accessory.NewContactSensor(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.ContactSensor.AddC(fault.C)
			bindings.contacts[item.ID], bindings.faults[item.ID] = a.ContactSensor.ContactSensorState, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		case device.TypeMotionSensor:
			a := accessory.NewMotionSensor(info)
			a.A.Id = accessoryIDs[item.ID]
			fault := characteristic.NewStatusFault()
			a.MotionSensor.AddC(fault.C)
			bindings.motions[item.ID], bindings.faults[item.ID] = a.MotionSensor.MotionDetected, fault
			bindings.accessories = append(bindings.accessories, a.A)
			created = a.A
		}
		if created != nil {
			bindings.byDevice[item.ID] = created
			bindings.update(item)
		}
	}
	return bindings
}

func (b *accessoryBindings) update(item device.Device) {
	fault, exists := b.faults[item.ID]
	if !exists {
		return
	}
	if !item.Online {
		_ = fault.SetValue(characteristic.StatusFaultGeneralFault)
		return
	}
	_ = fault.SetValue(characteristic.StatusFaultNoFault)
	if current, ok := b.switches[item.ID]; ok {
		if property, found := item.Property("main", "switch", "power"); found && property.Value.Bool != nil {
			current.SetValue(*property.Value.Bool)
			if inUse, exists := b.outletInUse[item.ID]; exists {
				inUse.SetValue(*property.Value.Bool)
			}
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
		}
	}
	if current, ok := b.humidities[item.ID]; ok {
		if property, found := item.Property("main", "humidity", "current-humidity"); found && property.Value.Number != nil {
			current.SetValue(*property.Value.Number)
		}
	}
	if current, ok := b.contacts[item.ID]; ok {
		if property, found := item.Property("main", "contact", "contact-detected"); found && property.Value.Bool != nil {
			value := characteristic.ContactSensorStateContactNotDetected
			if *property.Value.Bool {
				value = characteristic.ContactSensorStateContactDetected
			}
			_ = current.SetValue(value)
		}
	}
	if current, ok := b.motions[item.ID]; ok {
		if property, found := item.Property("main", "motion", "motion-detected"); found && property.Value.Bool != nil {
			current.SetValue(*property.Value.Bool)
		}
	}
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

	server, err := hap.NewServer(hap.NewFsStore(config.StorePath), bridge.A, bindings.accessories...)
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
		bindings.update(item)
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
