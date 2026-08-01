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

func TestCentralConfigSeparatesCameraControlIdentityFromCanonicalCamera(t *testing.T) {
	for name, id := range map[string]string{"missing": "", "legacy-collision": "xiaomi-1178028045"} {
		t.Run(name, func(t *testing.T) {
			config := centralRouteConfig("")
			config.Devices[0] = DeviceConfig{
				DID: "1178028045", ID: id, Name: "小米智能摄像机", Type: device.TypeCamera,
				Properties: []PropertyMapping{},
			}
			config.applyDefaults()
			if got := config.Devices[0].ID; got != "xiaomi-control-1178028045" {
				t.Fatalf("camera control id = %q", got)
			}
		})
	}
	config := centralRouteConfig("")
	config.Devices[0] = DeviceConfig{DID: "1178028045", ID: "custom-camera-control", Name: "摄像头", Type: device.TypeCamera}
	config.applyDefaults()
	if got := config.Devices[0].ID; got != "custom-camera-control" {
		t.Fatalf("custom camera control id = %q", got)
	}
}

func TestProviderHidesEveryCameraIdentity(t *testing.T) {
	config := centralRouteConfig("")
	config.Devices = []DeviceConfig{
		{DID: "1178028045", ID: "camera-control", Name: "摄像头控制", Type: device.TypeCamera},
		{DID: "other", ID: "visible-switch", Name: "开关", Type: device.TypeSwitch},
	}
	provider := &Provider{config: config}
	hidden := provider.HiddenDeviceIDs()
	if len(hidden) != 1 || hidden[0] != "camera-control" {
		t.Fatalf("hidden device IDs = %#v", hidden)
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
