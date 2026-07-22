package xiaomi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/autopaho/queue/memory"
	"github.com/eclipse/paho.golang/paho"
)

const (
	cloudMIPSPort       = 8883
	cloudMIPSEventQueue = 512
)

type cloudMIPSMessageKind string

const (
	cloudMIPSProperty cloudMIPSMessageKind = "property"
	cloudMIPSEvent    cloudMIPSMessageKind = "event"
	cloudMIPSState    cloudMIPSMessageKind = "state"
)

type cloudMIPSMessage struct {
	Kind       cloudMIPSMessageKind
	DID        string
	SIID       int
	PIID       int
	EIID       int
	Value      any
	Arguments  []any
	Online     *bool
	ObservedAt time.Time
}

type cloudMIPSStats struct {
	Connected            bool
	MessagesReceived     uint64
	MessagesInvalid      uint64
	Reconnects           uint64
	SubscriptionFailures uint64
}

type cloudMIPSConnectionEvent struct {
	Connected   bool
	Reconnected bool
}

type cloudMIPSClient interface {
	Connect(context.Context, context.Context) error
	Close(context.Context) error
	ReplaceDevices(context.Context, []string) error
	UpdateOAuth(OAuthConfig)
	SetIncomingHandler(func(cloudMIPSMessage))
	SetConnectionHandler(func(cloudMIPSConnectionEvent))
	Stats() cloudMIPSStats
}

type mqttCloudMIPSClient struct {
	brokerURL *url.URL
	tlsConfig *tls.Config
	clientID  string
	username  string
	timeout   time.Duration

	accessToken atomic.Value
	connected   atomic.Bool
	received    atomic.Uint64
	invalid     atomic.Uint64
	reconnects  atomic.Uint64
	subFailures atomic.Uint64
	connections atomic.Uint64

	mu                sync.RWMutex
	manager           *autopaho.ConnectionManager
	dids              map[string]struct{}
	handler           func(cloudMIPSMessage)
	connectionHandler func(cloudMIPSConnectionEvent)
}

func newCloudMIPSClient(oauth OAuthConfig, timeout time.Duration) (*mqttCloudMIPSClient, error) {
	brokerURL, tlsConfig, clientID, username, token, err := cloudMIPSConnectionConfig(oauth)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = defaultTimeout * time.Second
	}
	client := &mqttCloudMIPSClient{
		brokerURL: brokerURL,
		tlsConfig: tlsConfig,
		clientID:  clientID,
		username:  username,
		timeout:   timeout,
		dids:      make(map[string]struct{}),
	}
	client.accessToken.Store(token)
	return client, nil
}

func cloudMIPSConnectionConfig(oauth OAuthConfig) (*url.URL, *tls.Config, string, string, string, error) {
	region := strings.ToLower(strings.TrimSpace(oauth.Region))
	validRegions := map[string]bool{"cn": true, "de": true, "i2": true, "ru": true, "sg": true, "tw": true, "us": true, "in": true}
	if !validRegions[region] {
		return nil, nil, "", "", "", fmt.Errorf("unsupported Xiaomi cloud region %q", region)
	}
	uuid := strings.TrimSpace(oauth.OAuthUUID)
	clientID := strings.TrimSpace(oauth.ClientID)
	token := strings.TrimSpace(oauth.AccessToken)
	if uuid == "" || clientID == "" || token == "" {
		return nil, nil, "", "", "", errors.New("Xiaomi cloud MQTT requires oauthUuid, clientId and accessToken")
	}
	if len(uuid) != 32 {
		return nil, nil, "", "", "", errors.New("Xiaomi cloud MQTT oauthUuid must contain 32 hexadecimal characters")
	}
	if _, err := hex.DecodeString(uuid); err != nil {
		return nil, nil, "", "", "", errors.New("Xiaomi cloud MQTT oauthUuid must contain 32 hexadecimal characters")
	}
	host := region + "-ha.mqtt.io.mi.com"
	brokerURL := &url.URL{Scheme: "tls", Host: host + ":" + strconv.Itoa(cloudMIPSPort)}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	return brokerURL, tlsConfig, "ha." + uuid, clientID, token, nil
}

