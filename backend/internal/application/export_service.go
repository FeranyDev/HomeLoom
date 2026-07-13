package application

import (
	"context"
	"sort"
	"time"

	"github.com/feranydev/homeloom/backend/internal/buildinfo"
	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

const exportFormatVersion = 1

type ExportTargetConfig struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Address   string   `json:"address,omitempty"`
	SetupID   string   `json:"setupId,omitempty"`
	DeviceIDs []string `json:"deviceIds"`
}

type ConfigurationExport struct {
	FormatVersion int                     `json:"formatVersion"`
	GeneratedAt   time.Time               `json:"generatedAt"`
	Providers     []providerconfig.Config `json:"providers"`
	Targets       []ExportTargetConfig    `json:"targets"`
	Settings      RuntimeSettings         `json:"settings"`
	Profiles      []ProfileInfo           `json:"profiles"`
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
	result := ConfigurationExport{FormatVersion: exportFormatVersion, GeneratedAt: generatedAt, Providers: []providerconfig.Config{}, Targets: []ExportTargetConfig{}, Profiles: []ProfileInfo{}}
	if s.providers != nil {
		result.Providers = s.providers.ExportConfigs()
	}
	if s.targets != nil {
		for _, target := range s.targets.List() {
			result.Targets = append(result.Targets, ExportTargetConfig{ID: target.ID, Type: target.Type, Name: target.Name, Enabled: target.Enabled, Address: target.Address, SetupID: target.SetupID, DeviceIDs: append([]string{}, target.DeviceIDs...)})
		}
		sort.Slice(result.Targets, func(i, j int) bool { return result.Targets[i].ID < result.Targets[j].ID })
	}
	if s.settings != nil {
		result.Settings = s.settings.Get()
	}
	if s.profiles != nil {
		result.Profiles = s.profiles.List()
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
