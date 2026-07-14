package xiaomi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memorySpecCache struct {
	mu        sync.Mutex
	document  []byte
	specType  string
	model     string
	fetchedAt time.Time
}

func (c *memorySpecCache) LoadMIoTSpec(_ context.Context, specType, model string) ([]byte, string, time.Time, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.document) == 0 || (specType != "" && specType != c.specType) || (specType == "" && model != c.model) {
		return nil, "", time.Time{}, false, nil
	}
	return append([]byte(nil), c.document...), c.specType, c.fetchedAt, true, nil
}

func (c *memorySpecCache) SaveMIoTSpec(_ context.Context, specType, model string, document []byte, fetchedAt time.Time) error {
	c.mu.Lock()
	c.document, c.specType, c.model, c.fetchedAt = append([]byte(nil), document...), specType, model, fetchedAt
	c.mu.Unlock()
	return nil
}

func TestSpecResolverResolvesModelAndReusesCache(t *testing.T) {
	const (
		model    = "vendor.switch.v1"
		specType = "urn:miot-spec-v2:device:switch:0000A003:vendor-v1:1"
	)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/instances":
			_, _ = writer.Write([]byte(`{"instances":[{"model":"vendor.switch.v1","version":1,"type":"urn:miot-spec-v2:device:switch:0000A003:vendor-v1:1"}]}`))
		case "/instance":
			if request.URL.Query().Get("type") != specType {
				http.Error(writer, "wrong type", http.StatusBadRequest)
				return
			}
			_, _ = writer.Write([]byte(`{"type":"urn:miot-spec-v2:device:switch:0000A003:vendor-v1:1","description":"Switch","services":[{"iid":2,"type":"urn:miot-spec-v2:service:switch:0000780C:vendor-v1:1","description":"Switch","properties":[]}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	cache := &memorySpecCache{}
	resolver := NewSpecResolver(cache)
	resolver.baseURL, resolver.client = server.URL, server.Client()
	document, _, source, err := resolver.Resolve(context.Background(), "", model)
	if err != nil || document.Type != specType || source != "miot-spec.org" || requests.Load() != 2 {
		t.Fatalf("first resolve=%#v source=%q requests=%d err=%v", document, source, requests.Load(), err)
	}
	second := NewSpecResolver(cache)
	second.baseURL, second.client = server.URL, server.Client()
	document, _, source, err = second.Resolve(context.Background(), "", model)
	if err != nil || document.Type != specType || source != "miot-spec-cache" || requests.Load() != 2 {
		t.Fatalf("cached resolve=%#v source=%q requests=%d err=%v", document, source, requests.Load(), err)
	}
}
