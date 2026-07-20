package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	mochimqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"
)

func TestServerModeGeneratesLeastPrivilegeDeviceACL(t *testing.T) {
	config := Config{Mode: ModeServer, Username: "device", Password: "secret", Devices: []DeviceConfig{{ID: "server-switch", TopicPrefix: "server"}}}
	config.applyDefaults("mqtt-server")
	ledger := brokerAuthLedger(config, configuredSubscriptions(config.Devices))
	client := &mochimqtt.Client{ID: "device-1", Properties: mochimqtt.ClientProperties{Username: []byte("device")}, Net: mochimqtt.ClientConnection{Remote: "127.0.0.1:10000"}}
	if _, ok := ledger.AuthOk(client, packets.Packet{Connect: packets.ConnectParams{Password: []byte("secret")}}); !ok {
		t.Fatal("configured credentials were rejected")
	}
	if _, ok := ledger.AuthOk(client, packets.Packet{Connect: packets.ConnectParams{Password: []byte("wrong")}}); ok {
		t.Fatal("invalid password was accepted")
	}
	state := stateTopic("server", "server-switch", "main", "switch", "power")
	command := commandTopic("server", "server-switch", "main", "switch", "power")
	assertACL := func(topic string, write, expected bool) {
		t.Helper()
		if _, ok := ledger.ACLOk(client, topic, write); ok != expected {
			t.Fatalf("ACLOk(%q, write=%v) = %v, want %v", topic, write, ok, expected)
		}
	}
	assertACL(state, true, true)
	assertACL(state, false, false)
	assertACL(command, false, true)
	assertACL(command, true, false)
	assertACL("unconfigured/topic", true, false)
	assertACL("unconfigured/topic", false, false)
}

func TestServerModeAcceptsDevicePublicationsAndDeliversCommands(t *testing.T) {
	address := availableTCPAddress(t)
	provider, err := NewProviderFromConfig(providerconfig.Config{
		ID: "mqtt-server", Name: "Embedded Broker",
		Config: json.RawMessage(fmt.Sprintf(`{"mode":"server","listenAddress":%q,"username":"device","password":"secret","connectTimeoutSeconds":2,"devices":[{"id":"server-switch","topicPrefix":"server","qos":1}]}`, address)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close(context.Background()) })

	commands := make(chan *paho.Publish, 1)
	client := connectBrokerDevice(t, address, "device", "secret", commands)
	t.Cleanup(func() { _ = client.Disconnect(&paho.Disconnect{}) })
	commandTopic := commandTopic("server", "server-switch", "main", "switch", "power")
	if _, err := client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: commandTopic, QoS: 1}}}); err != nil {
		t.Fatalf("subscribe command topic: %v", err)
	}

	events := make(chan device.Device, 2)
	provider.Subscribe(func(item device.Device) { events <- item })
	discovered := mqttSwitch("server-switch", true, false)
	payload, err := json.Marshal(discovered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Publish(ctx, &paho.Publish{Topic: discoveryTopic("server", discovered.ID), QoS: 1, Retain: true, Payload: payload}); err != nil {
		t.Fatalf("publish discovery: %v", err)
	}
	if item := waitDevice(t, events); item.ID != discovered.ID {
		t.Fatalf("discovered item = %#v", item)
	}

	if _, err := provider.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: discovered.ID, EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)}); err != nil {
		t.Fatalf("write property: %v", err)
	}
	select {
	case command := <-commands:
		if command.Topic != commandTopic {
			t.Fatalf("command topic = %q", command.Topic)
		}
	case <-time.After(time.Second):
		t.Fatal("embedded broker did not deliver command")
	}
}

func TestServerModeRejectsInvalidCredentials(t *testing.T) {
	address := availableTCPAddress(t)
	provider, err := NewProviderFromConfig(providerconfig.Config{ID: "mqtt-auth", Name: "Auth", Config: json.RawMessage(fmt.Sprintf(`{"mode":"server","listenAddress":%q,"username":"device","password":"secret","devices":[]}`, address))})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close(context.Background()) })

	connection, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := paho.NewClient(paho.ClientConfig{Conn: connection, PacketTimeout: time.Second})
	connectContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	connack, connectErr := client.Connect(connectContext, &paho.Connect{KeepAlive: 30, ClientID: "unauthorized-device", CleanStart: true, UsernameFlag: true, PasswordFlag: true, Username: "device", Password: []byte("wrong")})
	if connectErr == nil && connack != nil && connack.ReasonCode == 0 {
		t.Fatal("invalid MQTT credentials were accepted")
	}
}

func availableTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func connectBrokerDevice(t *testing.T, address, username, password string, messages chan<- *paho.Publish) *paho.Client {
	t.Helper()
	connection, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	client := paho.NewClient(paho.ClientConfig{
		Conn: connection, ClientID: "server-mode-device", PacketTimeout: time.Second,
		OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(received paho.PublishReceived) (bool, error) {
			messages <- received.Packet
			return true, nil
		}},
	})
	connack, err := client.Connect(context.Background(), &paho.Connect{KeepAlive: 30, ClientID: "server-mode-device", CleanStart: true, UsernameFlag: username != "", PasswordFlag: password != "", Username: username, Password: []byte(password)})
	if err != nil {
		t.Fatal(err)
	}
	if connack.ReasonCode != 0 {
		t.Fatalf("connect MQTT device: reason %d", connack.ReasonCode)
	}
	return client
}
