package mqtt

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/autopaho/queue/memory"
	"github.com/eclipse/paho.golang/paho"
)

type transportHandlers struct {
	onMessage        func(inboundMessage)
	onConnectionUp   func()
	onConnectionDown func()
	onError          func(error)
}

type mqttTransport interface {
	Start(context.Context, context.Context, time.Duration) error
	Publish(context.Context, string, byte, bool, []byte) error
	Close(context.Context) error
}

type transportFactory func(Config, *url.URL, *tls.Config, transportHandlers) mqttTransport

type pahoTransport struct {
	config           Config
	brokerURL        *url.URL
	tlsConfig        *tls.Config
	handlers         transportHandlers
	reconnectBackoff autopaho.Backoff
	mu               sync.RWMutex
	manager          *autopaho.ConnectionManager
}

func newPahoTransport(config Config, brokerURL *url.URL, tlsConfig *tls.Config, handlers transportHandlers) mqttTransport {
	return &pahoTransport{config: config, brokerURL: brokerURL, tlsConfig: tlsConfig, handlers: handlers, reconnectBackoff: autopaho.DefaultExponentialBackoff()}
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
	subscriptions := make([]paho.SubscribeOptions, 0, len(subscriptionTopics(t.config.TopicPrefix)))
	for _, topic := range subscriptionTopics(t.config.TopicPrefix) {
		subscriptions = append(subscriptions, paho.SubscribeOptions{Topic: topic, QoS: t.config.QoS, RetainAsPublished: true})
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