func (c *mqttCloudMIPSClient) SetIncomingHandler(handler func(cloudMIPSMessage)) {
	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
}

func (c *mqttCloudMIPSClient) SetConnectionHandler(handler func(cloudMIPSConnectionEvent)) {
	c.mu.Lock()
	c.connectionHandler = handler
	c.mu.Unlock()
}

func (c *mqttCloudMIPSClient) Connect(lifecycle, initial context.Context) error {
	ready := make(chan error, 1)
	var initialResult sync.Once
	config := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{c.brokerURL},
		TlsCfg:                        c.tlsConfig,
		KeepAlive:                     60,
		CleanStartOnInitialConnection: true,
		SessionExpiryInterval:         0,
		ConnectTimeout:                c.timeout,
		Queue:                         memory.New(),
		ConnectUsername:               c.username,
		ConnectPassword:               []byte(c.currentAccessToken()),
		ClientConfig: paho.ClientConfig{
			ClientID:      c.clientID,
			PacketTimeout: c.timeout,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(received paho.PublishReceived) (bool, error) {
				if received.Packet != nil {
					c.onMessage(received.Packet.Topic, received.Packet.Payload)
				}
				return true, nil
			}},
		},
	}
	// The builder is invoked for every reconnect. Updating the token therefore
	// affects the next authentication exchange without replacing this client.
	config.ConnectPacketBuilder = func(packet *paho.Connect, _ *url.URL) (*paho.Connect, error) {
		packet.UsernameFlag = true
		packet.Username = c.username
		packet.PasswordFlag = true
		packet.Password = []byte(c.currentAccessToken())
		return packet, nil
	}
	config.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		connection := c.connections.Add(1)
		if connection > 1 {
			c.reconnects.Add(1)
		}
		c.connected.Store(true)
		go func() {
			err := c.subscribeAll(manager)
			if err != nil {
				c.subFailures.Add(1)
			}
			if err == nil {
				c.notifyConnection(cloudMIPSConnectionEvent{Connected: true, Reconnected: connection > 1})
			}
			initialResult.Do(func() { ready <- err })
		}()
	}
	config.OnConnectionDown = func() bool {
		c.connected.Store(false)
		c.notifyConnection(cloudMIPSConnectionEvent{Connected: false})
		return true
	}
	config.OnConnectError = func(err error) {
		c.connected.Store(false)
		initialResult.Do(func() { ready <- err })
	}
	manager, err := autopaho.NewConnection(lifecycle, config)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.manager = manager
	c.mu.Unlock()
	select {
	case err := <-ready:
		return err
	case <-time.After(c.timeout):
		return errors.New("Xiaomi cloud MQTT initial connection timed out")
	case <-initial.Done():
		return initial.Err()
	case <-lifecycle.Done():
		return lifecycle.Err()
	}
}

func (c *mqttCloudMIPSClient) notifyConnection(event cloudMIPSConnectionEvent) {
	c.mu.RLock()
	handler := c.connectionHandler
	c.mu.RUnlock()
	if handler != nil {
		handler(event)
	}
}

func (c *mqttCloudMIPSClient) Close(ctx context.Context) error {
	c.mu.Lock()
	manager := c.manager
	c.manager = nil
	c.mu.Unlock()
	c.connected.Store(false)
	if manager == nil {
		return nil
	}
	return manager.Disconnect(ctx)
}

func (c *mqttCloudMIPSClient) UpdateOAuth(oauth OAuthConfig) {
	if token := strings.TrimSpace(oauth.AccessToken); token != "" {
		c.accessToken.Store(token)
	}
}

func (c *mqttCloudMIPSClient) ReplaceDevices(ctx context.Context, dids []string) error {
	next := make(map[string]struct{}, len(dids))
	for _, did := range dids {
		if did = strings.TrimSpace(did); did != "" {
			next[did] = struct{}{}
		}
	}
	c.mu.Lock()
	previous := c.dids
	c.dids = next
	manager := c.manager
	connected := c.connected.Load()
	c.mu.Unlock()
	if manager == nil || !connected {
		return nil
	}
	added, removed := differenceDIDs(previous, next), differenceDIDs(next, previous)
	if err := c.subscribe(manager, added); err != nil {
		c.subFailures.Add(1)
		return err
	}
	if len(removed) > 0 {
		topics := cloudMIPSTopics(removed)
		if _, err := manager.Unsubscribe(ctx, &paho.Unsubscribe{Topics: topics}); err != nil {
			c.subFailures.Add(1)
			return fmt.Errorf("unsubscribe Xiaomi cloud MQTT topics: %w", err)
		}
	}
	return nil
}

