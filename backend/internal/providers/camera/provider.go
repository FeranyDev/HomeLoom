package camera

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
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/media"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
)

const ProviderType = "camera"

type Config struct {
	Cameras []Entry `json:"cameras"`
}

type Entry struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Driver         string               `json:"driver"`
	HomeID         string               `json:"homeId,omitempty"`
	Home           string               `json:"home,omitempty"`
	RoomID         string               `json:"roomId,omitempty"`
	Room           string               `json:"room,omitempty"`
	ConnectionMode media.StreamMode     `json:"connectionMode,omitempty"`
	Profiles       []media.MediaProfile `json:"profiles"`
	RTSP           *RTSPConfig          `json:"rtsp,omitempty"`
	ONVIF          *ONVIFConfig         `json:"onvif,omitempty"`
	Xiaomi         *XiaomiConfig        `json:"xiaomi,omitempty"`
	Control        *ControlBinding      `json:"control,omitempty"`
	Enabled        *bool                `json:"enabled,omitempty"`
}

// ControlBinding identifies a device whose non-media capabilities are
// projected onto this canonical Camera. Provider credentials and Xiaomi DID
// remain exclusively in the referenced Provider configuration.
type ControlBinding struct {
	ProviderRef string `json:"providerRef"`
	DeviceID    string `json:"deviceId"`
}

type RTSPConfig struct {
	Host     string         `json:"host"`
	Port     int            `json:"port,omitempty"`
	Path     string         `json:"path"`
	AuthType media.AuthType `json:"authType,omitempty"`
	Username string         `json:"username,omitempty"`
	Password string         `json:"password,omitempty"`
}

type ONVIFConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// XiaomiConfig deliberately owns an optional, independent MIoT session.
// A future credentialProviderRef can resolve the same fields from an account
// Provider without changing Camera Device or MediaSource identities.
type XiaomiConfig struct {
	CredentialProviderRef string `json:"credentialProviderRef,omitempty"`
	Region                string `json:"region,omitempty"`
	Username              string `json:"username,omitempty"`
	Password              string `json:"password,omitempty"`
	UserID                string `json:"userId,omitempty"`
	Ssecurity             string `json:"ssecurity,omitempty"`
	ServiceToken          string `json:"serviceToken,omitempty"`
	PassToken             string `json:"passToken,omitempty"`
	DID                   string `json:"did"`
	Model                 string `json:"model"`
	LocalIP               string `json:"localIp"`
	Subtype               string `json:"subtype,omitempty"`
	Channel               int    `json:"channel,omitempty"`
	RequestTimeoutSec     int    `json:"requestTimeoutSeconds,omitempty"`
}

type Provider struct {
	id                 string
	name               string
	config             Config
	devices            []device.Device
	xiaomiCredentialFn func(string) (xiaomi.CloudConfig, error)
	xiaomiAuthorizeFn  func(context.Context, xiaomi.CloudConfig, string, string) (xiaomi.MISSAuthorizationResult, error)
	mu                 sync.RWMutex

	xiaomiAuthMu    sync.Mutex
	xiaomiAuthCache map[string]xiaomi.MISSAuthorizationResult
}

var (
	_ providersdk.Provider                = (*Provider)(nil)
	_ providersdk.Discoverer              = (*Provider)(nil)
	_ providersdk.MediaSourceDiscoverer   = (*Provider)(nil)
	_ providersdk.MediaSourceRefresher    = (*Provider)(nil)
	_ providersdk.MediaAuthorizer         = (*Provider)(nil)
	_ providersdk.CapabilityBindingSource = (*Provider)(nil)
)

func NewProviderFromConfig(item providerconfig.Config) (*Provider, error) {
	return NewProviderFromConfigWithXiaomiCredentialResolver(item, nil)
}

