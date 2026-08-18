package gormstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gorm.io/gorm"
)

const (
	legacyEncryptedPrefix    = "enc:v1:"
	versionedEncryptedPrefix = "enc:v2:"
	// encryptedPrefix remains for schema/backup checks that only need to know
	// that a modern value is encrypted. New writes always use this format.
	encryptedPrefix       = versionedEncryptedPrefix
	masterKeyringFormat   = "homeloom-master-keyring"
	masterKeyringFormatV1 = 1
)

// masterKeyring is deliberately stored separately from database values. The
// active key is used for every new write while retained keys are read-only and
// exist solely to decrypt data and backups created before a rotation.
//
// The keyring never contains a key identifier derived from secret plaintext;
// the ciphertext's version is authenticated by AES-GCM's associated data.
type masterKeyring struct {
	Format        string            `json:"format"`
	FormatVersion uint32            `json:"formatVersion"`
	Active        uint32            `json:"active"`
	Keys          map[uint32]string `json:"keys"`
}

type decodedMasterKeyring struct {
	active uint32
	keys   map[uint32][]byte
}

type secretCodec struct {
	mu     sync.RWMutex
	active uint32
	aeads  map[uint32]cipher.AEAD
	keys   map[uint32][]byte
}

func (s *Store) initializeSecrets(ctx context.Context) error {
	keyring, err := loadOrCreateMasterKeyring(ctx, s)
	if err != nil {
		return err
	}
	s.secrets, err = newSecretCodec(keyring)
	if err != nil {
		return err
	}
	if err := s.encryptPlaintextTargetPINs(ctx); err != nil {
		return err
	}
	if err := s.encryptPlaintextMatterPasscodes(ctx); err != nil {
		return err
	}
	if err := s.encryptPlaintextMatterRuntimeValues(ctx); err != nil {
		return err
	}
	if err := s.encryptPlaintextMediaCredentials(ctx); err != nil {
		return err
	}
	if err := s.encryptPlaintextMediaRuntimeValues(ctx); err != nil {
		return err
	}
	return s.encryptPlaintextProviderConfigs(ctx)
}

func loadOrCreateMasterKeyring(ctx context.Context, store *Store) (decodedMasterKeyring, error) {
	info, err := os.Lstat(store.keyPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return decodedMasterKeyring{}, errors.New("master key must be a regular file")
		}
		keyring, readErr := readMasterKeyring(store.keyPath)
		if readErr != nil {
			return decodedMasterKeyring{}, readErr
		}
		if chmodErr := os.Chmod(store.keyPath, 0o600); chmodErr != nil {
			return decodedMasterKeyring{}, fmt.Errorf("secure master key permissions: %w", chmodErr)
		}
		return keyring, nil
	}
	if !os.IsNotExist(err) {
		return decodedMasterKeyring{}, fmt.Errorf("inspect master key: %w", err)
	}
	var encryptedTargets int64
	if queryErr := store.orm.WithContext(ctx).Model(&targetRow{}).Where("pin LIKE ?", "enc:v%").Count(&encryptedTargets).Error; queryErr != nil {
		return decodedMasterKeyring{}, fmt.Errorf("inspect encrypted target secrets: %w", queryErr)
	}
	var encryptedMatterTargets int64
	if queryErr := store.orm.WithContext(ctx).Model(&targetRow{}).Where("matter_passcode LIKE ?", "enc:v%").Count(&encryptedMatterTargets).Error; queryErr != nil {
		return decodedMasterKeyring{}, fmt.Errorf("inspect encrypted Matter target secrets: %w", queryErr)
	}
	var matterIdentityRows int64
	if queryErr := store.orm.WithContext(ctx).Model(&matterRuntimeKVRow{}).Count(&matterIdentityRows).Error; queryErr != nil {
		return decodedMasterKeyring{}, fmt.Errorf("inspect Matter runtime identities: %w", queryErr)
	}
	var mediaCredentialRows int64
	if queryErr := store.orm.WithContext(ctx).Model(&mediaCredentialRow{}).Count(&mediaCredentialRows).Error; queryErr != nil {
		return decodedMasterKeyring{}, fmt.Errorf("inspect media credentials: %w", queryErr)
	}
	var mediaRuntimeRows int64
	if queryErr := store.orm.WithContext(ctx).Model(&mediaRuntimeKVRow{}).Count(&mediaRuntimeRows).Error; queryErr != nil {
		return decodedMasterKeyring{}, fmt.Errorf("inspect media runtime identities: %w", queryErr)
	}
	var encryptedProviders int64
	providerSecretQuery := "config_json LIKE ?"
	if store.databaseKind == databasePostgreSQL {
		providerSecretQuery = "config_json::text LIKE ?"
	}
	if queryErr := store.orm.WithContext(ctx).Model(&providerRow{}).Where(providerSecretQuery, "%enc:v%").Count(&encryptedProviders).Error; queryErr != nil {
		return decodedMasterKeyring{}, fmt.Errorf("inspect encrypted provider secrets: %w", queryErr)
	}
	if encryptedTargets > 0 || encryptedMatterTargets > 0 || matterIdentityRows > 0 ||
		mediaCredentialRows > 0 || mediaRuntimeRows > 0 || encryptedProviders > 0 {
		return decodedMasterKeyring{}, errors.New("master key is missing for encrypted database secrets")
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return decodedMasterKeyring{}, fmt.Errorf("generate master key: %w", err)
	}
	keyring := decodedMasterKeyring{active: 1, keys: map[uint32][]byte{1: key}}
	if err := writeMasterKeyring(store.keyPath, keyring, true); err != nil {
		return decodedMasterKeyring{}, err
	}
	return keyring, nil
}

