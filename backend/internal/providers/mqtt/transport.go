package mqtt

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/autopaho/queue/memory"
	"github.com/eclipse/paho.golang/paho"
	"github.com/feranydev/homeloom/backend/internal/platform/logging"
	mochimqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
	"go.uber.org/zap"
)

type transportHandlers struct {
	onMessage        func(inboundMessage)
	onConnectionUp   func()
	onConnectionDown func()
	onError          func(error)
}

type mqttTransport interface {
	Start(context.Context, context.Context, time.Duration) error
	ReplaceSubscriptions(context.Context, []mqttSubscription) error
	Publish(context.Context, string, byte, bool, []byte) error
	Close(context.Context) error
}

type transportFactory func(Config, *url.URL, *tls.Config, transportHandlers) mqttTransport

func newMQTTTransport(config Config, brokerURL *url.URL, tlsConfig *tls.Config, handlers transportHandlers) mqttTransport {
	if config.Mode == ModeServer {
		return newBrokerTransport(config, tlsConfig, handlers)
	}
	return newPahoTransport(config, brokerURL, tlsConfig, handlers)
}

type pahoTransport struct {
	config           Config
	brokerURL        *url.URL
	tlsConfig        *tls.Config
	handlers         transportHandlers
	reconnectBackoff autopaho.Backoff
	mu               sync.RWMutex
	manager          *autopaho.ConnectionManager
	subscriptions    []mqttSubscription
}

func newPahoTransport(config Config, brokerURL *url.URL, tlsConfig *tls.Config, handlers transportHandlers) mqttTransport {
	return &pahoTransport{config: config, brokerURL: brokerURL, tlsConfig: tlsConfig, handlers: handlers, reconnectBackoff: autopaho.DefaultExponentialBackoff(), subscriptions: configuredSubscriptions(config.Devices)}
}

func (t *pahoTransport) Start(lifecycle, initialContext context.Context, timeout time.Duration) error {
	ready := make(chan error, 1)
	var initialSubscription atomic.Bool
	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{t.brokerURL},
		TlsCfg:                        t.tlsConfig,
		KeepAlive:                     uint16(t.config.KeepAliveSeconds),
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         t.config.SessionExpirySeconds,
		ReconnectBackoff:              t.reconnectBackoff,
		ConnectTimeout:                timeout,
		Queue:                         memory.New(),
		ConnectUsername:               t.config.Username,
		ConnectPassword:               []byte(t.config.Password),
		ClientConfig: paho.ClientConfig{
			ClientID:      t.config.ClientID,
			PacketTimeout: timeout,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(received paho.PublishReceived) (bool, error) {
				if received.Packet == nil {
					return false, nil
				}
				t.handlers.onMessage(inboundMessage{topic: received.Packet.Topic, payload: append([]byte(nil), received.Packet.Payload...), retained: received.Packet.Retain})
				return true, nil
			}},
		},
	}
	clientConfig.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		go func() {
			for {
				err := t.subscribe(manager, timeout)
				if err == nil {
					if t.handlers.onConnectionUp != nil {
						t.handlers.onConnectionUp()
					}
					if initialSubscription.CompareAndSwap(false, true) {
						ready <- nil
					}
					return
				}
				if t.handlers.onError != nil {
					t.handlers.onError(err)
				}
				if initialSubscription.CompareAndSwap(false, true) {
					ready <- err
					return
				}
				timer := time.NewTimer(time.Second)
				select {
				case <-timer.C:
				case <-lifecycle.Done():
					timer.Stop()
					return
				}
			}
		}()
	}
	clientConfig.OnConnectionDown = func() bool {
		if t.handlers.onConnectionDown != nil {
			t.handlers.onConnectionDown()
		}
		return true
	}
	clientConfig.OnConnectError = func(err error) {
		if t.handlers.onError != nil {
			t.handlers.onError(err)
		}
	}
	manager, err := autopaho.NewConnection(lifecycle, clientConfig)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.manager = manager
	t.mu.Unlock()
	waitContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case err := <-ready:
		return err
	case <-waitContext.Done():
		return fmt.Errorf("mqtt initial connection and subscription: %w", waitContext.Err())
	case <-initialContext.Done():
		return initialContext.Err()
	case <-lifecycle.Done():
		return lifecycle.Err()
	}
}