func NewProviderFromConfigWithXiaomiCredentialResolver(item providerconfig.Config, resolver func(string) (xiaomi.CloudConfig, error)) (*Provider, error) {
	var config Config
	decoder := json.NewDecoder(strings.NewReader(string(item.Config)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode camera config: %w", err)
	}
	if !device.ValidStableID(item.ID) || strings.TrimSpace(item.Name) == "" {
		return nil, errors.New("camera provider id and name are required")
	}
	provider := &Provider{
		id: item.ID, name: item.Name, config: config, xiaomiCredentialFn: resolver,
		xiaomiAuthorizeFn: xiaomi.AcquireMISSCameraAuthorization,
		xiaomiAuthCache:   make(map[string]xiaomi.MISSAuthorizationResult),
	}
	seen := make(map[string]struct{}, len(config.Cameras))
	for index := range provider.config.Cameras {
		entry := &provider.config.Cameras[index]
		applyDefaults(entry)
		if err := validateEntry(*entry); err != nil {
			return nil, err
		}
		if entry.Control != nil && entry.Control.ProviderRef == item.ID {
			return nil, fmt.Errorf("camera %q cannot use its own Provider as control source", entry.ID)
		}
		if _, duplicate := seen[entry.ID]; duplicate {
			return nil, fmt.Errorf("duplicate camera id %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		if !entryEnabled(*entry) {
			continue
		}
		provider.devices = append(provider.devices, buildDevice(item.ID, *entry))
	}
	sort.Slice(provider.devices, func(i, j int) bool { return provider.devices[i].ID < provider.devices[j].ID })
	return provider, nil
}

func (p *Provider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: ProviderType, Name: p.name, Version: "0.1.0"}
}

func (p *Provider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true}
}

func (p *Provider) Initialize(context.Context) error { return nil }
func (p *Provider) Close(context.Context) error      { return nil }

func (p *Provider) DiscoverDevices(context.Context) ([]device.Device, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]device.Device, len(p.devices))
	for index := range p.devices {
		result[index] = p.devices[index].Clone()
	}
	return result, nil
}

func (p *Provider) CapabilityBindings() []providersdk.CapabilityBinding {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]providersdk.CapabilityBinding, 0)
	for _, entry := range p.config.Cameras {
		if !entryEnabled(entry) || entry.Control == nil {
			continue
		}
		result = append(result, providersdk.CapabilityBinding{
			DeviceID: entry.ID, ProviderID: entry.Control.ProviderRef, SourceDeviceID: entry.Control.DeviceID,
		})
	}
	return result
}

func (p *Provider) DiscoverMediaSources(ctx context.Context) ([]media.MediaSourceDescriptor, error) {
	p.mu.RLock()
	entries := append([]Entry(nil), p.config.Cameras...)
	p.mu.RUnlock()
	result := make([]media.MediaSourceDescriptor, 0, len(entries))
	for _, entry := range entries {
		source, err := p.source(ctx, entry)
		if err != nil {
			return nil, err
		}
		// A disabled configured camera remains a known media source so the
		// catalog can stop its stream without treating the source as removed.
		// That distinction keeps stable Camera Target publication state, such
		// as the HomeKit accessory identity and pairings, intact for re-enable.
		source.Enabled = entryEnabled(entry)
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DeviceID < result[j].DeviceID })
	return result, nil
}

func (p *Provider) RefreshMediaSource(ctx context.Context, deviceID string) (*media.MediaSourceDescriptor, error) {
	p.mu.RLock()
	entries := append([]Entry(nil), p.config.Cameras...)
	p.mu.RUnlock()
	for _, entry := range entries {
		if entry.ID == deviceID && entryEnabled(entry) {
			source, err := p.source(ctx, entry)
			return &source, err
		}
	}
	return nil, providersdk.ErrDeviceNotFound
}

