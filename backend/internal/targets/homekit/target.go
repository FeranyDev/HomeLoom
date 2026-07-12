package homekit

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	homekitqr "github.com/kradalby/homekit-qr"
)

type Config struct {
	ID        string
	Name      string
	Address   string
	Pin       string
	SetupID   string
	StorePath string
	DeviceIDs []string
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

func New(ctx context.Context, config Config, devices *application.DeviceService, logger *slog.Logger) (*Target, error) {
	items, err := devices.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}

	selected := make(map[string]bool, len(config.DeviceIDs))
	for _, id := range config.DeviceIDs {
		selected[id] = true
	}
	bridge := accessory.NewBridge(accessory.Info{
		Name: config.Name, SerialNumber: "homeloom-" + config.ID,
		Manufacturer: "HomeLoom", Model: "HomeLoom Demo", Firmware: "0.0.1",
	})
	accessories := make([]*accessory.A, 0, len(items))
	switches := make(map[string]*characteristic.On)

	for _, item := range items {
		if len(selected) > 0 && !selected[item.ID] {
			continue
		}
		info := accessory.Info{
			Name: item.Name, SerialNumber: item.ID,
			Manufacturer: "HomeLoom", Model: string(item.Type), Firmware: "0.0.1",
		}
		switch item.Type {
		case device.TypeSwitch:
			a := accessory.NewSwitch(info)
			if item.State.Power != nil {
				a.Switch.On.SetValue(*item.State.Power)
			}
			deviceID := item.ID
			a.Switch.On.OnValueRemoteUpdate(func(value bool) {
				if _, updateErr := devices.SetPower(context.Background(), deviceID, value); updateErr != nil {
					logger.Error("HomeKit command failed", "device_id", deviceID, "error", updateErr)
				}
			})
			switches[item.ID] = a.Switch.On
			accessories = append(accessories, a.A)
		case device.TypeTemperatureSensor:
			a := accessory.NewTemperatureSensor(info)
			if item.State.Temperature != nil {
				a.TempSensor.CurrentTemperature.SetValue(*item.State.Temperature)
			}
			accessories = append(accessories, a.A)
		}
	}

	server, err := hap.NewServer(hap.NewFsStore(config.StorePath), bridge.A, accessories...)
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
		if characteristic, ok := switches[item.ID]; ok && item.State.Power != nil {
			characteristic.SetValue(*item.State.Power)
		}
	})
	return target, nil
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