func (t *pahoTransport) subscribe(manager *autopaho.ConnectionManager, timeout time.Duration) error {
	t.mu.RLock()
	desired := append([]mqttSubscription(nil), t.subscriptions...)
	t.mu.RUnlock()
	if len(desired) == 0 {
		return nil
	}
	subscriptions := make([]paho.SubscribeOptions, 0, len(desired))
	for _, item := range desired {
		subscriptions = append(subscriptions, paho.SubscribeOptions{Topic: item.Topic, QoS: item.QoS, RetainAsPublished: true})
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ack, err := manager.Subscribe(ctx, &paho.Subscribe{Subscriptions: subscriptions})
	if err != nil {
		return fmt.Errorf("subscribe mqtt topics: %w", err)
	}
	if len(ack.Reasons) != len(subscriptions) {
		return fmt.Errorf("subscribe mqtt topics: broker returned %d acknowledgements for %d topics", len(ack.Reasons), len(subscriptions))
	}
	for index, reason := range ack.Reasons {
		if reason >= 0x80 {
			return fmt.Errorf("subscribe mqtt topic %q rejected with reason 0x%x", subscriptions[index].Topic, reason)
		}
	}
	return nil
}

func (t *pahoTransport) ReplaceSubscriptions(ctx context.Context, desired []mqttSubscription) error {
	t.mu.RLock()
	manager := t.manager
	current := append([]mqttSubscription(nil), t.subscriptions...)
	t.mu.RUnlock()
	if manager == nil {
		t.mu.Lock()
		t.subscriptions = append([]mqttSubscription(nil), desired...)
		t.mu.Unlock()
		return nil
	}
	currentByTopic := make(map[string]byte, len(current))
	for _, item := range current {
		currentByTopic[item.Topic] = item.QoS
	}
	desiredByTopic := make(map[string]byte, len(desired))
	toSubscribe := make([]paho.SubscribeOptions, 0)
	for _, item := range desired {
		desiredByTopic[item.Topic] = item.QoS
		if qos, exists := currentByTopic[item.Topic]; !exists || qos != item.QoS {
			toSubscribe = append(toSubscribe, paho.SubscribeOptions{Topic: item.Topic, QoS: item.QoS, RetainAsPublished: true})
		}
	}
	if len(toSubscribe) > 0 {
		ack, err := manager.Subscribe(ctx, &paho.Subscribe{Subscriptions: toSubscribe})
		if err != nil {
			return fmt.Errorf("subscribe updated mqtt device topics: %w", err)
		}
		if len(ack.Reasons) != len(toSubscribe) {
			return fmt.Errorf("subscribe updated mqtt device topics: broker returned %d acknowledgements for %d topics", len(ack.Reasons), len(toSubscribe))
		}
		for index, reason := range ack.Reasons {
			if reason >= 0x80 {
				return fmt.Errorf("subscribe updated mqtt topic %q rejected with reason 0x%x", toSubscribe[index].Topic, reason)
			}
		}
	}
	toUnsubscribe := make([]string, 0)
	for _, item := range current {
		if _, exists := desiredByTopic[item.Topic]; !exists {
			toUnsubscribe = append(toUnsubscribe, item.Topic)
		}
	}
	if len(toUnsubscribe) > 0 {
		ack, err := manager.Unsubscribe(ctx, &paho.Unsubscribe{Topics: toUnsubscribe})
		if err != nil {
			return fmt.Errorf("unsubscribe removed mqtt device topics: %w", err)
		}
		if len(ack.Reasons) != len(toUnsubscribe) {
			return fmt.Errorf("unsubscribe removed mqtt device topics: broker returned %d acknowledgements for %d topics", len(ack.Reasons), len(toUnsubscribe))
		}
		for index, reason := range ack.Reasons {
			if reason >= 0x80 {
				return fmt.Errorf("unsubscribe mqtt topic %q rejected with reason 0x%x", toUnsubscribe[index], reason)
			}
		}
	}
	t.mu.Lock()
	t.subscriptions = append([]mqttSubscription(nil), desired...)
	t.mu.Unlock()
	return nil
}

func (t *pahoTransport) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error {
	t.mu.RLock()
	manager := t.manager
	t.mu.RUnlock()
	if manager == nil {
		return fmt.Errorf("mqtt transport is not started")
	}
	payloadFormat := byte(1)
	_, err := manager.Publish(ctx, &paho.Publish{Topic: topic, QoS: qos, Retain: retained, Payload: append([]byte(nil), payload...), Properties: &paho.PublishProperties{ContentType: "application/json", PayloadFormat: &payloadFormat}})
	if err != nil {
		return fmt.Errorf("publish mqtt topic %q: %w", topic, err)
	}
	return nil
}

func (t *pahoTransport) Close(ctx context.Context) error {
	t.mu.Lock()
	manager := t.manager
	t.manager = nil
	t.mu.Unlock()
	if manager == nil {
		return nil
	}
	return manager.Disconnect(ctx)
}

type brokerTransport struct {
	config        Config
	tlsConfig     *tls.Config
	handlers      transportHandlers
	mu            sync.Mutex
	server        *mochimqtt.Server
	ledger        *auth.Ledger
	subscriptions []mqttSubscription
}

func newBrokerTransport(config Config, tlsConfig *tls.Config, handlers transportHandlers) mqttTransport {
	return &brokerTransport{config: config, tlsConfig: tlsConfig, handlers: handlers, subscriptions: configuredSubscriptions(config.Devices)}
}

func (t *brokerTransport) Start(lifecycle, initialContext context.Context, _ time.Duration) error {
	select {
	case <-initialContext.Done():
		return initialContext.Err()
	default:
	}
	logger := zap.L().With(zap.String("module", "mqtt-broker"))
	server := mochimqtt.New(&mochimqtt.Options{InlineClient: true, Logger: logging.SlogAdapter(logger)})
	ledger := brokerAuthLedger(t.config, t.subscriptions)
	if err := server.AddHook(new(auth.Hook), &auth.Options{Ledger: ledger}); err != nil {
		return fmt.Errorf("configure embedded mqtt authentication: %w", err)
	}
	listener := listeners.NewTCP(listeners.Config{ID: "homeloom-mqtt", Address: t.config.ListenAddress, TLSConfig: t.tlsConfig})
	if err := server.AddListener(listener); err != nil {
		return fmt.Errorf("listen for mqtt devices on %q: %w", t.config.ListenAddress, err)
	}
	if err := subscribeBroker(server, t.subscriptions, t.handlers); err != nil {
		_ = server.Close()
		return err
	}
	if err := server.Serve(); err != nil {
		_ = server.Close()
		return fmt.Errorf("start embedded mqtt broker: %w", err)
	}
	t.mu.Lock()
	t.server, t.ledger = server, ledger
	t.mu.Unlock()
	if t.handlers.onConnectionUp != nil {
		t.handlers.onConnectionUp()
	}
	go func() {
		<-lifecycle.Done()
		t.mu.Lock()
		current := t.server
		t.server = nil
		t.mu.Unlock()
		if current != nil {
			_ = current.Close()
		}
		if t.handlers.onConnectionDown != nil {
			t.handlers.onConnectionDown()
		}
	}()
	return nil
}

func (t *brokerTransport) ReplaceSubscriptions(_ context.Context, desired []mqttSubscription) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.server == nil {
		return fmt.Errorf("mqtt broker transport is not started")
	}
	current := append([]mqttSubscription(nil), t.subscriptions...)
	for index, item := range uniqueSubscriptions(current) {
		if err := t.server.Unsubscribe(item.Topic, index+1); err != nil {
			return fmt.Errorf("unsubscribe embedded mqtt topic %q: %w", item.Topic, err)
		}
	}
	if err := subscribeBroker(t.server, desired, t.handlers); err != nil {
		_ = subscribeBroker(t.server, current, t.handlers)
		return err
	}
	t.ledger.Update(brokerAuthLedger(t.config, desired))
	t.subscriptions = append([]mqttSubscription(nil), desired...)
	return nil
}

