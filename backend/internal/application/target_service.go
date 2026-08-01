package application

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	homekitqr "github.com/kradalby/homekit-qr"
	qrcode "github.com/skip2/go-qrcode"
)

var ErrTargetNotFound = errors.New("target not found")

// TargetIssue captures a device-level bridge projection or contract problem that
// prevented an accessory/endpoint from being published. Targets may still run
// with a partial accessory graph when issues are present.
type TargetIssue struct {
	DeviceID   string `json:"deviceId,omitempty"`
	DeviceName string `json:"deviceName,omitempty"`
	DeviceType string `json:"deviceType,omitempty"`
	Stage      string `json:"stage"`
	Message    string `json:"message"`
}

type TargetInfo struct {
	ID                           string                       `json:"id"`
	Type                         string                       `json:"type"`
	ConsumerID                   string                       `json:"consumerId"`
	Name                         string                       `json:"name"`
	Enabled                      bool                         `json:"enabled"`
	Status                       string                       `json:"status"`
	Address                      string                       `json:"address,omitempty"`
	SetupID                      string                       `json:"setupId,omitempty"`
	PairingCode                  string                       `json:"pairingCode,omitempty"`
	SetupURI                     string                       `json:"setupUri,omitempty"`
	Paired                       bool                         `json:"paired"`
	DeviceIDs                    []string                     `json:"deviceIds"`
	Devices                      []domaintarget.VirtualDevice `json:"devices"`
	Error                        string                       `json:"error,omitempty"`
	Issues                       []TargetIssue                `json:"issues,omitempty"`
	Diagnostics                  map[string]string            `json:"diagnostics,omitempty"`
	NetworkInterface             string                       `json:"networkInterface,omitempty"`
	UDPPort                      uint16                       `json:"udpPort,omitempty"`
	Discriminator                uint16                       `json:"discriminator,omitempty"`
	VendorID                     uint16                       `json:"vendorId,omitempty"`
	ProductID                    uint16                       `json:"productId,omitempty"`
	ProductName                  string                       `json:"productName,omitempty"`
	SerialNumber                 string                       `json:"serialNumber,omitempty"`
	CommissioningWindowSeconds   uint32                       `json:"commissioningWindowSeconds,omitempty"`
	CommissioningState           string                       `json:"commissioningState,omitempty"`
	CommissioningWindowOpen      bool                         `json:"commissioningWindowOpen,omitempty"`
	CommissioningWindowExpiresAt string                       `json:"commissioningWindowExpiresAt,omitempty"`
	ManualPairingCode            string                       `json:"manualPairingCode,omitempty"`
	SetupPayload                 string                       `json:"setupPayload,omitempty"`
	FabricCount                  int                          `json:"fabricCount,omitempty"`
	Fabrics                      []MatterFabric               `json:"fabrics,omitempty"`
	EndpointCount                int                          `json:"endpointCount,omitempty"`
	ProtocolVersion              string                       `json:"protocolVersion,omitempty"`
	Certification                string                       `json:"certification,omitempty"`
	Removed                      bool                         `json:"removed,omitempty"`
}

type MatterFabric struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
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
	IsPaired(domaintarget.Config) bool
}

type TargetDeletionRuntime interface {
	RemoveTarget(context.Context, domaintarget.Config) error
}

type MatterTargetRuntime interface {
	OpenCommissioningWindow(context.Context, string, uint32) (TargetRegistration, error)
	CloseCommissioningWindow(context.Context, string) (TargetRegistration, error)
	RemoveFabric(context.Context, string, string) (TargetRegistration, error)
	FactoryResetMatter(context.Context, string) (TargetRegistration, error)
}

type MatterEndpointTypeRuntime interface {
	ConfirmMatterEndpointDeviceType(context.Context, string, string, device.Type) error
}

type TargetRuntimeInfo interface {
	RuntimeInfo(domaintarget.Config) TargetInfo
}

