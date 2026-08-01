package gormstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// These persistence DTOs intentionally use strings for domain enums. The media
// domain owns protocol, credential, stream-mode, and lease-state semantics;
// gormstore only enforces the durable subset of those contracts.
type MediaSource struct {
	DeviceID         string
	ProviderID       string
	ProviderDeviceID string
	Protocol         string
	CredentialRef    string
	ProfilesJSON     []byte
	SourceConfigJSON []byte
	Revision         uint64
	Enabled          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type MediaCredential struct {
	ID             string
	DeviceID       string
	CredentialType string
	Blob           []byte
	KeyVersion     uint32
	Version        uint64
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type MediaStream struct {
	ID              string
	DeviceID        string
	Protocol        string
	CredentialRef   string
	Profile         string
	Mode            string
	AudioEnabled    bool
	TalkbackEnabled bool
	OptionsJSON     []byte
	Revision        uint64
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type MediaRuntimeValue struct {
	Namespace string
	Key       string
	Value     []byte
	Sensitive bool
	UpdatedAt time.Time
}

type MediaAuthLease struct {
	ID                  string
	WorkerID            string
	WorkerInstanceID    string
	DeviceID            string
	Protocol            string
	Purpose             string
	Status              string
	ExpiresAt           time.Time
	RequestID           string
	RequestMaterialHash string
	MaxUses             uint32
	UseCount            uint32
	CreatedAt           time.Time
	ClaimedAt           time.Time
	UsedAt              time.Time
	EndedAt             time.Time
}

type MediaAuthAudit struct {
	ID             int64
	WorkerID       string
	DeviceID       string
	Provider       string
	Action         string
	Result         string
	ErrorCode      string
	RemoteIdentity string
	CreatedAt      time.Time
}

func mediaCredentialSecretScope(id, deviceID string) string {
	return "media-credential:" + deviceID + ":" + id
}

func mediaRuntimeSecretScope(namespace, key string) string {
	return "media-runtime-kv:" + namespace + ":" + key
}

func validateMediaIdentifier(label, value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 1024 || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s must contain 1 to 1024 bytes and no NUL", label)
	}
	return nil
}

func normalizedJSON(raw []byte, fallback string, label string) ([]byte, error) {
	if len(raw) == 0 {
		raw = []byte(fallback)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("%s must be valid JSON", label)
	}
	return raw, nil
}

func (s *Store) SaveMediaSource(ctx context.Context, item MediaSource) error {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media device ID", item.DeviceID); err != nil {
		return err
	}
	if err := validateMediaIdentifier("media provider ID", item.ProviderID); err != nil {
		return err
	}
	if err := validateMediaIdentifier("media protocol", item.Protocol); err != nil {
		return err
	}
	if !domainmedia.Protocol(item.Protocol).Valid() {
		return fmt.Errorf("unsupported media source protocol %q", item.Protocol)
	}
	if item.Revision == 0 {
		return errors.New("media source revision must be positive")
	}
	profiles, err := normalizedJSON(item.ProfilesJSON, "[]", "media profiles")
	if err != nil {
		return err
	}
	var decodedProfiles []domainmedia.MediaProfile
	if err := json.Unmarshal(profiles, &decodedProfiles); err != nil {
		return fmt.Errorf("decode media profiles: %w", err)
	}
	for _, profile := range decodedProfiles {
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("validate media profile: %w", err)
		}
	}
	config, err := normalizedJSON(item.SourceConfigJSON, "{}", "media source config")
	if err != nil {
		return err
	}
	if err := domainmedia.ValidateLogicalConfig(config); err != nil {
		return fmt.Errorf("validate media source config: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	row := mediaSourceRow{
		DeviceID: item.DeviceID, ProviderID: item.ProviderID, ProviderDeviceID: item.ProviderDeviceID,
		Protocol: item.Protocol, CredentialRef: item.CredentialRef, ProfilesJSON: jsonDocument(profiles),
		SourceConfigJSON: jsonDocument(config), Revision: item.Revision, Enabled: item.Enabled,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.orm.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "device_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"provider_id", "provider_device_id", "protocol", "credential_ref", "profiles_json",
			"source_config_json", "revision", "enabled", "updated_at",
		}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("save media source: %w", err)
	}
	return nil
}