// readMasterKey is retained for backup/restore validation callers. It returns
// the active key, never an older retained key.
func readMasterKey(path string) ([]byte, error) {
	keyring, err := readMasterKeyring(path)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), keyring.keys[keyring.active]...), nil
}

func readMasterKeyring(path string) (decodedMasterKeyring, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return decodedMasterKeyring{}, fmt.Errorf("read master key: %w", err)
	}
	trimmed := strings.TrimSpace(string(encoded))
	// The original raw base64 file is version 1. Keep accepting it so existing
	// encrypted databases can upgrade without an out-of-band migration.
	if key, decodeErr := base64.RawStdEncoding.DecodeString(trimmed); decodeErr == nil && len(key) == 32 {
		return decodedMasterKeyring{active: 1, keys: map[uint32][]byte{1: key}}, nil
	}
	var encodedKeyring masterKeyring
	if err := json.Unmarshal([]byte(trimmed), &encodedKeyring); err != nil {
		return decodedMasterKeyring{}, errors.New("master key is invalid")
	}
	if encodedKeyring.Format != masterKeyringFormat || encodedKeyring.FormatVersion != masterKeyringFormatV1 || encodedKeyring.Active == 0 || len(encodedKeyring.Keys) == 0 {
		return decodedMasterKeyring{}, errors.New("master key is invalid")
	}
	keys := make(map[uint32][]byte, len(encodedKeyring.Keys))
	for version, value := range encodedKeyring.Keys {
		key, decodeErr := base64.RawStdEncoding.DecodeString(value)
		if version == 0 || decodeErr != nil || len(key) != 32 {
			return decodedMasterKeyring{}, errors.New("master key is invalid")
		}
		keys[version] = key
	}
	if _, ok := keys[encodedKeyring.Active]; !ok {
		return decodedMasterKeyring{}, errors.New("master key is invalid")
	}
	return decodedMasterKeyring{active: encodedKeyring.Active, keys: keys}, nil
}

func writeMasterKeyring(path string, keyring decodedMasterKeyring, create bool) error {
	if keyring.active == 0 || len(keyring.keys) == 0 {
		return errors.New("master key is invalid")
	}
	encoded := masterKeyring{Format: masterKeyringFormat, FormatVersion: masterKeyringFormatV1, Active: keyring.active, Keys: make(map[uint32]string, len(keyring.keys))}
	for version, key := range keyring.keys {
		if version == 0 || len(key) != 32 {
			return errors.New("master key is invalid")
		}
		encoded.Keys[version] = base64.RawStdEncoding.EncodeToString(key)
	}
	if _, ok := encoded.Keys[keyring.active]; !ok {
		return errors.New("master key is invalid")
	}
	payload, err := json.Marshal(encoded)
	if err != nil {
		return fmt.Errorf("encode master key: %w", err)
	}
	payload = append(payload, '\n')
	if create {
		file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			return fmt.Errorf("create master key: %w", openErr)
		}
		if _, err = file.Write(payload); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return fmt.Errorf("write master key: %w", err)
		}
		if closeErr != nil {
			return fmt.Errorf("close master key: %w", closeErr)
		}
		return nil
	}
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".homeloom-master-key-")
	if err != nil {
		return fmt.Errorf("create master key update: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err == nil {
		_, err = file.Write(payload)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("write master key update: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close master key update: %w", closeErr)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace master key: %w", err)
	}
	// Persist the directory entry too: otherwise a power failure can leave the
	// old name after a successfully committed database re-encryption.
	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open master key directory: %w", err)
	}
	err = dir.Sync()
	closeErr = dir.Close()
	if err != nil {
		return fmt.Errorf("sync master key directory: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close master key directory: %w", closeErr)
	}
	return nil
}

