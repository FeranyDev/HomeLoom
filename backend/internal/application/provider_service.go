package application

import (
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
	Status         string                   `json:"status"`
	Error          string                   `json:"error,omitempty"`
	Manifest       *providersdk.Manifest    `json:"manifest,omitempty"`
	Capabilities   providersdk.Capabilities `json:"capabilities"`
	RetryCount     int                      `json:"retryCount"`
	NextRetryAt    *time.Time               `json:"nextRetryAt,omitempty"`
	TransitionedAt time.Time                `json:"transitionedAt,omitempty"`
	Metrics        map[string]uint64        `json:"metrics,omitempty"`
}

type ProviderService struct {
	mu      sync.RWMutex
	configs map[string]providerconfig.Config
	store   ProviderStore
	factory *providersdk.Factory
	runtime ProviderRuntime
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
	s := &ProviderService{configs: make(map[string]providerconfig.Config), store: store, factory: factory, runtime: runtime}
	for _, item := range configs {
		s.configs[item.ID] = item
	}
	return s
}

func (s *ProviderService) List() []ProviderInfo {
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
			info.RetryCount, info.NextRetryAt, info.TransitionedAt, info.Metrics = live.RetryCount, live.NextRetryAt, live.TransitionedAt, live.Metrics
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
	if err := s.store.SaveProvider(ctx, item); err != nil {
		return ProviderInfo{}, err
	}
	s.mu.Lock()
	s.configs[item.ID] = item
	s.mu.Unlock()
	if item.Enabled {
		if err := s.runtime.Apply(ctx, instance); err != nil {
			return ProviderInfo{}, err
		}
	} else {
		if existed && previous.Enabled {
			if err := s.runtime.Remove(ctx, item.ID); err != nil {
				return ProviderInfo{}, err
			}
		}
	}
	for _, info := range s.List() {
		if info.ID == item.ID {
			return info, nil
		}
	}
	return ProviderInfo{}, fmt.Errorf("provider %q was not saved", item.ID)
}

func (s *ProviderService) Delete(ctx context.Context, id string) error {
	s.mu.RLock()
	item, exists := s.configs[id]
	s.mu.RUnlock()
	if !exists {
		return fmt.Errorf("provider %q not found", id)
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
	s.mu.Unlock()
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
	if err := instance.Initialize(ctx); err != nil {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = instance.Close(closeContext)
		cancel()
		return err
	}
	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return instance.Close(closeContext)
}