func NewTargetService(registrations []TargetRegistration, store TargetStore, configs ...domaintarget.Config) *TargetService {
	service := &TargetService{targets: make(map[string]TargetRegistration), configs: make(map[string]domaintarget.Config), store: store, listeners: make(map[uint64]*targetSubscription)}
	for _, registration := range registrations {
		service.order = append(service.order, registration.Info.ID)
		service.targets[registration.Info.ID] = registration
	}
	for _, config := range configs {
		config = config.NormalizeProtocolConfig()
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
		descriptor, _ := domaintarget.DescriptorForType(item.Type)
		if descriptor.SupportsHomeKitPairing {
			registration.Info.Paired = runtime.IsPaired(item)
			registration.Info = pairingSafeInfo(registration.Info)
		}
		s.mu.Lock()
		s.targets[item.ID] = registration
		s.mu.Unlock()
		s.notify(registration.Info)
	}
	return nil
}

func (s *TargetService) Save(ctx context.Context, item domaintarget.Config) (TargetInfo, error) {
	s.mu.RLock()
	existing, editing := s.configs[item.ID]
	runtime := s.runtime
	if editing && item.Type != existing.Type {
		s.mu.RUnlock()
		return TargetInfo{}, NewValidationError("invalid target configuration", map[string]string{"type": "cannot be changed after the target is created"})
	}
	wasPaired := editing && s.targets[item.ID].Info.Paired
	descriptor, _ := domaintarget.DescriptorForType(existing.Type)
	if editing && descriptor.SupportsHomeKitPairing && runtime != nil {
		wasPaired = runtime.IsPaired(existing)
	}
	if editing {
		if existing.Type == "apple-hap" || existing.Type == "homekit-camera" {
			next := domaintarget.HomeKitConfig{}
			if item.HomeKitConfig != nil {
				next = *item.HomeKitConfig
			}
			if next.Address == "" {
				next.Address = item.Address
			}
			if next.Pin == "" {
				next.Pin = item.Pin
			}
			if next.SetupID == "" {
				next.SetupID = item.SetupID
			}
			if next.StorePath == "" {
				next.StorePath = item.StorePath
			}
			if next.Pin == "" || wasPaired {
				next.Pin = existing.Pin
			}
			if next.SetupID == "" || wasPaired {
				next.SetupID = existing.SetupID
			}
			item.HomeKitConfig = &next
			item.Address, item.Pin, item.SetupID, item.StorePath = next.Address, next.Pin, next.SetupID, next.StorePath
		}
		if domaintarget.IsMatterType(existing.Type) {
			previous := domaintarget.MatterConfig{}
			if existing.MatterConfig != nil {
				previous = *existing.MatterConfig
			}
			next := previous
			if item.MatterConfig != nil {
				next = *item.MatterConfig
			}
			if next.Passcode == "" {
				next.Passcode = previous.Passcode
			}
			if next.Discriminator == nil {
				next.Discriminator = previous.Discriminator
			}
			item.MatterConfig = &next
		}
	}
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
	applyMatterConfigInfo(&info, item)
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
		category := homekitqr.CategoryBridge
		if item.Type == "homekit-camera" {
			category = homekitqr.CategoryIPCamera
		}
		qrConfig := homekitqr.QRCodeConfig{SetupURIConfig: homekitqr.SetupURIConfig{
			Category: category, Flag: 2, PairingCode: item.Pin, SetupID: item.SetupID,
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
	descriptor, _ = domaintarget.DescriptorForType(item.Type)
	if descriptor.SupportsHomeKitPairing && runtime != nil {
		registration.Info.Paired = runtime.IsPaired(item)
		registration.Info = pairingSafeInfo(registration.Info)
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

func applyMatterConfigInfo(info *TargetInfo, config domaintarget.Config) {
	if !domaintarget.IsMatterType(config.Type) || config.MatterConfig == nil {
		return
	}
	matter := config.MatterConfig
	info.NetworkInterface, info.UDPPort = matter.NetworkInterface, matter.UDPPort
	if matter.Discriminator != nil {
		info.Discriminator = *matter.Discriminator
	}
	info.VendorID, info.ProductID = matter.VendorID, matter.ProductID
	info.ProductName, info.SerialNumber = matter.ProductName, matter.SerialNumber
	info.CommissioningWindowSeconds = matter.CommissioningWindowSeconds
	info.ProtocolVersion, info.Certification = "1.6.0", "test"
	if info.CommissioningState == "" {
		info.CommissioningState = "uncommissioned"
	}
}

// TargetInfoFromConfig creates the non-sensitive public projection used when a
// runtime cannot start. Commissioning passcodes and identity material are
// intentionally absent.
func TargetInfoFromConfig(config domaintarget.Config, status string) TargetInfo {
	descriptor, _ := domaintarget.DescriptorForType(config.Type)
	info := TargetInfo{
		ID: config.ID, Type: config.Type, ConsumerID: descriptor.ConsumerID,
		Name: config.Name, Enabled: config.Enabled, Status: status,
		Address: config.Address, SetupID: config.SetupID,
		DeviceIDs: append([]string(nil), config.DeviceIDs...),
		Devices:   append([]domaintarget.VirtualDevice(nil), config.Devices...),
	}
	applyMatterConfigInfo(&info, config)
	return info
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
		if item.MatterConfig != nil {
			return item, NewValidationError("invalid target configuration", map[string]string{"matterConfig": "is only valid for Matter targets"})
		}
		item = item.NormalizeProtocolConfig()
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
		item = item.NormalizeProtocolConfig()
	}
	if domaintarget.IsMatterType(item.Type) {
		if item.HomeKitConfig != nil || item.Address != "" || item.Pin != "" || item.SetupID != "" || item.StorePath != "" {
			return item, NewValidationError("invalid target configuration", map[string]string{"homeKitConfig": "is only valid for HomeKit targets"})
		}
		config := domaintarget.MatterConfig{}
		if item.MatterConfig != nil {
			config = *item.MatterConfig
		}
		if config.Discriminator == nil {
			value, err := randomMatterDiscriminator()
			if err != nil {
				return item, fmt.Errorf("generate Matter discriminator: %w", err)
			}
			config.Discriminator = &value
		}
		if config.Passcode == "" {
			passcode, err := randomMatterPasscode()
			if err != nil {
				return item, fmt.Errorf("generate Matter passcode: %w", err)
			}
			config.Passcode = passcode
		}
		if config.VendorID == 0 {
			config.VendorID = domaintarget.DefaultMatterVendorID
		}
		if config.ProductID == 0 {
			config.ProductID = domaintarget.DefaultMatterProductID
		}
		if config.ProductName == "" {
			config.ProductName = "HomeLoom Matter Bridge"
			if item.Type == "matter-camera" {
				config.ProductName = "HomeLoom Matter Camera"
			}
		}
		if config.SerialNumber == "" {
			config.SerialNumber = item.ID
		}
		if config.CommissioningWindowSeconds == 0 {
			config.CommissioningWindowSeconds = domaintarget.DefaultMatterCommissioningWindowSeconds
		}
		item.MatterConfig = &config
		item = item.NormalizeProtocolConfig()
	}
	if len(item.Devices) == 0 && len(item.DeviceIDs) > 0 {
		for _, id := range item.DeviceIDs {
			item.Devices = append(item.Devices, domaintarget.VirtualDevice{ID: id, Name: id, SourceDeviceID: id, Enabled: true})
		}
	}
	item.DeviceIDs = item.DeviceIDs[:0]
	seenSourceIDs := make(map[string]struct{})
	for _, current := range item.Devices {
		if current.Enabled {
			for _, sourceID := range current.SourceDeviceIDs() {
				if _, exists := seenSourceIDs[sourceID]; exists {
					continue
				}
				seenSourceIDs[sourceID] = struct{}{}
				item.DeviceIDs = append(item.DeviceIDs, sourceID)
			}
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

func randomMatterDiscriminator() (uint16, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(4096))
	if err != nil {
		return 0, err
	}
	return uint16(value.Uint64()), nil
}

func randomMatterPasscode() (string, error) {
	max := big.NewInt(99_999_998)
	for {
		random, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		value := random.Uint64() + 1
		passcode := fmt.Sprintf("%08d", value)
		if validMatterPasscode(passcode) {
			return passcode, nil
		}
	}
}

func (s *TargetService) Delete(ctx context.Context, id string) error {
	if s.store == nil {
		return errors.New("target store is unavailable")
	}
	s.mu.RLock()
	config, hasConfig := s.configs[id]
	runtime := s.runtime
	s.mu.RUnlock()
	cameraCleanupCompleted := false
	if hasConfig && config.Type == "homekit-camera" {
		// Independent publishers own pairing state outside the target row.
		// Destroy that state before committing deletion so a cleanup failure
		// cannot be reported as a successfully deleted target.
		deletionRuntime, ok := runtime.(TargetDeletionRuntime)
		if !ok {
			return errors.New("remove target runtime and pairing identity: deletion runtime is unavailable")
		}
		if err := deletionRuntime.RemoveTarget(ctx, config); err != nil {
			return fmt.Errorf("remove target runtime and pairing identity: %w", err)
		}
		cameraCleanupCompleted = true
	}
	if err := s.store.DeleteTarget(ctx, id); err != nil {
		return err
	}
	if runtime != nil {
		// Persistence is the source of truth for deletion. Runtime shutdown is
		// best-effort here: Manager.Remove detaches and cancels the runtime
		// before it can return a timeout, so a slow sidecar must not leave the
		// already-deleted target visible in the in-memory projection.
		if !cameraCleanupCompleted {
			_ = runtime.Remove(ctx, id)
		}
	}
	s.mu.Lock()
	registration := s.targets[id]
	delete(s.targets, id)
	delete(s.configs, id)
	for index, current := range s.order {
		if current == id {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	s.mu.Unlock()
	registration.Info.ID = id
	registration.Info.Removed = true
	s.notify(registration.Info)
	return nil
}

func (s *TargetService) RegeneratePairing(ctx context.Context, id string) (TargetInfo, error) {
	s.mu.RLock()
	config, ok := s.configs[id]
	runtime := s.runtime
	paired := ok && s.targets[id].Info.Paired
	s.mu.RUnlock()
	if !ok {
		return TargetInfo{}, fmt.Errorf("%w: %s", ErrTargetNotFound, id)
	}
	descriptor, _ := domaintarget.DescriptorForType(config.Type)
	if !descriptor.SupportsHomeKitPairing {
		return TargetInfo{}, fmt.Errorf("target %q does not support HomeKit pairing", id)
	}
	if runtime != nil {
		paired = runtime.IsPaired(config)
	}
	if paired {
		return TargetInfo{}, errors.New("HomeKit target is already paired; clear the pairing identity before generating new setup parameters")
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
	if config.HomeKitConfig != nil {
		next := *config.HomeKitConfig
		next.Pin, next.SetupID = pin, setupID
		config.HomeKitConfig = &next
	}
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
	if item.Type == "homekit-camera" {
		if len(item.Devices) != 1 {
			fields["devices"] = "must contain exactly one camera"
		} else {
			camera := item.Devices[0]
			if camera.Type != device.TypeCamera {
				fields["devices.0.type"] = "must be camera"
			}
			if len(camera.AuxiliarySourceDeviceIDs) != 0 {
				fields["devices.0.auxiliarySourceDeviceIds"] = "HomeKit Camera does not support auxiliary sources"
			}
			if !camera.Enabled {
				fields["devices.0.enabled"] = "camera publication must be enabled"
			}
		}
	}
	if item.Type == "matter-camera" {
		if len(item.Devices) != 1 {
			fields["devices"] = "must contain exactly one camera"
		} else {
			camera := item.Devices[0]
			if camera.Type != device.TypeCamera {
				fields["devices.0.type"] = "must be camera"
			}
			if len(camera.AuxiliarySourceDeviceIDs) != 0 {
				fields["devices.0.auxiliarySourceDeviceIds"] = "Matter Camera does not support auxiliary sources"
			}
			if !camera.Enabled {
				fields["devices.0.enabled"] = "camera publication must be enabled"
			}
		}
	}
	if domaintarget.IsMatterType(item.Type) {
		config := item.MatterConfig
		if config == nil {
			fields["matterConfig"] = "required"
		} else {
			if config.UDPPort > 0 && config.UDPPort < 1024 {
				fields["matterConfig.udpPort"] = "must be zero for automatic allocation or between 1024 and 65535"
			}
			if config.Discriminator == nil || *config.Discriminator > 4095 {
				fields["matterConfig.discriminator"] = "must be between 0 and 4095"
			}
			if !validMatterPasscode(config.Passcode) {
				fields["matterConfig.passcode"] = "must be a non-reserved 8-digit Matter passcode"
			}
			if config.VendorID == 0 {
				fields["matterConfig.vendorId"] = "must be between 1 and 65535"
			}
			if config.ProductID == 0 {
				fields["matterConfig.productId"] = "must be between 1 and 65535"
			}
			if strings.TrimSpace(config.ProductName) == "" || utf8.RuneCountInString(config.ProductName) > 32 {
				fields["matterConfig.productName"] = "must contain between 1 and 32 characters"
			}
			if !validMatterSerial.MatchString(config.SerialNumber) || len(config.SerialNumber) > 32 {
				fields["matterConfig.serialNumber"] = "must contain 1 to 32 ASCII letters, numbers, dots, underscores or hyphens"
			}
			if config.CommissioningWindowSeconds < 180 || config.CommissioningWindowSeconds > 900 {
				fields["matterConfig.commissioningWindowSeconds"] = "must be between 180 and 900 seconds"
			}
			if len(config.NetworkInterface) > 128 || strings.ContainsAny(config.NetworkInterface, "\x00\r\n") {
				fields["matterConfig.networkInterface"] = "must be at most 128 characters and contain no control separators"
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
		seenSources := map[string]bool{current.SourceDeviceID: true}
		for sourceIndex, sourceID := range current.AuxiliarySourceDeviceIDs {
			field := fmt.Sprintf("%s.auxiliarySourceDeviceIds.%d", prefix, sourceIndex)
			if strings.TrimSpace(sourceID) == "" {
				fields[field] = "must not be empty"
			} else if !device.ValidStableID(sourceID) {
				fields[field] = "must reference a stable unified device ID"
			} else if seenSources[sourceID] {
				fields[field] = "must be unique and different from the primary source"
			}
			seenSources[sourceID] = true
		}
		if current.Type != "" {
			if _, supported := device.ModelContractFor(current.Type); !supported {
				fields[prefix+".type"] = "must reference a supported unified device model"
			} else if known, supported := mapping.ConsumerModelSupport(descriptor.ConsumerID, current.Type); known && !supported {
				fields[prefix+".type"] = fmt.Sprintf("unified model %q is not supported by consumer %q", current.Type, descriptor.ConsumerID)
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

var validMatterSerial = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validMatterPasscode(value string) bool {
	if len(value) != 8 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	if value == "00000000" || value == "12345678" || value == "87654321" || value > "99999998" {
		return false
	}
	allSame := true
	for index := 1; index < len(value); index++ {
		allSame = allSame && value[index] == value[0]
	}
	return !allSame
}

func targetConsumerID(targetType string) string {
	descriptor, _ := domaintarget.DescriptorForType(targetType)
	return descriptor.ConsumerID
}

func (s *TargetService) List() []TargetInfo {
	s.mu.Lock()
	result := make([]TargetInfo, 0, len(s.order))
	for _, id := range s.order {
		info := s.targets[id].Info
		if config, exists := s.configs[id]; exists && s.runtime != nil {
			descriptor, _ := domaintarget.DescriptorForType(config.Type)
			if descriptor.SupportsHomeKitPairing {
				info.Paired = s.runtime.IsPaired(config)
			}
			if runtime, ok := s.runtime.(TargetRuntimeInfo); ok && domaintarget.IsMatterType(config.Type) {
				info = runtime.RuntimeInfo(config)
				registration := s.targets[id]
				registration.Info = info
				s.targets[id] = registration
			}
		}
		result = append(result, pairingSafeInfo(info))
	}
	s.mu.Unlock()
	return result
}

func (s *TargetService) QR(id string) ([]byte, error) {
	s.mu.RLock()
	registration, ok := s.targets[id]
	config, hasConfig := s.configs[id]
	runtime := s.runtime
	paired := ok && registration.Info.Paired
	s.mu.RUnlock()
	if hasConfig && runtime != nil {
		paired = runtime.IsPaired(config)
	}
	if !ok || paired || len(registration.QR) == 0 {
		return nil, errors.New("pairing QR code not found")
	}
	return append([]byte(nil), registration.QR...), nil
}

func (s *TargetService) MatterQR(id string) ([]byte, error) {
	s.mu.Lock()
	registration, found := s.targets[id]
	config, hasConfig := s.configs[id]
	if found && hasConfig {
		if runtime, ok := s.runtime.(TargetRuntimeInfo); ok {
			registration.Info = runtime.RuntimeInfo(config)
			s.targets[id] = registration
		}
	}
	s.mu.Unlock()
	if !found || !domaintarget.IsMatterType(registration.Info.Type) || !registration.Info.CommissioningWindowOpen || registration.Info.SetupPayload == "" {
		return nil, errors.New("Matter commissioning QR code not found")
	}
	png, err := qrcode.Encode(registration.Info.SetupPayload, qrcode.Medium, 320)
	if err != nil {
		return nil, fmt.Errorf("encode Matter commissioning QR: %w", err)
	}
	return png, nil
}

func (s *TargetService) OpenMatterCommissioningWindow(ctx context.Context, id string, durationSeconds uint32) (TargetInfo, error) {
	s.mu.RLock()
	config, found := s.configs[id]
	runtime, ok := s.runtime.(MatterTargetRuntime)
	s.mu.RUnlock()
	if !found {
		return TargetInfo{}, ErrTargetNotFound
	}
	if !domaintarget.IsMatterType(config.Type) {
		return TargetInfo{}, errors.New("target is not a Matter target")
	}
	if !ok {
		return TargetInfo{}, errors.New("Matter runtime controls are unavailable")
	}
	if durationSeconds == 0 && config.MatterConfig != nil {
		durationSeconds = config.MatterConfig.CommissioningWindowSeconds
	}
	if durationSeconds < 180 || durationSeconds > 900 {
		return TargetInfo{}, NewValidationError("invalid commissioning window", map[string]string{"durationSeconds": "must be between 180 and 900 seconds"})
	}
	registration, err := runtime.OpenCommissioningWindow(ctx, id, durationSeconds)
	return s.applyMatterRegistration(id, registration, err)
}

func (s *TargetService) CloseMatterCommissioningWindow(ctx context.Context, id string) (TargetInfo, error) {
	runtime, err := s.matterRuntime(id)
	if err != nil {
		return TargetInfo{}, err
	}
	registration, callErr := runtime.CloseCommissioningWindow(ctx, id)
	return s.applyMatterRegistration(id, registration, callErr)
}

func (s *TargetService) RemoveMatterFabric(ctx context.Context, id, fabricID string) (TargetInfo, error) {
	if strings.TrimSpace(fabricID) == "" {
		return TargetInfo{}, NewValidationError("invalid Matter Fabric", map[string]string{"fabricId": "required"})
	}
	runtime, err := s.matterRuntime(id)
	if err != nil {
		return TargetInfo{}, err
	}
	registration, callErr := runtime.RemoveFabric(ctx, id, fabricID)
	return s.applyMatterRegistration(id, registration, callErr)
}

func (s *TargetService) FactoryResetMatter(ctx context.Context, id string) (TargetInfo, error) {
	runtime, err := s.matterRuntime(id)
	if err != nil {
		return TargetInfo{}, err
	}
	registration, callErr := runtime.FactoryResetMatter(ctx, id)
	return s.applyMatterRegistration(id, registration, callErr)
}

func (s *TargetService) ConfirmMatterEndpointDeviceType(ctx context.Context, id, consumerDeviceID string, nextType device.Type) (TargetInfo, error) {
	s.mu.RLock()
	config, found := s.configs[id]
	runtime := s.runtime
	endpointRuntime, supported := runtime.(MatterEndpointTypeRuntime)
	s.mu.RUnlock()
	if !found {
		return TargetInfo{}, ErrTargetNotFound
	}
	if config.Type != "matter" {
		return TargetInfo{}, errors.New("target is not a Matter bridge")
	}
	if strings.TrimSpace(consumerDeviceID) == "" {
		return TargetInfo{}, NewValidationError("invalid Matter endpoint", map[string]string{"consumerDeviceId": "required"})
	}
	if !supported {
		return TargetInfo{}, errors.New("Matter endpoint identity controls are unavailable")
	}
	if s.store == nil {
		return TargetInfo{}, errors.New("target store is unavailable")
	}
	if _, ok := mapping.ConsumerContract("matter", nextType); !ok {
		return TargetInfo{}, NewValidationError("invalid Matter endpoint device type", map[string]string{"deviceType": fmt.Sprintf("%q is not supported by the Matter consumer", nextType)})
	}
	next := config
	next.Devices = append([]domaintarget.VirtualDevice(nil), config.Devices...)
	deviceIndex := -1
	var previousType device.Type
	for index := range next.Devices {
		if next.Devices[index].ID == consumerDeviceID {
			deviceIndex, previousType = index, next.Devices[index].Type
			break
		}
	}
	if deviceIndex < 0 {
		return TargetInfo{}, NewValidationError("Matter endpoint not found", map[string]string{"consumerDeviceId": "does not belong to this target"})
	}
	if previousType == nextType {
		return TargetInfo{}, NewValidationError("Matter endpoint device type is unchanged", map[string]string{"deviceType": "must differ from the current device type"})
	}
	next.Devices[deviceIndex].Type = nextType
	if err := validateTarget(next); err != nil {
		return TargetInfo{}, err
	}
	if err := endpointRuntime.ConfirmMatterEndpointDeviceType(ctx, id, consumerDeviceID, nextType); err != nil {
		return TargetInfo{}, err
	}
	if err := s.store.SaveTarget(ctx, next); err != nil {
		_ = endpointRuntime.ConfirmMatterEndpointDeviceType(ctx, id, consumerDeviceID, previousType)
		return TargetInfo{}, err
	}
	registration, err := runtime.Apply(ctx, next)
	if err != nil {
		registration.Info = TargetInfoFromConfig(next, "error")
		registration.Info.Error = fmt.Sprintf("Matter endpoint type was confirmed and persisted, but applying the target failed: %v", err)
	} else {
		registration.Info.ConsumerID = "matter"
	}
	s.mu.Lock()
	if previous, ok := s.targets[id]; ok && len(registration.QR) == 0 {
		registration.QR = previous.QR
	}
	s.configs[id] = next
	s.targets[id] = registration
	s.mu.Unlock()
	s.notify(registration.Info)
	return registration.Info, nil
}

func (s *TargetService) matterRuntime(id string) (MatterTargetRuntime, error) {
	s.mu.RLock()
	config, found := s.configs[id]
	runtime, ok := s.runtime.(MatterTargetRuntime)
	s.mu.RUnlock()
	if !found {
		return nil, ErrTargetNotFound
	}
	if !domaintarget.IsMatterType(config.Type) {
		return nil, errors.New("target is not a Matter target")
	}
	if !ok {
		return nil, errors.New("Matter runtime controls are unavailable")
	}
	return runtime, nil
}

func (s *TargetService) applyMatterRegistration(id string, registration TargetRegistration, err error) (TargetInfo, error) {
	if err != nil {
		return TargetInfo{}, err
	}
	registration.Info.ConsumerID = targetConsumerID(registration.Info.Type)
	s.mu.Lock()
	previous, found := s.targets[id]
	if found && len(registration.QR) == 0 {
		registration.QR = previous.QR
	}
	s.targets[id] = registration
	s.mu.Unlock()
	s.notify(registration.Info)
	return registration.Info, nil
}

func pairingSafeInfo(info TargetInfo) TargetInfo {
	if info.Paired {
		info.SetupID = ""
		info.PairingCode = ""
		info.SetupURI = ""
	}
	return info
}

func (s *TargetService) SetStatus(id, status string) {
	s.mu.Lock()
	registration, ok := s.targets[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	registration.Info.Status = status
	if config, exists := s.configs[id]; exists && domaintarget.IsMatterType(config.Type) {
		if runtime, supported := s.runtime.(TargetRuntimeInfo); supported {
			registration.Info = runtime.RuntimeInfo(config)
			registration.Info.Status = status
		}
	}
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
