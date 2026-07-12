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
	Status       string                   `json:"status"`
	Error        string                   `json:"error,omitempty"`
	Manifest     *providersdk.Manifest    `json:"manifest,omitempty"`
	Capabilities providersdk.Capabilities `json:"capabilities"`
}

type ProviderService struct {
	mu      sync.RWMutex
	configs map[string]providerconfig.Config
	store   ProviderStore
	factory *providersdk.Factory
	runtime ProviderRuntime
}

func NewProviderService(configs []providerconfig.Config, store ProviderStore, factory *providersdk.Factory, runtime ProviderRuntime) *ProviderService {
	s := &ProviderService{configs: make(map[string]providerconfig.Config), store: store, factory: factory, runtime: runtime}
	for _, item := range configs {
		s.configs[item.ID] = item
	}
	return s
}

func (s *ProviderService) List() []ProviderInfo {
	s.mu.RLock()
	configs := make([]providerconfig.Config, 0, len(s.configs))
	for _, item := range s.configs {
		item.Config = append(json.RawMessage(nil), item.Config...)
		configs = append(configs, item)
	}
	s.mu.RUnlock()
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
		}
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
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
	if !validProviderID.MatchString(item.ID) {
		return ProviderInfo{}, errors.New("id may contain only letters, numbers, underscores and hyphens")
	}
	if len(item.Config) == 0 {
		item.Config = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(item.Config, &object); err != nil || object == nil {
		return ProviderInfo{}, errors.New("config must be a JSON object")
	}
	if s.store == nil || s.factory == nil || s.runtime == nil {
		return ProviderInfo{}, errors.New("provider management is unavailable")
	}
	s.mu.RLock()
	previous, existed := s.configs[item.ID]
	s.mu.RUnlock()
	if err := s.store.SaveProvider(ctx, item); err != nil {
		return ProviderInfo{}, err
	}
	s.mu.Lock()
	s.configs[item.ID] = item
	s.mu.Unlock()
	if item.Enabled {
		instance, err := s.factory.Create(item)
		if err != nil {
			return ProviderInfo{}, err
		}
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