func (s *Store) GetMediaSource(ctx context.Context, deviceID string) (MediaSource, bool, error) {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media device ID", deviceID); err != nil {
		return MediaSource{}, false, err
	}
	var row mediaSourceRow
	err := s.orm.WithContext(ctx).Where("device_id = ?", deviceID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MediaSource{}, false, nil
	}
	if err != nil {
		return MediaSource{}, false, fmt.Errorf("read media source: %w", err)
	}
	return mediaSourceFromRow(row), true, nil
}

func (s *Store) ListMediaSources(ctx context.Context) ([]MediaSource, error) {
	defer s.observe(time.Now())
	var rows []mediaSourceRow
	if err := s.orm.WithContext(ctx).Order("created_at, device_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list media sources: %w", err)
	}
	result := make([]MediaSource, 0, len(rows))
	for _, row := range rows {
		result = append(result, mediaSourceFromRow(row))
	}
	return result, nil
}

func mediaSourceFromRow(row mediaSourceRow) MediaSource {
	return MediaSource{
		DeviceID: row.DeviceID, ProviderID: row.ProviderID, ProviderDeviceID: row.ProviderDeviceID,
		Protocol: row.Protocol, CredentialRef: row.CredentialRef, ProfilesJSON: []byte(row.ProfilesJSON),
		SourceConfigJSON: []byte(row.SourceConfigJSON), Revision: row.Revision, Enabled: row.Enabled,
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
	}
}

func (s *Store) DeleteMediaSource(ctx context.Context, deviceID string) error {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media device ID", deviceID); err != nil {
		return err
	}
	result := s.orm.WithContext(ctx).Where("device_id = ?", deviceID).Delete(&mediaSourceRow{})
	if result.Error != nil {
		return fmt.Errorf("delete media source: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("media source %q not found", deviceID)
	}
	return nil
}

func (s *Store) SaveMediaCredential(ctx context.Context, item MediaCredential) error {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media credential ID", item.ID); err != nil {
		return err
	}
	if err := validateMediaIdentifier("media credential device ID", item.DeviceID); err != nil {
		return err
	}
	if item.CredentialType == "" {
		return errors.New("media credential type is required")
	}
	if len(item.Blob) == 0 {
		return errors.New("media credential blob is required")
	}
	if item.KeyVersion == 0 {
		item.KeyVersion = 1
	}
	if item.Version == 0 {
		item.Version = 1
	}
	if item.Status == "" {
		item.Status = "active"
	}
	encoded := base64.RawStdEncoding.EncodeToString(item.Blob)
	encrypted, err := s.secrets.encrypt(mediaCredentialSecretScope(item.ID, item.DeviceID), encoded)
	if err != nil {
		return fmt.Errorf("encrypt media credential: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	row := mediaCredentialRow{
		ID: item.ID, DeviceID: item.DeviceID, CredentialType: item.CredentialType,
		CredentialBlobEncrypted: encrypted, KeyVersion: item.KeyVersion, Version: item.Version,
		Status: item.Status, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.orm.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"device_id", "credential_type", "credential_blob_encrypted", "key_version",
			"version", "status", "updated_at",
		}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("save media credential: %w", err)
	}
	return nil
}

func (s *Store) GetMediaCredential(ctx context.Context, id string) (MediaCredential, bool, error) {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media credential ID", id); err != nil {
		return MediaCredential{}, false, err
	}
	var row mediaCredentialRow
	err := s.orm.WithContext(ctx).Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MediaCredential{}, false, nil
	}
	if err != nil {
		return MediaCredential{}, false, fmt.Errorf("read media credential: %w", err)
	}
	result, err := s.mediaCredentialFromRow(row)
	if err != nil {
		return MediaCredential{}, false, err
	}
	return result, true, nil
}