func newSecretCodec(keyring decodedMasterKeyring) (*secretCodec, error) {
	aeads, err := keyringAEADs(keyring)
	if err != nil {
		return nil, err
	}
	return &secretCodec{active: keyring.active, aeads: aeads, keys: cloneMasterKeys(keyring.keys)}, nil
}

func keyringAEADs(keyring decodedMasterKeyring) (map[uint32]cipher.AEAD, error) {
	if keyring.active == 0 || len(keyring.keys) == 0 {
		return nil, errors.New("master key is invalid")
	}
	aeads := make(map[uint32]cipher.AEAD, len(keyring.keys))
	for version, key := range keyring.keys {
		if version == 0 {
			return nil, errors.New("master key is invalid")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("initialize secret cipher: %w", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("initialize secret encryption: %w", err)
		}
		aeads[version] = aead
	}
	if _, ok := aeads[keyring.active]; !ok {
		return nil, errors.New("master key is invalid")
	}
	return aeads, nil
}

func (c *secretCodec) encrypt(scope, plaintext string) (string, error) {
	if plaintext == "" || isEncryptedSecret(plaintext) {
		return plaintext, nil
	}
	c.mu.RLock()
	version, aead := c.active, c.aeads[c.active]
	c.mu.RUnlock()
	if aead == nil {
		return "", errors.New("active master key is unavailable")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate secret nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), versionedAssociatedData(scope, version))
	return versionedEncryptedPrefix + strconv.FormatUint(uint64(version), 10) + ":" + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *secretCodec) decrypt(scope, ciphertext string) (string, error) {
	if ciphertext == "" || !isEncryptedSecret(ciphertext) {
		return ciphertext, nil
	}
	if strings.HasPrefix(ciphertext, legacyEncryptedPrefix) {
		return c.decryptLegacy(scope, strings.TrimPrefix(ciphertext, legacyEncryptedPrefix))
	}
	parts := strings.Split(strings.TrimPrefix(ciphertext, versionedEncryptedPrefix), ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("encrypted secret is malformed")
	}
	version, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil || version == 0 || strconv.FormatUint(version, 10) != parts[0] {
		return "", errors.New("encrypted secret is malformed")
	}
	c.mu.RLock()
	aead := c.aeads[uint32(version)]
	c.mu.RUnlock()
	if aead == nil {
		return "", errors.New("encrypted secret requires an unavailable master key version")
	}
	payload, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil || len(payload) < aead.NonceSize() {
		return "", errors.New("encrypted secret is malformed")
	}
	nonce, encrypted := payload[:aead.NonceSize()], payload[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, encrypted, versionedAssociatedData(scope, uint32(version)))
	if err != nil {
		return "", errors.New("encrypted secret authentication failed")
	}
	return string(plaintext), nil
}

func (c *secretCodec) decryptLegacy(scope, encoded string) (string, error) {
	c.mu.RLock()
	aead := c.aeads[1]
	c.mu.RUnlock()
	if aead == nil {
		return "", errors.New("encrypted secret requires an unavailable master key version")
	}
	payload, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(payload) < aead.NonceSize() {
		return "", errors.New("encrypted secret is malformed")
	}
	nonce, encrypted := payload[:aead.NonceSize()], payload[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, encrypted, []byte(scope))
	if err != nil {
		return "", errors.New("encrypted secret authentication failed")
	}
	return string(plaintext), nil
}

