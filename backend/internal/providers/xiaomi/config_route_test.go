package xiaomi

import (
	"strings"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func centralRouteConfig(mode string) Config {
	return Config{
		Host: "hub.local", Port: 8883, ClientID: "123456", CACertificate: "ca", ClientCertificate: "cert", PrivateKey: "key",
		RequestTimeoutSec: 10, PollIntervalSec: 60,
		Devices: []DeviceConfig{{
			DID: "123", ID: "xiaomi-switch", Name: "开关", Type: device.TypeSwitch, ConnectionMode: mode,
			Properties: []PropertyMapping{{CapabilityID: "switch", CapabilityType: "switch", PropertyID: "power", ValueType: device.ValueTypeBool, SIID: 2, PIID: 1}},
		}},
	}
}

func TestCentralConfigDefaultsDeviceConnectionModeToAuto(t *testing.T) {
	config := centralRouteConfig("")
	config.applyDefaults()
	if config.Devices[0].ConnectionMode != connectionModeAuto {
		t.Fatalf("connection mode = %q", config.Devices[0].ConnectionMode)
	}
}

func TestCentralConfigValidatesConnectionModeAndCloudCredential(t *testing.T) {
	invalid := centralRouteConfig("invalid")
	if _, err := invalid.validate(); err == nil || !strings.Contains(err.Error(), "connectionMode") {
		t.Fatalf("invalid mode error = %v", err)
	}
	cloud := centralRouteConfig(connectionModeCloud)
	if _, err := cloud.validate(); err == nil || !strings.Contains(err.Error(), "OAuth accessToken") {
		t.Fatalf("missing cloud credential error = %v", err)
	}
	cloud.OAuth = &OAuthConfig{AccessToken: "token"}
	if _, err := cloud.validate(); err != nil {
		t.Fatalf("cloud route validation = %v", err)
	}
}
