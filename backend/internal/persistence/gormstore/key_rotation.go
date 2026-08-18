package gormstore

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	domainmaintenance "github.com/feranydev/homeloom/backend/internal/domain/maintenance"
	"gorm.io/gorm"
)

// MasterKeyStatus returns opaque rotation state. It deliberately counts only
// encrypted values, so an administrator can safely use it in the web UI or an
// audit record without exposing a secret, a provider ID, or a credential name.
func (s *Store) MasterKeyStatus(ctx context.Context) (domainmaintenance.MasterKeyStatus, error) {
	if s.secrets == nil {
		return domainmaintenance.MasterKeyStatus{}, errors.New("master key service is unavailable")
	}
	s.secretRotationMu.Lock()
	defer s.secretRotationMu.Unlock()
	return s.masterKeyStatus(ctx)
}

// RotateMasterKey adds a fresh active key and re-encrypts all durable secret
// values in one database transaction. The keyring is made durable before that
// transaction begins: if the process crashes or the transaction fails, both
// old and new values stay decryptable. Call ResumeMasterKeyRotation after a
// failed attempt rather than creating another active key.
func (s *Store) RotateMasterKey(ctx context.Context) (domainmaintenance.MasterKeyRotation, error) {
	if s.secrets == nil {
		return domainmaintenance.MasterKeyRotation{}, errors.New("master key service is unavailable")
	}
	s.secretRotationMu.Lock()
	defer s.secretRotationMu.Unlock()

	current := s.secrets.keyring()
	nextVersion, err := nextMasterKeyVersion(current)
	if err != nil {
		return domainmaintenance.MasterKeyRotation{}, err
	}
	nextKey := make([]byte, 32)
	if _, err := rand.Read(nextKey); err != nil {
		return domainmaintenance.MasterKeyRotation{}, fmt.Errorf("generate replacement master key: %w", err)
	}
	next := decodedMasterKeyring{active: nextVersion, keys: cloneMasterKeys(current.keys)}
	next.keys[nextVersion] = nextKey
	if err := writeMasterKeyring(s.keyPath, next, false); err != nil {
		return domainmaintenance.MasterKeyRotation{}, err
	}
	if err := s.secrets.replace(next); err != nil {
		return domainmaintenance.MasterKeyRotation{}, fmt.Errorf("activate replacement master key: %w", err)
	}
	rotation, err := s.reencryptActiveMasterKey(ctx, current.active)
	if err != nil {
		return rotation, fmt.Errorf("master key version %d is active; re-encryption can be safely resumed: %w", nextVersion, err)
	}
	return rotation, nil
}

// ResumeMasterKeyRotation safely retries the transactional batch using the
// already active key. This is the recovery path after a database outage or a
// process interruption between writing the durable keyring and committing the
// batch. It never deletes prior keys.
func (s *Store) ResumeMasterKeyRotation(ctx context.Context) (domainmaintenance.MasterKeyRotation, error) {
	if s.secrets == nil {
		return domainmaintenance.MasterKeyRotation{}, errors.New("master key service is unavailable")
	}
	s.secretRotationMu.Lock()
	defer s.secretRotationMu.Unlock()
	return s.reencryptActiveMasterKey(ctx, s.secrets.keyring().active)
}

func nextMasterKeyVersion(keyring decodedMasterKeyring) (uint32, error) {
	var largest uint32
	for version := range keyring.keys {
		if version > largest {
			largest = version
		}
	}
	if largest == 0 || largest == math.MaxUint32 {
		return 0, errors.New("cannot allocate a new master key version")
	}
	return largest + 1, nil
}

func (s *Store) reencryptActiveMasterKey(ctx context.Context, previous uint32) (domainmaintenance.MasterKeyRotation, error) {
	active := s.secrets.keyring().active
	updated, err := s.reencryptAllSecrets(ctx)
	if err != nil {
		status, statusErr := s.masterKeyStatus(ctx)
		if statusErr != nil {
			return domainmaintenance.MasterKeyRotation{PreviousVersion: previous, ActiveVersion: active}, err
		}
		return domainmaintenance.MasterKeyRotation{PreviousVersion: previous, ActiveVersion: active, Status: status}, err
	}
	status, err := s.masterKeyStatus(ctx)
	if err != nil {
		return domainmaintenance.MasterKeyRotation{PreviousVersion: previous, ActiveVersion: active, Reencrypted: updated}, err
	}
	return domainmaintenance.MasterKeyRotation{PreviousVersion: previous, ActiveVersion: active, Reencrypted: updated, Status: status}, nil
}

