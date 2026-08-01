package xiaomi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/media"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

const cameraAuthorizationLease = time.Minute

type rtspSourceConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	Path string `json:"path"`
}

type rtspSecretMaterial struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type xiaomiNativeSourceConfig struct {
	DID     string `json:"did"`
	Model   string `json:"model"`
	LocalIP string `json:"localIp"`
	Subtype string `json:"subtype"`
	Channel int    `json:"channel"`
}

type xiaomiMISSClientMaterial struct {
	ClientPublic string `json:"clientPublic"`
}

type xiaomiMISSPublicMaterial struct {
	UserID       string `json:"userId"`
	Region       string `json:"region"`
	DID          string `json:"did"`
	Model        string `json:"model"`
	LocalIP      string `json:"localIP"`
	Subtype      string `json:"subtype"`
	Channel      int    `json:"channel"`
	DevicePublic string `json:"devicePublic"`
	Vendor       string `json:"vendor"`
	UID          string `json:"uid,omitempty"`
}

type xiaomiMISSSecretMaterial struct {
	// KernelPassToken is scoped to the short-lived, single-use authorization
	// lease. It must only be materialized in the Worker child process memory.
	KernelPassToken string `json:"kernelPassToken"`
	Sign            string `json:"sign,omitempty"`
}

type xiaomiMISSAuthorization struct {
	DevicePublic string
	Sign         string
	Vendor       string
	UID          string
}

type xiaomiMISSMediaAuthorizer interface {
	AcquireMISSAuthorization(context.Context, string, string) (xiaomiMISSAuthorization, error)
}

func applyCameraMediaDefaults(item *DeviceConfig) {
	if item == nil || item.Media == nil {
		return
	}
	config := item.Media
	config.Host = strings.TrimSpace(config.Host)
	config.Path = strings.TrimSpace(config.Path)
	config.Username = strings.TrimSpace(config.Username)
	if config.Protocol == "" {
		config.Protocol = media.ProtocolRTSP
	}
	if config.Protocol == media.ProtocolRTSP && config.Port == 0 {
		config.Port = 554
	}
	switch config.Protocol {
	case media.ProtocolRTSP:
		hasCredentials := config.Username != "" || config.Password != ""
		if config.AuthType == "" {
			if hasCredentials {
				config.AuthType = media.AuthTypeBasic
			} else {
				config.AuthType = media.AuthTypeNone
			}
		}
	case media.ProtocolXiaomiMISS:
		if config.AuthType == "" {
			config.AuthType = media.AuthTypeVendor
		}
		config.Subtype = strings.ToLower(strings.TrimSpace(config.Subtype))
		if config.Subtype == "" {
			config.Subtype = "hd"
		}
		if config.Channel == 0 {
			config.Channel = 1
		}
	}
	for index := range config.Profiles {
		profile := &config.Profiles[index]
		if profile.SchemaVersion == 0 {
			profile.SchemaVersion = media.SchemaVersion
		}
		profile.ID = strings.TrimSpace(profile.ID)
		profile.Name = strings.TrimSpace(profile.Name)
		if profile.AudioCodec == "" {
			profile.AudioCodec = media.AudioCodecNone
		}
	}
}

func validateCameraMediaConfig(item DeviceConfig) error {
	if item.Media == nil {
		return nil
	}
	if item.Type != device.TypeCamera {
		return fmt.Errorf("device %q media configuration requires type camera", item.ID)
	}
	config := item.Media
	if config.Protocol != media.ProtocolRTSP && config.Protocol != media.ProtocolXiaomiMISS {
		return fmt.Errorf("device %q media protocol %q is not supported by the Xiaomi Core adapter", item.ID, config.Protocol)
	}
	if config.Host != "" && strings.ContainsAny(config.Host, "/@?#") {
		return fmt.Errorf("device %q media host must not contain URI components", item.ID)
	}
	switch config.Protocol {
	case media.ProtocolRTSP:
		if config.Port < 1 || config.Port > 65535 {
			return fmt.Errorf("device %q media port must be between 1 and 65535", item.ID)
		}
		if config.Path == "" || !strings.HasPrefix(config.Path, "/") || strings.ContainsAny(config.Path, "?#") {
			return fmt.Errorf("device %q media path must be an absolute path without query or fragment", item.ID)
		}
		hasUsername, hasPassword := config.Username != "", config.Password != ""
		if hasUsername != hasPassword {
			return fmt.Errorf("device %q media username and password must be configured together", item.ID)
		}
		if hasUsername {
			if config.AuthType != media.AuthTypeBasic && config.AuthType != media.AuthTypeDigest {
				return fmt.Errorf("device %q media authType must be basic or digest when credentials are configured", item.ID)
			}
		} else if config.AuthType != media.AuthTypeNone {
			return fmt.Errorf("device %q media authType must be none when credentials are omitted", item.ID)
		}
	case media.ProtocolXiaomiMISS:
		if config.Port != 0 || config.Path != "" || config.Username != "" || config.Password != "" {
			return fmt.Errorf("device %q Xiaomi MISS media must not contain RTSP port, path, username, or password", item.ID)
		}
		if config.AuthType != media.AuthTypeVendor {
			return fmt.Errorf("device %q Xiaomi MISS media authType must be vendor", item.ID)
		}
		if !validXiaomiMediaSubtype(config.Subtype) {
			return fmt.Errorf("device %q Xiaomi MISS subtype must be auto, sd, hd, or a number from 0 to 5", item.ID)
		}
		if config.Channel < 1 || config.Channel > 8 {
			return fmt.Errorf("device %q Xiaomi MISS channel must be between 1 and 8", item.ID)
		}
	}
	if len(config.Profiles) == 0 {
		return fmt.Errorf("device %q media profiles are required", item.ID)
	}
	seen := make(map[string]struct{}, len(config.Profiles))
	for _, profile := range config.Profiles {
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("device %q media profile %q: %w", item.ID, profile.ID, err)
		}
		if _, duplicate := seen[profile.ID]; duplicate {
			return fmt.Errorf("device %q has duplicate media profile %q", item.ID, profile.ID)
		}
		seen[profile.ID] = struct{}{}
	}
	return nil
}

