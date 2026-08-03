package sonoff

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

func TestDecodeConfigAppliesSafeDefaults(t *testing.T) {
	raw, err := json.Marshal(Config{Devices: []DeviceConfig{{ID: "switch", DeviceID: "1000abc", Name: "客厅开关", DIY: true}}})
	if err != nil {
		t.Fatal(err)
	}
	config, err := decodeConfig(providerconfig.Config{ID: "sonoff-main", Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != ModeAuto || config.Region != "auto" || config.RequestTimeoutSeconds != defaultRequestTimeoutSeconds || config.Devices[0].Port != defaultLANPort || config.Devices[0].Channels != 1 {
		t.Fatalf("defaults not applied: %#v", config)
	}
}

func TestDecodeConfigRejectsSecretsInCloudlessInvalidShape(t *testing.T) {
	_, err := decodeConfig(providerconfig.Config{ID: "sonoff-main", Config: []byte(`{"mode":"local","devices":[{"id":"switch","deviceId":"1000abc","name":"开关","host":"127.0.0.1"}]}`)})
	if err == nil || !strings.Contains(err.Error(), "deviceKey") {
		t.Fatalf("expected deviceKey validation error, got %v", err)
	}
}

func TestDecodeConfigRequiresHTTPSCloudEndpointAndToken(t *testing.T) {
	cases := []string{
		`{"mode":"cloud","cloud":{"endpoint":"http://cloud.example"}}`,
		`{"mode":"cloud","cloud":{"endpoint":"https://cloud.example"}}`,
	}
	for _, raw := range cases {
		if _, err := decodeConfig(providerconfig.Config{ID: "sonoff-main", Config: []byte(raw)}); err == nil {
			t.Fatalf("config unexpectedly accepted: %s", raw)
		}
	}
}

func TestDecodeConfigRejectsUnknownFields(t *testing.T) {
	_, err := decodeConfig(providerconfig.Config{ID: "sonoff-main", Config: []byte(`{"unknown":true}`)})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}
