package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type ProviderStore interface {
	ListProviders(context.Context) ([]providerconfig.Config, error)
	SaveProvider(context.Context, providerconfig.Config) error
	DeleteProvider(context.Context, string) error
}

type ProviderRuntime interface {
	Apply(context.Context, providersdk.Provider) error
	Remove(context.Context, string) error
	ProviderInfos() []providersdk.RuntimeInfo
}

type ProviderInfo struct {
	providerconfig.Config
	Status            string                        `json:"status"`
	Error             string                        `json:"error,omitempty"`
	Manifest          *providersdk.Manifest         `json:"manifest,omitempty"`
	Capabilities      providersdk.Capabilities      `json:"capabilities"`
	RetryCount        int                           `json:"retryCount"`
	NextRetryAt       *time.Time                    `json:"nextRetryAt,omitempty"`
	TransitionedAt    time.Time                     `json:"transitionedAt,omitempty"`
	Metrics           map[string]uint64             `json:"metrics,omitempty"`
	Diagnostics       map[string]string             `json:"diagnostics,omitempty"`
	Credentials       *providersdk.CredentialStatus `json:"credentials,omitempty"`
	CredentialError   string                        `json:"credentialError,omitempty"`
	CredentialRetryAt *time.Time                    `json:"credentialRetryAt,omitempty"`
	AuthChallenge     *ProviderAuthChallenge        `json:"authChallenge,omitempty"`
}

