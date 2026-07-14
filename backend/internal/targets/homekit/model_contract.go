package homekit

import (
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func homeKitModelContract(deviceType device.Type) (device.ConsumerModelContract, bool) {
	return mapping.HomeKitConsumerContract(deviceType)
}
