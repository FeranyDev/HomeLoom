// Package runtime embeds HomeLoom's media lifecycle and constrained camera
// engine into Core. It contains no IPC client and starts no worker process.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mediaruntime/adapter"
	"github.com/feranydev/homeloom/backend/internal/mediaruntime/contract"
	mediago2rtc "github.com/feranydev/homeloom/backend/internal/mediaruntime/go2rtc"
	"github.com/feranydev/homeloom/backend/internal/mediaruntime/source"
	"github.com/feranydev/homeloom/backend/internal/mediaruntime/worker"
)

type Config struct {
	RuntimeDir         string
	HAPHost            string
	HAPPort            int
	RTSPPort           int
	SRTPPort           int
	CameraKernelBinary string
	HomeKitPIN         string
	Authorize          AuthorizeFunc
	OnPairing          func(PairingOutput)
	OnError            func(error)
	ChildLogLevel      string
	ChildLogWriter     func(string, string) io.Writer
}

type PairingOutput struct {
	Name     string
	StreamID string
	PIN      string
	HAPHost  string
	LogPath  string
}

type StreamSpec struct {
	SchemaVersion int
	ID            string
	DeviceID      string
	Protocol      string
	CredentialRef string
	Profile       string
	Mode          string
	Audio         bool
	Talkback      bool
	Options       json.RawMessage
}

type Replay struct {
	SchemaVersion int
	Generation    uint64
	Revision      uint64
	Streams       []StreamSpec
}

type Mutation struct {
	SchemaVersion int
	Generation    uint64
	Revision      uint64
	Action        string
	Spec          StreamSpec
}

type AuthorizationRequest struct {
	RequestID      string
	DeviceID       string
	Protocol       string
	Purpose        string
	Attempt        int
	ClientMaterial json.RawMessage
	SessionOffer   []byte
}

type Endpoint struct {
	Protocol string
	Host     string
	Port     int
	Path     string
}

type AuthorizationResponse struct {
	LeaseID        string
	ExpiresAt      time.Time
	Endpoint       Endpoint
	AuthType       string
	PublicMaterial json.RawMessage
	SecretMaterial json.RawMessage
	SessionAnswer  []byte
	Reusable       bool
	MaxUses        int
}

type AuthorizeFunc func(context.Context, AuthorizationRequest) (AuthorizationResponse, error)

type Runtime struct {
	manager *worker.Manager
	mu      sync.RWMutex
	closed  bool
}

func Start(config Config) (*Runtime, error) {
	if config.Authorize == nil || config.RuntimeDir == "" || config.HAPHost == "" {
		return nil, errors.New("embedded media runtime configuration is incomplete")
	}
	runtimeDir, err := filepath.Abs(config.RuntimeDir)
	if err != nil {
		return nil, err
	}
	cameraKernelBinary, err := filepath.Abs(config.CameraKernelBinary)
	if err != nil {
		return nil, err
	}
	producer, err := mediago2rtc.NewPublisherProducer(mediago2rtc.PublisherProducerConfig{
		Binary: cameraKernelBinary, RuntimeDir: runtimeDir, HAPHost: config.HAPHost,
		HAPPortBase: config.HAPPort, RTSPPortBase: config.RTSPPort, SRTPPortBase: config.SRTPPort,
		HomeKitPIN: config.HomeKitPIN,
		OnPairing: func(output mediago2rtc.PublisherOutput) {
			if config.OnPairing != nil {
				config.OnPairing(PairingOutput{
					Name: output.Name, StreamID: output.StreamID, PIN: output.PIN, HAPHost: output.HAPHost, LogPath: output.LogPath,
				})
			}
		},
		OnError:  config.OnError,
		LogLevel: config.ChildLogLevel, LogWriter: config.ChildLogWriter,
	})
	if err != nil {
		return nil, err
	}
	registry := adapter.NewRegistry()
	for _, protocol := range []string{"rtsp", "onvif", "xiaomi-miss"} {
		if err := registry.Register(protocol, producer); err != nil {
			return nil, err
		}
	}
	resolver := &source.Resolver{}
	resolver.SetAuthorizationClient(authBridge{authorize: config.Authorize})
	lifecycle, err := adapter.NewLifecycleAdapter(registry, resolver.Resolve)
	if err != nil {
		return nil, err
	}
	return &Runtime{manager: worker.NewManager(lifecycle)}, nil
}