func (p *Provider) AcquireMediaAuthorization(ctx context.Context, request media.AuthorizationRequest) (*media.AuthorizationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	var entry *Entry
	for index := range p.config.Cameras {
		if p.config.Cameras[index].ID == request.DeviceID && entryEnabled(p.config.Cameras[index]) {
			copy := p.config.Cameras[index]
			entry = &copy
			break
		}
	}
	p.mu.RUnlock()
	if entry == nil {
		return nil, providersdk.ErrDeviceNotFound
	}
	if entry.Driver == "xiaomi-miss" {
		if request.Protocol != media.ProtocolXiaomiMISS {
			return nil, providersdk.ErrPropertyUnsupported
		}
		resolved := *entry
		xiaomiConfig := *entry.Xiaomi
		resolved.Xiaomi = &xiaomiConfig
		if ref := strings.TrimSpace(resolved.Xiaomi.CredentialProviderRef); ref != "" {
			if p.xiaomiCredentialFn == nil {
				return nil, fmt.Errorf("Xiaomi credential Provider %q is unavailable", ref)
			}
			credentials, resolveErr := p.xiaomiCredentialFn(ref)
			if resolveErr != nil {
				return nil, fmt.Errorf("resolve Xiaomi credential Provider %q: %w", ref, resolveErr)
			}
			mergeXiaomiCredentials(resolved.Xiaomi, credentials)
		}
		return p.acquireXiaomiAuthorization(ctx, resolved, request)
	}
	if entry.Driver != "rtsp" && entry.Driver != "onvif" {
		return nil, providersdk.ErrPropertyUnsupported
	}
	var protocol media.Protocol
	var endpoint media.EndpointSpec
	var authType media.AuthType
	var username, password string
	if entry.Driver == "onvif" {
		protocol = media.ProtocolONVIF
		endpoint = media.EndpointSpec{
			Protocol: media.ProtocolONVIF, Host: entry.ONVIF.Host, Port: entry.ONVIF.Port,
			Path: entry.ONVIF.Profile,
		}
		authType = media.AuthTypeDigest
		username, password = entry.ONVIF.Username, entry.ONVIF.Password
	} else {
		protocol = media.ProtocolRTSP
		endpoint = media.EndpointSpec{
			Protocol: media.ProtocolRTSP, Host: entry.RTSP.Host, Port: entry.RTSP.Port, Path: entry.RTSP.Path,
		}
		authType = entry.RTSP.AuthType
		username, password = entry.RTSP.Username, entry.RTSP.Password
	}
	if request.Protocol != protocol {
		return nil, providersdk.ErrPropertyUnsupported
	}
	leaseID, err := newLeaseID()
	if err != nil {
		return nil, err
	}
	response := &media.AuthorizationResponse{
		SchemaVersion: media.SchemaVersion, LeaseID: leaseID, ExpiresAt: time.Now().UTC().Add(time.Minute),
		Endpoint: endpoint, AuthType: authType, MaxUses: 1,
	}
	if username != "" {
		response.SecretMaterial, err = json.Marshal(map[string]string{"username": username, "password": password})
		if err != nil {
			return nil, err
		}
	}
	return response, response.Validate()
}

func mergeXiaomiCredentials(target *XiaomiConfig, source xiaomi.CloudConfig) {
	if target.Region == "" {
		target.Region = source.Region
	}
	if target.Username == "" {
		target.Username = source.Username
	}
	if target.Password == "" {
		target.Password = source.Password
	}
	if target.UserID == "" {
		target.UserID = source.UserID
	}
	if target.Ssecurity == "" {
		target.Ssecurity = source.Ssecurity
	}
	if target.ServiceToken == "" {
		target.ServiceToken = source.ServiceToken
	}
	if target.PassToken == "" {
		target.PassToken = source.PassToken
	}
	if target.RequestTimeoutSec == 0 {
		target.RequestTimeoutSec = source.RequestTimeoutSec
	}
}

func (p *Provider) source(_ context.Context, entry Entry) (media.MediaSourceDescriptor, error) {
	if entry.Driver == "xiaomi-miss" {
		config, err := json.Marshal(map[string]any{
			"did": entry.Xiaomi.DID, "model": entry.Xiaomi.Model, "localIp": entry.Xiaomi.LocalIP,
			"subtype": entry.Xiaomi.Subtype, "channel": entry.Xiaomi.Channel,
		})
		if err != nil {
			return media.MediaSourceDescriptor{}, err
		}
		source := media.MediaSourceDescriptor{
			SchemaVersion: media.SchemaVersion, DeviceID: entry.ID, ProviderID: p.id, ProviderDeviceID: entry.ID,
			Protocol: media.ProtocolXiaomiMISS, CredentialRef: entry.ID + "-xiaomi-account",
			ConnectionMode: entry.ConnectionMode, Profiles: cloneProfiles(entry.Profiles), SourceConfig: config, Revision: 1, Enabled: true,
		}
		return source, source.Validate()
	}
	protocol := media.ProtocolRTSP
	var configValue map[string]any
	credentialRef := ""
	if entry.Driver == "onvif" {
		protocol = media.ProtocolONVIF
		configValue = map[string]any{
			"host": entry.ONVIF.Host, "port": entry.ONVIF.Port, "profile": entry.ONVIF.Profile,
		}
		credentialRef = entry.ID + "-onvif-auth"
	} else {
		configValue = map[string]any{"host": entry.RTSP.Host, "port": entry.RTSP.Port, "path": entry.RTSP.Path}
		if entry.RTSP.Username != "" {
			credentialRef = entry.ID + "-rtsp-auth"
		}
	}
	config, err := json.Marshal(configValue)
	if err != nil {
		return media.MediaSourceDescriptor{}, err
	}
	source := media.MediaSourceDescriptor{
		SchemaVersion: media.SchemaVersion, DeviceID: entry.ID, ProviderID: p.id, ProviderDeviceID: entry.ID,
		Protocol: protocol, CredentialRef: credentialRef,
		ConnectionMode: entry.ConnectionMode, Profiles: cloneProfiles(entry.Profiles), SourceConfig: config, Revision: 1, Enabled: true,
	}
	return source, source.Validate()
}

