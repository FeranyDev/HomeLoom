package mqtt5

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	packetConnect     = 1
	packetConnAck     = 2
	packetPublish     = 3
	packetPubAck      = 4
	packetPubRec      = 5
	packetPubRel      = 6
	packetPubComp     = 7
	packetSubscribe   = 8
	packetSubAck      = 9
	packetPingReq     = 12
	packetPingResp    = 13
	packetDisconnect  = 14
	maxRemainingBytes = 268435455
)

// Message is an incoming MQTT PUBLISH packet.
type Message struct {
	Topic   string
	Payload []byte
	QoS     byte
	Retain  bool
}

type ack struct {
	reason byte
	err    error
}

// Client is a deliberately small MQTT 5 client that supports the subset used
// by Xiaomi central hubs: mTLS, QoS 2 publish/subscribe and keepalive.
type Client struct {
	clientID  string
	keepAlive uint16

	conn   net.Conn
	reader *bufio.Reader

	writeMu sync.Mutex
	stateMu sync.Mutex
	closed  bool

	nextPacketID atomic.Uint32
	pendingSub   map[uint16]chan ack
	pendingPub   map[uint16]chan ack

	onMessage func(Message)
	done      chan struct{}
	readErr   chan error
}

func Dial(ctx context.Context, address, clientID string, tlsConfig *tls.Config, keepAlive uint16, onMessage func(Message)) (*Client, error) {
	if clientID == "" {
		return nil, errors.New("MQTT client ID is required")
	}
	if keepAlive == 0 {
		keepAlive = 60
	}
	dialer := &tls.Dialer{Config: tlsConfig, NetDialer: &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("TLS dial: %w", err)
	}
	client := &Client{
		clientID:   clientID,
		keepAlive:  keepAlive,
		conn:       conn,
		reader:     bufio.NewReader(conn),
		pendingSub: make(map[uint16]chan ack),
		pendingPub: make(map[uint16]chan ack),
		onMessage:  onMessage,
		done:       make(chan struct{}),
		readErr:    make(chan error, 1),
	}
	client.nextPacketID.Store(uint32(time.Now().UnixNano()) & 0xffff)
	if err := client.connect(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	go client.readLoop()
	go client.keepaliveLoop()
	return client, nil
}

func (c *Client) Done() <-chan struct{} { return c.done }
func (c *Client) Errors() <-chan error  { return c.readErr }

func (c *Client) connect(ctx context.Context) error {
	var body bytes.Buffer
	writeUTF8(&body, "MQTT")
	body.WriteByte(5)    // MQTT v5
	body.WriteByte(0x02) // clean start, no username/password/will
	_ = binary.Write(&body, binary.BigEndian, c.keepAlive)
	body.WriteByte(0) // connect properties length
	writeUTF8(&body, c.clientID)
	if err := c.writePacket(ctx, 0x10, body.Bytes()); err != nil {
		return fmt.Errorf("send CONNECT: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetReadDeadline(deadline)
		defer c.conn.SetReadDeadline(time.Time{})
	}

	typ, _, payload, err := readPacket(c.reader)
	if err != nil {
		return fmt.Errorf("read CONNACK: %w", err)
	}
	if typ != packetConnAck || len(payload) < 3 {
		return fmt.Errorf("unexpected CONNACK packet type=%d length=%d", typ, len(payload))
	}
	if payload[1] != 0 {
		return fmt.Errorf("broker rejected CONNECT, reason code=0x%02x", payload[1])
	}
	return nil
}

func (c *Client) Subscribe(ctx context.Context, topics ...string) error {
	if len(topics) == 0 {
		return errors.New("at least one topic is required")
	}
	packetID := c.newPacketID()
	ch := make(chan ack, 1)
	c.stateMu.Lock()
	if c.closed {
		c.stateMu.Unlock()
		return net.ErrClosed
	}
	c.pendingSub[packetID] = ch
	c.stateMu.Unlock()
	defer c.removePendingSub(packetID)

	var body bytes.Buffer
	_ = binary.Write(&body, binary.BigEndian, packetID)
	body.WriteByte(0) // properties length
	for _, topic := range topics {
		if topic == "" {
			return errors.New("empty subscribe topic")
		}
		writeUTF8(&body, topic)
		body.WriteByte(0x02) // QoS 2, default retain flags
	}
	if err := c.writePacket(ctx, 0x82, body.Bytes()); err != nil {
		return fmt.Errorf("send SUBSCRIBE: %w", err)
	}
	select {
	case result := <-ch:
		if result.err != nil {
			return result.err
		}
		if result.reason >= 0x80 {
			return fmt.Errorf("SUBACK rejected subscription, reason=0x%02x", result.reason)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return net.ErrClosed
	}
}

func (c *Client) Publish(ctx context.Context, topic string, payload []byte, qos byte) error {
	if topic == "" {
		return errors.New("publish topic is required")
	}
	if qos > 2 {
		return fmt.Errorf("unsupported QoS: %d", qos)
	}
	var packetID uint16
	var ch chan ack
	if qos > 0 {
		packetID = c.newPacketID()
		ch = make(chan ack, 1)
		c.stateMu.Lock()
		if c.closed {
			c.stateMu.Unlock()
			return net.ErrClosed
		}
		c.pendingPub[packetID] = ch
		c.stateMu.Unlock()
		defer c.removePendingPub(packetID)
	}

	var body bytes.Buffer
	writeUTF8(&body, topic)
	if qos > 0 {
		_ = binary.Write(&body, binary.BigEndian, packetID)
	}
	body.WriteByte(0) // publish properties length
	body.Write(payload)
	header := byte(0x30 | (qos << 1))
	if err := c.writePacket(ctx, header, body.Bytes()); err != nil {
		return fmt.Errorf("send PUBLISH: %w", err)
	}
	if qos == 0 {
		return nil
	}
	select {
	case result := <-ch:
		if result.err != nil {
			return result.err
		}
		if result.reason >= 0x80 {
			return fmt.Errorf("publish rejected, reason=0x%02x", result.reason)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return net.ErrClosed
	}
}

func (c *Client) Close() error {
	c.stateMu.Lock()
	alreadyClosed := c.closed
	c.stateMu.Unlock()
	if alreadyClosed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = c.writePacket(ctx, 0xE0, nil)
	cancel()
	c.shutdown(net.ErrClosed)
	return nil
}

func (c *Client) readLoop() {
	defer c.shutdown(io.EOF)
	for {
		typ, flags, payload, err := readPacket(c.reader)
		if err != nil {
			c.shutdown(err)
			return
		}
		switch typ {
		case packetPublish:
			if err := c.handlePublish(flags, payload); err != nil {
				c.shutdown(err)
				return
			}
		case packetPubAck:
			c.handlePubAck(payload)
		case packetPubRec:
			if err := c.handlePubRec(payload); err != nil {
				c.shutdown(err)
				return
			}
		case packetPubRel:
			if err := c.handleIncomingPubRel(payload); err != nil {
				c.shutdown(err)
				return
			}
		case packetPubComp:
			c.handlePubAck(payload)
		case packetSubAck:
			c.handleSubAck(payload)
		case packetPingResp:
			// no-op
		case packetDisconnect:
			reason := byte(0)
			if len(payload) > 0 {
				reason = payload[0]
			}
			c.shutdown(fmt.Errorf("broker DISCONNECT reason=0x%02x", reason))
			return
		default:
			// Ignore packet types not needed by this client.
		}
	}
}

func (c *Client) handlePublish(flags byte, payload []byte) error {
	qos := (flags >> 1) & 0x03
	retain := flags&0x01 != 0
	offset := 0
	topic, n, err := readUTF8(payload)
	if err != nil {
		return fmt.Errorf("decode incoming PUBLISH topic: %w", err)
	}
	offset += n
	var packetID uint16
	if qos > 0 {
		if len(payload)-offset < 2 {
			return errors.New("incoming PUBLISH missing packet ID")
		}
		packetID = binary.BigEndian.Uint16(payload[offset : offset+2])
		offset += 2
	}
	propLen, consumed, err := decodeVarInt(payload[offset:])
	if err != nil {
		return fmt.Errorf("decode PUBLISH properties: %w", err)
	}
	offset += consumed + propLen
	if offset > len(payload) {
		return errors.New("incoming PUBLISH properties exceed packet")
	}
	messagePayload := append([]byte(nil), payload[offset:]...)
	if c.onMessage != nil {
		c.onMessage(Message{Topic: topic, Payload: messagePayload, QoS: qos, Retain: retain})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch qos {
	case 1:
		return c.writeAckPacket(ctx, 0x40, packetID, 0)
	case 2:
		return c.writeAckPacket(ctx, 0x50, packetID, 0)
	default:
		return nil
	}
}

func (c *Client) handlePubRec(payload []byte) error {
	packetID, reason, err := parseAck(payload)
	if err != nil {
		return err
	}
	if reason >= 0x80 {
		c.resolvePub(packetID, ack{reason: reason})
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// PUBREL fixed header requires flags 0b0010.
	return c.writeAckPacket(ctx, 0x62, packetID, 0)
}

func (c *Client) handleIncomingPubRel(payload []byte) error {
	packetID, _, err := parseAck(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.writeAckPacket(ctx, 0x70, packetID, 0)
}

func (c *Client) handlePubAck(payload []byte) {
	packetID, reason, err := parseAck(payload)
	if err != nil {
		return
	}
	c.resolvePub(packetID, ack{reason: reason})
}

func (c *Client) handleSubAck(payload []byte) {
	if len(payload) < 3 {
		return
	}
	packetID := binary.BigEndian.Uint16(payload[:2])
	propLen, n, err := decodeVarInt(payload[2:])
	if err != nil {
		return
	}
	offset := 2 + n + propLen
	if offset >= len(payload) {
		return
	}
	reason := payload[offset]
	c.stateMu.Lock()
	ch := c.pendingSub[packetID]
	c.stateMu.Unlock()
	if ch != nil {
		select {
		case ch <- ack{reason: reason}:
		default:
		}
	}
}

func (c *Client) resolvePub(packetID uint16, result ack) {
	c.stateMu.Lock()
	ch := c.pendingPub[packetID]
	c.stateMu.Unlock()
	if ch != nil {
		select {
		case ch <- result:
		default:
		}
	}
}

func (c *Client) writeAckPacket(ctx context.Context, header byte, packetID uint16, reason byte) error {
	payload := []byte{byte(packetID >> 8), byte(packetID), reason, 0}
	return c.writePacket(ctx, header, payload)
}

func (c *Client) keepaliveLoop() {
	interval := time.Duration(c.keepAlive) * time.Second / 2
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := c.writePacket(ctx, 0xC0, nil)
			cancel()
			if err != nil {
				c.shutdown(err)
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *Client) writePacket(ctx context.Context, header byte, body []byte) error {
	if len(body) > maxRemainingBytes {
		return errors.New("MQTT packet too large")
	}
	var packet bytes.Buffer
	packet.WriteByte(header)
	packet.Write(encodeVarInt(len(body)))
	packet.Write(body)

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
		defer c.conn.SetWriteDeadline(time.Time{})
	}
	_, err := c.conn.Write(packet.Bytes())
	return err
}

func (c *Client) shutdown(err error) {
	c.stateMu.Lock()
	if c.closed {
		c.stateMu.Unlock()
		return
	}
	c.closed = true
	pendingSub := c.pendingSub
	pendingPub := c.pendingPub
	c.pendingSub = make(map[uint16]chan ack)
	c.pendingPub = make(map[uint16]chan ack)
	close(c.done)
	c.stateMu.Unlock()
	_ = c.conn.Close()
	for _, ch := range pendingSub {
		select {
		case ch <- ack{err: err}:
		default:
		}
	}
	for _, ch := range pendingPub {
		select {
		case ch <- ack{err: err}:
		default:
		}
	}
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		select {
		case c.readErr <- err:
		default:
		}
	}
}

func (c *Client) newPacketID() uint16 {
	for {
		id := uint16(c.nextPacketID.Add(1))
		if id != 0 {
			return id
		}
	}
}

func (c *Client) removePendingSub(id uint16) {
	c.stateMu.Lock()
	delete(c.pendingSub, id)
	c.stateMu.Unlock()
}

func (c *Client) removePendingPub(id uint16) {
	c.stateMu.Lock()
	delete(c.pendingPub, id)
	c.stateMu.Unlock()
}

func readPacket(reader *bufio.Reader) (packetType byte, flags byte, payload []byte, err error) {
	header, err := reader.ReadByte()
	if err != nil {
		return 0, 0, nil, err
	}
	remaining, err := readVarInt(reader)
	if err != nil {
		return 0, 0, nil, err
	}
	payload = make([]byte, remaining)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, 0, nil, err
	}
	return header >> 4, header & 0x0f, payload, nil
}

func readVarInt(reader io.ByteReader) (int, error) {
	multiplier, value := 1, 0
	for i := 0; i < 4; i++ {
		encoded, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		value += int(encoded&127) * multiplier
		if encoded&128 == 0 {
			return value, nil
		}
		multiplier *= 128
	}
	return 0, errors.New("malformed MQTT variable byte integer")
}

func encodeVarInt(value int) []byte {
	var out []byte
	for {
		encoded := byte(value % 128)
		value /= 128
		if value > 0 {
			encoded |= 128
		}
		out = append(out, encoded)
		if value == 0 {
			return out
		}
	}
}

func decodeVarInt(data []byte) (value, consumed int, err error) {
	multiplier := 1
	for i := 0; i < 4; i++ {
		if i >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		encoded := data[i]
		value += int(encoded&127) * multiplier
		consumed++
		if encoded&128 == 0 {
			return value, consumed, nil
		}
		multiplier *= 128
	}
	return 0, 0, errors.New("malformed MQTT variable byte integer")
}

func writeUTF8(buffer *bytes.Buffer, value string) {
	_ = binary.Write(buffer, binary.BigEndian, uint16(len(value)))
	buffer.WriteString(value)
}

func readUTF8(data []byte) (string, int, error) {
	if len(data) < 2 {
		return "", 0, io.ErrUnexpectedEOF
	}
	length := int(binary.BigEndian.Uint16(data[:2]))
	if len(data) < 2+length {
		return "", 0, io.ErrUnexpectedEOF
	}
	return string(data[2 : 2+length]), 2 + length, nil
}

func parseAck(payload []byte) (uint16, byte, error) {
	if len(payload) < 2 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	packetID := binary.BigEndian.Uint16(payload[:2])
	reason := byte(0)
	if len(payload) >= 3 {
		reason = payload[2]
	}
	return packetID, reason, nil
}
