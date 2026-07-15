package target

import "github.com/feranydev/homeloom/backend/internal/domain/device"

type TypeDescriptor struct {
	Type                   string
	ConsumerID             string
	DefaultIDPrefix        string
	DefaultName            string
	SupportsHomeKitPairing bool
}

var builtInTypeDescriptors = map[string]TypeDescriptor{
	"apple-hap": {Type: "apple-hap", ConsumerID: "homekit", DefaultIDPrefix: "apple", DefaultName: "HomeLoom Apple Home Bridge", SupportsHomeKitPairing: true},
	"matter":    {Type: "matter", ConsumerID: "matter", DefaultIDPrefix: "matter", DefaultName: "HomeLoom Matter Bridge"},
}

func DescriptorForType(value string) (TypeDescriptor, bool) {
	descriptor, found := builtInTypeDescriptors[value]
	return descriptor, found
}

// VirtualDevice is a Consumer-side device owned by one Target instance. It
// keeps the concrete Target identity separate from the unified source device.
type VirtualDevice struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Type           device.Type `json:"type"`
	SourceDeviceID string      `json:"sourceDeviceId"`
	Enabled        bool        `json:"enabled"`
}

type Config struct {
	ID        string
	Type      string
	Name      string
	Enabled   bool
	Address   string
	Pin       string
	SetupID   string
	StorePath string
	DeviceIDs []string
	Devices   []VirtualDevice
}