func (c *mqttCloudMIPSClient) Stats() cloudMIPSStats {
	return cloudMIPSStats{
		Connected:            c.connected.Load(),
		MessagesReceived:     c.received.Load(),
		MessagesInvalid:      c.invalid.Load(),
		Reconnects:           c.reconnects.Load(),
		SubscriptionFailures: c.subFailures.Load(),
	}
}

func (c *mqttCloudMIPSClient) currentAccessToken() string {
	value := c.accessToken.Load()
	if value == nil {
		return ""
	}
	return value.(string)
}

func (c *mqttCloudMIPSClient) subscribeAll(manager *autopaho.ConnectionManager) error {
	c.mu.RLock()
	dids := make([]string, 0, len(c.dids))
	for did := range c.dids {
		dids = append(dids, did)
	}
	c.mu.RUnlock()
	return c.subscribe(manager, dids)
}

func (c *mqttCloudMIPSClient) subscribe(manager *autopaho.ConnectionManager, dids []string) error {
	if len(dids) == 0 {
		return nil
	}
	topics := cloudMIPSTopics(dids)
	subscriptions := make([]paho.SubscribeOptions, 0, len(topics))
	for _, topic := range topics {
		subscriptions = append(subscriptions, paho.SubscribeOptions{Topic: topic, QoS: 2})
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	ack, err := manager.Subscribe(ctx, &paho.Subscribe{Subscriptions: subscriptions})
	if err != nil {
		return fmt.Errorf("subscribe Xiaomi cloud MQTT topics: %w", err)
	}
	for _, reason := range ack.Reasons {
		if reason >= 0x80 {
			return fmt.Errorf("Xiaomi cloud MQTT subscription rejected: 0x%x", reason)
		}
	}
	return nil
}

func (c *mqttCloudMIPSClient) onMessage(topic string, payload []byte) {
	c.received.Add(1)
	message, err := parseCloudMIPSMessage(topic, payload)
	if err != nil {
		c.invalid.Add(1)
		return
	}
	c.mu.RLock()
	_, expected := c.dids[message.DID]
	handler := c.handler
	c.mu.RUnlock()
	if !expected || handler == nil {
		if !expected {
			c.invalid.Add(1)
		}
		return
	}
	handler(message)
}

func cloudMIPSTopics(dids []string) []string {
	unique := make(map[string]struct{}, len(dids))
	for _, did := range dids {
		if did = strings.TrimSpace(did); did != "" {
			unique[did] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(unique))
	for did := range unique {
		ordered = append(ordered, did)
	}
	sort.Strings(ordered)
	result := make([]string, 0, len(ordered)*3)
	for _, did := range ordered {
		result = append(result,
			"device/"+did+"/up/properties_changed/#",
			"device/"+did+"/up/event_occured/#",
		)
		// Xiaomi cloud does not publish reliable state messages for BLE and
		// proxy children, so subscribing would incorrectly imply availability.
		if !strings.HasPrefix(did, "blt.") && !strings.HasPrefix(did, "proxy.") {
			result = append(result, "device/"+did+"/state/#")
		}
	}
	return result
}

func differenceDIDs(existing, desired map[string]struct{}) []string {
	result := make([]string, 0)
	for did := range desired {
		if _, ok := existing[did]; !ok {
			result = append(result, did)
		}
	}
	sort.Strings(result)
	return result
}

func parseCloudMIPSMessage(topic string, payload []byte) (cloudMIPSMessage, error) {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) < 4 || parts[0] != "device" || strings.TrimSpace(parts[1]) == "" {
		return cloudMIPSMessage{}, errors.New("invalid Xiaomi cloud MQTT topic")
	}
	did := parts[1]
	switch {
	case len(parts) >= 5 && parts[2] == "up" && parts[3] == "properties_changed":
		var envelope struct {
			Timestamp json.Number `json:"timestamp"`
			Params    struct {
				DID       string          `json:"did"`
				SIID      int             `json:"siid"`
				PIID      int             `json:"piid"`
				Value     json.RawMessage `json:"value"`
				Timestamp json.Number     `json:"timestamp"`
			} `json:"params"`
		}
		if err := decodeCloudJSON(payload, &envelope); err != nil || envelope.Params.SIID <= 0 || envelope.Params.PIID <= 0 || len(envelope.Params.Value) == 0 {
			return cloudMIPSMessage{}, errors.New("invalid Xiaomi cloud property payload")
		}
		if envelope.Params.DID != "" && envelope.Params.DID != did {
			return cloudMIPSMessage{}, errors.New("Xiaomi cloud property DID does not match topic")
		}
		value, err := decodeCloudValue(envelope.Params.Value)
		if err != nil {
			return cloudMIPSMessage{}, fmt.Errorf("decode Xiaomi cloud property value: %w", err)
		}
		observedAt := cloudTimestamp(envelope.Params.Timestamp)
		if observedAt.IsZero() {
			observedAt = cloudTimestamp(envelope.Timestamp)
		}
		return cloudMIPSMessage{Kind: cloudMIPSProperty, DID: did, SIID: envelope.Params.SIID, PIID: envelope.Params.PIID, Value: value, ObservedAt: observedAt}, nil
	case len(parts) >= 5 && parts[2] == "up" && parts[3] == "event_occured":
		var envelope struct {
			Timestamp json.Number `json:"timestamp"`
			Params    struct {
				DID       string          `json:"did"`
				SIID      int             `json:"siid"`
				EIID      int             `json:"eiid"`
				Arguments json.RawMessage `json:"arguments"`
				Timestamp json.Number     `json:"timestamp"`
			} `json:"params"`
		}
		if err := decodeCloudJSON(payload, &envelope); err != nil || envelope.Params.SIID <= 0 || envelope.Params.EIID <= 0 || len(envelope.Params.Arguments) == 0 {
			return cloudMIPSMessage{}, errors.New("invalid Xiaomi cloud event payload")
		}
		if envelope.Params.DID != "" && envelope.Params.DID != did {
			return cloudMIPSMessage{}, errors.New("Xiaomi cloud event DID does not match topic")
		}
		var arguments []any
		if err := decodeCloudJSON(envelope.Params.Arguments, &arguments); err != nil {
			return cloudMIPSMessage{}, errors.New("invalid Xiaomi cloud event arguments")
		}
		observedAt := cloudTimestamp(envelope.Params.Timestamp)
		if observedAt.IsZero() {
			observedAt = cloudTimestamp(envelope.Timestamp)
		}
		return cloudMIPSMessage{Kind: cloudMIPSEvent, DID: did, SIID: envelope.Params.SIID, EIID: envelope.Params.EIID, Arguments: arguments, ObservedAt: observedAt}, nil
	case parts[2] == "state":
		var state struct {
			DID       string      `json:"device_id"`
			Event     string      `json:"event"`
			Timestamp json.Number `json:"timestamp"`
		}
		if err := decodeCloudJSON(payload, &state); err != nil || state.DID != did {
			return cloudMIPSMessage{}, errors.New("invalid Xiaomi cloud state payload")
		}
		online := state.Event == "online"
		if !online && state.Event != "offline" {
			return cloudMIPSMessage{}, errors.New("invalid Xiaomi cloud state event")
		}
		return cloudMIPSMessage{Kind: cloudMIPSState, DID: did, Online: &online, ObservedAt: cloudTimestamp(state.Timestamp)}, nil
	default:
		return cloudMIPSMessage{}, errors.New("unsupported Xiaomi cloud MQTT topic")
	}
}

func decodeCloudJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func decodeCloudValue(raw json.RawMessage) (any, error) {
	var value any
	if err := decodeCloudJSON(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func cloudTimestamp(value json.Number) time.Time {
	if value == "" {
		return time.Time{}
	}
	number, err := value.Int64()
	if err != nil || number <= 0 {
		return time.Time{}
	}
	if number > 10_000_000_000 {
		return time.UnixMilli(number).UTC()
	}
	return time.Unix(number, 0).UTC()
}
