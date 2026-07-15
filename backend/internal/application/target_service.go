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

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	homekitqr "github.com/kradalby/homekit-qr"
)

var ErrTargetNotFound = errors.New("target not found")

type TargetInfo struct {
	ID          string                       `json:"id"`
	Type        string                       `json:"type"`
	ConsumerID  string                       `json:"consumerId"`
	Name        string                       `json:"name"`
	Enabled     bool                         `json:"enabled"`
	Status      string                       `json:"status"`
	Address     string                       `json:"address,omitempty"`
	SetupID     string                       `json:"setupId,omitempty"`
	PairingCode string                       `json:"pairingCode,omitempty"`
	SetupURI    string                       `json:"setupUri,omitempty"`
	DeviceIDs   []string                     `json:"deviceIds"`
	Devices     []domaintarget.VirtualDevice `json:"devices"`
	Error       string                       `json:"error,omitempty"`
}

type TargetRegistration struct {
	Info TargetInfo
	QR   []byte
}

type TargetService struct {
	mu           sync.RWMutex
	order        []string
	targets      map[string]TargetRegistration
	configs      map[string]domaintarget.Config
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
	ResetPairing(context.Context, domaintarget.Config) (TargetRegistration, error)
}

func NewTargetService(registrations []TargetRegistration, store TargetStore, configs ...domaintarget.Config) *TargetService {
	service := &TargetService{targets: make(map[string]TargetRegistration), configs: make(map[string]domaintarget.Config), store: store, listeners: make(map[uint64]*targetSubscription)}
	for _, registration := range registrations {
		service.order = append(service.order, registration.Info.ID)
		service.targets[registration.Info.ID] = registration
	}
	for _, config := range configs {
		service.configs[config.ID] = config
	}
	return service
}

func (s *TargetService) SetRuntime(runtime TargetRuntime) { s.runtime = runtime }

