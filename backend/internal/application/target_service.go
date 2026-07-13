package application

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	homekitqr "github.com/kradalby/homekit-qr"
)

type TargetInfo struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
	Status      string   `json:"status"`
	Address     string   `json:"address,omitempty"`
	SetupID     string   `json:"setupId,omitempty"`
	PairingCode string   `json:"pairingCode,omitempty"`
	SetupURI    string   `json:"setupUri,omitempty"`
	DeviceIDs   []string `json:"deviceIds"`
	Error       string   `json:"error,omitempty"`
}

type TargetRegistration struct {
	Info TargetInfo
	QR   []byte
}

type TargetService struct {
	mu           sync.RWMutex
	order        []string
	targets      map[string]TargetRegistration
	store        TargetStore
	runtime      TargetRuntime
	nextListener uint64
	listeners    map[uint64]*targetSubscription
}

type targetSubscription struct {
	queue chan TargetInfo
	done  chan struct{}
}

type TargetStore interface {
	SaveTarget(context.Context, domaintarget.Config) error
	DeleteTarget(context.Context, string) error
}

type TargetRuntime interface {
	Apply(context.Context, domaintarget.Config) (TargetRegistration, error)
	Remove(context.Context, string) error
}

func NewTargetService(registrations []TargetRegistration, store TargetStore) *TargetService {
	service := &TargetService{targets: make(map[string]TargetRegistration), store: store, listeners: make(map[uint64]*targetSubscription)}
	for _, registration := range registrations {
		service.order = append(service.order, registration.Info.ID)
		service.targets[registration.Info.ID] = registration
	}
	return service
}

func (s *TargetService) SetRuntime(runtime TargetRuntime) { s.runtime = runtime }

func (s *TargetService) Save(ctx context.Context, item domaintarget.Config) (TargetInfo, error) {
	s.mu.RLock()
	item, err := s.withDefaults(item)
	s.mu.RUnlock()
	if err != nil {
		return TargetInfo{}, err
	}
	if err := validateTarget(item); err != nil {
		return TargetInfo{}, err
	}
	if s.store == nil {
		return TargetInfo{}, errors.New("target store is unavailable")
	}
	if err := s.store.SaveTarget(ctx, item); err != nil {
		return TargetInfo{}, err
	}
	info := TargetInfo{
		ID: item.ID, Type: item.Type, Name: item.Name, Enabled: item.Enabled,
		Status: "disabled", Address: item.Address, SetupID: item.SetupID,
		DeviceIDs: append([]string{}, item.DeviceIDs...),
	}
	registration := TargetRegistration{Info: info}
	if s.runtime != nil {
		applied, applyErr := s.runtime.Apply(ctx, item)
		if applyErr != nil {
			registration.Info.Status = "error"
			registration.Info.Error = applyErr.Error()
		} else {
			registration = applied
		}
	} else if item.Type == "apple-hap" {
		qrConfig := homekitqr.QRCodeConfig{SetupURIConfig: homekitqr.SetupURIConfig{
			Category: homekitqr.CategoryBridge, Flag: 2, PairingCode: item.Pin, SetupID: item.SetupID,
		}, Size: 320}
		uri, err := homekitqr.ComposeSetupURI(qrConfig.SetupURIConfig)
		if err != nil {
			return TargetInfo{}, err
		}
		qr, err := homekitqr.GenerateQRPNG(qrConfig)
		if err != nil {
			return TargetInfo{}, err
		}
		registration.Info.PairingCode = homekitqr.FormatPairingCode(item.Pin)
		registration.Info.SetupURI = uri
		registration.QR = qr
	}
	s.mu.Lock()
	if _, exists := s.targets[item.ID]; !exists {
		s.order = append(s.order, item.ID)
	}
	s.targets[item.ID] = registration
	s.mu.Unlock()
	s.notify(registration.Info)
	return registration.Info, nil
}

