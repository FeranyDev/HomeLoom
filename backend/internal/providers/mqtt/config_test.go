package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

func TestConfigDefaultsAndMQTTSNormalization(t *testing.T) {
	config, broker, tlsConfig, err := decodeConfig(providerconfig.Config{ID: "mqtt-lab", Config: json.RawMessage(`{"brokerUrl":"mqtts://broker.example:8883","devices":[{"id":"desk-lamp","topicPrefix":"home/desk"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if broker.Scheme != "tls" || config.ClientID != "homeloom-mqtt-lab" || config.KeepAliveSeconds != 30 || config.ConnectTimeoutSeconds != 10 || config.SessionExpirySeconds != 86400 || config.RetainedStateMaxAgeSeconds != 300 || tlsConfig == nil {
		t.Fatalf("config = %#v, broker = %#v, tls = %#v", config, broker, tlsConfig)
	}
	item := config.Devices[0]
	if item.effectiveQoS() != 1 || item.Protocol != "homeloom-v1" || item.Topics.Discovery != "home/desk/discovery/desk-lamp" || item.Topics.State != "home/desk/state/desk-lamp/{endpointId}/{capabilityId}/{propertyId}" {
		t.Fatalf("device config = %#v", item)
	}
}

func TestServerModeDefaultsAndValidation(t *testing.T) {
	config, broker, tlsConfig, err := decodeConfig(providerconfig.Config{ID: "mqtt-listener", Config: json.RawMessage(`{"mode":"server","devices":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != ModeServer || config.ListenAddress != "127.0.0.1:1883" || config.ClientID != "" || broker != nil || tlsConfig != nil {
		t.Fatalf("config = %#v, broker = %#v, tls = %#v", config, broker, tlsConfig)
	}
}

func TestTransportFactorySelectsConfiguredRuntimeMode(t *testing.T) {
	clientConfig, broker, clientTLS, err := decodeConfig(providerconfig.Config{ID: "mqtt-client", Config: json.RawMessage(`{"brokerUrl":"mqtt://broker:1883"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := newMQTTTransport(clientConfig, broker, clientTLS, transportHandlers{}).(*pahoTransport); !ok {
		t.Fatal("client mode did not create a Paho client transport")
	}
	serverConfig, broker, serverTLS, err := decodeConfig(providerconfig.Config{ID: "mqtt-server", Config: json.RawMessage(`{"mode":"server"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := newMQTTTransport(serverConfig, broker, serverTLS, transportHandlers{}).(*brokerTransport); !ok {
		t.Fatal("server mode did not create an embedded broker transport")
	}
}

func TestConfigRejectsUnsafeOrAmbiguousValues(t *testing.T) {
	tests := []string{
		`{}`,
		`{"brokerUrl":"http://broker"}`,
		`{"brokerUrl":"mqtt://user:password@broker"}`,
		`{"brokerUrl":"mqtt://broker/path"}`,
		`{"brokerUrl":"mqtt://broker","topicPrefix":"house/+/state"}`,
		`{"brokerUrl":"mqtt://broker","devices":[{"id":"Bad ID","topicPrefix":"house"}]}`,
		`{"brokerUrl":"mqtt://broker","devices":[{"id":"one","topicPrefix":"house","qos":3}]}`,
		`{"brokerUrl":"mqtt://broker","devices":[{"id":"one","topicPrefix":"house","topics":{"state":"house/state/one/{endpointId}"}}]}`,
		`{"brokerUrl":"mqtt://broker","devices":[{"id":"one","topicPrefix":"house"},{"id":"one","topicPrefix":"other"}]}`,
		`{"brokerUrl":"mqtt://broker","devices":[{"id":"one","topicPrefix":"house","topics":{"state":"shared/{endpointId}/{capabilityId}/{propertyId}"}},{"id":"two","topicPrefix":"other","topics":{"availability":"shared/main/switch/power"}}]}`,
		`{"brokerUrl":"mqtt://broker","devices":[{"id":"one","topicPrefix":"house","topics":{"command":"house/state/one/{endpointId}/{capabilityId}/{operationId}"}}]}`,
		`{"brokerUrl":"mqtt://broker","tls":{"insecureSkipVerify":true}}`,
		`{"brokerUrl":"tls://broker","tls":{"certFile":"client.pem"}}`,
		`{"brokerUrl":"mqtt://broker","unknown":true}`,
		`{"mode":"unknown","brokerUrl":"mqtt://broker"}`,
		`{"mode":"server","brokerUrl":"mqtt://broker"}`,
		`{"mode":"server","listenAddress":"localhost"}`,
		`{"mode":"server","listenAddress":"127.0.0.1:0"}`,
		`{"mode":"server","tls":{"insecureSkipVerify":true}}`,
	}
	for _, document := range tests {
		if _, _, _, err := decodeConfig(providerconfig.Config{ID: "mqtt-test", Config: json.RawMessage(document)}); err == nil {
			t.Errorf("decodeConfig(%s) accepted", document)
		}
	}
}