func (s *Store) ListMediaCredentials(ctx context.Context, deviceID string) ([]MediaCredential, error) {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media credential device ID", deviceID); err != nil {
		return nil, err
	}
	var rows []mediaCredentialRow
	if err := s.orm.WithContext(ctx).Where("device_id = ?", deviceID).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list media credentials: %w", err)
	}
	result := make([]MediaCredential, 0, len(rows))
	for _, row := range rows {
		item, err := s.mediaCredentialFromRow(row)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) mediaCredentialFromRow(row mediaCredentialRow) (MediaCredential, error) {
	decrypted, err := s.secrets.decrypt(mediaCredentialSecretScope(row.ID, row.DeviceID), row.CredentialBlobEncrypted)
	if err != nil {
		return MediaCredential{}, fmt.Errorf("decrypt media credential %q: %w", row.ID, err)
	}
	blob, err := base64.RawStdEncoding.DecodeString(decrypted)
	if err != nil {
		return MediaCredential{}, fmt.Errorf("media credential %q is malformed", row.ID)
	}
	return MediaCredential{
		ID: row.ID, DeviceID: row.DeviceID, CredentialType: row.CredentialType, Blob: blob,
		KeyVersion: row.KeyVersion, Version: row.Version, Status: row.Status,
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
	}, nil
}

func (s *Store) DeleteMediaCredential(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media credential ID", id); err != nil {
		return err
	}
	result := s.orm.WithContext(ctx).Where("id = ?", id).Delete(&mediaCredentialRow{})
	if result.Error != nil {
		return fmt.Errorf("delete media credential: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("media credential %q not found", id)
	}
	return nil
}

func (s *Store) SaveMediaStream(ctx context.Context, item MediaStream) error {
	_, err := s.SaveMediaStreamVersioned(ctx, item)
	return err
}

// SaveMediaStreamVersioned commits the desired stream and its new global
// configuration revision in one transaction. The caller-supplied Revision is
// ignored; the returned durable version is authoritative.
func (s *Store) SaveMediaStreamVersioned(ctx context.Context, item MediaStream) (MediaConfigVersion, error) {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media stream ID", item.ID); err != nil {
		return MediaConfigVersion{}, err
	}
	if err := validateMediaIdentifier("media stream device ID", item.DeviceID); err != nil {
		return MediaConfigVersion{}, err
	}
	if err := validateMediaIdentifier("media stream protocol", item.Protocol); err != nil {
		return MediaConfigVersion{}, err
	}
	if err := validateMediaIdentifier("media stream profile", item.Profile); err != nil {
		return MediaConfigVersion{}, err
	}
	if item.CredentialRef != "" {
		if err := validateMediaIdentifier("media stream credential reference", item.CredentialRef); err != nil {
			return MediaConfigVersion{}, err
		}
	}
	if !domainmedia.Protocol(item.Protocol).Valid() {
		return MediaConfigVersion{}, fmt.Errorf("unsupported media stream protocol %q", item.Protocol)
	}
	if !domainmedia.StreamMode(item.Mode).Valid() {
		return MediaConfigVersion{}, fmt.Errorf("unsupported media stream mode %q", item.Mode)
	}
	if item.TalkbackEnabled && !item.AudioEnabled {
		return MediaConfigVersion{}, errors.New("media stream talkback requires audio")
	}
	options, err := normalizedJSON(item.OptionsJSON, "{}", "media stream options")
	if err != nil {
		return MediaConfigVersion{}, err
	}
	if err := domainmedia.ValidateLogicalConfig(options); err != nil {
		return MediaConfigVersion{}, fmt.Errorf("validate media stream options: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	var version MediaConfigVersion
	err = s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var bumpErr error
		version, bumpErr = s.bumpMediaConfigRevisionTx(tx)
		if bumpErr != nil {
			return bumpErr
		}
		row := mediaStreamRow{
			ID: item.ID, DeviceID: item.DeviceID, Protocol: item.Protocol,
			CredentialRef: item.CredentialRef, Profile: item.Profile,
			Mode: item.Mode, AudioEnabled: item.AudioEnabled, TalkbackEnabled: item.TalkbackEnabled,
			OptionsJSON: jsonDocument(options), Revision: version.Revision, Enabled: item.Enabled,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"device_id", "protocol", "credential_ref", "profile", "mode", "audio_enabled", "talkback_enabled",
				"options_json", "revision", "enabled", "updated_at",
			}),
		}).Create(&row).Error; err != nil {
			return fmt.Errorf("save media stream: %w", err)
		}
		return nil
	})
	if err != nil {
		return MediaConfigVersion{}, err
	}
	return version, nil
}

