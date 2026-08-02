// Package adapter provides the protocol-neutral lifecycle boundary between
// desired stream state and a media kernel. It deliberately contains no
// credential store and no persistence.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mediaruntime/contract"
)

var (
	ErrProducerNotFound = errors.New("media producer is not registered")
	ErrResolverMissing  = errors.New("media source resolver is required")
)

// Source is an ephemeral input for a producer. URI may contain credential
// material and must only be used while Start is executing. Implementations
// must not write it to logs or disk and must not return it in errors.
type Source struct {
	URI            string
	PublicMaterial json.RawMessage
	SecretMaterial json.RawMessage
}

// SourceResolver obtains a short-lived source from Core's credential broker.
// It is deliberately outside StreamSpec: desired state stores only a
// CredentialRef, never the resolved material.
type SourceResolver func(context.Context, contract.StreamSpec) (Source, error)

// Session is an active media pipeline. Close must release all sockets and
// child processes owned by the producer.
type Session interface {
	Close(context.Context) error
}

// Producer starts one protocol-specific input. A real RTSP producer may be a
// go2rtc external-kernel client, but it is not coupled to go2rtc Go internals.
type Producer interface {
	Start(context.Context, contract.StreamSpec, Source) (Session, error)
}

// Registry maps a validated protocol name to its producer. It is safe to
// construct at startup and to extend in tests or feature-gated builds.
type Registry struct {
	mu        sync.RWMutex
	producers map[string]Producer
}

func NewRegistry() *Registry {
	return &Registry{producers: make(map[string]Producer)}
}

