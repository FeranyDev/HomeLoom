package xiaomi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/autopaho/queue/memory"
	"github.com/eclipse/paho.golang/paho"
)

const (
	topicDeviceList       = "master/proxy/getDevList"
	topicGetProperty      = "master/proxy/get"
	topicRPCRequest       = "master/proxy/rpcReq"
	topicDeviceListChange = "master/appMsg/devListChange"
)

type hubIncoming struct {
	Topic   string
	Payload json.RawMessage
}

type hubReply struct {
	payload json.RawMessage
	err     error
}

type hubClient interface {
	Connect(context.Context, context.Context) error
	Close(context.Context) error
	DeviceList(context.Context) (json.RawMessage, error)
	GetProperty(context.Context, string, int, int) (json.RawMessage, error)
	SetProperty(context.Context, string, int, int, any) (json.RawMessage, error)
	Action(context.Context, string, int, int, []any) (json.RawMessage, error)
	SetIncomingHandler(func(hubIncoming))
}

type mipsClient struct {
	config    Config
	brokerURL *url.URL
	tlsConfig *tls.Config
	nextID    atomic.Uint32

	managerMu sync.RWMutex
	manager   *autopaho.ConnectionManager
	pendingMu sync.Mutex
	pending   map[uint32]chan hubReply
	handlerMu sync.RWMutex
	handler   func(hubIncoming)
}

func newMIPSClient(config Config, brokerURL *url.URL, tlsConfig *tls.Config) *mipsClient {
	client := &mipsClient{config: config, brokerURL: brokerURL, tlsConfig: tlsConfig, pending: make(map[uint32]chan hubReply)}
	client.nextID.Store(rand.Uint32() | 1)
	return client
}

func (c *mipsClient) SetIncomingHandler(handler func(hubIncoming)) {
	c.handlerMu.Lock()
	c.handler = handler
	c.handlerMu.Unlock()
}

func (c *mipsClient) Connect(lifecycle, initial context.Context) error {
	ready := make(chan error, 1)
	var connected atomic.Bool
	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{c.brokerURL},
		TlsCfg:                        c.tlsConfig,
		KeepAlive:                     60,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         86400,
		ConnectTimeout:                c.config.requestTimeout(),
		Queue:                         memory.New(),
		ClientConfig: paho.ClientConfig{
			ClientID:      c.config.ClientID,
			PacketTimeout: c.config.requestTimeout(),
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(received paho.PublishReceived) (bool, error) {
				if received.Packet != nil {
					c.onMessage(received.Packet.Topic, received.Packet.Payload)
				}
				return true, nil
			}},
		},
	}
	clientConfig.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), c.config.requestTimeout())
			defer cancel()
			ack, err := manager.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{
				{Topic: c.config.ClientID + "/#", QoS: 2},
				{Topic: topicDeviceListChange, QoS: 2},
				{Topic: "master/appMsg/notify/iot/+/property/#", QoS: 2},
				{Topic: "master/appMsg/notify/iot/+/event/#", QoS: 2},
			}})
			if err == nil {
				for _, reason := range ack.Reasons {
					if reason >= 0x80 {
						err = fmt.Errorf("Xiaomi MQTT subscription rejected: 0x%x", reason)
						break
					}
				}
			}
			if connected.CompareAndSwap(false, true) {
				ready <- err
			}
		}()
	}
	clientConfig.OnConnectionDown = func() bool {
		c.failPending(errors.New("Xiaomi gateway connection closed"))
		return true
	}
	clientConfig.OnConnectError = func(err error) {
		if connected.CompareAndSwap(false, true) {
			ready <- err
		}
	}
	manager, err := autopaho.NewConnection(lifecycle, clientConfig)
	if err != nil {
		return err
	}
	c.managerMu.Lock()
	c.manager = manager
	c.managerMu.Unlock()
	select {
	case err := <-ready:
		return err
	case <-time.After(c.config.requestTimeout()):
		return errors.New("Xiaomi gateway initial connection timed out")
	case <-initial.Done():
		return initial.Err()
	case <-lifecycle.Done():
		return lifecycle.Err()
	}
}

