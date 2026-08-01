package matter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrClosed       = errors.New("Matter IPC client is closed")
	ErrBackpressure = errors.New("Matter IPC outbound queue is full")
)

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("Matter RPC error %d: %s", e.Code, e.Message)
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type rpcResponse struct {
	result json.RawMessage
	err    error
}

type RequestHandler func(context.Context, string, json.RawMessage) (any, error)
type NotificationHandler func(string, json.RawMessage)

type ClientOptions struct {
	QueueCapacity       int
	DefaultTimeout      time.Duration
	MaxFrameBytes       int
	RequestHandler      RequestHandler
	NotificationHandler NotificationHandler
}

// Client implements newline-delimited JSON-RPC 2.0 over a connected Unix
// socket. It deliberately bounds writes; callers must replay a full snapshot
// after reconnect instead of allowing stale deltas to consume unbounded RAM.
type Client struct {
	conn        net.Conn
	writer      *bufio.Writer
	outbound    chan []byte
	done        chan struct{}
	closeOnce   sync.Once
	writeMu     sync.Mutex
	pendingMu   sync.Mutex
	pending     map[string]chan rpcResponse
	nextID      atomic.Uint64
	defaultWait time.Duration
	maxFrame    int
	onRequest   RequestHandler
	onNotify    NotificationHandler
	terminalMu  sync.Mutex
	terminalErr error
}

func NewClient(conn net.Conn, options ClientOptions) *Client {
	capacity := options.QueueCapacity
	if capacity <= 0 {
		capacity = 128
	}
	timeout := options.DefaultTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	maxFrame := options.MaxFrameBytes
	if maxFrame <= 0 {
		maxFrame = 4 << 20
	}
	client := &Client{
		conn: conn, writer: bufio.NewWriter(conn), outbound: make(chan []byte, capacity),
		done: make(chan struct{}), pending: make(map[string]chan rpcResponse),
		defaultWait: timeout, maxFrame: maxFrame,
		onRequest: options.RequestHandler, onNotify: options.NotificationHandler,
	}
	go client.writeLoop()
	go client.readLoop()
	return client
}

func Dial(ctx context.Context, socketPath string, options ClientOptions) (*Client, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial Matter runtime socket: %w", err)
	}
	return NewClient(conn, options), nil
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	if method == "" {
		return errors.New("Matter RPC method is required")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.defaultWait)
		defer cancel()
	}
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode Matter RPC params: %w", err)
	}
	encoded, err := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method, Params: payload})
	if err != nil {
		return fmt.Errorf("encode Matter RPC request: %w", err)
	}
	response := make(chan rpcResponse, 1)
	c.pendingMu.Lock()
	select {
	case <-c.done:
		c.pendingMu.Unlock()
		return c.closeError()
	default:
	}
	c.pending[id] = response
	c.pendingMu.Unlock()
	if err := c.enqueue(encoded); err != nil {
		c.removePending(id)
		return err
	}
	select {
	case received := <-response:
		if received.err != nil {
			return received.err
		}
		if result == nil || len(received.result) == 0 || string(received.result) == "null" {
			return nil
		}
		if err := json.Unmarshal(received.result, result); err != nil {
			return fmt.Errorf("decode Matter RPC result: %w", err)
		}
		return nil
	case <-ctx.Done():
		c.removePending(id)
		return fmt.Errorf("Matter RPC %s: %w", method, ctx.Err())
	case <-c.done:
		c.removePending(id)
		return c.closeError()
	}
}

func (c *Client) Notify(method string, params any) error {
	if method == "" {
		return errors.New("Matter RPC method is required")
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode Matter notification params: %w", err)
	}
	encoded, err := json.Marshal(rpcMessage{JSONRPC: "2.0", Method: method, Params: payload})
	if err != nil {
		return fmt.Errorf("encode Matter notification: %w", err)
	}
	return c.enqueue(encoded)
}

func (c *Client) enqueue(encoded []byte) error {
	select {
	case <-c.done:
		return c.closeError()
	default:
	}
	encoded = append(encoded, '\n')
	select {
	case c.outbound <- encoded:
		return nil
	default:
		return ErrBackpressure
	}
}

func (c *Client) writeLoop() {
	for {
		select {
		case encoded := <-c.outbound:
			c.writeMu.Lock()
			_, err := c.writer.Write(encoded)
			if err == nil {
				err = c.writer.Flush()
			}
			c.writeMu.Unlock()
			if err != nil {
				c.closeWithError(fmt.Errorf("write Matter IPC: %w", err))
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *Client) readLoop() {
	scanner := bufio.NewScanner(c.conn)
	initialBuffer := 64 << 10
	if c.maxFrame < initialBuffer {
		initialBuffer = c.maxFrame
	}
	scanner.Buffer(make([]byte, initialBuffer), c.maxFrame)
	for scanner.Scan() {
		encoded := scanner.Bytes()
		var message rpcMessage
		if err := json.Unmarshal(encoded, &message); err != nil || message.JSONRPC != "2.0" {
			continue
		}
		if len(message.ID) > 0 && message.Method == "" {
			c.handleResponse(message)
			continue
		}
		if message.Method != "" {
			go c.handleInbound(message)
		}
	}
	err := scanner.Err()
	if err != nil && !errors.Is(err, io.EOF) {
		err = fmt.Errorf("read Matter IPC: %w", err)
	}
	c.closeWithError(err)
}

func (c *Client) handleResponse(message rpcMessage) {
	id := string(message.ID)
	c.pendingMu.Lock()
	response, found := c.pending[id]
	if found {
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
	if !found {
		return
	}
	if message.Error != nil {
		response <- rpcResponse{err: message.Error}
	} else {
		response <- rpcResponse{result: message.Result}
	}
}

func (c *Client) handleInbound(message rpcMessage) {
	if len(message.ID) == 0 {
		if c.onNotify != nil {
			c.onNotify(message.Method, message.Params)
		}
		return
	}
	var (
		result any
		err    error
	)
	if c.onRequest == nil {
		err = &RPCError{Code: -32601, Message: "method not found"}
	} else {
		result, err = c.onRequest(context.Background(), message.Method, message.Params)
	}
	response := rpcMessage{JSONRPC: "2.0", ID: message.ID}
	if err != nil {
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) {
			response.Error = rpcErr
		} else {
			response.Error = &RPCError{Code: -32000, Message: err.Error()}
		}
	} else {
		response.Result, _ = json.Marshal(result)
	}
	encoded, marshalErr := json.Marshal(response)
	if marshalErr == nil {
		_ = c.enqueue(encoded)
	}
}

func (c *Client) removePending(id string) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *Client) closeWithError(err error) {
	c.closeOnce.Do(func() {
		if err == nil {
			err = ErrClosed
		}
		c.terminalMu.Lock()
		c.terminalErr = err
		c.terminalMu.Unlock()
		close(c.done)
		_ = c.conn.Close()
		c.pendingMu.Lock()
		for id, response := range c.pending {
			delete(c.pending, id)
			response <- rpcResponse{err: err}
		}
		c.pendingMu.Unlock()
	})
}

func (c *Client) closeError() error {
	c.terminalMu.Lock()
	defer c.terminalMu.Unlock()
	if c.terminalErr != nil {
		return c.terminalErr
	}
	return ErrClosed
}

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Close() error {
	c.closeWithError(ErrClosed)
	return nil
}
