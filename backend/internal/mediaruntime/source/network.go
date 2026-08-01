package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/feranydev/homeloom/backend/internal/mediaruntime/adapter"
	"github.com/feranydev/homeloom/backend/internal/mediaruntime/contract"
)

// Resolver is the complete Camera Kernel input boundary. It intentionally
// dispatches only the three protocols compiled into HomeLoom.
type Resolver struct {
	Xiaomi  XiaomiResolver
	network networkResolver
}

// SetAuthorizationClient attaches Core's in-process authorization adapter.
// It replaces the former IPC-only dependency without changing source handling.
func (r *Resolver) SetAuthorizationClient(auth authorizationClient) {
	r.Xiaomi.setAuthorizationClient(auth)
	r.network.setAuthClient(auth)
}

func (r *Resolver) Resolve(ctx context.Context, stream contract.StreamSpec) (adapter.Source, error) {
	switch stream.Protocol {
	case "xiaomi-miss":
		return r.Xiaomi.Resolve(ctx, stream)
	case "rtsp", "onvif":
		return r.network.resolve(ctx, stream)
	default:
		return adapter.Source{}, errors.New("unsupported camera source protocol")
	}
}

type networkResolver struct {
	mu   sync.RWMutex
	auth authorizationClient
	next atomic.Uint64
}

func (r *networkResolver) setAuthClient(auth authorizationClient) {
	r.mu.Lock()
	r.auth = auth
	r.mu.Unlock()
}

func (r *networkResolver) resolve(ctx context.Context, stream contract.StreamSpec) (adapter.Source, error) {
	r.mu.RLock()
	auth := r.auth
	r.mu.RUnlock()
	if auth == nil {
		return adapter.Source{}, errors.New("Core authorization session is not ready")
	}
	response, err := auth.Acquire(ctx, contract.AuthorizationRequest{
		RequestID: fmt.Sprintf("media-auth-%d", r.next.Add(1)), DeviceID: stream.DeviceID,
		Protocol: stream.Protocol, Purpose: "playback", Attempt: 1,
	})
	if err != nil {
		return adapter.Source{}, err
	}
	if response.Endpoint.Protocol != stream.Protocol || response.Endpoint.Host == "" || response.Endpoint.Port < 1 {
		return adapter.Source{}, errors.New("invalid camera authorization endpoint")
	}
	var credentials struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if len(response.SecretMaterial) != 0 {
		if err := decodeSecret(response.SecretMaterial, &credentials); err != nil {
			return adapter.Source{}, errors.New("invalid camera authorization material")
		}
	}
	if response.AuthType != "none" && (credentials.Username == "" || credentials.Password == "") {
		return adapter.Source{}, errors.New("incomplete camera authorization material")
	}
	source := &url.URL{
		Scheme: stream.Protocol,
		Host:   response.Endpoint.Host + ":" + strconv.Itoa(response.Endpoint.Port),
	}
	if credentials.Username != "" {
		source.User = url.UserPassword(credentials.Username, credentials.Password)
	}
	if stream.Protocol == "rtsp" {
		source.Path = response.Endpoint.Path
	} else if response.Endpoint.Path != "" {
		query := source.Query()
		query.Set("subtype", response.Endpoint.Path)
		source.RawQuery = query.Encode()
	}
	return adapter.Source{URI: source.String()}, nil
}

func decodeSecret(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}
