package xiaomi

import (
	"context"
	"errors"
	"net"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/platform/logging"
	mochimqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
	"go.uber.org/zap"
)

type brokerCredential struct {
	username string
	password string
}

type cloudMIPSBrokerHook struct {
	mochimqtt.HookBase
	mu                 sync.Mutex
	expectedPassword   string
	credentials        []brokerCredential
	subscriptions      [][]string
	connections        int
	allowSubscriptions atomic.Bool
}

func newCloudMIPSBrokerHook(password string) *cloudMIPSBrokerHook {
	hook := &cloudMIPSBrokerHook{expectedPassword: password}
	hook.allowSubscriptions.Store(true)
	return hook
}

func (*cloudMIPSBrokerHook) ID() string { return "xiaomi-cloud-mips-test" }

func (*cloudMIPSBrokerHook) Provides(capability byte) bool {
	switch capability {
	case mochimqtt.OnConnectAuthenticate, mochimqtt.OnACLCheck, mochimqtt.OnConnect, mochimqtt.OnSubscribed:
		return true
	default:
		return false
	}
}

func (h *cloudMIPSBrokerHook) OnConnectAuthenticate(_ *mochimqtt.Client, packet packets.Packet) bool {
	h.mu.Lock()
	credential := brokerCredential{username: string(packet.Connect.Username), password: string(packet.Connect.Password)}
	h.credentials = append(h.credentials, credential)
	expected := h.expectedPassword
	h.mu.Unlock()
	return credential.password == expected
}

func (h *cloudMIPSBrokerHook) OnACLCheck(_ *mochimqtt.Client, _ string, write bool) bool {
	return write || h.allowSubscriptions.Load()
}

func (h *cloudMIPSBrokerHook) OnConnect(_ *mochimqtt.Client, _ packets.Packet) error {
	h.mu.Lock()
	h.connections++
	h.mu.Unlock()
	return nil
}

func (h *cloudMIPSBrokerHook) OnSubscribed(_ *mochimqtt.Client, packet packets.Packet, _ []byte) {
	topics := make([]string, 0, len(packet.Filters))
	for _, subscription := range packet.Filters {
		topics = append(topics, subscription.Filter)
	}
	h.mu.Lock()
	h.subscriptions = append(h.subscriptions, topics)
	h.mu.Unlock()
}

func (h *cloudMIPSBrokerHook) setExpectedPassword(password string) {
	h.mu.Lock()
	h.expectedPassword = password
	h.mu.Unlock()
}

func (h *cloudMIPSBrokerHook) snapshot() (int, []brokerCredential, [][]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	credentials := append([]brokerCredential(nil), h.credentials...)
	subscriptions := make([][]string, len(h.subscriptions))
	for index := range h.subscriptions {
		subscriptions[index] = append([]string(nil), h.subscriptions[index]...)
	}
	return h.connections, credentials, subscriptions
}

