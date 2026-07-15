package mapping

import (
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

// CustomModel is a database-backed unified model namespace. Its properties
// are configured independently through CustomModelProperty.
type CustomModel struct {
	DeviceType device.Type `json:"deviceType"`
	Name       string      `json:"name"`
	Version    int         `json:"version"`
}

func ValidateCustomModel(item CustomModel) error {
	fields := make(map[string]string)
	if !device.ValidStableID(string(item.DeviceType)) {
		fields["deviceType"] = "must be a stable lowercase identifier"
	}
	if name := strings.TrimSpace(item.Name); name == "" || len(name) > 80 {
		fields["name"] = "must be between 1 and 80 characters"
	}
	if item.Version < 1 {
		fields["version"] = "must be greater than zero"
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}