func (r *Registry) Register(protocol string, producer Producer) error {
	if protocol = normalizeProtocol(protocol); protocol == "" || producer == nil {
		return fmt.Errorf("invalid media producer registration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.producers[protocol]; exists {
		return fmt.Errorf("producer for %q already registered", protocol)
	}
	r.producers[protocol] = producer
	return nil
}

func (r *Registry) Producer(protocol string) (Producer, error) {
	r.mu.RLock()
	producer := r.producers[normalizeProtocol(protocol)]
	r.mu.RUnlock()
	if producer == nil {
		return nil, fmt.Errorf("%w: %s", ErrProducerNotFound, protocol)
	}
	return producer, nil
}

func normalizeProtocol(protocol string) string { return strings.ToLower(strings.TrimSpace(protocol)) }

// LifecycleAdapter turns desired stream mutations into producer sessions. It
// implements worker.Adapter structurally without importing worker, which keeps
// the media engine replaceable. It only retains opaque Session values, never
// resolved Sources or credentials.
type LifecycleAdapter struct {
	registry *Registry
	resolve  SourceResolver
	timeout  time.Duration

	mu       sync.Mutex
	sessions map[string]activeSession
}

type activeSession struct {
	spec    contract.StreamSpec
	session Session
}

func NewLifecycleAdapter(registry *Registry, resolve SourceResolver) (*LifecycleAdapter, error) {
	if registry == nil {
		return nil, fmt.Errorf("registry is required")
	}
	if resolve == nil {
		return nil, ErrResolverMissing
	}
	return &LifecycleAdapter{
		registry: registry, resolve: resolve, timeout: 15 * time.Second,
		sessions: make(map[string]activeSession),
	}, nil
}

// Replace prepares brand-new pipelines before changing existing sessions.
// Changed streams are restarted in place because their producer may own
// deterministic listener ports. A failed in-place replacement restores its
// previous desired state when possible; unchanged and not-yet-changed sessions
// remain live for a later retry.
func (a *LifecycleAdapter) Replace(streams []contract.StreamSpec) error {
	desired := make(map[string]contract.StreamSpec, len(streams))
	for _, stream := range streams {
		desired[stream.ID] = stream
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	prepared := make(map[string]activeSession)
	for id, spec := range desired {
		_, exists := a.sessions[id]
		if exists {
			continue
		}
		if !isActive(spec) {
			continue
		}
		candidate, err := a.start(spec)
		if err != nil {
			a.closePrepared(prepared)
			return err
		}
		prepared[id] = candidate
	}

	for id, current := range a.sessions {
		next, exists := desired[id]
		if !exists || !isActive(next) || sameSpec(current.spec, next) {
			continue
		}
		if err := a.close(current.session); err != nil {
			a.closePrepared(prepared)
			return err
		}
		delete(a.sessions, id)
		candidate, err := a.start(next)
		if err != nil {
			restored, restoreErr := a.start(current.spec)
			if restoreErr == nil {
				a.sessions[id] = restored
			} else {
				err = errors.Join(err, errors.New("media producer rollback failed"))
			}
			a.closePrepared(prepared)
			return err
		}
		a.sessions[id] = candidate
	}

	for id, current := range a.sessions {
		if next, exists := desired[id]; !exists || !isActive(next) {
			if err := a.close(current.session); err != nil {
				a.closePrepared(prepared)
				return err
			}
			delete(a.sessions, id)
		}
	}
	for id, candidate := range prepared {
		a.sessions[id] = candidate
	}
	return nil
}

func (a *LifecycleAdapter) Upsert(spec contract.StreamSpec) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.sessions[spec.ID]
	if exists && sameSpec(current.spec, spec) {
		return nil
	}
	if !isActive(spec) {
		if !exists {
			return nil
		}
		if err := a.close(current.session); err != nil {
			return err
		}
		delete(a.sessions, spec.ID)
		return nil
	}
	// A replacement commonly owns deterministic per-stream listener ports.
	// Release the previous session before starting its replacement so the new
	// producer can bind those ports. If the replacement cannot start, make a
	// best-effort restart of the previous desired state.
	if exists {
		if err := a.close(current.session); err != nil {
			return err
		}
		delete(a.sessions, spec.ID)
	}
	candidate, err := a.start(spec)
	if err != nil {
		if exists {
			restored, restoreErr := a.start(current.spec)
			if restoreErr == nil {
				a.sessions[spec.ID] = restored
			} else {
				return errors.Join(err, errors.New("media producer rollback failed"))
			}
		}
		return err
	}
	a.sessions[spec.ID] = candidate
	return nil
}

func (a *LifecycleAdapter) Delete(streamID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.sessions[streamID]
	if !exists {
		return nil
	}
	if err := a.close(current.session); err != nil {
		return err
	}
	delete(a.sessions, streamID)
	return nil
}

func (a *LifecycleAdapter) start(spec contract.StreamSpec) (activeSession, error) {
	producer, err := a.registry.Producer(spec.Protocol)
	if err != nil {
		return activeSession{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
	defer cancel()
	source, err := a.resolve(ctx, spec)
	if err != nil {
		return activeSession{}, fmt.Errorf("media source resolution failed: %w", err)
	}
	session, err := producer.Start(ctx, spec, source)
	if err != nil {
		return activeSession{}, fmt.Errorf("media producer start failed: %w", err)
	}
	if session == nil {
		return activeSession{}, errors.New("media producer returned no session")
	}
	return activeSession{spec: cloneSpec(spec), session: session}, nil
}

func (a *LifecycleAdapter) close(session Session) error {
	ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
	defer cancel()
	if err := session.Close(ctx); err != nil {
		return fmt.Errorf("media producer stop failed: %w", err)
	}
	return nil
}

func (a *LifecycleAdapter) closePrepared(prepared map[string]activeSession) {
	for _, candidate := range prepared {
		_ = a.close(candidate.session)
	}
}

func isActive(spec contract.StreamSpec) bool { return spec.Mode != "disabled" }

func sameSpec(left, right contract.StreamSpec) bool {
	return left.SchemaVersion == right.SchemaVersion && left.ID == right.ID && left.DeviceID == right.DeviceID &&
		left.Protocol == right.Protocol && left.CredentialRef == right.CredentialRef && left.Profile == right.Profile &&
		left.Mode == right.Mode && left.Audio == right.Audio && left.Talkback == right.Talkback && string(left.Options) == string(right.Options)
}

func cloneSpec(spec contract.StreamSpec) contract.StreamSpec {
	spec.Options = append([]byte(nil), spec.Options...)
	return spec
}
