package sqlite

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
)

const encryptedPrefix = "enc:v1:"

type secretCodec struct{ aead cipher.AEAD }

func (s *Store) initializeSecrets(ctx context.Context) error {
	key, err := loadOrCreateMasterKey(ctx, s)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("initialize secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("initialize secret encryption: %w", err)
	}
	s.secrets = &secretCodec{aead: aead}
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
		encoded, readErr := os.ReadFile(store.keyPath)
		if readErr != nil {
			return nil, fmt.Errorf("read master key: %w", readErr)
		}
		key, decodeErr := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
		if decodeErr != nil || len(key) != 32 {
			return nil, errors.New("master key is invalid")
		}
		if chmodErr := os.Chmod(store.keyPath, 0o600); chmodErr != nil {
			return nil, fmt.Errorf("secure master key permissions: %w", chmodErr)
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect master key: %w", err)
	}
	var encryptedTargets int
	if queryErr := store.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM targets WHERE pin LIKE 'enc:v1:%'").Scan(&encryptedTargets); queryErr != nil {
		return nil, fmt.Errorf("inspect encrypted target secrets: %w", queryErr)
	}
	var encryptedProviders int
	if queryErr := store.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM providers WHERE config_json LIKE '%enc:v1:%'").Scan(&encryptedProviders); queryErr != nil {
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
	rows, err := s.database.QueryContext(ctx, "SELECT id, pin FROM targets WHERE pin <> '' AND pin NOT LIKE 'enc:v1:%'")
	if err != nil {
		return fmt.Errorf("list plaintext target secrets: %w", err)
	}
	type targetPIN struct{ id, pin string }
	items := make([]targetPIN, 0)
	for rows.Next() {
		var item targetPIN
		if err := rows.Scan(&item.id, &item.pin); err != nil {
			rows.Close()
			return fmt.Errorf("scan plaintext target secret: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close plaintext target secrets: %w", err)
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin target secret encryption: %w", err)
	}
	defer transaction.Rollback()
	for _, item := range items {
		encrypted, err := s.secrets.encrypt("target-pin:"+item.id, item.pin)
		if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, "UPDATE targets SET pin = ? WHERE id = ?", encrypted, item.id); err != nil {
			return fmt.Errorf("encrypt target secret: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit target secret encryption: %w", err)
	}
	return nil
}

func (s *Store) encryptPlaintextProviderConfigs(ctx context.Context) error {
	rows, err := s.database.QueryContext(ctx, "SELECT id, config_json FROM providers")
	if err != nil {
		return fmt.Errorf("list plaintext provider secrets: %w", err)
	}
	type providerConfig struct{ id, raw string }
	items := make([]providerConfig, 0)
	for rows.Next() {
		var item providerConfig
		if err := rows.Scan(&item.id, &item.raw); err != nil {
			rows.Close()
			return fmt.Errorf("scan plaintext provider secret: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close plaintext provider secrets: %w", err)
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin provider secret encryption: %w", err)
	}
	defer transaction.Rollback()
	for _, item := range items {
		encrypted, changed, err := s.transformProviderConfigSecrets(item.id, []byte(item.raw), true)
		if err != nil {
			return err
		}
		if changed {
			if _, err := transaction.ExecContext(ctx, "UPDATE providers SET config_json = ? WHERE id = ?", string(encrypted), item.id); err != nil {
				return fmt.Errorf("encrypt provider secret: %w", err)
			}
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit provider secret encryption: %w", err)
	}
	return nil
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
	for _, suffix := range []string{"password", "passphrase", "secret", "token", "apikey", "privatekey"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func (s *Store) hasEncryptedTargetPINs(ctx context.Context) (bool, error) {
	var tables int
	if err := s.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'targets'").Scan(&tables); err != nil {
		return false, fmt.Errorf("inspect target table: %w", err)
	}
	if tables == 0 {
		return false, nil
	}
	var encrypted int
	if err := s.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM targets WHERE pin LIKE 'enc:v1:%'").Scan(&encrypted); err != nil {
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
