package application

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

func TestExportServiceRedactsConfigurationAndOmitsPairingMaterial(t *testing.T) {
	providers := NewProviderService([]providerconfig.Config{{ID: "mqtt-main", Type: "mqtt", Name: "MQTT", Enabled: true, Config: json.RawMessage(`{"host":"localhost","password":"secret-value","nested":{"apiToken":"token-value"}}`)}}, nil, nil, nil)
	targets := NewTargetService([]TargetRegistration{{Info: TargetInfo{ID: "apple-main", Type: "apple-hap", Name: "Home", Enabled: true, Address: ":51826", SetupID: "ABCD", PairingCode: "111-22-333", SetupURI: "X-HM://sensitive", DeviceIDs: []string{"switch-1"}}}}, nil)
	service := NewExportService(nil, providers, targets, nil, nil)
	service.now = func() time.Time { return time.Date(2026, 7, 13, 1, 2, 3, 0, time.UTC) }

	encoded, err := json.Marshal(service.Configuration())
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"secret-value", "token-value", "111-22-333", "X-HM://sensitive", "storePath", "pairingCode", "setupUri"} {
		if strings.Contains(text, secret) {
			t.Fatalf("export contains %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, `"password":"********"`) || !strings.Contains(text, `"apiToken":"********"`) || !strings.Contains(text, `"setupId":"ABCD"`) {
		t.Fatalf("unexpected export: %s", text)
	}
}