func (r *Runtime) Replay(value Replay) error {
	if !r.ready() {
		return errors.New("embedded media runtime is closed")
	}
	streams := make([]contract.StreamSpec, len(value.Streams))
	for index := range value.Streams {
		streams[index] = contractSpec(value.Streams[index])
	}
	_, err := r.manager.Replay(contract.ReplayParams{
		SchemaVersion: value.SchemaVersion, Generation: value.Generation,
		Revision: value.Revision, Streams: streams,
	})
	return err
}

func (r *Runtime) Apply(value Mutation) error {
	if !r.ready() {
		return errors.New("embedded media runtime is closed")
	}
	switch value.Action {
	case "upsert":
		_, err := r.manager.Upsert(contract.UpsertParams{
			SchemaVersion: value.SchemaVersion, Generation: value.Generation,
			Revision: value.Revision, Stream: contractSpec(value.Spec),
		})
		return err
	case "delete":
		_, err := r.manager.Delete(contract.DeleteParams{
			SchemaVersion: value.SchemaVersion, Generation: value.Generation,
			Revision: value.Revision, StreamID: value.Spec.ID,
		})
		return err
	default:
		return errors.New("unsupported embedded media mutation")
	}
}

func (r *Runtime) Ready() bool { return r.ready() }

func (r *Runtime) ready() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return !r.closed
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()
	generation, revision, _ := r.manager.Snapshot()
	if generation == 0 {
		return nil
	}
	_, err := r.manager.Replay(contract.ReplayParams{
		SchemaVersion: 1, Generation: generation, Revision: revision,
		Streams: nil,
	})
	return err
}

func contractSpec(value StreamSpec) contract.StreamSpec {
	return contract.StreamSpec{
		SchemaVersion: value.SchemaVersion, ID: value.ID, DeviceID: value.DeviceID,
		Protocol: value.Protocol, CredentialRef: value.CredentialRef, Profile: value.Profile,
		Mode: value.Mode, Audio: value.Audio, Talkback: value.Talkback,
		Options: append(json.RawMessage(nil), value.Options...),
	}
}

type authBridge struct{ authorize AuthorizeFunc }

func (a authBridge) Acquire(ctx context.Context, value contract.AuthorizationRequest) (contract.AuthorizationResponse, error) {
	response, err := a.authorize(ctx, AuthorizationRequest{
		RequestID: value.RequestID, DeviceID: value.DeviceID, Protocol: value.Protocol,
		Purpose: value.Purpose, Attempt: value.Attempt,
		ClientMaterial: append(json.RawMessage(nil), value.ClientMaterial...),
		SessionOffer:   append([]byte(nil), value.SessionOffer...),
	})
	if err != nil {
		return contract.AuthorizationResponse{}, err
	}
	return contract.AuthorizationResponse{
		SchemaVersion: 1, LeaseID: response.LeaseID, ExpiresAt: response.ExpiresAt,
		Endpoint: contract.EndpointSpec{
			Protocol: response.Endpoint.Protocol, Host: response.Endpoint.Host,
			Port: response.Endpoint.Port, Path: response.Endpoint.Path,
		},
		AuthType:       response.AuthType,
		PublicMaterial: append(json.RawMessage(nil), response.PublicMaterial...),
		SecretMaterial: append(json.RawMessage(nil), response.SecretMaterial...),
		SessionAnswer:  append([]byte(nil), response.SessionAnswer...),
		Reusable:       response.Reusable, MaxUses: response.MaxUses,
	}, nil
}