func (s *Store) GetMediaStream(ctx context.Context, id string) (MediaStream, bool, error) {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media stream ID", id); err != nil {
		return MediaStream{}, false, err
	}
	var row mediaStreamRow
	err := s.orm.WithContext(ctx).Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MediaStream{}, false, nil
	}
	if err != nil {
		return MediaStream{}, false, fmt.Errorf("read media stream: %w", err)
	}
	return mediaStreamFromRow(row), true, nil
}

func (s *Store) ListMediaStreams(ctx context.Context, deviceID string) ([]MediaStream, error) {
	defer s.observe(time.Now())
	query := s.orm.WithContext(ctx)
	if deviceID != "" {
		if err := validateMediaIdentifier("media stream device ID", deviceID); err != nil {
			return nil, err
		}
		query = query.Where("device_id = ?", deviceID)
	}
	var rows []mediaStreamRow
	if err := query.Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list media streams: %w", err)
	}
	result := make([]MediaStream, 0, len(rows))
	for _, row := range rows {
		result = append(result, mediaStreamFromRow(row))
	}
	return result, nil
}

func mediaStreamFromRow(row mediaStreamRow) MediaStream {
	return MediaStream{
		ID: row.ID, DeviceID: row.DeviceID, Protocol: row.Protocol,
		CredentialRef: row.CredentialRef, Profile: row.Profile,
		Mode: row.Mode, AudioEnabled: row.AudioEnabled, TalkbackEnabled: row.TalkbackEnabled,
		OptionsJSON: []byte(row.OptionsJSON), Revision: row.Revision, Enabled: row.Enabled,
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
	}
}

func (s *Store) DeleteMediaStream(ctx context.Context, id string) error {
	_, err := s.DeleteMediaStreamVersioned(ctx, id)
	return err
}

// DeleteMediaStreamVersioned removes a desired stream and advances the global
// configuration revision atomically.
func (s *Store) DeleteMediaStreamVersioned(ctx context.Context, id string) (MediaConfigVersion, error) {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media stream ID", id); err != nil {
		return MediaConfigVersion{}, err
	}
	var version MediaConfigVersion
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var bumpErr error
		version, bumpErr = s.bumpMediaConfigRevisionTx(tx)
		if bumpErr != nil {
			return bumpErr
		}
		result := tx.Where("id = ?", id).Delete(&mediaStreamRow{})
		if result.Error != nil {
			return fmt.Errorf("delete media stream: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("media stream %q not found", id)
		}
		return nil
	})
	if err != nil {
		return MediaConfigVersion{}, err
	}
	return version, nil
}

func (s *Store) PutMediaRuntimeValue(ctx context.Context, namespace, key string, value []byte, sensitive bool) error {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media runtime namespace", namespace); err != nil {
		return err
	}
	if err := validateMediaIdentifier("media runtime key", key); err != nil {
		return err
	}
	encoded := base64.RawStdEncoding.EncodeToString(value)
	encrypted, err := s.secrets.encrypt(mediaRuntimeSecretScope(namespace, key), encoded)
	if err != nil {
		return fmt.Errorf("encrypt media runtime value: %w", err)
	}
	row := mediaRuntimeKVRow{
		Namespace: namespace, Key: key, Value: encrypted, Sensitive: sensitive,
		UpdatedAt: time.Now().UTC().UnixMilli(),
	}
	if err := s.orm.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "namespace"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value_encrypted", "sensitive", "updated_at"}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("save media runtime value: %w", err)
	}
	return nil
}

func (s *Store) GetMediaRuntimeValue(ctx context.Context, namespace, key string) ([]byte, bool, error) {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media runtime namespace", namespace); err != nil {
		return nil, false, err
	}
	if err := validateMediaIdentifier("media runtime key", key); err != nil {
		return nil, false, err
	}
	var row mediaRuntimeKVRow
	err := s.orm.WithContext(ctx).Where("namespace = ? AND key = ?", namespace, key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read media runtime value: %w", err)
	}
	value, err := s.decodeMediaRuntimeValue(row)
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (s *Store) ListMediaRuntimeValues(ctx context.Context, namespace string) ([]MediaRuntimeValue, error) {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media runtime namespace", namespace); err != nil {
		return nil, err
	}
	var rows []mediaRuntimeKVRow
	if err := s.orm.WithContext(ctx).Where("namespace = ?", namespace).Order("key").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list media runtime values: %w", err)
	}
	result := make([]MediaRuntimeValue, 0, len(rows))
	for _, row := range rows {
		value, err := s.decodeMediaRuntimeValue(row)
		if err != nil {
			return nil, err
		}
		result = append(result, MediaRuntimeValue{
			Namespace: row.Namespace, Key: row.Key, Value: value, Sensitive: row.Sensitive,
			UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
		})
	}
	return result, nil
}

