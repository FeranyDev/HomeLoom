package target

import "github.com/feranydev/homeloom/backend/internal/domain/device"

const (
	DefaultMatterVendorID                   uint16 = 0xFFF1
	DefaultMatterProductID                  uint16 = 0x8000
	DefaultMatterCommissioningWindowSeconds uint32 = 900
)

type TypeDescriptor struct {
	Type                   string
	ConsumerID             string
	DefaultIDPrefix        string
	DefaultName            string
	SupportsHomeKitPairing bool
}

var builtInTypeDescriptors = map[string]TypeDescriptor{
	"apple-hap":      {Type: "apple-hap", ConsumerID: "homekit", DefaultIDPrefix: "apple", DefaultName: "HomeLoom Apple Home Bridge", SupportsHomeKitPairing: true},
	"homekit-camera": {Type: "homekit-camera", ConsumerID: "homekit-camera", DefaultIDPrefix: "camera-homekit", DefaultName: "HomeLoom HomeKit Camera", SupportsHomeKitPairing: true},
	"matter":         {Type: "matter", ConsumerID: "matter", DefaultIDPrefix: "matter", DefaultName: "HomeLoom Matter Bridge"},
	"matter-camera":  {Type: "matter-camera", ConsumerID: "matter-camera", DefaultIDPrefix: "camera-matter", DefaultName: "HomeLoom Matter Camera"},
}

func DescriptorForType(value string) (TypeDescriptor, bool) {
	descriptor, found := builtInTypeDescriptors[value]
	return descriptor, found
}

// IsMatterType reports whether a target owns an independent Matter runtime.
func IsMatterType(value string) bool {
	return value == "matter" || value == "matter-camera"
}

// VirtualDevice is a Consumer-side device owned by one Target instance. It
// keeps the concrete Target identity separate from the unified source device.
type VirtualDevice struct {
	ID                       string      `json:"id"`
	Name                     string      `json:"name"`
	Type                     device.Type `json:"type"`
	SourceDeviceID           string      `json:"sourceDeviceId"`
	AuxiliarySourceDeviceIDs []string    `json:"auxiliarySourceDeviceIds,omitempty"`
	Enabled                  bool        `json:"enabled"`
}

// SourceDeviceIDs returns the aggregate sources in routing-priority order.
// The primary source wins when multiple explicit mappings target the same
// Consumer property; auxiliary sources are considered in their stored order.
func (v VirtualDevice) SourceDeviceIDs() []string {
	result := make([]string, 0, 1+len(v.AuxiliarySourceDeviceIDs))
	seen := make(map[string]struct{}, 1+len(v.AuxiliarySourceDeviceIDs))
	for _, id := range append([]string{v.SourceDeviceID}, v.AuxiliarySourceDeviceIDs...) {
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

// HomeKitConfig contains fields that are meaningful only to the apple-hap
// runtime. Config keeps the old flat fields during the migration so older
// clients and persisted rows remain readable.
type HomeKitConfig struct {
	Address   string `json:"address"`
	Pin       string `json:"pin,omitempty"`
	SetupID   string `json:"setupId"`
	StorePath string `json:"storePath,omitempty"`
}

// MatterConfig contains the commissioning and network identity of one Matter
// bridge. A zero UDPPort requests runtime allocation. Discriminator is a
// pointer because zero is a valid discriminator and must be distinguishable
// from an omitted value before defaults are applied.
type MatterConfig struct {
	NetworkInterface           string  `json:"networkInterface,omitempty"`
	UDPPort                    uint16  `json:"udpPort,omitempty"`
	Discriminator              *uint16 `json:"discriminator,omitempty"`
	Passcode                   string  `json:"passcode,omitempty"`
	VendorID                   uint16  `json:"vendorId"`
	ProductID                  uint16  `json:"productId"`
	ProductName                string  `json:"productName"`
	SerialNumber               string  `json:"serialNumber"`
	CommissioningWindowSeconds uint32  `json:"commissioningWindowSeconds"`
}

type Config struct {
	ID            string
	Type          string
	Name          string
	Enabled       bool
	HomeKitConfig *HomeKitConfig
	MatterConfig  *MatterConfig
	DeviceIDs     []string
	Devices       []VirtualDevice

	// Deprecated HomeKit compatibility fields. New code should use
	// HomeKitConfig. They are synchronized at application and persistence
	// boundaries and can be removed after all API clients have migrated.
	Address   string
	Pin       string
	SetupID   string
	StorePath string
}

// NormalizeProtocolConfig imports legacy HomeKit fields and clears fields for
// protocols to which they do not belong. It deliberately returns a copy.
func (c Config) NormalizeProtocolConfig() Config {
	switch c.Type {
	case "", "apple-hap", "homekit-camera":
		homekit := HomeKitConfig{}
		if c.HomeKitConfig != nil {
			homekit = *c.HomeKitConfig
		}
		if homekit.Address == "" {
			homekit.Address = c.Address
		}
		if homekit.Pin == "" {
			homekit.Pin = c.Pin
		}
		if homekit.SetupID == "" {
			homekit.SetupID = c.SetupID
		}
		if homekit.StorePath == "" {
			homekit.StorePath = c.StorePath
		}
		c.HomeKitConfig = &homekit
		c.MatterConfig = nil
		c.Address, c.Pin, c.SetupID, c.StorePath = homekit.Address, homekit.Pin, homekit.SetupID, homekit.StorePath
	case "matter", "matter-camera":
		c.HomeKitConfig = nil
		c.Address, c.Pin, c.SetupID, c.StorePath = "", "", "", ""
	}
	return c
}