// reencrypt always emits the active version for a non-empty value. It is used
// exclusively by the rotation transaction; ordinary writes continue to leave
// already encrypted values untouched to avoid accidental double encryption.
func (c *secretCodec) reencrypt(scope, value string) (string, bool, error) {
	if value == "" {
		return value, false, nil
	}
	plaintext, err := c.decrypt(scope, value)
	if err != nil {
		return "", false, err
	}
	c.mu.RLock()
	version, aead := c.active, c.aeads[c.active]
	c.mu.RUnlock()
	if aead == nil {
		return "", false, errors.New("active master key is unavailable")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", false, fmt.Errorf("generate secret nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), versionedAssociatedData(scope, version))
	return versionedEncryptedPrefix + strconv.FormatUint(uint64(version), 10) + ":" + base64.RawStdEncoding.EncodeToString(sealed), true, nil
}

func (c *secretCodec) keyring() decodedMasterKeyring {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return decodedMasterKeyring{active: c.active, keys: cloneMasterKeys(c.keys)}
}

func (c *secretCodec) replace(keyring decodedMasterKeyring) error {
	aeads, err := keyringAEADs(keyring)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.active, c.aeads, c.keys = keyring.active, aeads, cloneMasterKeys(keyring.keys)
	c.mu.Unlock()
	return nil
}

func cloneMasterKeys(input map[uint32][]byte) map[uint32][]byte {
	result := make(map[uint32][]byte, len(input))
	for version, key := range input {
		result[version] = append([]byte(nil), key...)
	}
	return result
}

func versionedAssociatedData(scope string, version uint32) []byte {
	return []byte("homeloom-secret:v2:" + strconv.FormatUint(uint64(version), 10) + ":" + scope)
}

func isEncryptedSecret(value string) bool {
	return strings.HasPrefix(value, legacyEncryptedPrefix) || strings.HasPrefix(value, versionedEncryptedPrefix)
}

