package tuya

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	tuyaapi "github.com/feranydev/homeloom/backend/internal/providers/tuya/api"
)

func TestDecodeConfigAcceptsHomeAssistantSharingCredentials(t *testing.T) {
	raw, err := json.Marshal(Config{
		AuthType:          "homeassistant",
		UID:               "uid-1",
		UserCode:          "user-code-1",
		Endpoint:          "https://openapi.tuyaus.com/",
		ClientID:          "HA_3y9q4ak7g4ephrvke",
		TerminalID:        "terminal-1",
		AccessToken:       "access-token",
		RefreshToken:      "refresh-token",
		RequestTimeoutSec: 15,
		PollIntervalSec:   21600,
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := decodeConfig("tuya-main", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !config.usesSharing() || config.Endpoint != "https://openapi.tuyaus.com" || config.ClientID != "HA_3y9q4ak7g4ephrvke" {
		t.Fatalf("sharing config = %#v", config)
	}
}

func TestDecodeConfigRejectsIncompleteHomeAssistantSharingCredentials(t *testing.T) {
	cases := []Config{
		{AuthType: "sharing", UID: "uid-1", UserCode: "user-code-1", Endpoint: "https://openapi.tuyaus.com", TerminalID: "terminal-1", AccessToken: "access-token"},
		{AuthType: "sharing", UID: "uid-1", UserCode: "user-code-1", Endpoint: "http://openapi.tuyaus.com", TerminalID: "terminal-1", AccessToken: "access-token", RefreshToken: "refresh-token"},
	}
	for index, config := range cases {
		raw, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeConfig("tuya-main", raw); err == nil {
			t.Fatalf("case %d unexpectedly accepted", index)
		}
	}
}

func TestNewProviderFromConfigUsesSharingClient(t *testing.T) {
	raw, err := json.Marshal(Config{
		AuthType:       "sharing",
		UID:            "uid-1",
		UserCode:       "user-code-1",
		Endpoint:       "https://openapi.tuyaus.com",
		TerminalID:     "terminal-1",
		AccessToken:    "access-token",
		RefreshToken:   "refresh-token",
		TokenExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewProviderFromConfig(providerconfig.Config{ID: "tuya-sharing", Type: ProviderType, Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.client.(*tuyaapi.SharingClient); !ok {
		t.Fatalf("client type = %T", provider.client)
	}
}

func TestDecodeConfigDefaultsToLightweightOneMinutePolling(t *testing.T) {
	raw, err := json.Marshal(Config{AccessID: "access", AccessSecret: "secret", UID: "user"})
	if err != nil {
		t.Fatal(err)
	}
	config, err := decodeConfig("tuya-main", raw)
	if err != nil {
		t.Fatal(err)
	}
	if config.PollIntervalSec != 60 {
		t.Fatalf("poll interval = %d", config.PollIntervalSec)
	}
}
