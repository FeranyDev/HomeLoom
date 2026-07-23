package application

import (
	"context"
	"sort"
	"time"

	"github.com/feranydev/homeloom/backend/internal/buildinfo"
	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

const exportFormatVersion = 1

type ExportTargetConfig struct {
	ID           string                       `json:"id"`
	Type         string                       `json:"type"`
	Name         string                       `json:"name"`
	Enabled      bool                         `json:"enabled"`
	Address      string                       `json:"address,omitempty"`
	SetupID      string                       `json:"setupId,omitempty"`
	MatterConfig *domaintarget.MatterConfig   `json:"matterConfig,omitempty"`
	DeviceIDs    []string                     `json:"deviceIds"`
	Devices      []domaintarget.VirtualDevice `json:"devices"`
}

type ConfigurationExport struct {
	FormatVersion         int                           `json:"formatVersion"`
	GeneratedAt           time.Time                     `json:"generatedAt"`
	Providers             []providerconfig.Config       `json:"providers"`
	Targets               []ExportTargetConfig          `json:"targets"`
	Settings              RuntimeSettings               `json:"settings"`
	Profiles              []ProfileInfo                 `json:"profiles"`
	Bindings              []mapping.Binding             `json:"bindings"`
	CustomModels          []mapping.CustomModel         `json:"customModels"`
	CustomModelProperties []mapping.CustomModelProperty `json:"customModelProperties"`
}

type DiagnosticBundle struct {
	FormatVersion int                 `json:"formatVersion"`
	GeneratedAt   time.Time           `json:"generatedAt"`
	Build         buildinfo.Info      `json:"build"`
	Metrics       DeviceMetrics       `json:"metrics"`
	Configuration ConfigurationExport `json:"configuration"`
	RecentAudit   []domainaudit.Event `json:"recentAudit"`
}

type ExportService struct {
	devices   *DeviceService
	providers *ProviderService
	targets   *TargetService
	settings  *SettingsService
	audit     *AuditService
	profiles  *ProfileService
	now       func() time.Time
}

func NewExportService(devices *DeviceService, providers *ProviderService, targets *TargetService, settings *SettingsService, audit *AuditService, profileServices ...*ProfileService) *ExportService {
	service := &ExportService{devices: devices, providers: providers, targets: targets, settings: settings, audit: audit, now: time.Now}
	if len(profileServices) > 0 {
		service.profiles = profileServices[0]
	}
	return service
}

func (s *ExportService) Configuration() ConfigurationExport {
	return s.configurationAt(s.now().UTC())
}

func (s *ExportService) configurationAt(generatedAt time.Time) ConfigurationExport {
	result := ConfigurationExport{FormatVersion: exportFormatVersion, GeneratedAt: generatedAt, Providers: []providerconfig.Config{}, Targets: []ExportTargetConfig{}, Profiles: []ProfileInfo{}, Bindings: []mapping.Binding{}, CustomModels: []mapping.CustomModel{}, CustomModelProperties: []mapping.CustomModelProperty{}}
	if s.providers != nil {
		result.Providers = s.providers.ExportConfigs()
	}
	if s.targets != nil {
		for _, target := range s.targets.List() {
			exported := ExportTargetConfig{ID: target.ID, Type: target.Type, Name: target.Name, Enabled: target.Enabled, Address: target.Address, SetupID: target.SetupID, DeviceIDs: append([]string{}, target.DeviceIDs...), Devices: append([]domaintarget.VirtualDevice(nil), target.Devices...)}
			if target.Type == "matter" {
				discriminator := target.Discriminator
				exported.MatterConfig = &domaintarget.MatterConfig{
					NetworkInterface: target.NetworkInterface, UDPPort: target.UDPPort,
					Discriminator: &discriminator, VendorID: target.VendorID, ProductID: target.ProductID,
					ProductName: target.ProductName, SerialNumber: target.SerialNumber,
					CommissioningWindowSeconds: target.CommissioningWindowSeconds,
				}
			}
			result.Targets = append(result.Targets, exported)
		}
		sort.Slice(result.Targets, func(i, j int) bool { return result.Targets[i].ID < result.Targets[j].ID })
	}
	if s.settings != nil {
		result.Settings = s.settings.Get()
	}
	if s.profiles != nil {
		result.Profiles = s.profiles.List()
		result.Bindings = s.profiles.ListBindings()
		result.CustomModels = s.profiles.ListCustomModels()
		result.CustomModelProperties = s.profiles.ListCustomModelProperties()
	}
	return result
}

func (s *ExportService) Diagnostics(ctx context.Context) (DiagnosticBundle, error) {
	generatedAt := s.now().UTC()
	result := DiagnosticBundle{FormatVersion: exportFormatVersion, GeneratedAt: generatedAt, Build: buildinfo.Current(), Configuration: s.configurationAt(generatedAt), RecentAudit: []domainaudit.Event{}}
	if s.devices != nil {
		result.Metrics = s.devices.Metrics()
	}
	if s.audit != nil {
		items, err := s.audit.List(ctx, 100)
		if err != nil {
			return DiagnosticBundle{}, err
		}
		result.RecentAudit = items
	}
	return result, nil
}