func (s *Store) encryptPlaintextTargetPINs(ctx context.Context) error {
	var rows []targetRow
	if err := s.orm.WithContext(ctx).Select("id", "pin").Where("pin <> ? AND pin NOT LIKE ?", "", "enc:v%").Find(&rows).Error; err != nil {
		return fmt.Errorf("list plaintext target secrets: %w", err)
	}
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range rows {
			encrypted, err := s.secrets.encrypt("target-pin:"+item.ID, item.PIN)
			if err != nil {
				return err
			}
			if err := tx.Model(&targetRow{}).Where("id = ?", item.ID).Update("pin", encrypted).Error; err != nil {
				return fmt.Errorf("encrypt target secret: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) encryptPlaintextMatterPasscodes(ctx context.Context) error {
	var rows []targetRow
	if err := s.orm.WithContext(ctx).Select("id", "matter_passcode").Where("matter_passcode <> ? AND matter_passcode NOT LIKE ?", "", "enc:v%").Find(&rows).Error; err != nil {
		return fmt.Errorf("list plaintext Matter target secrets: %w", err)
	}
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range rows {
			encrypted, err := s.secrets.encrypt("target-matter-passcode:"+item.ID, item.MatterPasscode)
			if err != nil {
				return err
			}
			if err := tx.Model(&targetRow{}).Where("id = ?", item.ID).Update("matter_passcode", encrypted).Error; err != nil {
				return fmt.Errorf("encrypt Matter target secret: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) encryptPlaintextMatterRuntimeValues(ctx context.Context) error {
	var rows []matterRuntimeKVRow
	if err := s.orm.WithContext(ctx).Where("value NOT LIKE ?", "enc:v%").Find(&rows).Error; err != nil {
		return fmt.Errorf("list plaintext Matter runtime values: %w", err)
	}
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range rows {
			encrypted, err := s.secrets.encrypt(matterRuntimeSecretScope(item.TargetID, item.Key), item.Value)
			if err != nil {
				return err
			}
			if err := tx.Model(&matterRuntimeKVRow{}).Where("target_id = ? AND key = ?", item.TargetID, item.Key).Update("value", encrypted).Error; err != nil {
				return fmt.Errorf("encrypt Matter runtime value: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) encryptPlaintextMediaCredentials(ctx context.Context) error {
	var rows []mediaCredentialRow
	if err := s.orm.WithContext(ctx).
		Where("credential_blob_encrypted NOT LIKE ?", "enc:v%").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("list plaintext media credentials: %w", err)
	}
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range rows {
			encrypted, err := s.secrets.encrypt(
				mediaCredentialSecretScope(item.ID, item.DeviceID),
				item.CredentialBlobEncrypted,
			)
			if err != nil {
				return err
			}
			if err := tx.Model(&mediaCredentialRow{}).Where("id = ?", item.ID).
				Update("credential_blob_encrypted", encrypted).Error; err != nil {
				return fmt.Errorf("encrypt media credential: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) encryptPlaintextMediaRuntimeValues(ctx context.Context) error {
	var rows []mediaRuntimeKVRow
	if err := s.orm.WithContext(ctx).Where("value_encrypted NOT LIKE ?", "enc:v%").Find(&rows).Error; err != nil {
		return fmt.Errorf("list plaintext media runtime values: %w", err)
	}
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range rows {
			encrypted, err := s.secrets.encrypt(mediaRuntimeSecretScope(item.Namespace, item.Key), item.Value)
			if err != nil {
				return err
			}
			if err := tx.Model(&mediaRuntimeKVRow{}).
				Where("namespace = ? AND key = ?", item.Namespace, item.Key).
				Update("value_encrypted", encrypted).Error; err != nil {
				return fmt.Errorf("encrypt media runtime value: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) encryptPlaintextProviderConfigs(ctx context.Context) error {
	var rows []providerRow
	if err := s.orm.WithContext(ctx).Select("id", "config_json").Find(&rows).Error; err != nil {
		return fmt.Errorf("list plaintext provider secrets: %w", err)
	}
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range rows {
			encrypted, changed, err := s.transformProviderConfigSecrets(item.ID, []byte(item.ConfigJSON), true)
			if err != nil {
				return err
			}
			if changed {
				if err := tx.Model(&providerRow{}).Where("id = ?", item.ID).Update("config_json", jsonDocument(encrypted)).Error; err != nil {
					return fmt.Errorf("encrypt provider secret: %w", err)
				}
			}
		}
		return nil
	})
}

func (s *Store) transformProviderConfigSecrets(providerID string, raw []byte, encrypt bool) ([]byte, bool, error) {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, fmt.Errorf("decode provider %q secrets: %w", providerID, err)
	}
	changed, err := s.transformProviderSecretValue(providerID, "$", value, encrypt)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return append([]byte(nil), raw...), false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false, fmt.Errorf("encode provider %q secrets: %w", providerID, err)
	}
	return encoded, true, nil
}

func (s *Store) transformProviderSecretValue(providerID, path string, value any, encrypt bool) (bool, error) {
	switch current := value.(type) {
	case map[string]any:
		changed := false
		for childKey, child := range current {
			childPath := path + "." + childKey
			if providerConfigSecretKey(childKey) {
				plaintext, ok := child.(string)
				if !ok || plaintext == "" {
					continue
				}
				var transformed string
				var err error
				if encrypt {
					transformed, err = s.secrets.encrypt("provider-config:"+providerID+":"+childPath, plaintext)
				} else {
					transformed, err = s.secrets.decrypt("provider-config:"+providerID+":"+childPath, plaintext)
				}
				if err != nil {
					return false, fmt.Errorf("transform provider %q secret %s: %w", providerID, childPath, err)
				}
				if transformed != plaintext {
					current[childKey], changed = transformed, true
				}
				continue
			}
			childChanged, err := s.transformProviderSecretValue(providerID, childPath, child, encrypt)
			if err != nil {
				return false, err
			}
			changed = changed || childChanged
		}
		return changed, nil
	case []any:
		changed := false
		for index, child := range current {
			childChanged, err := s.transformProviderSecretValue(providerID, path+"["+strconv.Itoa(index)+"]", child, encrypt)
			if err != nil {
				return false, err
			}
			changed = changed || childChanged
		}
		return changed, nil
	default:
		return false, nil
	}
}

func providerConfigSecretKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	if normalized == "ssecurity" {
		return true
	}
	for _, suffix := range []string{"password", "passphrase", "secret", "token", "apikey", "privatekey", "devicekey"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func copyPrivateFile(source, destination string) error {
	info, err := os.Lstat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("source master key must be a regular file")
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(content); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