func (s *Store) reencryptAllSecrets(ctx context.Context) (uint64, error) {
	var updated uint64
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var targets []targetRow
		if err := tx.Find(&targets).Error; err != nil {
			return fmt.Errorf("list target secrets for rotation: %w", err)
		}
		for _, row := range targets {
			pin, changed, err := s.secrets.reencrypt("target-pin:"+row.ID, row.PIN)
			if err != nil {
				return fmt.Errorf("re-encrypt target pin: %w", err)
			}
			passcode, passcodeChanged, err := s.secrets.reencrypt("target-matter-passcode:"+row.ID, row.MatterPasscode)
			if err != nil {
				return fmt.Errorf("re-encrypt Matter target passcode: %w", err)
			}
			if !changed && !passcodeChanged {
				continue
			}
			updates := map[string]any{}
			if changed {
				updates["pin"] = pin
				updated++
			}
			if passcodeChanged {
				updates["matter_passcode"] = passcode
				updated++
			}
			if err := tx.Model(&targetRow{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
				return fmt.Errorf("update target secrets: %w", err)
			}
		}

		var matterValues []matterRuntimeKVRow
		if err := tx.Find(&matterValues).Error; err != nil {
			return fmt.Errorf("list Matter runtime values for rotation: %w", err)
		}
		for _, row := range matterValues {
			value, changed, err := s.secrets.reencrypt(matterRuntimeSecretScope(row.TargetID, row.Key), row.Value)
			if err != nil {
				return fmt.Errorf("re-encrypt Matter runtime value: %w", err)
			}
			if changed {
				if err := tx.Model(&matterRuntimeKVRow{}).Where("target_id = ? AND key = ?", row.TargetID, row.Key).Update("value", value).Error; err != nil {
					return fmt.Errorf("update Matter runtime value: %w", err)
				}
				updated++
			}
		}

		var credentials []mediaCredentialRow
		if err := tx.Find(&credentials).Error; err != nil {
			return fmt.Errorf("list media credentials for rotation: %w", err)
		}
		for _, row := range credentials {
			value, changed, err := s.secrets.reencrypt(mediaCredentialSecretScope(row.ID, row.DeviceID), row.CredentialBlobEncrypted)
			if err != nil {
				return fmt.Errorf("re-encrypt media credential: %w", err)
			}
			if changed {
				if err := tx.Model(&mediaCredentialRow{}).Where("id = ?", row.ID).Update("credential_blob_encrypted", value).Error; err != nil {
					return fmt.Errorf("update media credential: %w", err)
				}
				updated++
			}
		}

		var mediaValues []mediaRuntimeKVRow
		if err := tx.Find(&mediaValues).Error; err != nil {
			return fmt.Errorf("list media runtime values for rotation: %w", err)
		}
		for _, row := range mediaValues {
			value, changed, err := s.secrets.reencrypt(mediaRuntimeSecretScope(row.Namespace, row.Key), row.Value)
			if err != nil {
				return fmt.Errorf("re-encrypt media runtime value: %w", err)
			}
			if changed {
				if err := tx.Model(&mediaRuntimeKVRow{}).Where("namespace = ? AND key = ?", row.Namespace, row.Key).Update("value_encrypted", value).Error; err != nil {
					return fmt.Errorf("update media runtime value: %w", err)
				}
				updated++
			}
		}

		var providers []providerRow
		if err := tx.Find(&providers).Error; err != nil {
			return fmt.Errorf("list provider secrets for rotation: %w", err)
		}
		for _, row := range providers {
			config, changes, err := s.reencryptProviderConfigSecrets(row.ID, []byte(row.ConfigJSON))
			if err != nil {
				return err
			}
			if changes == 0 {
				continue
			}
			if err := tx.Model(&providerRow{}).Where("id = ?", row.ID).Update("config_json", jsonDocument(config)).Error; err != nil {
				return fmt.Errorf("update provider secrets: %w", err)
			}
			updated += changes
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return updated, nil
}

func (s *Store) reencryptProviderConfigSecrets(providerID string, raw []byte) ([]byte, uint64, error) {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, 0, fmt.Errorf("decode provider %q secrets for rotation: %w", providerID, err)
	}
	changes, err := s.reencryptProviderSecretValue(providerID, "$", value)
	if err != nil {
		return nil, 0, err
	}
	if changes == 0 {
		return append([]byte(nil), raw...), 0, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, 0, fmt.Errorf("encode provider %q secrets after rotation: %w", providerID, err)
	}
	return encoded, changes, nil
}

func (s *Store) reencryptProviderSecretValue(providerID, path string, value any) (uint64, error) {
	switch current := value.(type) {
	case map[string]any:
		var changes uint64
		for key, child := range current {
			childPath := path + "." + key
			if providerConfigSecretKey(key) {
				plaintext, ok := child.(string)
				if !ok || plaintext == "" {
					continue
				}
				rotated, changed, err := s.secrets.reencrypt("provider-config:"+providerID+":"+childPath, plaintext)
				if err != nil {
					return 0, fmt.Errorf("re-encrypt provider %q secret %s: %w", providerID, childPath, err)
				}
				if changed {
					current[key] = rotated
					changes++
				}
				continue
			}
			childChanges, err := s.reencryptProviderSecretValue(providerID, childPath, child)
			if err != nil {
				return 0, err
			}
			changes += childChanges
		}
		return changes, nil
	case []any:
		var changes uint64
		for index, child := range current {
			childChanges, err := s.reencryptProviderSecretValue(providerID, path+"["+strconv.Itoa(index)+"]", child)
			if err != nil {
				return 0, err
			}
			changes += childChanges
		}
		return changes, nil
	default:
		return 0, nil
	}
}

func (s *Store) masterKeyStatus(ctx context.Context) (domainmaintenance.MasterKeyStatus, error) {
	keyring := s.secrets.keyring()
	status := domainmaintenance.MasterKeyStatus{
		ActiveVersion:        keyring.active,
		CiphertextsByVersion: make(map[uint32]uint64),
	}
	for version := range keyring.keys {
		status.RetainedVersions = append(status.RetainedVersions, version)
	}
	sort.Slice(status.RetainedVersions, func(i, j int) bool { return status.RetainedVersions[i] < status.RetainedVersions[j] })
	add := func(value string) {
		version, legacy, encrypted := encryptedSecretVersion(value)
		if !encrypted {
			return
		}
		status.CiphertextsByVersion[version]++
		if legacy {
			status.LegacyCiphertexts++
		}
		if version != status.ActiveVersion || legacy {
			status.NeedsReencryption = true
		}
	}

	var targets []targetRow
	if err := s.orm.WithContext(ctx).Select("pin", "matter_passcode").Find(&targets).Error; err != nil {
		return domainmaintenance.MasterKeyStatus{}, fmt.Errorf("list target secret status: %w", err)
	}
	for _, row := range targets {
		add(row.PIN)
		add(row.MatterPasscode)
	}
	var matterValues []matterRuntimeKVRow
	if err := s.orm.WithContext(ctx).Select("value").Find(&matterValues).Error; err != nil {
		return domainmaintenance.MasterKeyStatus{}, fmt.Errorf("list Matter runtime secret status: %w", err)
	}
	for _, row := range matterValues {
		add(row.Value)
	}
	var credentials []mediaCredentialRow
	if err := s.orm.WithContext(ctx).Select("credential_blob_encrypted").Find(&credentials).Error; err != nil {
		return domainmaintenance.MasterKeyStatus{}, fmt.Errorf("list media credential status: %w", err)
	}
	for _, row := range credentials {
		add(row.CredentialBlobEncrypted)
	}
	var mediaValues []mediaRuntimeKVRow
	if err := s.orm.WithContext(ctx).Select("value_encrypted").Find(&mediaValues).Error; err != nil {
		return domainmaintenance.MasterKeyStatus{}, fmt.Errorf("list media runtime secret status: %w", err)
	}
	for _, row := range mediaValues {
		add(row.Value)
	}
	var providers []providerRow
	if err := s.orm.WithContext(ctx).Select("config_json").Find(&providers).Error; err != nil {
		return domainmaintenance.MasterKeyStatus{}, fmt.Errorf("list provider secret status: %w", err)
	}
	for _, row := range providers {
		if err := countProviderEncryptedSecrets([]byte(row.ConfigJSON), add); err != nil {
			return domainmaintenance.MasterKeyStatus{}, err
		}
	}
	return status, nil
}

func countProviderEncryptedSecrets(raw []byte, add func(string)) error {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode provider secret status: %w", err)
	}
	var visit func(any)
	visit = func(current any) {
		switch item := current.(type) {
		case map[string]any:
			for key, child := range item {
				if providerConfigSecretKey(key) {
					if text, ok := child.(string); ok {
						add(text)
					}
					continue
				}
				visit(child)
			}
		case []any:
			for _, child := range item {
				visit(child)
			}
		}
	}
	visit(value)
	return nil
}

func encryptedSecretVersion(value string) (version uint32, legacy, encrypted bool) {
	if strings.HasPrefix(value, legacyEncryptedPrefix) {
		return 1, true, true
	}
	if !strings.HasPrefix(value, versionedEncryptedPrefix) {
		return 0, false, false
	}
	parts := strings.Split(strings.TrimPrefix(value, versionedEncryptedPrefix), ":")
	if len(parts) != 2 {
		return 0, false, false
	}
	parsed, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != parts[0] {
		return 0, false, false
	}
	return uint32(parsed), false, true
}