func startCloudMIPSTestBroker(t *testing.T, password string) (*mochimqtt.Server, *cloudMIPSBrokerHook, string) {
	t.Helper()
	address := availableCloudMIPSTestAddress(t)
	server := mochimqtt.New(&mochimqtt.Options{InlineClient: true, Logger: logging.SlogAdapter(zap.NewNop())})
	hook := newCloudMIPSBrokerHook(password)
	if err := server.AddHook(hook, nil); err != nil {
		t.Fatal(err)
	}
	if err := server.AddListener(listeners.NewTCP(listeners.Config{ID: "xiaomi-cloud-mips", Address: address})); err != nil {
		t.Fatal(err)
	}
	if err := server.Serve(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, hook, address
}

func availableCloudMIPSTestAddress(t *testing.T) string {
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

func newCloudMIPSTestClient(t *testing.T, address, token string) *mqttCloudMIPSClient {
	t.Helper()
	client, err := newCloudMIPSClient(OAuthConfig{Region: "cn", OAuthUUID: "0123456789abcdef0123456789abcdef", ClientID: "oauth-client", AccessToken: token}, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	client.brokerURL = &url.URL{Scheme: "mqtt", Host: address}
	client.tlsConfig = nil
	client.reconnectDelay = 20 * time.Millisecond
	return client
}

func closeCloudMIPSTestClient(t *testing.T, client *mqttCloudMIPSClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("close cloud MIPS client: %v", err)
	}
}

func waitCloudMIPSCondition(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCloudMIPSFakeBrokerHandshakeSubscriptionAndPublish(t *testing.T) {
	server, hook, address := startCloudMIPSTestBroker(t, "old-token")
	client := newCloudMIPSTestClient(t, address, "old-token")
	defer closeCloudMIPSTestClient(t, client)
	if err := client.ReplaceDevices(context.Background(), []string{"123"}); err != nil {
		t.Fatal(err)
	}
	messages := make(chan cloudMIPSMessage, 1)
	client.SetIncomingHandler(func(message cloudMIPSMessage) { messages <- message })
	lifecycle, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Connect(lifecycle, context.Background()); err != nil {
		t.Fatal(err)
	}
	wantTopics := cloudMIPSTopics([]string{"123"})
	waitCloudMIPSCondition(t, time.Second, func() bool {
		connections, credentials, subscriptions := hook.snapshot()
		return connections == 1 && len(credentials) == 1 && credentials[0] == (brokerCredential{username: "oauth-client", password: "old-token"}) && len(subscriptions) == 1 && reflect.DeepEqual(subscriptions[0], wantTopics)
	}, "cloud MIPS client did not authenticate and subscribe")
	if err := server.Publish("device/123/up/properties_changed/2/1", []byte(`{"params":{"did":"123","siid":2,"piid":1,"value":true}}`), false, 2); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-messages:
		if message.Kind != cloudMIPSProperty || message.DID != "123" || message.SIID != 2 || message.PIID != 1 || message.Value != true {
			t.Fatalf("message=%#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("cloud MIPS publish was not delivered")
	}
}

func TestCloudMIPSFakeBrokerRecoversAuthenticationAfterTokenUpdate(t *testing.T) {
	server, hook, address := startCloudMIPSTestBroker(t, "old-token")
	client := newCloudMIPSTestClient(t, address, "old-token")
	defer closeCloudMIPSTestClient(t, client)
	if err := client.ReplaceDevices(context.Background(), []string{"123"}); err != nil {
		t.Fatal(err)
	}
	messages := make(chan cloudMIPSMessage, 1)
	connectionEvents := make(chan cloudMIPSConnectionEvent, 8)
	client.SetIncomingHandler(func(message cloudMIPSMessage) { messages <- message })
	client.SetConnectionHandler(func(event cloudMIPSConnectionEvent) { connectionEvents <- event })
	lifecycle, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Connect(lifecycle, context.Background()); err != nil {
		t.Fatal(err)
	}

	hook.setExpectedPassword("new-token")
	brokerClient, exists := server.Clients.Get(client.clientID)
	if !exists {
		t.Fatal("broker did not register cloud MIPS client")
	}
	brokerClient.Stop(errors.New("test network interruption"))
	waitCloudMIPSCondition(t, time.Second, func() bool {
		_, credentials, _ := hook.snapshot()
		stats := client.Stats()
		return !stats.Connected && stats.LastConnectError != "" && len(credentials) >= 3 && credentials[len(credentials)-1].password == "old-token"
	}, "cloud MIPS client did not expose the rejected stale token")
	client.UpdateOAuth(OAuthConfig{AccessToken: "new-token"})
	recovered := func() bool {
		stats := client.Stats()
		connections, credentials, subscriptions := hook.snapshot()
		return stats.Connected && stats.Reconnects == 1 && client.connections.Load() == 2 && connections >= 3 && len(credentials) >= 3 && credentials[len(credentials)-1].password == "new-token" && len(subscriptions) >= 2
	}
	deadline := time.Now().Add(2 * time.Second)
	for !recovered() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !recovered() {
		connections, credentials, subscriptions := hook.snapshot()
		t.Fatalf("cloud MIPS client did not reconnect with renewed token: stats=%#v connections=%d credentials=%#v subscriptions=%#v", client.Stats(), connections, credentials, subscriptions)
	}
	waitCloudMIPSCondition(t, time.Second, func() bool { return len(connectionEvents) >= 3 }, "cloud MIPS connection events were incomplete")
	time.Sleep(30 * time.Millisecond)
	events := make([]cloudMIPSConnectionEvent, 0, len(connectionEvents))
	for len(connectionEvents) > 0 {
		events = append(events, <-connectionEvents)
	}
	if len(events) != 3 || !events[0].Connected || events[1].Connected || !events[2].Connected {
		t.Fatalf("connection events=%#v", events)
	}
	if err := server.Publish("device/123/up/properties_changed/2/1", []byte(`{"params":{"did":"123","siid":2,"piid":1,"value":true}}`), false, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-messages:
	case <-time.After(time.Second):
		t.Fatal("restored subscription did not receive a publish")
	}
}

func TestCloudMIPSFakeBrokerIncrementallyReplacesDeviceSubscriptions(t *testing.T) {
	server, hook, address := startCloudMIPSTestBroker(t, "token")
	client := newCloudMIPSTestClient(t, address, "token")
	defer closeCloudMIPSTestClient(t, client)
	if err := client.ReplaceDevices(context.Background(), []string{"123"}); err != nil {
		t.Fatal(err)
	}
	messages := make(chan cloudMIPSMessage, 2)
	client.SetIncomingHandler(func(message cloudMIPSMessage) { messages <- message })
	lifecycle, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Connect(lifecycle, context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.ReplaceDevices(context.Background(), []string{"456"}); err != nil {
		t.Fatal(err)
	}
	waitCloudMIPSCondition(t, time.Second, func() bool {
		connections, _, subscriptions := hook.snapshot()
		return connections == 1 && len(subscriptions) == 2 && reflect.DeepEqual(subscriptions[1], cloudMIPSTopics([]string{"456"}))
	}, "incremental cloud MIPS subscription was not installed")
	if client.connections.Load() != 1 {
		t.Fatalf("physical MQTT connections=%d", client.connections.Load())
	}
	if err := server.Publish("device/123/up/properties_changed/2/1", []byte(`{"params":{"did":"123","siid":2,"piid":1,"value":true}}`), false, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-messages:
		t.Fatalf("removed DID still received message: %#v", message)
	case <-time.After(50 * time.Millisecond):
	}
	if err := server.Publish("device/456/up/properties_changed/2/1", []byte(`{"params":{"did":"456","siid":2,"piid":1,"value":true}}`), false, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-messages:
		if message.DID != "456" {
			t.Fatalf("message=%#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("new DID subscription did not receive message")
	}
}

func TestCloudMIPSFakeBrokerRetriesRejectedSubscriptionWithoutReconnect(t *testing.T) {
	_, hook, address := startCloudMIPSTestBroker(t, "token")
	hook.allowSubscriptions.Store(false)
	client := newCloudMIPSTestClient(t, address, "token")
	defer closeCloudMIPSTestClient(t, client)
	if err := client.ReplaceDevices(context.Background(), []string{"123"}); err != nil {
		t.Fatal(err)
	}
	connectionEvents := make(chan cloudMIPSConnectionEvent, 4)
	client.SetConnectionHandler(func(event cloudMIPSConnectionEvent) { connectionEvents <- event })
	lifecycle, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Connect(lifecycle, context.Background()); err == nil {
		t.Fatal("initial rejected subscription unexpectedly succeeded")
	}
	if client.Stats().Connected {
		t.Fatal("physical connection with rejected subscriptions was reported healthy")
	}
	hook.allowSubscriptions.Store(true)
	waitCloudMIPSCondition(t, time.Second, func() bool {
		return client.Stats().Connected && client.Stats().SubscriptionFailures >= 1
	}, "rejected cloud MIPS subscription was not retried")
	connections, _, subscriptions := hook.snapshot()
	if connections != 1 || len(subscriptions) < 2 || !reflect.DeepEqual(subscriptions[len(subscriptions)-1], cloudMIPSTopics([]string{"123"})) || client.connections.Load() != 1 {
		t.Fatalf("connections=%d physical=%d subscriptions=%#v", connections, client.connections.Load(), subscriptions)
	}
	waitCloudMIPSCondition(t, time.Second, func() bool { return len(connectionEvents) == 2 }, "subscription retry connection events were incomplete")
	first, second := <-connectionEvents, <-connectionEvents
	if first.Connected || first.Cause == "" || !second.Connected {
		t.Fatalf("connection events=%#v %#v", first, second)
	}
}
