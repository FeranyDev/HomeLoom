package mqtt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type brokerPublication struct {
	topic   string
	payload []byte
}

type testMQTTClient struct {
	connection net.Conn
	writeMu    sync.Mutex
}

func (c *testMQTTClient) write(header byte, body []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	packet := []byte{header}
	packet = append(packet, encodeRemainingLength(len(body))...)
	packet = append(packet, body...)
	_, err := c.connection.Write(packet)
	return err
}

type testMQTTBroker struct {
	listener      net.Listener
	mu            sync.Mutex
	clients       map[*testMQTTClient]struct{}
	done          chan struct{}
	publications  chan brokerPublication
	subscriptions atomic.Uint64
	closeOnce     sync.Once
}

func newTestMQTTBroker(t *testing.T, address string) *testMQTTBroker {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("start test MQTT broker: %v", err)
	}
	broker := &testMQTTBroker{listener: listener, clients: make(map[*testMQTTClient]struct{}), done: make(chan struct{}), publications: make(chan brokerPublication, 16)}
	go broker.accept()
	return broker
}

func (b *testMQTTBroker) address() string { return b.listener.Addr().String() }

func (b *testMQTTBroker) accept() {
	defer close(b.done)
	for {
		connection, err := b.listener.Accept()
		if err != nil {
			return
		}
		client := &testMQTTClient{connection: connection}
		b.mu.Lock()
		b.clients[client] = struct{}{}
		b.mu.Unlock()
		go b.serve(client)
	}
}

func (b *testMQTTBroker) serve(client *testMQTTClient) {
	defer func() {
		_ = client.connection.Close()
		b.mu.Lock()
		delete(b.clients, client)
		b.mu.Unlock()
	}()
	reader := bufio.NewReader(client.connection)
	for {
		header, body, err := readMQTTPacket(reader)
		if err != nil {
			return
		}
		switch header >> 4 {
		case 1: // CONNECT
			if err := client.write(0x20, []byte{0, 0, 0}); err != nil {
				return
			}
		case 3: // PUBLISH
			topic, payload, packetID, err := parseClientPublish(header, body)
			if err != nil {
				return
			}
			select {
			case b.publications <- brokerPublication{topic: topic, payload: payload}:
			default:
			}
			if packetID != 0 {
				if err := client.write(0x40, []byte{byte(packetID >> 8), byte(packetID)}); err != nil {
					return
				}
			}
		case 8: // SUBSCRIBE
			packetID, topics, err := parseSubscribe(body)
			if err != nil {
				return
			}
			response := []byte{byte(packetID >> 8), byte(packetID), 0}
			for range topics {
				response = append(response, 1)
			}
			if err := client.write(0x90, response); err != nil {
				return
			}
			b.subscriptions.Add(1)
		case 12: // PINGREQ
			if err := client.write(0xd0, nil); err != nil {
				return
			}
		case 14: // DISCONNECT
			return
		}
	}
}

func (b *testMQTTBroker) publish(t *testing.T, topic string, payload any) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 2, 3+len(topic)+len(encoded))
	binary.BigEndian.PutUint16(body, uint16(len(topic)))
	body = append(body, topic...)
	body = append(body, 0) // MQTT 5 property length.
	body = append(body, encoded...)
	b.mu.Lock()
	clients := make([]*testMQTTClient, 0, len(b.clients))
	for client := range b.clients {
		clients = append(clients, client)
	}
	b.mu.Unlock()
	if len(clients) == 0 {
		t.Fatal("test MQTT broker has no connected client")
	}
	for _, client := range clients {
		if err := client.write(0x30, body); err != nil {
			t.Fatalf("publish test MQTT message: %v", err)
		}
	}
}

func (b *testMQTTBroker) close() {
	b.closeOnce.Do(func() {
		_ = b.listener.Close()
		b.mu.Lock()
		for client := range b.clients {
			_ = client.connection.Close()
		}
		b.mu.Unlock()
		<-b.done
	})
}

func readMQTTPacket(reader *bufio.Reader) (byte, []byte, error) {
	header, err := reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	length, err := readRemainingLength(reader)
	if err != nil {
		return 0, nil, err
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return 0, nil, err
	}
	return header, body, nil
}

func readRemainingLength(reader io.ByteReader) (int, error) {
	value, multiplier := 0, 1
	for count := 0; count < 4; count++ {
		current, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		value += int(current&127) * multiplier
		if current&128 == 0 {
			return value, nil
		}
		multiplier *= 128
	}
	return 0, errors.New("invalid MQTT remaining length")
}

func encodeRemainingLength(length int) []byte {
	result := make([]byte, 0, 4)
	for {
		current := byte(length % 128)
		length /= 128
		if length > 0 {
			current |= 128
		}
		result = append(result, current)
		if length == 0 {
			return result
		}
	}
}

