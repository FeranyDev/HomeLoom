package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

func TestConfigDefaultsAndMQTTSNormalization(t *testing.T) {
	config, broker, tlsConfig, err := decodeConfig(providerconfig.Config{ID: "mqtt-lab", Config: json.RawMessage(`{"brokerUrl":"mqtts://broker.example:8883"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if broker.Scheme != "tls" || config.ClientID != "homeloom-mqtt-lab" || config.TopicPrefix != "homeloom" || config.KeepAliveSeconds != 30 || config.ConnectTimeoutSeconds != 10 || config.SessionExpirySeconds != 86400 || config.RetainedStateMaxAgeSeconds != 300 || tlsConfig == nil {
		t.Fatalf("config = %#v, broker = %#v, tls = %#v", config, broker, tlsConfig)
	}
}

func TestConfigRejectsUnsafeOrAmbiguousValues(t *testing.T) {
	tests := []string{
		`{}`,
		`{"brokerUrl":"http://broker"}`,
		`{"brokerUrl":"mqtt://user:password@broker"}`,
		`{"brokerUrl":"mqtt://broker/path"}`,
		`{"brokerUrl":"mqtt://broker","qos":3}`,
		`{"brokerUrl":"mqtt://broker","topicPrefix":"house/+/state"}`,
		`{"brokerUrl":"mqtt://broker","tls":{"insecureSkipVerify":true}}`,
		`{"brokerUrl":"tls://broker","tls":{"certFile":"client.pem"}}`,
		`{"brokerUrl":"mqtt://broker","unknown":true}`,
	}
	for _, document := range tests {
		if _, _, _, err := decodeConfig(providerconfig.Config{ID: "mqtt-test", Config: json.RawMessage(document)}); err == nil {
			t.Errorf("decodeConfig(%s) accepted", document)
		}
	}
}