func validXiaomiMediaSubtype(value string) bool {
	switch value {
	case "auto", "sd", "hd":
		return true
	}
	number, err := strconv.Atoi(value)
	return err == nil && number >= 0 && number <= 5
}

func (p *Provider) DiscoverMediaSources(context.Context) ([]media.MediaSourceDescriptor, error) {
	p.mu.RLock()
	configured := cloneDeviceConfigs(p.config.Devices)
	directory := append([]HubDevice(nil), p.directory...)
	providerID := p.id
	p.mu.RUnlock()
	return discoverCameraMediaSources(providerID, configured, directory)
}

func (p *Provider) RefreshMediaSource(_ context.Context, deviceID string) (*media.MediaSourceDescriptor, error) {
	p.mu.RLock()
	configured := cloneDeviceConfigs(p.config.Devices)
	directory := append([]HubDevice(nil), p.directory...)
	providerID := p.id
	p.mu.RUnlock()
	return refreshCameraMediaSource(providerID, configured, directory, deviceID)
}

func (p *Provider) AcquireMediaAuthorization(ctx context.Context, request media.AuthorizationRequest) (*media.AuthorizationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	configured := cloneDeviceConfigs(p.config.Devices)
	directory := append([]HubDevice(nil), p.directory...)
	p.mu.RUnlock()
	return acquireCameraMediaAuthorization(ctx, configured, directory, request, time.Now().UTC(), "", "", "", nil)
}

func (p *CloudProvider) DiscoverMediaSources(context.Context) ([]media.MediaSourceDescriptor, error) {
	p.mu.RLock()
	configured := cloneDeviceConfigs(p.config.Devices)
	directory := append([]HubDevice(nil), p.directory...)
	providerID := p.id
	p.mu.RUnlock()
	return discoverCameraMediaSources(providerID, configured, directory)
}

func (p *CloudProvider) RefreshMediaSource(_ context.Context, deviceID string) (*media.MediaSourceDescriptor, error) {
	p.mu.RLock()
	configured := cloneDeviceConfigs(p.config.Devices)
	directory := append([]HubDevice(nil), p.directory...)
	providerID := p.id
	p.mu.RUnlock()
	return refreshCameraMediaSource(providerID, configured, directory, deviceID)
}

func (p *CloudProvider) AcquireMediaAuthorization(ctx context.Context, request media.AuthorizationRequest) (*media.AuthorizationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	configured := cloneDeviceConfigs(p.config.Devices)
	directory := append([]HubDevice(nil), p.directory...)
	passToken := p.config.PassToken
	userID := p.config.UserID
	region := p.config.Region
	client := p.client
	p.mu.RUnlock()
	var native xiaomiMISSMediaAuthorizer
	if client != nil {
		native, _ = client.(xiaomiMISSMediaAuthorizer)
	}
	return acquireCameraMediaAuthorization(ctx, configured, directory, request, time.Now().UTC(), userID, region, passToken, native)
}