func (t *brokerTransport) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	t.mu.Lock()
	server := t.server
	t.mu.Unlock()
	if server == nil {
		return fmt.Errorf("mqtt broker transport is not started")
	}
	if err := server.Publish(topic, append([]byte(nil), payload...), retained, qos); err != nil {
		return fmt.Errorf("publish embedded mqtt topic %q: %w", topic, err)
	}
	return nil
}

func (t *brokerTransport) Close(context.Context) error {
	t.mu.Lock()
	server := t.server
	t.server = nil
	t.mu.Unlock()
	if server == nil {
		return nil
	}
	return server.Close()
}

func uniqueSubscriptions(items []mqttSubscription) []mqttSubscription {
	byTopic := make(map[string]mqttSubscription, len(items))
	for _, item := range items {
		if current, exists := byTopic[item.Topic]; !exists || item.QoS > current.QoS {
			byTopic[item.Topic] = item
		}
	}
	result := make([]mqttSubscription, 0, len(byTopic))
	for _, item := range byTopic {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Topic < result[j].Topic })
	return result
}

func subscribeBroker(server *mochimqtt.Server, items []mqttSubscription, handlers transportHandlers) error {
	for index, item := range uniqueSubscriptions(items) {
		subscription := item
		// Mochi invokes retained replay handlers synchronously inside Subscribe,
		// then invokes the same inline handler with the publisher's original
		// packet for later live traffic. Unlike a normal MQTT client delivery,
		// that live packet still carries the publisher's RETAIN bit. Restrict the
		// retained marker to the synchronous replay phase so server mode has the
		// same semantics as client mode: retained means historical subscription
		// replay, not merely that the publisher asked the broker to store it.
		var replayingRetained atomic.Bool
		replayingRetained.Store(true)
		err := server.Subscribe(subscription.Topic, index+1, func(_ *mochimqtt.Client, _ packets.Subscription, packet packets.Packet) {
			if handlers.onMessage != nil {
				handlers.onMessage(inboundMessage{topic: packet.TopicName, payload: append([]byte(nil), packet.Payload...), retained: packet.FixedHeader.Retain && replayingRetained.Load()})
			}
		})
		replayingRetained.Store(false)
		if err != nil {
			return fmt.Errorf("subscribe embedded mqtt topic %q: %w", subscription.Topic, err)
		}
	}
	return nil
}

func brokerAuthLedger(config Config, subscriptions []mqttSubscription) *auth.Ledger {
	username := auth.RString(config.Username)
	rules := make(auth.ACLRules, 0, len(subscriptions)+len(config.Devices)+1)
	for _, item := range uniqueSubscriptions(subscriptions) {
		rules = append(rules, auth.ACLRule{Username: username, Filters: auth.Filters{auth.RString(item.Topic): auth.WriteOnly}})
	}
	for _, item := range config.Devices {
		rules = append(rules, auth.ACLRule{Username: username, Filters: auth.Filters{auth.RString(topicSubscription(item.Topics.Command)): auth.ReadOnly}})
	}
	rules = append(rules, auth.ACLRule{Username: username, Filters: auth.Filters{"#": auth.Deny}})
	return &auth.Ledger{
		Auth: auth.AuthRules{{Username: username, Password: auth.RString(config.Password), Allow: true}},
		ACL:  rules,
	}
}
