package matter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

func TestClientCallCorrelatesJSONRPCResponse(t *testing.T) {
	clientConn, runtimeConn := net.Pipe()
	client := NewClient(clientConn, ClientOptions{DefaultTimeout: time.Second})
	defer client.Close()
	defer runtimeConn.Close()
	go func() {
		line, _ := bufio.NewReader(runtimeConn).ReadBytes('\n')
		var request rpcMessage
		_ = json.Unmarshal(line, &request)
		response, _ := json.Marshal(rpcMessage{
			JSONRPC: "2.0", ID: request.ID,
			Result: json.RawMessage(`{"protocolVersion":"1.1","replayRequired":true}`),
		})
		_, _ = runtimeConn.Write(append(response, '\n'))
	}()
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ReplayRequired  bool   `json:"replayRequired"`
	}
	err := client.Call(context.Background(), "runtime.handshake", map[string]string{
		"protocolVersion": "1.1", "targetId": "matter-one",
	}, &result)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if result.ProtocolVersion != "1.1" || !result.ReplayRequired {
		t.Fatalf("Call() result = %#v", result)
	}
}

func TestClientServesRuntimeRequest(t *testing.T) {
	clientConn, runtimeConn := net.Pipe()
	client := NewClient(clientConn, ClientOptions{
		RequestHandler: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			if method != "attribute.write" {
				t.Errorf("method = %q", method)
			}
			return map[string]any{"accepted": true}, nil
		},
	})
	defer client.Close()
	defer runtimeConn.Close()
	request := []byte(`{"jsonrpc":"2.0","id":7,"method":"attribute.write","params":{"endpointId":2}}` + "\n")
	if _, err := runtimeConn.Write(request); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(runtimeConn).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response rpcMessage
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatal(err)
	}
	if string(response.ID) != "7" || response.Error != nil || string(response.Result) != `{"accepted":true}` {
		t.Fatalf("response = %s", line)
	}
}

func TestClientTimeoutRemovesPendingRequest(t *testing.T) {
	clientConn, runtimeConn := net.Pipe()
	client := NewClient(clientConn, ClientOptions{DefaultTimeout: 20 * time.Millisecond})
	defer client.Close()
	defer runtimeConn.Close()
	go func() {
		_, _ = bufio.NewReader(runtimeConn).ReadBytes('\n')
	}()
	err := client.Call(context.Background(), "runtime.ping", map[string]any{}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Call() error = %v", err)
	}
	client.pendingMu.Lock()
	defer client.pendingMu.Unlock()
	if len(client.pending) != 0 {
		t.Fatalf("pending requests = %d", len(client.pending))
	}
}

func TestClientBoundsOutboundQueue(t *testing.T) {
	clientConn, runtimeConn := net.Pipe()
	client := NewClient(clientConn, ClientOptions{QueueCapacity: 1})
	defer client.Close()
	defer runtimeConn.Close()
	deadline := time.Now().Add(time.Second)
	for {
		err := client.Notify("attribute.update", map[string]int{"value": 1})
		if errors.Is(err, ErrBackpressure) {
			return
		}
		if err != nil {
			t.Fatalf("Notify() error = %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("outbound queue never applied backpressure")
		}
	}
}

func TestClientDisconnectFailsPendingCall(t *testing.T) {
	clientConn, runtimeConn := net.Pipe()
	client := NewClient(clientConn, ClientOptions{DefaultTimeout: time.Second})
	defer client.Close()
	go func() {
		_, _ = bufio.NewReader(runtimeConn).ReadBytes('\n')
		_ = runtimeConn.Close()
	}()
	if err := client.Call(context.Background(), "runtime.ping", map[string]any{}, nil); err == nil {
		t.Fatal("Call() succeeded after runtime disconnect")
	}
}

func TestClientRejectsOversizedInboundFrame(t *testing.T) {
	clientConn, runtimeConn := net.Pipe()
	client := NewClient(clientConn, ClientOptions{MaxFrameBytes: 128})
	defer client.Close()
	defer runtimeConn.Close()
	go func() {
		_, _ = runtimeConn.Write(append(bytes.Repeat([]byte("x"), 256), '\n'))
	}()
	select {
	case <-client.Done():
		if client.closeError() == nil {
			t.Fatal("oversized inbound frame closed without an error")
		}
	case <-time.After(time.Second):
		t.Fatal("oversized inbound frame was not rejected")
	}
}