func (s *Store) decodeMediaRuntimeValue(row mediaRuntimeKVRow) ([]byte, error) {
	decrypted, err := s.secrets.decrypt(mediaRuntimeSecretScope(row.Namespace, row.Key), row.Value)
	if err != nil {
		return nil, fmt.Errorf("decrypt media runtime key %q: %w", row.Key, err)
	}
	value, err := base64.RawStdEncoding.DecodeString(decrypted)
	if err != nil {
		return nil, fmt.Errorf("media runtime key %q is malformed", row.Key)
	}
	return value, nil
}

func (s *Store) DeleteMediaRuntimeValue(ctx context.Context, namespace, key string) error {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media runtime namespace", namespace); err != nil {
		return err
	}
	if err := validateMediaIdentifier("media runtime key", key); err != nil {
		return err
	}
	if err := s.orm.WithContext(ctx).Where("namespace = ? AND key = ?", namespace, key).Delete(&mediaRuntimeKVRow{}).Error; err != nil {
		return fmt.Errorf("delete media runtime value: %w", err)
	}
	return nil
}

func (s *Store) SaveMediaAuthLease(ctx context.Context, item MediaAuthLease) error {
	defer s.observe(time.Now())
	for label, value := range map[string]string{
		"media authorization lease ID": item.ID, "media authorization worker ID": item.WorkerID,
		"media authorization worker instance ID": item.WorkerInstanceID,
		"media authorization device ID":          item.DeviceID,
		"media authorization protocol":           item.Protocol,
		"media authorization purpose":            item.Purpose,
		"media authorization request ID":         item.RequestID,
	} {
		if err := validateMediaIdentifier(label, value); err != nil {
			return err
		}
	}
	if item.MaxUses == 0 {
		item.MaxUses = 1
	}
	if item.UseCount > item.MaxUses {
		return errors.New("media authorization use count exceeds max uses")
	}
	if !domainmedia.Protocol(item.Protocol).Valid() {
		return fmt.Errorf("unsupported media authorization protocol %q", item.Protocol)
	}
	if !domainmedia.AuthorizationPurpose(item.Purpose).Valid() {
		return fmt.Errorf("unsupported media authorization purpose %q", item.Purpose)
	}
	if item.ExpiresAt.IsZero() {
		return errors.New("media authorization expiry is required")
	}
	if item.Status == "" {
		item.Status = "claimed"
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	row := mediaAuthLeaseRow{
		ID: item.ID, WorkerID: item.WorkerID, WorkerInstanceID: item.WorkerInstanceID,
		DeviceID: item.DeviceID, Protocol: item.Protocol, Purpose: item.Purpose, Status: item.Status,
		ExpiresAt: item.ExpiresAt.UTC().UnixMilli(), RequestID: item.RequestID,
		RequestMaterialHash: item.RequestMaterialHash, MaxUses: item.MaxUses, UseCount: item.UseCount,
		CreatedAt: item.CreatedAt.UTC().UnixMilli(), ClaimedAt: unixMillis(item.ClaimedAt),
		UsedAt: unixMillis(item.UsedAt), EndedAt: unixMillis(item.EndedAt),
	}
	if err := s.orm.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status", "expires_at", "request_material_hash", "max_uses", "use_count",
			"claimed_at", "used_at", "ended_at",
		}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("save media authorization lease: %w", err)
	}
	return nil
}

func (s *Store) GetMediaAuthLease(ctx context.Context, id string) (MediaAuthLease, bool, error) {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media authorization lease ID", id); err != nil {
		return MediaAuthLease{}, false, err
	}
	var row mediaAuthLeaseRow
	err := s.orm.WithContext(ctx).Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MediaAuthLease{}, false, nil
	}
	if err != nil {
		return MediaAuthLease{}, false, fmt.Errorf("read media authorization lease: %w", err)
	}
	return mediaAuthLeaseFromRow(row), true, nil
}