var validTargetID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (s *TargetService) withDefaults(item domaintarget.Config) (domaintarget.Config, error) {
	if item.Type == "" {
		item.Type = "apple-hap"
	}
	if item.ID == "" {
		suffix, err := randomString("abcdefghijklmnopqrstuvwxyz0123456789", 6)
		if err != nil {
			return item, fmt.Errorf("generate target id: %w", err)
		}
		item.ID = "apple-" + suffix
	}
	if !validTargetID.MatchString(item.ID) {
		return item, NewValidationError("invalid target configuration", map[string]string{"id": "may contain only letters, numbers, underscores and hyphens"})
	}
	if item.Name == "" {
		item.Name = "HomeLoom Bridge"
	}
	if item.Type == "apple-hap" {
		if item.Address == "" {
			item.Address = s.nextAddress()
		}
		if item.Pin == "" {
			pin, err := randomString("0123456789", 8)
			if err != nil {
				return item, fmt.Errorf("generate pin: %w", err)
			}
			item.Pin = pin
		}
		if item.SetupID == "" {
			setupID, err := randomString("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", 4)
			if err != nil {
				return item, fmt.Errorf("generate setup id: %w", err)
			}
			item.SetupID = setupID
		}
		item.StorePath = filepath.Join("data", "hap", item.ID)
	}
	return item, nil
}

func (s *TargetService) nextAddress() string {
	used := make(map[int]bool)
	for _, registration := range s.targets {
		_, portText, err := net.SplitHostPort(registration.Info.Address)
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err == nil {
			used[port] = true
		}
	}
	for port := 51826; port <= 65535; port++ {
		if !used[port] {
			return fmt.Sprintf(":%d", port)
		}
	}
	return ":0"
}

func randomString(alphabet string, length int) (string, error) {
	result := make([]byte, length)
	random := make([]byte, length)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	for index, value := range random {
		result[index] = alphabet[int(value)%len(alphabet)]
	}
	return string(result), nil
}

func (s *TargetService) Delete(ctx context.Context, id string) error {
	if s.store == nil {
		return errors.New("target store is unavailable")
	}
	if err := s.store.DeleteTarget(ctx, id); err != nil {
		return err
	}
	if s.runtime != nil {
		if err := s.runtime.Remove(ctx, id); err != nil {
			return err
		}
	}
	s.mu.Lock()
	delete(s.targets, id)
	for index, current := range s.order {
		if current == id {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	s.mu.Unlock()
	return nil
}

func validateTarget(item domaintarget.Config) error {
	fields := make(map[string]string)
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Name) == "" {
		if strings.TrimSpace(item.ID) == "" {
			fields["id"] = "required"
		}
		if strings.TrimSpace(item.Name) == "" {
			fields["name"] = "required"
		}
	}
	if item.Type != "apple-hap" && item.Type != "matter" {
		fields["type"] = fmt.Sprintf("unsupported target type %q", item.Type)
	}
	if item.Type == "apple-hap" {
		if item.Address == "" {
			fields["address"] = "required"
		}
		if len(item.Pin) != 8 {
			fields["pin"] = "must contain 8 digits"
		}
		if len(item.SetupID) != 4 {
			fields["setupId"] = "must contain 4 characters"
		}
		if item.StorePath == "" {
			fields["storePath"] = "required"
		}
		for _, char := range item.Pin {
			if char < '0' || char > '9' {
				fields["pin"] = "must contain only digits"
				break
			}
		}
	}
	if len(fields) > 0 {
		return NewValidationError("invalid target configuration", fields)
	}
	return nil
}

func (s *TargetService) List() []TargetInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]TargetInfo, 0, len(s.order))
	for _, id := range s.order {
		result = append(result, s.targets[id].Info)
	}
	return result
}

func (s *TargetService) QR(id string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	registration, ok := s.targets[id]
	if !ok || len(registration.QR) == 0 {
		return nil, errors.New("pairing QR code not found")
	}
	return append([]byte(nil), registration.QR...), nil
}

func (s *TargetService) SetStatus(id, status string) {
	s.mu.Lock()
	registration, ok := s.targets[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	registration.Info.Status = status
	s.targets[id] = registration
	s.mu.Unlock()
	s.notify(registration.Info)
}

func (s *TargetService) Subscribe(handler func(TargetInfo)) func() {
	s.mu.Lock()
	s.nextListener++
	id := s.nextListener
	subscription := &targetSubscription{queue: make(chan TargetInfo, 16), done: make(chan struct{})}
	s.listeners[id] = subscription
	s.mu.Unlock()
	go func() {
		defer close(subscription.done)
		for info := range subscription.queue {
			handler(info)
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			current, ok := s.listeners[id]
			if ok {
				delete(s.listeners, id)
				close(current.queue)
			}
			s.mu.Unlock()
			if ok {
				<-current.done
			}
		})
	}
}

func (s *TargetService) notify(info TargetInfo) {
	s.mu.RLock()
	for _, subscription := range s.listeners {
		select {
		case subscription.queue <- info:
		default:
		}
	}
	s.mu.RUnlock()
}