// ProviderAuthChallenge is an in-memory, short-lived Xiaomi identity gate.
// Challenge IDs and URLs are safe to return to the administrator; account
// passwords and submitted verification codes are deliberately absent.
type ProviderAuthChallenge struct {
	Status          string    `json:"status"`
	ChallengeID     string    `json:"challengeId"`
	VerificationURL string    `json:"verificationUrl"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

// ProviderAuthChallengeError is returned by Save when Xiaomi paused provider
// startup for SMS/email identity verification. The provider configuration has
// already been durably saved (with its normal secret protection); callers can
// query Challenge and submit the code through VerifyAuthChallenge.
type ProviderAuthChallengeError struct {
	Challenge ProviderAuthChallenge
	cause     error
}

func (e *ProviderAuthChallengeError) Error() string {
	return "Xiaomi MIoT cloud provider requires identity verification"
}

func (e *ProviderAuthChallengeError) Unwrap() error { return e.cause }

type credentialRetry struct {
	count        int
	next         time.Time
	err          string
	applyPending bool
}

type ProviderService struct {
	mu                sync.RWMutex
	configs           map[string]providerconfig.Config
	store             ProviderStore
	factory           *providersdk.Factory
	runtime           ProviderRuntime
	maintenanceOnce   sync.Once
	maintenanceWake   chan struct{}
	credentialRun     sync.Mutex
	credentialRetries map[string]credentialRetry
	authRun           sync.Mutex
	authChallenges    map[string]*providerAuthChallenge
	changeHandler     func(context.Context, providerconfig.Config, bool) error
}

type providerAuthChallenge struct {
	mu       sync.Mutex
	provider providersdk.Provider
	value    ProviderAuthChallenge
	attempts int
	config   []byte
}

const (
	providerAuthChallengeTTL = 10 * time.Minute
	providerAuthMaxAttempts  = 5
	providerAuthMaxPending   = 128
)

func (s *ProviderService) SetChangeHandler(handler func(context.Context, providerconfig.Config, bool) error) {
	s.mu.Lock()
	s.changeHandler = handler
	s.mu.Unlock()
}

type authChallengeProvider interface {
	IdentityVerificationURL() (string, bool)
	CompleteIdentityVerification(context.Context, string) (json.RawMessage, error)
}

type authChallengeExpiryProvider interface {
	IdentityVerificationExpiresAt() time.Time
}

type authenticationRequiredError interface {
	AuthenticationRequired() bool
}

type providerAnyRuntime interface {
	ProviderAny(string) (providersdk.Provider, bool)
}

// GetAuthChallenge returns the current Xiaomi identity gate for one Provider.
// Challenges live only in memory and are removed after TTL, max-attempt, or a
// successful verification.
func (s *ProviderService) GetAuthChallenge(id string) (ProviderAuthChallenge, bool) {
	id = strings.TrimSpace(id)
	s.cleanupAuthChallenges(time.Now())
	s.syncRuntimeAuthChallenge(id, time.Now())
	s.mu.RLock()
	challenge := s.authChallenges[id]
	s.mu.RUnlock()
	if challenge == nil {
		return ProviderAuthChallenge{}, false
	}
	challenge.mu.Lock()
	defer challenge.mu.Unlock()
	if !time.Now().Before(challenge.value.ExpiresAt) {
		return ProviderAuthChallenge{}, false
	}
	return challenge.value, true
}

// GetXiaomiProviderAuthChallenge is the provider-specific alias used by
// management adapters.
func (s *ProviderService) GetXiaomiProviderAuthChallenge(id string) (ProviderAuthChallenge, bool) {
	return s.GetAuthChallenge(id)
}

// VerifyAuthChallenge submits an SMS/email code to a pending Xiaomi cloud
// Provider. Successful verification replaces only the durable credential
// document, then applies a fresh Provider instance to the runtime. The
// challenge is removed before Apply so a successful code is single-use even if
// runtime reconciliation later fails.
func (s *ProviderService) VerifyAuthChallenge(ctx context.Context, id, challengeID, code string) (ProviderInfo, error) {
	id, challengeID, code = strings.TrimSpace(id), strings.TrimSpace(challengeID), strings.TrimSpace(code)
	if id == "" || challengeID == "" || code == "" {
		return ProviderInfo{}, errors.New("provider id, challengeId and verification code are required")
	}
	if s.factory == nil || s.store == nil || s.runtime == nil {
		return ProviderInfo{}, errors.New("provider management is unavailable")
	}
	s.authRun.Lock()
	defer s.authRun.Unlock()
	now := time.Now()
	s.cleanupAuthChallenges(now)
	s.syncRuntimeAuthChallenge(id, now)
	s.mu.RLock()
	challenge := s.authChallenges[id]
	current, exists := s.configs[id]
	s.mu.RUnlock()
	if challenge == nil || !exists {
		return ProviderInfo{}, errors.New("Xiaomi provider authentication challenge is missing or expired; start login again")
	}
	challenge.mu.Lock()
	if challenge.value.ChallengeID != challengeID || !time.Now().Before(challenge.value.ExpiresAt) {
		challenge.mu.Unlock()
		return ProviderInfo{}, errors.New("Xiaomi provider authentication challenge is missing or expired; start login again")
	}
	challenge.attempts++
	if challenge.attempts > providerAuthMaxAttempts {
		challenge.mu.Unlock()
		s.deleteAuthChallenge(id, challenge)
		return ProviderInfo{}, errors.New("too many Xiaomi provider authentication attempts; start login again")
	}
	verifier, ok := challenge.provider.(authChallengeProvider)
	if !ok {
		challenge.mu.Unlock()
		s.deleteAuthChallenge(id, challenge)
		return ProviderInfo{}, errors.New("Xiaomi provider does not support identity verification")
	}
	updatedConfig, err := verifier.CompleteIdentityVerification(ctx, code)
	if err != nil {
		attemptsExceeded := challenge.attempts >= providerAuthMaxAttempts
		challenge.mu.Unlock()
		if attemptsExceeded {
			s.deleteAuthChallenge(id, challenge)
		}
		return ProviderInfo{}, err
	}
	// Do not let a stale challenge overwrite an edit made while the code was
	// being entered. The comparison is performed before durable persistence.
	if !bytes.Equal(current.Config, challenge.config) {
		challenge.mu.Unlock()
		s.deleteAuthChallenge(id, challenge)
		return ProviderInfo{}, errors.New("Xiaomi provider configuration changed; start login again")
	}
	updated := current
	updated.Config = append(json.RawMessage(nil), updatedConfig...)
	replacement, err := s.factory.Create(updated)
	if err != nil {
		challenge.mu.Unlock()
		return ProviderInfo{}, err
	}
	if err := s.store.SaveProvider(ctx, updated); err != nil {
		challenge.mu.Unlock()
		return ProviderInfo{}, err
	}
	s.mu.Lock()
	s.configs[id] = updated
	delete(s.authChallenges, id)
	s.mu.Unlock()
	challenge.mu.Unlock()
	applyErr := s.runtime.Apply(ctx, replacement)
	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = challenge.provider.Close(closeContext)
	cancel()
	if applyErr != nil {
		return ProviderInfo{}, applyErr
	}
	s.bindRuntimeConfigChanges(id)
	for _, info := range s.List() {
		if info.ID == id {
			return info, nil
		}
	}
	return ProviderInfo{}, fmt.Errorf("provider %q was not verified", id)
}

// VerifyXiaomiAuthChallenge is a descriptive alias kept for HTTP adapters
// that use the provider-specific naming in their route layer.
func (s *ProviderService) VerifyXiaomiAuthChallenge(ctx context.Context, id, challengeID, code string) (ProviderInfo, error) {
	return s.VerifyAuthChallenge(ctx, id, challengeID, code)
}

// VerifyXiaomiProviderAuthChallenge is a compatibility alias for callers that
// keep the complete resource name in their application layer.
func (s *ProviderService) VerifyXiaomiProviderAuthChallenge(ctx context.Context, id, challengeID, code string) (ProviderInfo, error) {
	return s.VerifyAuthChallenge(ctx, id, challengeID, code)
}

func (s *ProviderService) syncRuntimeAuthChallenge(id string, now time.Time) {
	if id == "" {
		return
	}
	accessor, ok := s.runtime.(providerAnyRuntime)
	if !ok {
		return
	}
	provider, ok := accessor.ProviderAny(id)
	if !ok {
		return
	}
	authProvider, ok := provider.(authChallengeProvider)
	if !ok {
		return
	}
	url, ok := authProvider.IdentityVerificationURL()
	if !ok || strings.TrimSpace(url) == "" {
		return
	}
	s.mu.Lock()
	configured, configuredOK := s.configs[id]
	if !configuredOK {
		s.mu.Unlock()
		return
	}
	if _, exists := s.authChallenges[id]; exists {
		s.mu.Unlock()
		return
	}
	if len(s.authChallenges) >= providerAuthMaxPending {
		s.mu.Unlock()
		closeAuthProvider(provider)
		return
	}
	challenge := newProviderAuthChallenge(provider, url, now)
	if expiry, ok := provider.(authChallengeExpiryProvider); ok && expiry.IdentityVerificationExpiresAt().After(now) {
		challenge.value.ExpiresAt = expiry.IdentityVerificationExpiresAt()
	}
	challenge.config = append([]byte(nil), configured.Config...)
	s.authChallenges[id] = challenge
	s.mu.Unlock()
}

func (s *ProviderService) captureAuthChallengeLocked(item providerconfig.Config, instance providersdk.Provider, cause error) (ProviderAuthChallenge, bool) {
	if item.Type != "xiaomi-miot-cloud" {
		return ProviderAuthChallenge{}, false
	}
	var marker authenticationRequiredError
	if !errors.As(cause, &marker) || !marker.AuthenticationRequired() {
		return ProviderAuthChallenge{}, false
	}
	provider, ok := instance.(authChallengeProvider)
	if !ok {
		return ProviderAuthChallenge{}, false
	}
	url, ok := provider.IdentityVerificationURL()
	if !ok || strings.TrimSpace(url) == "" {
		return ProviderAuthChallenge{}, false
	}
	now := time.Now()
	s.cleanupAuthChallenges(now)
	s.mu.Lock()
	if len(s.authChallenges) >= providerAuthMaxPending {
		s.mu.Unlock()
		closeAuthProvider(instance)
		return ProviderAuthChallenge{}, false
	}
	challenge := newProviderAuthChallenge(instance, url, now)
	if expiry, ok := instance.(authChallengeExpiryProvider); ok && expiry.IdentityVerificationExpiresAt().After(now) {
		challenge.value.ExpiresAt = expiry.IdentityVerificationExpiresAt()
	}
	challenge.config = append([]byte(nil), item.Config...)
	s.authChallenges[item.ID] = challenge
	s.mu.Unlock()
	return challenge.value, true
}

func newProviderAuthChallenge(provider providersdk.Provider, url string, now time.Time) *providerAuthChallenge {
	return &providerAuthChallenge{provider: provider, value: ProviderAuthChallenge{Status: "auth_required", ChallengeID: randomProviderAuthChallengeID(), VerificationURL: strings.TrimSpace(url), ExpiresAt: now.Add(providerAuthChallengeTTL)}}
}

func randomProviderAuthChallengeID() string {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failure is exceptionally unlikely; returning a distinct
		// process-local value still prevents challenge ID reuse in this process.
		return fmt.Sprintf("provider-auth-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

func (s *ProviderService) cleanupAuthChallenges(now time.Time) {
	type expiredChallenge struct {
		id string
		*providerAuthChallenge
	}
	expired := make([]expiredChallenge, 0)
	s.mu.Lock()
	for id, challenge := range s.authChallenges {
		if now.Before(challenge.value.ExpiresAt) {
			continue
		}
		delete(s.authChallenges, id)
		expired = append(expired, expiredChallenge{id: id, providerAuthChallenge: challenge})
	}
	s.mu.Unlock()
	for _, item := range expired {
		item.mu.Lock()
		closeAuthProvider(item.provider)
		item.mu.Unlock()
	}
}

func (s *ProviderService) clearAuthChallengeLocked(id string) {
	s.mu.Lock()
	challenge := s.authChallenges[id]
	delete(s.authChallenges, id)
	s.mu.Unlock()
	if challenge != nil {
		challenge.mu.Lock()
		closeAuthProvider(challenge.provider)
		challenge.mu.Unlock()
	}
}

func (s *ProviderService) deleteAuthChallenge(id string, expected *providerAuthChallenge) {
	s.mu.Lock()
	if s.authChallenges[id] == expected {
		delete(s.authChallenges, id)
	}
	s.mu.Unlock()
	closeAuthProvider(expected.provider)
}

func closeAuthProvider(provider providersdk.Provider) {
	if provider == nil {
		return
	}
	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = provider.Close(closeContext)
	cancel()
}

// ConfiguredProviderDeviceIDs returns the explicit Device IDs controlled by a
// Provider configuration. The boolean is false for Providers whose devices
// are not represented by a devices/cameras array and therefore cannot be
// safely pruned from configuration alone.
func ConfiguredProviderDeviceIDs(item providerconfig.Config) (map[string]struct{}, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(item.Config, &object) != nil {
		return nil, false
	}
	key := "devices"
	if item.Type == "camera" {
		key = "cameras"
	}
	raw, exists := object[key]
	if !exists {
		return nil, false
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || raw[0] != '[' {
		return nil, false
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil, false
	}
	result := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if id := strings.TrimSpace(entry.ID); id != "" {
			result[id] = struct{}{}
		}
	}
	return result, true
}

// ResolveTransientConfig restores redacted secrets from the durable provider
// configuration for a short-lived operation such as gateway device discovery.
// The resolved value must never be returned by an HTTP response.
func (s *ProviderService) ResolveTransientConfig(item providerconfig.Config) (providerconfig.Config, error) {
	s.mu.RLock()
	previous := s.configs[strings.TrimSpace(item.ID)]
	s.mu.RUnlock()
	if len(item.Config) == 0 {
		item.Config = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(item.Config, &object); err != nil || object == nil {
		return providerconfig.Config{}, NewValidationError("invalid provider configuration", map[string]string{"config": "must be a JSON object"})
	}
	if err := restoreProviderSecrets(object, previous.Config); err != nil {
		return providerconfig.Config{}, NewValidationError("invalid provider configuration", map[string]string{"config": err.Error()})
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return providerconfig.Config{}, NewValidationError("invalid provider configuration", map[string]string{"config": "could not be encoded"})
	}
	item.Config = encoded
	return item, nil
}

// RuntimeProvider exposes only a currently running instance when the runtime
// supports safe lookup. Callers must keep the operation read-only.
func (s *ProviderService) RuntimeProvider(id string) (providersdk.Provider, bool) {
	accessor, ok := s.runtime.(interface {
		Provider(string) (providersdk.Provider, bool)
	})
	if !ok {
		return nil, false
	}
	return accessor.Provider(strings.TrimSpace(id))
}

func NewProviderService(configs []providerconfig.Config, store ProviderStore, factory *providersdk.Factory, runtime ProviderRuntime) *ProviderService {
	s := &ProviderService{configs: make(map[string]providerconfig.Config), store: store, factory: factory, runtime: runtime, maintenanceWake: make(chan struct{}, 1), credentialRetries: make(map[string]credentialRetry), authChallenges: make(map[string]*providerAuthChallenge)}
	for _, item := range configs {
		s.configs[item.ID] = item
		s.bindRuntimeConfigChanges(item.ID)
	}
	return s
}

// bindRuntimeConfigChanges connects a running Provider's narrowly-scoped
// self-repair notifications to durable Provider configuration. This is used
// for identities such as a Xiaomi gateway DID whose DHCP address may change;
// Providers cannot write the database directly.
func (s *ProviderService) bindRuntimeConfigChanges(id string) {
	accessor, ok := s.runtime.(providerAnyRuntime)
	if !ok {
		return
	}
	instance, ok := accessor.ProviderAny(id)
	if !ok {
		return
	}
	subscriber, ok := instance.(providersdk.RuntimeConfigChangeSubscriber)
	if !ok {
		return
	}
	subscriber.SetRuntimeConfigChangeHandler(func(previous, replacement json.RawMessage) {
		s.persistRuntimeConfigChange(id, previous, replacement)
	})
}

func (s *ProviderService) persistRuntimeConfigChange(id string, previous, replacement json.RawMessage) {
	if !json.Valid(previous) || !json.Valid(replacement) || s.store == nil {
		return
	}
	// Save serializes administrator edits, so an auto-recovered endpoint can
	// never overwrite a newer configuration submitted through the API.
	s.authRun.Lock()
	defer s.authRun.Unlock()
	s.mu.RLock()
	current, exists := s.configs[id]
	s.mu.RUnlock()
	if !exists || !bytes.Equal(current.Config, previous) {
		return
	}
	updated := current
	updated.Config = append(json.RawMessage(nil), replacement...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := s.store.SaveProvider(ctx, updated)
	cancel()
	if err != nil {
		return
	}
	s.mu.Lock()
	if currentConfig, stillCurrent := s.configs[id]; stillCurrent && bytes.Equal(currentConfig.Config, previous) {
		s.configs[id] = updated
	}
	s.mu.Unlock()
}

func (s *ProviderService) List() []ProviderInfo {
	now := time.Now()
	s.cleanupAuthChallenges(now)
	s.mu.RLock()
	runtimeIDs := make([]string, 0, len(s.configs))
	for id := range s.configs {
		runtimeIDs = append(runtimeIDs, id)
	}
	s.mu.RUnlock()
	for _, id := range runtimeIDs {
		s.syncRuntimeAuthChallenge(id, now)
	}
	configs := s.ExportConfigs()
	runtimes := make(map[string]providersdk.RuntimeInfo)
	if s.runtime != nil {
		for _, item := range s.runtime.ProviderInfos() {
			runtimes[item.Manifest.ID] = item
		}
	}
	result := make([]ProviderInfo, 0, len(configs))
	for _, item := range configs {
		info := ProviderInfo{Config: item, Status: "disabled"}
		if item.Enabled {
			info.Status = "stopped"
		}
		if live, ok := runtimes[item.ID]; ok {
			manifest := live.Manifest
			info.Manifest, info.Capabilities, info.Status, info.Error = &manifest, live.Capabilities, live.Status, live.Error
			info.RetryCount, info.NextRetryAt, info.TransitionedAt, info.Metrics, info.Diagnostics = live.RetryCount, live.NextRetryAt, live.TransitionedAt, live.Metrics, live.Diagnostics
		}
		s.mu.RLock()
		raw := s.configs[item.ID]
		retry := s.credentialRetries[item.ID]
		s.mu.RUnlock()
		if status, ok := s.credentialStatus(raw, now); ok {
			info.Credentials = &status
		}
		s.mu.RLock()
		challenge := s.authChallenges[item.ID]
		s.mu.RUnlock()
		if challenge != nil {
			challenge.mu.Lock()
			if now.Before(challenge.value.ExpiresAt) {
				value := challenge.value
				info.AuthChallenge = &value
				info.Status = value.Status
			}
			challenge.mu.Unlock()
		}
		if retry.err != "" {
			info.CredentialError = retry.err
			next := retry.next
			info.CredentialRetryAt = &next
		}
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// ExportConfigs returns a stable, detached snapshot that is safe to include in
// support artifacts. Secret values are replaced at every nesting level.
func (s *ProviderService) ExportConfigs() []providerconfig.Config {
	s.mu.RLock()
	configs := make([]providerconfig.Config, 0, len(s.configs))
	for _, item := range s.configs {
		item.Config = redactProviderConfig(item.Config)
		configs = append(configs, item)
	}
	s.mu.RUnlock()
	sort.Slice(configs, func(i, j int) bool { return configs[i].ID < configs[j].ID })
	return configs
}

var validProviderID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (s *ProviderService) Save(ctx context.Context, item providerconfig.Config) (ProviderInfo, error) {
	s.authRun.Lock()
	defer s.authRun.Unlock()
	item.ID = strings.TrimSpace(item.ID)
	item.Type = strings.TrimSpace(item.Type)
	item.Name = strings.TrimSpace(item.Name)
	if item.Type == "" {
		item.Type = "virtual"
	}
	if item.ID == "" {
		var raw [3]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return ProviderInfo{}, err
		}
		item.ID = item.Type + "-" + hex.EncodeToString(raw[:])
	}
	if item.Name == "" {
		item.Name = item.ID
	}
	s.mu.RLock()
	previous, existed := s.configs[item.ID]
	s.mu.RUnlock()
	if !validProviderID.MatchString(item.ID) {
		return ProviderInfo{}, NewValidationError("invalid provider configuration", map[string]string{"id": "may contain only letters, numbers, underscores and hyphens"})
	}
	if len(item.Config) == 0 {
		if existed {
			item.Config = append(json.RawMessage(nil), previous.Config...)
		} else {
			item.Config = json.RawMessage(`{}`)
		}
	}
	var object map[string]any
	if err := json.Unmarshal(item.Config, &object); err != nil || object == nil {
		return ProviderInfo{}, NewValidationError("invalid provider configuration", map[string]string{"config": "must be a JSON object"})
	}
	if err := restoreProviderSecrets(object, previous.Config); err != nil {
		return ProviderInfo{}, NewValidationError("invalid provider configuration", map[string]string{"config": err.Error()})
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return ProviderInfo{}, NewValidationError("invalid provider configuration", map[string]string{"config": "could not be encoded"})
	}
	item.Config = encoded
	if s.store == nil || s.factory == nil || s.runtime == nil {
		return ProviderInfo{}, errors.New("provider management is unavailable")
	}
	// Construct even disabled providers so type-specific configuration is
	// rejected before an invalid desired state reaches durable storage.
	instance, err := s.factory.Create(item)
	if err != nil {
		field := "config"
		if strings.Contains(err.Error(), "not registered") {
			field = "type"
		}
		return ProviderInfo{}, NewValidationError("invalid provider configuration", map[string]string{field: err.Error()})
	}
	if err := s.validateCameraControlProviders(item); err != nil {
		return ProviderInfo{}, err
	}
	if existed {
		if err := s.validateReferencedDeviceRemoval(previous, item); err != nil {
			return ProviderInfo{}, err
		}
	}
	if err := s.store.SaveProvider(ctx, item); err != nil {
		return ProviderInfo{}, err
	}
	s.mu.Lock()
	s.configs[item.ID] = item
	delete(s.credentialRetries, item.ID)
	s.mu.Unlock()
	s.clearAuthChallengeLocked(item.ID)
	s.wakeCredentialMaintenance()
	if item.Enabled {
		if err := s.runtime.Apply(ctx, instance); err != nil {
			if challenge, ok := s.captureAuthChallengeLocked(item, instance, err); ok {
				redacted := item
				redacted.Config = redactProviderConfig(item.Config)
				return ProviderInfo{Config: redacted, Status: challenge.Status, AuthChallenge: &challenge}, &ProviderAuthChallengeError{Challenge: challenge, cause: err}
			}
			return ProviderInfo{}, err
		}
		s.bindRuntimeConfigChanges(item.ID)
	} else {
		if existed && previous.Enabled {
			if err := s.runtime.Remove(ctx, item.ID); err != nil {
				return ProviderInfo{}, err
			}
		}
	}
	s.mu.RLock()
	changeHandler := s.changeHandler
	s.mu.RUnlock()
	if changeHandler != nil {
		if err := changeHandler(ctx, item, false); err != nil {
			return ProviderInfo{}, err
		}
	}
	for _, info := range s.List() {
		if info.ID == item.ID {
			return info, nil
		}
	}
	return ProviderInfo{}, fmt.Errorf("provider %q was not saved", item.ID)
}

// StartCredentialMaintenance starts one background scheduler for renewable
// Provider credentials. It wakes immediately after configuration changes and
// otherwise sleeps until the next token/certificate renewal boundary.
func (s *ProviderService) StartCredentialMaintenance(ctx context.Context) {
	s.maintenanceOnce.Do(func() { go s.credentialMaintenanceLoop(ctx) })
}

// RefreshDueCredentials performs one scheduler pass and is also suitable for
// an explicit administrative refresh. Failed providers are isolated and use
// exponential retry without delaying healthy providers.
func (s *ProviderService) RefreshDueCredentials(ctx context.Context, now time.Time) (time.Time, error) {
	s.credentialRun.Lock()
	defer s.credentialRun.Unlock()
	if s.factory == nil || s.store == nil || s.runtime == nil {
		return now.Add(time.Hour), errors.New("provider credential maintenance is unavailable")
	}
	s.mu.RLock()
	items := make([]providerconfig.Config, 0, len(s.configs))
	for _, item := range s.configs {
		if item.Enabled {
			items = append(items, item)
		}
	}
	s.mu.RUnlock()
	next := now.Add(time.Hour)
	var firstErr error
	for _, item := range items {
		s.mu.RLock()
		retry := s.credentialRetries[item.ID]
		s.mu.RUnlock()
		if retry.next.After(now) {
			next = earlierProviderTime(next, retry.next)
			continue
		}
		instance, err := s.factory.Create(item)
		if err != nil {
			continue
		}
		maintainer, ok := instance.(providersdk.CredentialMaintainer)
		if !ok {
			continue
		}
		if retry.applyPending {
			if err := s.runtime.Apply(ctx, instance); err != nil {
				retry = s.recordCredentialFailure(item.ID, now, err, true)
				next = earlierProviderTime(next, retry.next)
				if firstErr == nil {
					firstErr = fmt.Errorf("apply renewed provider %q credentials: %w", item.ID, err)
				}
				continue
			}
			s.mu.Lock()
			delete(s.credentialRetries, item.ID)
			s.mu.Unlock()
		}
		status, err := maintainer.CredentialStatus(now)
		if err != nil {
			retry = s.recordCredentialFailure(item.ID, now, err, false)
			next = earlierProviderTime(next, retry.next)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !status.Managed {
			continue
		}
		if status.RefreshAt.After(now) {
			next = earlierProviderTime(next, status.RefreshAt)
			continue
		}
		updatedConfig, err := maintainer.RenewCredentials(ctx)
		if err != nil {
			retry = s.recordCredentialFailure(item.ID, now, err, false)
			next = earlierProviderTime(next, retry.next)
			if firstErr == nil {
				firstErr = fmt.Errorf("renew provider %q credentials: %w", item.ID, err)
			}
			continue
		}
		updated := item
		updated.Config = updatedConfig
		replacement, err := s.factory.Create(updated)
		if err != nil {
			retry = s.recordCredentialFailure(item.ID, now, err, false)
			next = earlierProviderTime(next, retry.next)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.mu.RLock()
		current := s.configs[item.ID]
		s.mu.RUnlock()
		if !bytes.Equal(current.Config, item.Config) {
			next = earlierProviderTime(next, now.Add(time.Minute))
			continue
		}
		if err := s.store.SaveProvider(ctx, updated); err != nil {
			retry = s.recordCredentialFailure(item.ID, now, err, false)
			next = earlierProviderTime(next, retry.next)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.mu.Lock()
		s.configs[item.ID] = updated
		delete(s.credentialRetries, item.ID)
		s.mu.Unlock()
		if err := s.runtime.Apply(ctx, replacement); err != nil {
			retry = s.recordCredentialFailure(item.ID, now, err, true)
			next = earlierProviderTime(next, retry.next)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.bindRuntimeConfigChanges(item.ID)
		if renewed, ok := replacement.(providersdk.CredentialMaintainer); ok {
			if renewedStatus, statusErr := renewed.CredentialStatus(now); statusErr == nil && renewedStatus.Managed {
				next = earlierProviderTime(next, renewedStatus.RefreshAt)
			}
		}
	}
	return next, firstErr
}

func (s *ProviderService) credentialStatus(item providerconfig.Config, now time.Time) (providersdk.CredentialStatus, bool) {
	if s.factory == nil || item.ID == "" {
		return providersdk.CredentialStatus{}, false
	}
	instance, err := s.factory.Create(item)
	if err != nil {
		return providersdk.CredentialStatus{}, false
	}
	maintainer, ok := instance.(providersdk.CredentialMaintainer)
	if !ok {
		return providersdk.CredentialStatus{}, false
	}
	status, err := maintainer.CredentialStatus(now)
	return status, err == nil && status.Managed
}

func (s *ProviderService) recordCredentialFailure(id string, now time.Time, cause error, applyPending bool) credentialRetry {
	s.mu.Lock()
	retry := s.credentialRetries[id]
	retry.count++
	delay := time.Minute << min(retry.count-1, 5)
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	retry.next, retry.err = now.Add(delay), cause.Error()
	retry.applyPending = applyPending
	s.credentialRetries[id] = retry
	s.mu.Unlock()
	return retry
}

func (s *ProviderService) credentialMaintenanceLoop(ctx context.Context) {
	next := time.Now()
	for {
		delay := time.Until(next)
		if delay < 0 {
			delay = 0
		}
		if delay > time.Hour {
			delay = time.Hour
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.maintenanceWake:
			timer.Stop()
		case <-timer.C:
		}
		refreshAt, _ := s.RefreshDueCredentials(ctx, time.Now())
		next = refreshAt
	}
}

func (s *ProviderService) wakeCredentialMaintenance() {
	select {
	case s.maintenanceWake <- struct{}{}:
	default:
	}
}

func earlierProviderTime(left, right time.Time) time.Time {
	if left.IsZero() || right.Before(left) {
		return right
	}
	return left
}

func (s *ProviderService) Delete(ctx context.Context, id string) error {
	s.authRun.Lock()
	defer s.authRun.Unlock()
	s.mu.RLock()
	item, exists := s.configs[id]
	configs := make([]providerconfig.Config, 0, len(s.configs))
	for _, configured := range s.configs {
		configs = append(configs, configured)
	}
	s.mu.RUnlock()
	if !exists {
		return fmt.Errorf("provider %q not found", id)
	}
	for _, configured := range configs {
		if configured.ID == id {
			continue
		}
		references, err := providerReferences(configured)
		if err != nil {
			return fmt.Errorf("inspect provider %q references: %w", configured.ID, err)
		}
		for _, reference := range references {
			if reference.providerID == id {
				return fmt.Errorf("provider %q is still referenced by provider %q (%s)", id, configured.ID, reference.kind)
			}
		}
	}
	if item.Enabled {
		if err := s.runtime.Remove(ctx, id); err != nil {
			return err
		}
	}
	if err := s.store.DeleteProvider(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.configs, id)
	delete(s.credentialRetries, id)
	s.mu.Unlock()
	s.clearAuthChallengeLocked(id)
	s.wakeCredentialMaintenance()
	s.mu.RLock()
	changeHandler := s.changeHandler
	s.mu.RUnlock()
	if changeHandler != nil {
		if err := changeHandler(ctx, item, true); err != nil {
			return err
		}
	}
	return nil
}

type providerReference struct {
	providerID string
	deviceID   string
	kind       string
}

func providerReferences(item providerconfig.Config) ([]providerReference, error) {
	if item.Type != "camera" {
		return nil, nil
	}
	var config struct {
		Cameras []struct {
			Control *struct {
				ProviderRef string `json:"providerRef"`
				DeviceID    string `json:"deviceId"`
			} `json:"control"`
			Xiaomi *struct {
				CredentialProviderRef string `json:"credentialProviderRef"`
			} `json:"xiaomi"`
		} `json:"cameras"`
	}
	if err := json.Unmarshal(item.Config, &config); err != nil {
		return nil, fmt.Errorf("decode camera config: %w", err)
	}
	result := make([]providerReference, 0)
	for _, camera := range config.Cameras {
		if camera.Control != nil && strings.TrimSpace(camera.Control.ProviderRef) != "" {
			result = append(result, providerReference{
				providerID: strings.TrimSpace(camera.Control.ProviderRef),
				deviceID:   strings.TrimSpace(camera.Control.DeviceID),
				kind:       "camera control",
			})
		}
		if camera.Xiaomi != nil && strings.TrimSpace(camera.Xiaomi.CredentialProviderRef) != "" {
			result = append(result, providerReference{
				providerID: strings.TrimSpace(camera.Xiaomi.CredentialProviderRef),
				kind:       "camera credential",
			})
		}
	}
	return result, nil
}

func (s *ProviderService) validateCameraControlProviders(item providerconfig.Config) error {
	references, err := providerReferences(item)
	if err != nil {
		return NewValidationError("invalid provider configuration", map[string]string{"config": err.Error()})
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if item.Type != "xiaomi" && item.Type != "xiaomi-miot-cloud" {
		for _, configured := range s.configs {
			if configured.ID == item.ID {
				continue
			}
			configuredReferences, referenceErr := providerReferences(configured)
			if referenceErr != nil {
				return NewValidationError("invalid provider configuration", map[string]string{"config": referenceErr.Error()})
			}
			for _, reference := range configuredReferences {
				if reference.kind == "camera control" && reference.providerID == item.ID {
					return NewValidationError("invalid provider configuration", map[string]string{
						"type": fmt.Sprintf("provider is used as camera control source by %q and must remain xiaomi or xiaomi-miot-cloud", configured.ID),
					})
				}
			}
		}
	}
	for _, reference := range references {
		if reference.kind != "camera control" {
			continue
		}
		source, exists := s.configs[reference.providerID]
		if !exists {
			continue
		}
		if source.Type != "xiaomi" && source.Type != "xiaomi-miot-cloud" {
			return NewValidationError("invalid provider configuration", map[string]string{
				"config": fmt.Sprintf("camera control Provider %q must have type xiaomi or xiaomi-miot-cloud", reference.providerID),
			})
		}
	}
	return nil
}

func (s *ProviderService) validateReferencedDeviceRemoval(previous, replacement providerconfig.Config) error {
	previousIDs, previousKnown := ConfiguredProviderDeviceIDs(previous)
	replacementIDs, replacementKnown := ConfiguredProviderDeviceIDs(replacement)
	if !previousKnown || !replacementKnown {
		return nil
	}
	removed := make(map[string]struct{})
	for id := range previousIDs {
		if _, retained := replacementIDs[id]; !retained {
			removed[id] = struct{}{}
		}
	}
	if len(removed) == 0 {
		return nil
	}
	s.mu.RLock()
	configs := make([]providerconfig.Config, 0, len(s.configs))
	for _, configured := range s.configs {
		configs = append(configs, configured)
	}
	s.mu.RUnlock()
	for _, configured := range configs {
		if configured.ID == replacement.ID {
			continue
		}
		references, err := providerReferences(configured)
		if err != nil {
			return fmt.Errorf("inspect provider %q references: %w", configured.ID, err)
		}
		for _, reference := range references {
			if reference.providerID != replacement.ID || reference.deviceID == "" {
				continue
			}
			if _, wasRemoved := removed[reference.deviceID]; wasRemoved {
				return fmt.Errorf(
					"device %q from provider %q is still referenced by provider %q (%s)",
					reference.deviceID, replacement.ID, configured.ID, reference.kind,
				)
			}
		}
	}
	return nil
}

func (s *ProviderService) Restart(ctx context.Context, id string) (ProviderInfo, error) {
	s.mu.RLock()
	item, exists := s.configs[id]
	s.mu.RUnlock()
	if !exists {
		return ProviderInfo{}, fmt.Errorf("provider %q not found", id)
	}
	if !item.Enabled {
		return ProviderInfo{}, fmt.Errorf("provider %q is disabled", id)
	}
	if s.factory == nil || s.runtime == nil {
		return ProviderInfo{}, errors.New("provider management is unavailable")
	}
	instance, err := s.factory.Create(item)
	if err != nil {
		return ProviderInfo{}, err
	}
	if err := s.runtime.Apply(ctx, instance); err != nil {
		return ProviderInfo{}, err
	}
	s.bindRuntimeConfigChanges(id)
	for _, info := range s.List() {
		if info.ID == id {
			return info, nil
		}
	}
	return ProviderInfo{}, fmt.Errorf("provider %q was not restarted", id)
}

// TestConnection validates a provider configuration and starts a short-lived
// instance without persisting it or changing the active runtime.
func (s *ProviderService) TestConnection(ctx context.Context, item providerconfig.Config) error {
	item.ID = strings.TrimSpace(item.ID)
	item.Type = strings.TrimSpace(item.Type)
	if item.Type == "" {
		item.Type = "virtual"
	}
	if item.ID == "" {
		item.ID = item.Type + "-connection-test"
	}
	s.mu.RLock()
	previous := s.configs[item.ID]
	s.mu.RUnlock()
	if len(item.Config) == 0 {
		item.Config = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(item.Config, &object); err != nil || object == nil {
		return NewValidationError("invalid provider configuration", map[string]string{"config": "must be a JSON object"})
	}
	if err := restoreProviderSecrets(object, previous.Config); err != nil {
		return NewValidationError("invalid provider configuration", map[string]string{"config": err.Error()})
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return NewValidationError("invalid provider configuration", map[string]string{"config": "could not be encoded"})
	}
	item.Config = encoded
	if s.factory == nil {
		return errors.New("provider management is unavailable")
	}
	instance, err := s.factory.Create(item)
	if err != nil {
		return NewValidationError("invalid provider configuration", map[string]string{"config": err.Error()})
	}
	var connectionErr error
	if tester, ok := instance.(providersdk.ConnectionTester); ok {
		connectionErr = tester.TestConnection(ctx)
	} else {
		connectionErr = instance.Initialize(ctx)
	}
	if connectionErr != nil {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = instance.Close(closeContext)
		cancel()
		return connectionErr
	}
	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return instance.Close(closeContext)
}

// Scan creates a short-lived Provider and invokes its transient discovery
// capability. The configuration is never persisted or applied to the runtime;
// this is deliberately distinct from DiscoverDevices, which returns the
// configured runtime catalog.
func (s *ProviderService) Scan(ctx context.Context, item providerconfig.Config) ([]providersdk.DiscoveryCandidate, error) {
	item.ID = strings.TrimSpace(item.ID)
	item.Type = strings.TrimSpace(item.Type)
	item.Name = strings.TrimSpace(item.Name)
	if item.Type == "" {
		item.Type = "virtual"
	}
	if item.ID == "" {
		item.ID = item.Type + "-network-scan"
	}
	if item.Name == "" {
		item.Name = item.Type + " network scan"
	}
	if len(item.Config) == 0 {
		item.Config = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(item.Config, &object); err != nil || object == nil {
		return nil, NewValidationError("invalid provider configuration", map[string]string{"config": "must be a JSON object"})
	}
	s.mu.RLock()
	previous := s.configs[item.ID]
	s.mu.RUnlock()
	if err := restoreProviderSecrets(object, previous.Config); err != nil {
		return nil, NewValidationError("invalid provider configuration", map[string]string{"config": err.Error()})
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, NewValidationError("invalid provider configuration", map[string]string{"config": "could not be encoded"})
	}
	item.Config = encoded
	if s.factory == nil {
		return nil, errors.New("provider management is unavailable")
	}
	instance, err := s.factory.Create(item)
	if err != nil {
		return nil, NewValidationError("provider discovery is unavailable", map[string]string{"type": err.Error()})
	}
	scanner, ok := instance.(providersdk.DiscoveryScanner)
	if !ok {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = instance.Close(closeContext)
		cancel()
		return nil, NewValidationError("provider discovery is unavailable", map[string]string{"type": "provider does not support transient network scanning"})
	}
	items, scanErr := scanner.Scan(ctx)
	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	closeErr := instance.Close(closeContext)
	cancel()
	if scanErr != nil {
		return nil, scanErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return items, nil
}