func parseSubscribe(body []byte) (uint16, []string, error) {
	if len(body) < 4 {
		return 0, nil, errors.New("short MQTT subscribe")
	}
	packetID := binary.BigEndian.Uint16(body[:2])
	reader := bytes.NewReader(body[2:])
	propertyLength, err := readRemainingLength(reader)
	if err != nil || propertyLength > reader.Len() {
		return 0, nil, errors.New("invalid MQTT subscribe properties")
	}
	if _, err := reader.Seek(int64(propertyLength), io.SeekCurrent); err != nil {
		return 0, nil, err
	}
	topics := make([]string, 0)
	for reader.Len() > 0 {
		topic, err := readMQTTString(reader)
		if err != nil || reader.Len() < 1 {
			return 0, nil, errors.New("invalid MQTT subscription topic")
		}
		_, _ = reader.ReadByte()
		topics = append(topics, topic)
	}
	return packetID, topics, nil
}

func parseClientPublish(header byte, body []byte) (string, []byte, uint16, error) {
	reader := bytes.NewReader(body)
	topic, err := readMQTTString(reader)
	if err != nil {
		return "", nil, 0, err
	}
	var packetID uint16
	if qos := (header >> 1) & 3; qos > 0 {
		if reader.Len() < 2 {
			return "", nil, 0, errors.New("short MQTT publish packet id")
		}
		var id [2]byte
		_, _ = io.ReadFull(reader, id[:])
		packetID = binary.BigEndian.Uint16(id[:])
	}
	propertyLength, err := readRemainingLength(reader)
	if err != nil || propertyLength > reader.Len() {
		return "", nil, 0, errors.New("invalid MQTT publish properties")
	}
	if _, err := reader.Seek(int64(propertyLength), io.SeekCurrent); err != nil {
		return "", nil, 0, err
	}
	payload, err := io.ReadAll(reader)
	return topic, payload, packetID, err
}

func readMQTTString(reader *bytes.Reader) (string, error) {
	if reader.Len() < 2 {
		return "", io.ErrUnexpectedEOF
	}
	var size [2]byte
	_, _ = io.ReadFull(reader, size[:])
	length := int(binary.BigEndian.Uint16(size[:]))
	if length > reader.Len() {
		return "", io.ErrUnexpectedEOF
	}
	value := make([]byte, length)
	_, err := io.ReadFull(reader, value)
	return string(value), err
}

func waitUntil(t *testing.T, timeout time.Duration, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func TestPahoTransportRecoversAfterBrokerRestart(t *testing.T) {
	firstBroker := newTestMQTTBroker(t, "127.0.0.1:0")
	address := firstBroker.address()
	provider, err := newProviderFromConfig(providerconfig.Config{ID: "mqtt-reconnect", Name: "Reconnect", Config: json.RawMessage(fmt.Sprintf(`{"brokerUrl":"mqtt://%s","topicPrefix":"reconnect","qos":1,"connectTimeoutSeconds":2}`, address))}, func(config Config, brokerURL *url.URL, tlsConfig *tls.Config, handlers transportHandlers) mqttTransport {
		transport := newPahoTransport(config, brokerURL, tlsConfig, handlers).(*pahoTransport)
		transport.reconnectBackoff = autopaho.Backoff(func(int) time.Duration { return 20 * time.Millisecond })
		return transport
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := provider.Initialize(ctx); err != nil {
		firstBroker.close()
		t.Fatal(err)
	}
	defer func() {
		closeContext, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = provider.Close(closeContext)
	}()
	events := make(chan device.Device, 16)
	unsubscribe := provider.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()

	discovered := mqttSwitch("reconnect-switch", true, false)
	firstBroker.publish(t, discoveryTopic("reconnect", discovered.ID), discovered)
	if item := waitDevice(t, events); item.ID != discovered.ID {
		t.Fatalf("discovered item = %#v", item)
	}
	firstBroker.publish(t, availabilityTopic("reconnect", discovered.ID), availabilityMessage{SchemaVersion: 1, Availability: device.AvailabilityOnline, Sequence: 1, ObservedAt: time.Now().UTC()})
	if item := waitDevice(t, events); !item.IsOnline() {
		t.Fatalf("online item = %#v", item)
	}

	firstBroker.close()
	waitUntil(t, 3*time.Second, "provider disconnect", func() bool {
		_, err := provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: discovered.ID, EndpointID: "main", CapabilityID: "switch", PropertyID: "power"})
		return errors.Is(err, providersdk.ErrProviderUnavailable)
	})

	secondBroker := newTestMQTTBroker(t, address)
	t.Cleanup(secondBroker.close)
	waitUntil(t, 8*time.Second, "provider resubscription", func() bool { return secondBroker.subscriptions.Load() > 0 })
	secondBroker.publish(t, availabilityTopic("reconnect", discovered.ID), availabilityMessage{SchemaVersion: 1, Availability: device.AvailabilityOnline, Sequence: 2, ObservedAt: time.Now().UTC()})
	waitUntil(t, time.Second, "online event after reconnect", func() bool {
		select {
		case item := <-events:
			return item.ID == discovered.ID && item.IsOnline()
		default:
			return false
		}
	})

	if _, err := provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: discovered.ID, EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)}); err != nil {
		t.Fatalf("write after reconnect: %v", err)
	}
	select {
	case publication := <-secondBroker.publications:
		if publication.topic != commandTopic("reconnect", discovered.ID, "main", "switch", "power") {
			t.Fatalf("command topic after reconnect = %q", publication.topic)
		}
	case <-time.After(time.Second):
		t.Fatal("command was not published after reconnect")
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != discovered.ID {
		t.Fatalf("devices after reconnect = %#v, %v", items, err)
	}
}