func (s *Store) ListMediaAuthLeases(ctx context.Context, deviceID string) ([]MediaAuthLease, error) {
	defer s.observe(time.Now())
	query := s.orm.WithContext(ctx)
	if deviceID != "" {
		if err := validateMediaIdentifier("media authorization device ID", deviceID); err != nil {
			return nil, err
		}
		query = query.Where("device_id = ?", deviceID)
	}
	var rows []mediaAuthLeaseRow
	if err := query.Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list media authorization leases: %w", err)
	}
	result := make([]MediaAuthLease, 0, len(rows))
	for _, row := range rows {
		result = append(result, mediaAuthLeaseFromRow(row))
	}
	return result, nil
}

func mediaAuthLeaseFromRow(row mediaAuthLeaseRow) MediaAuthLease {
	return MediaAuthLease{
		ID: row.ID, WorkerID: row.WorkerID, WorkerInstanceID: row.WorkerInstanceID,
		DeviceID: row.DeviceID, Protocol: row.Protocol, Purpose: row.Purpose, Status: row.Status,
		ExpiresAt: time.UnixMilli(row.ExpiresAt).UTC(), RequestID: row.RequestID,
		RequestMaterialHash: row.RequestMaterialHash, MaxUses: row.MaxUses, UseCount: row.UseCount,
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), ClaimedAt: timeFromUnixMillis(row.ClaimedAt),
		UsedAt: timeFromUnixMillis(row.UsedAt), EndedAt: timeFromUnixMillis(row.EndedAt),
	}
}

func (s *Store) DeleteMediaAuthLease(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media authorization lease ID", id); err != nil {
		return err
	}
	result := s.orm.WithContext(ctx).Where("id = ?", id).Delete(&mediaAuthLeaseRow{})
	if result.Error != nil {
		return fmt.Errorf("delete media authorization lease: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("media authorization lease %q not found", id)
	}
	return nil
}

func (s *Store) AppendMediaAuthAudit(ctx context.Context, item MediaAuthAudit) (MediaAuthAudit, error) {
	defer s.observe(time.Now())
	if err := validateMediaIdentifier("media authorization audit worker ID", item.WorkerID); err != nil {
		return MediaAuthAudit{}, err
	}
	if err := validateMediaIdentifier("media authorization audit device ID", item.DeviceID); err != nil {
		return MediaAuthAudit{}, err
	}
	if item.Action == "" || item.Result == "" {
		return MediaAuthAudit{}, errors.New("media authorization audit action and result are required")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	row := mediaAuthAuditRow{
		WorkerID: item.WorkerID, DeviceID: item.DeviceID, Provider: item.Provider,
		Action: item.Action, Result: item.Result, ErrorCode: item.ErrorCode,
		RemoteIdentity: item.RemoteIdentity, CreatedAt: item.CreatedAt.UTC().UnixMilli(),
	}
	if err := s.orm.WithContext(ctx).Create(&row).Error; err != nil {
		return MediaAuthAudit{}, fmt.Errorf("append media authorization audit: %w", err)
	}
	item.ID = row.ID
	item.CreatedAt = item.CreatedAt.UTC()
	return item, nil
}

func (s *Store) ListMediaAuthAudits(ctx context.Context, deviceID string, limit int) ([]MediaAuthAudit, error) {
	defer s.observe(time.Now())
	if deviceID != "" {
		if err := validateMediaIdentifier("media authorization audit device ID", deviceID); err != nil {
			return nil, err
		}
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query := s.orm.WithContext(ctx)
	if deviceID != "" {
		query = query.Where("device_id = ?", deviceID)
	}
	var rows []mediaAuthAuditRow
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list media authorization audits: %w", err)
	}
	result := make([]MediaAuthAudit, 0, len(rows))
	for _, row := range rows {
		result = append(result, MediaAuthAudit{
			ID: row.ID, WorkerID: row.WorkerID, DeviceID: row.DeviceID, Provider: row.Provider,
			Action: row.Action, Result: row.Result, ErrorCode: row.ErrorCode,
			RemoteIdentity: row.RemoteIdentity, CreatedAt: time.UnixMilli(row.CreatedAt).UTC(),
		})
	}
	return result, nil
}

func unixMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func timeFromUnixMillis(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}