func discoverCameraMediaSources(providerID string, configured []DeviceConfig, directory []HubDevice) ([]media.MediaSourceDescriptor, error) {
	result := make([]media.MediaSourceDescriptor, 0)
	for _, item := range configured {
		if item.Type != device.TypeCamera || item.Media == nil {
			continue
		}
		source, err := buildCameraMediaSource(providerID, item, directory)
		if err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	sortMediaSources(result)
	return result, nil
}

func refreshCameraMediaSource(providerID string, configured []DeviceConfig, directory []HubDevice, deviceID string) (*media.MediaSourceDescriptor, error) {
	for _, item := range configured {
		if item.ID != deviceID {
			continue
		}
		if item.Type != device.TypeCamera || item.Media == nil {
			return nil, providersdk.ErrPropertyUnsupported
		}
		source, err := buildCameraMediaSource(providerID, item, directory)
		if err != nil {
			return nil, err
		}
		return &source, nil
	}
	return nil, providersdk.ErrDeviceNotFound
}

func buildCameraMediaSource(providerID string, configured DeviceConfig, directory []HubDevice) (media.MediaSource, error) {
	host, err := cameraMediaHost(configured, directory)
	if err != nil {
		return media.MediaSource{}, err
	}
	config := configured.Media
	var logicalConfig []byte
	switch config.Protocol {
	case media.ProtocolRTSP:
		logicalConfig, err = json.Marshal(rtspSourceConfig{Host: host, Port: config.Port, Path: config.Path})
	case media.ProtocolXiaomiMISS:
		model := cameraMediaModel(configured, directory)
		if model == "" {
			return media.MediaSource{}, fmt.Errorf("Xiaomi camera %q has no configured or discovered model", configured.ID)
		}
		logicalConfig, err = json.Marshal(xiaomiNativeSourceConfig{
			DID: configured.DID, Model: model, LocalIP: host,
			Subtype: config.Subtype, Channel: config.Channel,
		})
	default:
		return media.MediaSource{}, fmt.Errorf("Xiaomi camera %q uses unsupported media protocol %q", configured.ID, config.Protocol)
	}
	if err != nil {
		return media.MediaSource{}, errors.New("encode Xiaomi camera media source")
	}
	if err := media.ValidateLogicalConfig(logicalConfig); err != nil {
		return media.MediaSource{}, fmt.Errorf("validate Xiaomi camera media source %q: %w", configured.ID, err)
	}
	source := media.MediaSource{
		SchemaVersion:    media.SchemaVersion,
		DeviceID:         configured.ID,
		ProviderID:       providerID,
		ProviderDeviceID: configured.DID,
		Protocol:         config.Protocol,
		Profiles:         cloneMediaProfiles(config.Profiles),
		SourceConfig:     logicalConfig,
		Revision:         1,
		Enabled:          true,
	}
	if config.Username != "" || config.Protocol == media.ProtocolXiaomiMISS {
		source.CredentialRef = cameraCredentialRef(configured.ID, config.Protocol)
	}
	if err := source.Validate(); err != nil {
		return media.MediaSource{}, fmt.Errorf("validate Xiaomi camera media source %q: %w", configured.ID, err)
	}
	return source, nil
}

func acquireCameraMediaAuthorization(
	ctx context.Context,
	configured []DeviceConfig,
	directory []HubDevice,
	request media.AuthorizationRequest,
	now time.Time,
	userID string,
	region string,
	passToken string,
	native xiaomiMISSMediaAuthorizer,
) (*media.AuthorizationResponse, error) {
	var item *DeviceConfig
	for index := range configured {
		if configured[index].ID == request.DeviceID {
			item = &configured[index]
			break
		}
	}
	if item == nil {
		return nil, providersdk.ErrDeviceNotFound
	}
	if item.Type != device.TypeCamera || item.Media == nil || request.Protocol != item.Media.Protocol {
		return nil, providersdk.ErrPropertyUnsupported
	}
	if request.Purpose == media.PurposeTalkback && !cameraSupportsAudio(item.Media.Profiles) {
		return nil, providersdk.ErrCommandUnsupported
	}
	host, err := cameraMediaHost(*item, directory)
	if err != nil {
		return nil, err
	}
	leaseID, err := newCameraLeaseID()
	if err != nil {
		return nil, errors.New("create Xiaomi camera authorization lease")
	}
	response := &media.AuthorizationResponse{
		SchemaVersion: media.SchemaVersion,
		LeaseID:       leaseID,
		ExpiresAt:     now.Add(cameraAuthorizationLease),
		Endpoint: media.EndpointSpec{
			Protocol: item.Media.Protocol,
			Host:     host,
			Port:     item.Media.Port,
			Path:     item.Media.Path,
		},
		AuthType: item.Media.AuthType,
		MaxUses:  1,
	}
	switch item.Media.Protocol {
	case media.ProtocolRTSP:
		if item.Media.Username == "" {
			break
		}
		response.SecretMaterial, err = json.Marshal(rtspSecretMaterial{
			Username: item.Media.Username,
			Password: item.Media.Password,
		})
		if err != nil {
			return nil, errors.New("encode Xiaomi camera authorization material")
		}
	case media.ProtocolXiaomiMISS:
		if passToken == "" {
			return nil, errors.New("Xiaomi MISS passToken is unavailable; sign in with the Xiaomi account again")
		}
		model := cameraMediaModel(*item, directory)
		if userID == "" || region == "" || model == "" {
			return nil, errors.New("Xiaomi MISS account or camera descriptor is incomplete")
		}
		public := xiaomiMISSPublicMaterial{
			UserID: userID, Region: region, DID: item.DID, Model: model,
			LocalIP: host, Subtype: item.Media.Subtype, Channel: item.Media.Channel,
		}
		secret := xiaomiMISSSecretMaterial{KernelPassToken: passToken}
		var clientMaterial xiaomiMISSClientMaterial
		if len(strings.TrimSpace(string(request.ClientMaterial))) != 0 {
			if err := media.DecodeStrictConfig(request.ClientMaterial, &clientMaterial); err != nil {
				return nil, fmt.Errorf("decode Xiaomi MISS client material: %w", err)
			}
		}
		if clientMaterial.ClientPublic != "" {
			if err := validateClientPublic(clientMaterial.ClientPublic); err != nil {
				return nil, err
			}
			if native == nil {
				return nil, providersdk.ErrProviderUnavailable
			}
			authorization, err := native.AcquireMISSAuthorization(ctx, item.DID, strings.ToLower(clientMaterial.ClientPublic))
			if err != nil {
				return nil, fmt.Errorf("acquire Xiaomi MISS authorization: %w", err)
			}
			public.DevicePublic = authorization.DevicePublic
			public.Vendor = authorization.Vendor
			public.UID = authorization.UID
			secret.Sign = authorization.Sign
		}
		response.PublicMaterial, err = json.Marshal(public)
		if err == nil {
			response.SecretMaterial, err = json.Marshal(secret)
		}
		if err != nil {
			return nil, errors.New("encode Xiaomi MISS authorization material")
		}
	}
	if err := response.ValidateAt(now); err != nil {
		return nil, fmt.Errorf("validate Xiaomi camera authorization response: %w", err)
	}
	return response, nil
}

func validateClientPublic(value string) error {
	value = strings.TrimSpace(value)
	if len(value) < 64 || len(value) > 512 {
		return errors.New("Xiaomi MISS clientPublic has an invalid length")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) < 32 || len(decoded) > 256 {
		return errors.New("Xiaomi MISS clientPublic must be a hexadecimal public key")
	}
	return nil
}

func cameraMediaHost(configured DeviceConfig, directory []HubDevice) (string, error) {
	if configured.Media != nil && configured.Media.Host != "" {
		return configured.Media.Host, nil
	}
	for _, item := range directory {
		if item.DID == configured.DID && strings.TrimSpace(item.LocalIP) != "" {
			host := strings.TrimSpace(item.LocalIP)
			if strings.ContainsAny(host, "/@?#") {
				break
			}
			return host, nil
		}
	}
	return "", fmt.Errorf("Xiaomi camera %q has no configured or discovered local media host", configured.ID)
}

func cameraMediaModel(configured DeviceConfig, directory []HubDevice) string {
	if model := strings.TrimSpace(configured.Model); model != "" {
		return model
	}
	for _, item := range directory {
		if item.DID == configured.DID {
			return strings.TrimSpace(item.Model)
		}
	}
	return ""
}

func cameraSupportsAudio(profiles []media.MediaProfile) bool {
	for _, profile := range profiles {
		if profile.AudioCodec != media.AudioCodecNone {
			return true
		}
	}
	return false
}

func cameraCredentialRef(deviceID string, protocol media.Protocol) string {
	if protocol == media.ProtocolXiaomiMISS {
		return deviceID + "-xiaomi-account"
	}
	return deviceID + "-rtsp-auth"
}

func newCameraLeaseID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "lease-" + hex.EncodeToString(value[:]), nil
}

func cloneDeviceConfigs(items []DeviceConfig) []DeviceConfig {
	result := make([]DeviceConfig, len(items))
	for index, item := range items {
		result[index] = item
		result[index].Properties = append([]PropertyMapping(nil), item.Properties...)
		result[index].Actions = append([]ActionMapping(nil), item.Actions...)
		if item.Media != nil {
			config := *item.Media
			config.Profiles = cloneMediaProfiles(item.Media.Profiles)
			result[index].Media = &config
		}
	}
	return result
}

func cloneMediaProfiles(items []media.MediaProfile) []media.MediaProfile {
	return append([]media.MediaProfile(nil), items...)
}

func sortMediaSources(items []media.MediaSourceDescriptor) {
	sort.Slice(items, func(i, j int) bool { return items[i].DeviceID < items[j].DeviceID })
}
