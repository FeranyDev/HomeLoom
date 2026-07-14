package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/feranydev/xiaomi-central-hub-client/internal/mips"
	"github.com/feranydev/xiaomi-central-hub-client/internal/mqtt5"
)

const (
	topicDeviceList       = "master/proxy/getDevList"
	topicGetProperty      = "master/proxy/get"
	topicRPCRequest       = "master/proxy/rpcReq"
	topicDeviceListChange = "master/appMsg/devListChange"
)

// Incoming represents an unsolicited property/event/device-list message.
type Incoming struct {
	Topic   string          `json:"topic"`
	ID      uint32          `json:"id,omitempty"`
	From    string          `json:"from,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

type pendingReply struct {
	message mips.Message
	err     error
}

// Client is a local MIoT Pub/Sub client for a central hub.
type Client struct {
	cfg  Config
	mqtt *mqtt5.Client

	nextID atomic.Uint32

	pendingMu sync.Mutex
	pending   map[uint32]chan pendingReply

	handlerMu sync.RWMutex
	handler   func(Incoming)
}

func New(cfg Config) *Client {
	client := &Client{
		cfg:     cfg,
		pending: make(map[uint32]chan pendingReply),
	}
	client.nextID.Store(rand.Uint32() | 1)
	return client
}

// SetIncomingHandler registers a callback for property/event broadcasts.
func (c *Client) SetIncomingHandler(handler func(Incoming)) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()
	c.handler = handler
}

// Connect establishes an MQTT v5 connection over mutual TLS.
func (c *Client) Connect(ctx context.Context) error {
	if c.mqtt != nil {
		return errors.New("client is already connected")
	}
	tlsConfig, err := c.cfg.TLSConfig()
	if err != nil {
		return err
	}
	address := net.JoinHostPort(c.cfg.Host, fmt.Sprintf("%d", c.cfg.Port))
	mqttClient, err := mqtt5.Dial(ctx, address, c.cfg.ClientID, tlsConfig, 60, c.onMessage)
	if err != nil {
		return err
	}
	c.mqtt = mqttClient

	subCtx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout())
	defer cancel()
	if err := c.mqtt.Subscribe(subCtx, c.cfg.ClientID+"/#", topicDeviceListChange); err != nil {
		_ = c.mqtt.Close()
		c.mqtt = nil
		return fmt.Errorf("base MQTT subscription: %w", err)
	}
	return nil
}

func (c *Client) Close(_ context.Context) error {
	if c.mqtt == nil {
		return nil
	}
	err := c.mqtt.Close()
	c.mqtt = nil
	c.failAllPending(errors.New("client closed"))
	return err
}

func (c *Client) onMessage(packet mqtt5.Message) {
	decoded, decodeErr := mips.Decode(packet.Payload)
	if decodeErr == nil {
		if c.resolvePending(decoded) {
			return
		}
		c.dispatch(packet.Topic, decoded.ID, decoded.From, []byte(decoded.Payload))
		return
	}

	// Some notification topics can carry plain JSON. Preserve them instead of
	// dropping the message merely because it lacks a MIPS envelope.
	if json.Valid(packet.Payload) {
		c.dispatch(packet.Topic, 0, "", packet.Payload)
	}
}

func (c *Client) resolvePending(msg mips.Message) bool {
	c.pendingMu.Lock()
	ch, ok := c.pending[msg.ID]
	if ok {
		delete(c.pending, msg.ID)
	}
	c.pendingMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- pendingReply{message: msg}:
	default:
	}
	return true
}

func (c *Client) dispatch(topic string, id uint32, from string, payload []byte) {
	c.handlerMu.RLock()
	handler := c.handler
	c.handlerMu.RUnlock()
	if handler == nil {
		return
	}
	copyPayload := append(json.RawMessage(nil), payload...)
	handler(Incoming{Topic: topic, ID: id, From: from, Payload: copyPayload})
}

func (c *Client) failAllPending(err error) {
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = make(map[uint32]chan pendingReply)
	c.pendingMu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- pendingReply{err: err}:
		default:
		}
	}
}

func (c *Client) nextMessageID() uint32 {
	for {
		id := c.nextID.Add(1)
		if id != 0 {
			return id
		}
	}
}

func (c *Client) request(ctx context.Context, topic string, payload any) (json.RawMessage, error) {
	if c.mqtt == nil {
		return nil, errors.New("client is not connected")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode request JSON: %w", err)
	}
	id := c.nextMessageID()
	frame, err := mips.Encode(mips.Message{
		ID:         id,
		From:       "local",
		ReplyTopic: c.cfg.ClientID + "/reply",
		Payload:    string(body),
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan pendingReply, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.mqtt.Publish(ctx, topic, frame, 2); err != nil {
		return nil, fmt.Errorf("publish %s: %w", topic, err)
	}

	select {
	case reply := <-ch:
		if reply.err != nil {
			return nil, reply.err
		}
		if !json.Valid([]byte(reply.message.Payload)) {
			return nil, fmt.Errorf("gateway reply is not JSON: %q", reply.message.Payload)
		}
		return json.RawMessage(reply.message.Payload), nil
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for gateway reply: %w", ctx.Err())
	case <-c.mqtt.Done():
		return nil, errors.New("MQTT connection closed while waiting for reply")
	}
}

func (c *Client) DeviceList(ctx context.Context) (json.RawMessage, error) {
	return c.request(ctx, topicDeviceList, map[string]any{})
}

func (c *Client) GetProperty(ctx context.Context, did string, siid, piid int) (json.RawMessage, error) {
	if err := validateProperty(did, siid, piid); err != nil {
		return nil, err
	}
	return c.request(ctx, topicGetProperty, map[string]any{
		"did":  did,
		"siid": siid,
		"piid": piid,
	})
}

func (c *Client) SetProperty(ctx context.Context, did string, siid, piid int, value any) (json.RawMessage, error) {
	if err := validateProperty(did, siid, piid); err != nil {
		return nil, err
	}
	rpcID := c.nextMessageID()
	return c.request(ctx, topicRPCRequest, map[string]any{
		"did": did,
		"rpc": map[string]any{
			"id":     rpcID,
			"method": "set_properties",
			"params": []map[string]any{{
				"did":   did,
				"siid":  siid,
				"piid":  piid,
				"value": value,
			}},
		},
	})
}

func (c *Client) Action(ctx context.Context, did string, siid, aiid int, input []any) (json.RawMessage, error) {
	if strings.TrimSpace(did) == "" || siid <= 0 || aiid <= 0 {
		return nil, errors.New("did must be non-empty and siid/aiid must be positive")
	}
	rpcID := c.nextMessageID()
	return c.request(ctx, topicRPCRequest, map[string]any{
		"did": did,
		"rpc": map[string]any{
			"id":     rpcID,
			"method": "action",
			"params": map[string]any{
				"did":  did,
				"siid": siid,
				"aiid": aiid,
				"in":   input,
			},
		},
	})
}

func validateProperty(did string, siid, piid int) error {
	if strings.TrimSpace(did) == "" || siid <= 0 || piid <= 0 {
		return errors.New("did must be non-empty and siid/piid must be positive")
	}
	return nil
}

func (c *Client) SubscribeProperties(ctx context.Context, did string, siid, piid *int) error {
	if strings.TrimSpace(did) == "" {
		return errors.New("did must not be empty")
	}
	suffix := "#"
	if siid != nil && piid != nil {
		if *siid <= 0 || *piid <= 0 {
			return errors.New("siid/piid must be positive")
		}
		suffix = fmt.Sprintf("%d.%d", *siid, *piid)
	}
	return c.subscribe(ctx, fmt.Sprintf("master/appMsg/notify/iot/%s/property/%s", did, suffix))
}

func (c *Client) SubscribeEvents(ctx context.Context, did string, siid, eiid *int) error {
	if strings.TrimSpace(did) == "" {
		return errors.New("did must not be empty")
	}
	suffix := "#"
	if siid != nil && eiid != nil {
		if *siid <= 0 || *eiid <= 0 {
			return errors.New("siid/eiid must be positive")
		}
		suffix = fmt.Sprintf("%d.%d", *siid, *eiid)
	}
	return c.subscribe(ctx, fmt.Sprintf("master/appMsg/notify/iot/%s/event/%s", did, suffix))
}

func (c *Client) subscribe(ctx context.Context, topic string) error {
	if c.mqtt == nil {
		return errors.New("client is not connected")
	}
	if err := c.mqtt.Subscribe(ctx, topic); err != nil {
		return fmt.Errorf("subscribe %s: %w", topic, err)
	}
	return nil
}