// Refresh reapplies enabled Target configurations after Consumer mapping
// changes. Pairing identity remains in the existing store path while the
// accessory graph is rebuilt from the latest Consumer projection.
func (s *TargetService) Refresh(ctx context.Context) error {
	s.mu.RLock()
	runtime := s.runtime
	configs := make([]domaintarget.Config, 0, len(s.configs))
	for _, item := range s.configs {
		if item.Enabled {
			configs = append(configs, item)
		}
	}
	s.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	for _, item := range configs {
		registration, err := runtime.Apply(ctx, item)
		if err != nil {
			return fmt.Errorf("refresh target %q: %w", item.ID, err)
		}
		registration.Info.ConsumerID = targetConsumerID(item.Type)
		s.mu.Lock()
		s.targets[item.ID] = registration
		s.mu.Unlock()
		s.notify(registration.Info)
	}
	return nil
}

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
		ID: item.ID, Type: item.Type, ConsumerID: targetConsumerID(item.Type), Name: item.Name, Enabled: item.Enabled,
		Status: "disabled", Address: item.Address, SetupID: item.SetupID,
		DeviceIDs: append([]string{}, item.DeviceIDs...),
		Devices:   append([]domaintarget.VirtualDevice(nil), item.Devices...),
	}
	registration := TargetRegistration{Info: info}
	if s.runtime != nil {
		applied, applyErr := s.runtime.Apply(ctx, item)
		if applyErr != nil {
			registration.Info.Status = "error"
			registration.Info.Error = applyErr.Error()
		} else {
			registration = applied
			registration.Info.ConsumerID = targetConsumerID(item.Type)
		}
	} else if descriptor, found := domaintarget.DescriptorForType(item.Type); found && descriptor.SupportsHomeKitPairing {
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
	s.configs[item.ID] = item
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
		prefix := "target"
		if descriptor, found := domaintarget.DescriptorForType(item.Type); found {
			prefix = descriptor.DefaultIDPrefix
		}
		item.ID = prefix + "-" + suffix
	}
	if !validTargetID.MatchString(item.ID) {
		return item, NewValidationError("invalid target configuration", map[string]string{"id": "may contain only letters, numbers, underscores and hyphens"})
	}
	if item.Name == "" {
		item.Name = "HomeLoom Target"
		if descriptor, found := domaintarget.DescriptorForType(item.Type); found {
			item.Name = descriptor.DefaultName
		}
	}
	descriptor, _ := domaintarget.DescriptorForType(item.Type)
	if descriptor.SupportsHomeKitPairing {
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
	if len(item.Devices) == 0 && len(item.DeviceIDs) > 0 {
		for _, id := range item.DeviceIDs {
			item.Devices = append(item.Devices, domaintarget.VirtualDevice{ID: id, Name: id, SourceDeviceID: id, Enabled: true})
		}
	}
	item.DeviceIDs = item.DeviceIDs[:0]
	for _, current := range item.Devices {
		if current.Enabled {
			item.DeviceIDs = append(item.DeviceIDs, current.SourceDeviceID)
		}
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
	delete(s.configs, id)
	for index, current := range s.order {
		if current == id {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *TargetService) RegeneratePairing(ctx context.Context, id string) (TargetInfo, error) {
	s.mu.RLock()
	config, ok := s.configs[id]
	s.mu.RUnlock()
	if !ok {
		return TargetInfo{}, fmt.Errorf("%w: %s", ErrTargetNotFound, id)
	}
	descriptor, _ := domaintarget.DescriptorForType(config.Type)
	if !descriptor.SupportsHomeKitPairing {
		return TargetInfo{}, fmt.Errorf("target %q does not support HomeKit pairing", id)
	}
	pin, err := randomString("0123456789", 8)
	if err != nil {
		return TargetInfo{}, fmt.Errorf("generate target pin: %w", err)
	}
	setupID, err := randomString("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", 4)
	if err != nil {
		return TargetInfo{}, fmt.Errorf("generate target setup id: %w", err)
	}
	config.Pin, config.SetupID = pin, setupID
	return s.Save(ctx, config)
}

func (s *TargetService) ClearPairingIdentity(ctx context.Context, id string) (TargetInfo, error) {
	s.mu.RLock()
	config, ok := s.configs[id]
	s.mu.RUnlock()
	if !ok {
		return TargetInfo{}, fmt.Errorf("%w: %s", ErrTargetNotFound, id)
	}
	descriptor, _ := domaintarget.DescriptorForType(config.Type)
	if !descriptor.SupportsHomeKitPairing {
		return TargetInfo{}, fmt.Errorf("target %q does not support HomeKit pairing", id)
	}
	if s.runtime == nil {
		return TargetInfo{}, errors.New("target runtime is unavailable")
	}
	registration, err := s.runtime.ResetPairing(ctx, config)
	if err != nil {
		s.SetStatus(id, "error")
		return TargetInfo{}, err
	}
	s.mu.Lock()
	s.targets[id] = registration
	s.mu.Unlock()
	s.notify(registration.Info)
	return registration.Info, nil
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
	descriptor, supportedType := domaintarget.DescriptorForType(item.Type)
	if !supportedType {
		fields["type"] = fmt.Sprintf("unsupported target type %q", item.Type)
	}
	if descriptor.SupportsHomeKitPairing {
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
	seenIDs := make(map[string]bool)
	for index, current := range item.Devices {
		prefix := fmt.Sprintf("devices.%d", index)
		if !validTargetID.MatchString(current.ID) {
			fields[prefix+".id"] = "may contain only letters, numbers, underscores and hyphens"
		}
		if strings.TrimSpace(current.Name) == "" {
			fields[prefix+".name"] = "required"
		}
		if strings.TrimSpace(current.SourceDeviceID) == "" {
			fields[prefix+".sourceDeviceId"] = "required"
		} else if !device.ValidStableID(current.SourceDeviceID) {
			fields[prefix+".sourceDeviceId"] = "must reference a stable unified device ID"
		}
		if current.Type != "" {
			if _, supported := device.ModelContractFor(current.Type); !supported {
				fields[prefix+".type"] = "must reference a supported unified device model"
			}
		}
		if seenIDs[current.ID] {
			fields[prefix+".id"] = "must be unique within the target instance"
		}
		seenIDs[current.ID] = true
	}
	if len(fields) > 0 {
		return NewValidationError("invalid target configuration", fields)
	}
	return nil
}

func targetConsumerID(targetType string) string {
	descriptor, _ := domaintarget.DescriptorForType(targetType)
	return descriptor.ConsumerID
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