func (p *Provider) acquireXiaomiAuthorization(ctx context.Context, entry Entry, request media.AuthorizationRequest) (*media.AuthorizationResponse, error) {
	x := entry.Xiaomi
	if x.LocalIP == "" {
		return nil, errors.New("Xiaomi camera localIp is required for media playback")
	}
	leaseID, err := newLeaseID()
	if err != nil {
		return nil, err
	}
	public := map[string]any{
		"userId": x.UserID, "region": x.Region, "did": x.DID, "model": x.Model,
		"localIP": x.LocalIP, "subtype": x.Subtype, "channel": x.Channel,
	}
	secret := make(map[string]string)
	if len(request.ClientMaterial) != 0 {
		var client struct {
			ClientPublic string `json:"clientPublic"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(request.ClientMaterial)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&client); err != nil || client.ClientPublic == "" {
			return nil, errors.New("invalid Xiaomi MISS client material")
		}
		cloudConfig := xiaomi.CloudConfig{
			Region: x.Region, Username: x.Username, Password: x.Password, UserID: x.UserID,
			Ssecurity: x.Ssecurity, ServiceToken: x.ServiceToken, PassToken: x.PassToken,
			RequestTimeoutSec: x.RequestTimeoutSec,
		}
		authorization, err := p.acquireCachedMISSAuthorization(ctx, cloudConfig, x.DID, strings.ToLower(client.ClientPublic))
		if err != nil {
			return nil, fmt.Errorf("acquire Xiaomi MISS authorization: %w", err)
		}
		public["devicePublic"], public["vendor"], public["uid"] = authorization.DevicePublic, authorization.Vendor, authorization.UID
		secret["sign"] = authorization.Sign
	} else {
		// Compatibility path for callers that still delegate Xiaomi cloud
		// authorization to their media kernel. HomeLoom Worker always provides
		// client material and therefore never receives the account passToken.
		secret["kernelPassToken"] = x.PassToken
	}
	publicRaw, err := json.Marshal(public)
	if err != nil {
		return nil, err
	}
	secretRaw, err := json.Marshal(secret)
	if err != nil {
		return nil, err
	}
	response := &media.AuthorizationResponse{
		SchemaVersion: media.SchemaVersion, LeaseID: leaseID, ExpiresAt: time.Now().UTC().Add(time.Minute),
		Endpoint: media.EndpointSpec{Protocol: media.ProtocolXiaomiMISS, Host: x.LocalIP},
		AuthType: media.AuthTypeVendor, PublicMaterial: publicRaw, SecretMaterial: secretRaw, MaxUses: 1,
	}
	return response, response.Validate()
}

func (p *Provider) acquireCachedMISSAuthorization(
	ctx context.Context,
	config xiaomi.CloudConfig,
	did string,
	clientPublic string,
) (xiaomi.MISSAuthorizationResult, error) {
	cacheKey := config.UserID + "\x00" + did + "\x00" + clientPublic
	p.xiaomiAuthMu.Lock()
	defer p.xiaomiAuthMu.Unlock()
	if cached, exists := p.xiaomiAuthCache[cacheKey]; exists {
		return cached, nil
	}
	if p.xiaomiAuthorizeFn == nil {
		return xiaomi.MISSAuthorizationResult{}, errors.New("Xiaomi MISS authorizer is unavailable")
	}
	authorization, err := p.xiaomiAuthorizeFn(ctx, config, did, clientPublic)
	if err != nil {
		return xiaomi.MISSAuthorizationResult{}, err
	}
	if p.xiaomiAuthCache == nil {
		p.xiaomiAuthCache = make(map[string]xiaomi.MISSAuthorizationResult)
	}
	p.xiaomiAuthCache[cacheKey] = authorization
	return authorization, nil
}

func applyDefaults(entry *Entry) {
	entry.ID, entry.Name = strings.TrimSpace(entry.ID), strings.TrimSpace(entry.Name)
	entry.Driver = strings.ToLower(strings.TrimSpace(entry.Driver))
	if entry.Control != nil {
		entry.Control.ProviderRef = strings.TrimSpace(entry.Control.ProviderRef)
		entry.Control.DeviceID = strings.TrimSpace(entry.Control.DeviceID)
	}
	if entry.ConnectionMode == "" {
		entry.ConnectionMode = media.StreamOnDemand
	}
	if entry.RTSP != nil {
		entry.RTSP.Host, entry.RTSP.Path = strings.TrimSpace(entry.RTSP.Host), strings.TrimSpace(entry.RTSP.Path)
		entry.RTSP.Username = strings.TrimSpace(entry.RTSP.Username)
		if entry.RTSP.Port == 0 {
			entry.RTSP.Port = 554
		}
		if entry.RTSP.AuthType == "" {
			if entry.RTSP.Username == "" {
				entry.RTSP.AuthType = media.AuthTypeNone
			} else {
				entry.RTSP.AuthType = media.AuthTypeBasic
			}
		}
	}
	if entry.ONVIF != nil {
		entry.ONVIF.Host = strings.TrimSpace(entry.ONVIF.Host)
		entry.ONVIF.Profile = strings.TrimSpace(entry.ONVIF.Profile)
		entry.ONVIF.Username = strings.TrimSpace(entry.ONVIF.Username)
		if entry.ONVIF.Port == 0 {
			entry.ONVIF.Port = 80
		}
	}
	if entry.Xiaomi != nil {
		entry.Xiaomi.CredentialProviderRef = strings.TrimSpace(entry.Xiaomi.CredentialProviderRef)
		entry.Xiaomi.Region = strings.ToLower(strings.TrimSpace(entry.Xiaomi.Region))
		if entry.Xiaomi.Region == "" {
			entry.Xiaomi.Region = "cn"
		}
		entry.Xiaomi.Subtype = strings.ToLower(strings.TrimSpace(entry.Xiaomi.Subtype))
		if entry.Xiaomi.Subtype == "" {
			entry.Xiaomi.Subtype = "hd"
		}
		if entry.Xiaomi.Channel == 0 {
			entry.Xiaomi.Channel = 1
		}
		if entry.Xiaomi.RequestTimeoutSec == 0 {
			entry.Xiaomi.RequestTimeoutSec = 15
		}
	}
	for index := range entry.Profiles {
		if entry.Profiles[index].SchemaVersion == 0 {
			entry.Profiles[index].SchemaVersion = media.SchemaVersion
		}
		if entry.Profiles[index].AudioCodec == "" {
			entry.Profiles[index].AudioCodec = media.AudioCodecNone
		}
	}
}

func validateEntry(entry Entry) error {
	if !device.ValidStableID(entry.ID) || entry.Name == "" {
		return errors.New("every camera requires a stable id and name")
	}
	if len(entry.Profiles) == 0 {
		return fmt.Errorf("camera %q requires at least one media profile", entry.ID)
	}
	if !entry.ConnectionMode.Valid() {
		return fmt.Errorf("camera %q uses unsupported connection mode %q", entry.ID, entry.ConnectionMode)
	}
	if entry.Control != nil &&
		(!device.ValidStableID(entry.Control.ProviderRef) || !device.ValidStableID(entry.Control.DeviceID)) {
		return fmt.Errorf("camera %q has invalid control providerRef or deviceId", entry.ID)
	}
	for _, profile := range entry.Profiles {
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("camera %q profile: %w", entry.ID, err)
		}
	}
	switch entry.Driver {
	case "rtsp":
		if entry.RTSP == nil || entry.ONVIF != nil || entry.Xiaomi != nil {
			return fmt.Errorf("camera %q requires only rtsp config", entry.ID)
		}
		if entry.RTSP.Host == "" || strings.ContainsAny(entry.RTSP.Host, "/@?#") || entry.RTSP.Port < 1 || entry.RTSP.Port > 65535 || !strings.HasPrefix(entry.RTSP.Path, "/") {
			return fmt.Errorf("camera %q has invalid RTSP endpoint", entry.ID)
		}
		if (entry.RTSP.Username == "") != (entry.RTSP.Password == "") {
			return fmt.Errorf("camera %q RTSP username and password must be configured together", entry.ID)
		}
	case "onvif":
		if entry.ONVIF == nil || entry.RTSP != nil || entry.Xiaomi != nil {
			return fmt.Errorf("camera %q requires only onvif config", entry.ID)
		}
		if entry.ONVIF.Host == "" || strings.ContainsAny(entry.ONVIF.Host, "/@?#") ||
			entry.ONVIF.Port < 1 || entry.ONVIF.Port > 65535 ||
			entry.ONVIF.Username == "" || entry.ONVIF.Password == "" {
			return fmt.Errorf("camera %q has invalid ONVIF endpoint or credentials", entry.ID)
		}
	case "xiaomi-miss":
		if entry.Xiaomi == nil || entry.RTSP != nil || entry.ONVIF != nil {
			return fmt.Errorf("camera %q requires only xiaomi config", entry.ID)
		}
		x := entry.Xiaomi
		if x.DID == "" || x.Model == "" {
			return fmt.Errorf("camera %q requires Xiaomi did and model", entry.ID)
		}
		if !validXiaomiMISSSubtype(x.Subtype) {
			return fmt.Errorf("camera %q Xiaomi MISS subtype must be auto, sd, hd, or a number from 0 to 5", entry.ID)
		}
		if x.CredentialProviderRef == "" {
			if x.UserID == "" || x.PassToken == "" {
				return fmt.Errorf("camera %q requires Xiaomi userId and passToken or credentialProviderRef", entry.ID)
			}
			if (x.Username == "") != (x.Password == "") {
				return fmt.Errorf("camera %q Xiaomi username and password must be configured together", entry.ID)
			}
			if x.Username == "" && (x.Ssecurity == "" || x.ServiceToken == "") {
				return fmt.Errorf("camera %q requires Xiaomi account login or MIoT session", entry.ID)
			}
		}
	default:
		return fmt.Errorf("camera %q uses unsupported driver %q", entry.ID, entry.Driver)
	}
	return nil
}

func validXiaomiMISSSubtype(value string) bool {
	switch value {
	case "auto", "sd", "hd":
		return true
	}
	number, err := strconv.Atoi(value)
	return err == nil && number >= 0 && number <= 5
}

func buildDevice(providerID string, entry Entry) device.Device {
	item := device.Device{
		SchemaVersion: device.SchemaVersion, ID: entry.ID, ProviderID: providerID, Name: entry.Name, Type: device.TypeCamera,
		HomeID: entry.HomeID, HomeName: entry.Home, RoomID: entry.RoomID, RoomName: entry.Room,
		Endpoints: []device.Endpoint{{ID: "main", Name: "Camera", Type: string(device.TypeCamera), Capabilities: []device.Capability{
			{ID: "media", Type: "media", Properties: []device.Property{
				{Definition: device.PropertyDefinition{ID: "live-stream", Name: "实时视频", Type: device.ValueTypeBool, Readable: true}, Value: device.BoolValue(true)},
				{Definition: device.PropertyDefinition{ID: "microphone", Name: "麦克风", Type: device.ValueTypeBool, Readable: true}, Value: device.BoolValue(hasAudio(entry.Profiles))},
				{Definition: device.PropertyDefinition{ID: "talkback", Name: "双向语音", Type: device.ValueTypeBool, Readable: true}, Value: device.BoolValue(false)},
			}},
		}}},
		LastUpdateAt: time.Now().UTC(),
	}
	item.SetAvailability(device.AvailabilityUnknown)
	_ = item.NormalizeModelParameters()
	return item
}

func entryEnabled(entry Entry) bool { return entry.Enabled == nil || *entry.Enabled }
func hasAudio(profiles []media.MediaProfile) bool {
	for _, profile := range profiles {
		if profile.AudioCodec != media.AudioCodecNone {
			return true
		}
	}
	return false
}
func cloneProfiles(items []media.MediaProfile) []media.MediaProfile {
	return append([]media.MediaProfile(nil), items...)
}
func newLeaseID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "lease-" + hex.EncodeToString(value[:]), nil
}
