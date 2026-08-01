// Package source resolves short-lived Core authorization leases into an
// in-memory producer input. It never stores or logs authorization material.
package source

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
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

type authorizationClient interface {
	Acquire(context.Context, contract.AuthorizationRequest) (contract.AuthorizationResponse, error)
}

type XiaomiResolver struct {
	mu    sync.RWMutex
	auth  authorizationClient
	cache map[string]xiaomiClientKey
	next  atomic.Uint64

	resolveMu sync.Mutex
}

type xiaomiClientKey struct {
	public  string
	private string
}

func (r *XiaomiResolver) setAuthorizationClient(auth authorizationClient) {
	r.mu.Lock()
	r.auth = auth
	r.mu.Unlock()
}

func (r *XiaomiResolver) Resolve(ctx context.Context, stream contract.StreamSpec) (adapter.Source, error) {
	if stream.Protocol != "xiaomi-miss" {
		return adapter.Source{}, errors.New("unsupported publisher source protocol")
	}
	cacheKey := stream.DeviceID + "\x00" + stream.CredentialRef
	r.mu.RLock()
	key, exists := r.cache[cacheKey]
	auth := r.auth
	r.mu.RUnlock()
	if auth == nil {
		return adapter.Source{}, errors.New("Core authorization session is not ready")
	}

	if !exists {
		// Serialize key generation so concurrent preview and publisher requests
		// for one camera share the same in-memory X25519 identity.
		r.resolveMu.Lock()
		r.mu.RLock()
		key, exists = r.cache[cacheKey]
		r.mu.RUnlock()
		if !exists {
			privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
			if err != nil {
				r.resolveMu.Unlock()
				return adapter.Source{}, errors.New("generate Xiaomi MISS client key")
			}
			key = xiaomiClientKey{
				public:  hex.EncodeToString(privateKey.PublicKey().Bytes()),
				private: hex.EncodeToString(privateKey.Bytes()),
			}
			r.mu.Lock()
			if r.cache == nil {
				r.cache = make(map[string]xiaomiClientKey)
			}
			r.cache[cacheKey] = key
			r.mu.Unlock()
		}
		r.resolveMu.Unlock()
	}
	clientMaterial, err := json.Marshal(struct {
		ClientPublic string `json:"clientPublic"`
	}{ClientPublic: key.public})
	if err != nil {
		return adapter.Source{}, errors.New("encode Xiaomi MISS client material")
	}
	requestID := fmt.Sprintf("media-auth-%d", r.next.Add(1))
	response, err := auth.Acquire(ctx, contract.AuthorizationRequest{
		RequestID: requestID, DeviceID: stream.DeviceID, Protocol: stream.Protocol, Purpose: "playback", Attempt: 1,
		ClientMaterial: clientMaterial,
	})
	if err != nil {
		return adapter.Source{}, err
	}
	if response.Endpoint.Protocol != "xiaomi-miss" || response.Endpoint.Host == "" || response.AuthType != "vendor" {
		return adapter.Source{}, errors.New("invalid Xiaomi authorization endpoint")
	}
	var public xiaomiPublicAuthorization
	var secret xiaomiCoreSecretMaterial
	if err := json.Unmarshal(response.PublicMaterial, &public); err != nil ||
		json.Unmarshal(response.SecretMaterial, &secret) != nil ||
		public.DID == "" || public.Model == "" || public.LocalIP == "" ||
		public.DevicePublic == "" || public.Vendor == "" || secret.Sign == "" {
		return adapter.Source{}, errors.New("invalid Xiaomi MISS handshake material")
	}
	workerSecret, err := json.Marshal(xiaomiWorkerSecretMaterial{
		Sign:          secret.Sign,
		ClientPublic:  key.public,
		ClientPrivate: key.private,
	})
	if err != nil {
		return adapter.Source{}, errors.New("encode Xiaomi MISS handshake material")
	}
	source := adapter.Source{
		URI: (&url.URL{
			Scheme: "xiaomi", Host: public.LocalIP,
			RawQuery: xiaomiMISSQuery(public, secret.Sign, key).Encode(),
		}).String(),
		PublicMaterial: append([]byte(nil), response.PublicMaterial...),
		SecretMaterial: workerSecret,
	}
	return source, nil
}

func xiaomiMISSQuery(public xiaomiPublicAuthorization, sign string, key xiaomiClientKey) url.Values {
	query := url.Values{
		"did":            {public.DID},
		"model":          {public.Model},
		"subtype":        {public.Subtype},
		"client_public":  {key.public},
		"client_private": {key.private},
		"device_public":  {public.DevicePublic},
		"sign":           {sign},
		"vendor":         {public.Vendor},
	}
	if public.Channel > 1 {
		query.Set("channel", strconv.Itoa(public.Channel))
	}
	if public.UID != "" {
		query.Set("uid", public.UID)
	}
	return query
}

type xiaomiPublicAuthorization struct {
	UserID       string `json:"userId"`
	Region       string `json:"region"`
	DID          string `json:"did"`
	Model        string `json:"model"`
	LocalIP      string `json:"localIP"`
	Subtype      string `json:"subtype"`
	Channel      int    `json:"channel"`
	DevicePublic string `json:"devicePublic"`
	Vendor       string `json:"vendor"`
	UID          string `json:"uid,omitempty"`
}

type xiaomiCoreSecretMaterial struct {
	Sign string `json:"sign"`
}

type xiaomiWorkerSecretMaterial struct {
	Sign          string `json:"sign"`
	ClientPublic  string `json:"clientPublic"`
	ClientPrivate string `json:"clientPrivate"`
}
