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