func (c *mipsClient) Close(ctx context.Context) error {
	c.managerMu.Lock()
	manager := c.manager
	c.manager = nil
	c.managerMu.Unlock()
	c.failPending(errors.New("Xiaomi client closed"))
	if manager == nil {
		return nil
	}
	return manager.Disconnect(ctx)
}

func (c *mipsClient) onMessage(topic string, payload []byte) {
	decoded, err := decodeMIPS(payload)
	if err == nil {
		if json.Valid([]byte(decoded.Payload)) && c.resolve(decoded.ID, json.RawMessage(decoded.Payload)) {
			return
		}
		payload = []byte(decoded.Payload)
	}
	if !json.Valid(payload) {
		return
	}
	c.handlerMu.RLock()
	handler := c.handler
	c.handlerMu.RUnlock()
	if handler != nil {
		handler(hubIncoming{Topic: topic, Payload: append(json.RawMessage(nil), payload...)})
	}
}

func (c *mipsClient) resolve(id uint32, payload json.RawMessage) bool {
	c.pendingMu.Lock()
	channel, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
	if ok {
		select {
		case channel <- hubReply{payload: append(json.RawMessage(nil), payload...)}:
		default:
		}
	}
	return ok
}

func (c *mipsClient) failPending(err error) {
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = make(map[uint32]chan hubReply)
	c.pendingMu.Unlock()
	for _, channel := range pending {
		select {
		case channel <- hubReply{err: err}:
		default:
		}
	}
}

func (c *mipsClient) request(ctx context.Context, topic string, payload any) (json.RawMessage, error) {
	c.managerMu.RLock()
	manager := c.manager
	c.managerMu.RUnlock()
	if manager == nil {
		return nil, errors.New("Xiaomi gateway is not connected")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	id := c.nextID.Add(1)
	if id == 0 {
		id = c.nextID.Add(1)
	}
	frame, err := encodeMIPS(mipsMessage{ID: id, From: "local", ReplyTopic: c.config.ClientID + "/reply", Payload: string(body)})
	if err != nil {
		return nil, err
	}
	channel := make(chan hubReply, 1)
	c.pendingMu.Lock()
	c.pending[id] = channel
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()
	if _, err := manager.Publish(ctx, &paho.Publish{Topic: topic, QoS: 2, Payload: frame}); err != nil {
		return nil, fmt.Errorf("publish Xiaomi request: %w", err)
	}
	select {
	case reply := <-channel:
		return reply.payload, reply.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *mipsClient) DeviceList(ctx context.Context) (json.RawMessage, error) {
	return c.request(ctx, topicDeviceList, map[string]any{})
}

func (c *mipsClient) GetProperty(ctx context.Context, did string, siid, piid int) (json.RawMessage, error) {
	return c.request(ctx, topicGetProperty, map[string]any{"did": did, "siid": siid, "piid": piid})
}

func (c *mipsClient) SetProperty(ctx context.Context, did string, siid, piid int, value any) (json.RawMessage, error) {
	return c.request(ctx, topicRPCRequest, map[string]any{"did": did, "rpc": map[string]any{"id": c.nextID.Add(1), "method": "set_properties", "params": []map[string]any{{"did": did, "siid": siid, "piid": piid, "value": value}}}})
}

func (c *mipsClient) Action(ctx context.Context, did string, siid, aiid int, input []any) (json.RawMessage, error) {
	return c.request(ctx, topicRPCRequest, map[string]any{"did": did, "rpc": map[string]any{"id": c.nextID.Add(1), "method": "action", "params": map[string]any{"did": did, "siid": siid, "aiid": aiid, "in": input}}})
}
