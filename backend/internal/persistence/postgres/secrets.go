package postgres

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
	"strconv"
	"strings"

	"gorm.io/gorm"
)

const encryptedPrefix = "enc:v1:"

type secretCodec struct{ aead cipher.AEAD }

func (s *Store) initializeSecrets(ctx context.Context) error {
	key, err := loadOrCreateMasterKey(ctx, s)
	if err != nil {
		return err
	}
	s.secrets, err = newSecretCodec(key)
	if err != nil {
		return err
	}
	if err := s.encryptPlaintextTargetPINs(ctx); err != nil {
		return err
	}
	return s.encryptPlaintextProviderConfigs(ctx)
}

func loadOrCreateMasterKey(ctx context.Context, store *Store) ([]byte, error) {
	info, err := os.Lstat(store.keyPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("master key must be a regular file")
		}
		key, readErr := readMasterKey(store.keyPath)
		if readErr != nil {
			return nil, readErr
		}
		if chmodErr := os.Chmod(store.keyPath, 0o600); chmodErr != nil {
			return nil, fmt.Errorf("secure master key permissions: %w", chmodErr)
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect master key: %w", err)
	}
	var encryptedTargets int64
	if queryErr := store.orm.WithContext(ctx).Model(&targetRow{}).Where("pin LIKE ?", encryptedPrefix+"%").Count(&encryptedTargets).Error; queryErr != nil {
		return nil, fmt.Errorf("inspect encrypted target secrets: %w", queryErr)
	}
	var encryptedProviders int64
	if queryErr := store.orm.WithContext(ctx).Model(&providerRow{}).Where("config_json::text LIKE ?", "%"+encryptedPrefix+"%").Count(&encryptedProviders).Error; queryErr != nil {
		return nil, fmt.Errorf("inspect encrypted provider secrets: %w", queryErr)
	}
	if encryptedTargets > 0 || encryptedProviders > 0 {
		return nil, errors.New("master key is missing for encrypted database secrets")
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	file, err := os.OpenFile(store.keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create master key: %w", err)
	}
	encoded := base64.RawStdEncoding.EncodeToString(key)
	if _, err = file.WriteString(encoded + "\n"); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close master key: %w", closeErr)
	}
	return key, nil
}

func readMasterKey(path string) ([]byte, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("master key is invalid")
	}
	return key, nil
}

func newSecretCodec(key []byte) (*secretCodec, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize secret encryption: %w", err)
	}
	return &secretCodec{aead: aead}, nil
}

func (c *secretCodec) encrypt(scope, plaintext string) (string, error) {
	if plaintext == "" || strings.HasPrefix(plaintext, encryptedPrefix) {
		return plaintext, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate secret nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), []byte(scope))
	return encryptedPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *secretCodec) decrypt(scope, ciphertext string) (string, error) {
	if ciphertext == "" || !strings.HasPrefix(ciphertext, encryptedPrefix) {
		return ciphertext, nil
	}
	payload, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(ciphertext, encryptedPrefix))
	if err != nil || len(payload) < c.aead.NonceSize() {
		return "", errors.New("encrypted secret is malformed")
	}
	nonce, encrypted := payload[:c.aead.NonceSize()], payload[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, encrypted, []byte(scope))
	if err != nil {
		return "", errors.New("encrypted secret authentication failed")
	}
	return string(plaintext), nil
}

func (s *Store) encryptPlaintextTargetPINs(ctx context.Context) error {
	var rows []targetRow
	if err := s.orm.WithContext(ctx).Select("id", "pin").Where("pin <> ? AND pin NOT LIKE ?", "", encryptedPrefix+"%").Find(&rows).Error; err != nil {
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
				if err := tx.Model(&providerRow{}).Where("id = ?", item.ID).Update("config_json", string(encrypted)).Error; err != nil {
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
	for _, suffix := range []string{"password", "passphrase", "secret", "token", "apikey", "privatekey"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func (s *Store) hasEncryptedTargetPINs(ctx context.Context) (bool, error) {
	if !s.orm.WithContext(ctx).Migrator().HasTable(&targetRow{}) {
		return false, nil
	}
	var encrypted int64
	if err := s.orm.WithContext(ctx).Model(&targetRow{}).Where("pin LIKE ?", encryptedPrefix+"%").Count(&encrypted).Error; err != nil {
		return false, fmt.Errorf("inspect encrypted target secrets: %w", err)
	}
	return encrypted > 0, nil
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
