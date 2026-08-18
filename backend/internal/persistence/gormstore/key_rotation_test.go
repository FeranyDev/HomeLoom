package gormstore

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

func TestMasterKeyRotationReencryptsEveryDurableSecretAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}

	apple := target.Config{ID: "apple-rotation", Type: "apple-hap", Name: "Apple", Address: ":51829", Pin: "23456789", SetupID: "ROT8", StorePath: "./data/hap/apple-rotation"}
	if err := store.SaveTarget(ctx, apple); err != nil {
		t.Fatal(err)
	}
	matter := saveMatterTarget(t, ctx, store, "matter-rotation")
	if err := store.PutMatterRuntimeValue(ctx, matter.ID, "fabric", []byte("matter-secret-canary")); err != nil {
		t.Fatal(err)
	}
	provider := providerconfig.Config{ID: "provider-rotation", Type: "virtual", Name: "Provider", Config: []byte(`{"accessToken":"provider-secret-canary","public":"visible"}`)}
	if err := store.SaveProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	saveTestMediaSource(t, ctx, store, "camera-rotation")
	if err := store.SaveMediaCredential(ctx, MediaCredential{ID: "credential-rotation", DeviceID: "camera-rotation", CredentialType: "static_password", Blob: []byte("media-secret-canary")}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutMediaRuntimeValue(ctx, "camera-output/camera-rotation", "identity", []byte("media-runtime-secret-canary"), true); err != nil {
		t.Fatal(err)
	}

	before, err := store.MasterKeyStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.ActiveVersion != 1 || before.NeedsReencryption || before.CiphertextsByVersion[1] == 0 {
		t.Fatalf("status before rotation = %#v", before)
	}
	rotation, err := store.RotateMasterKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rotation.PreviousVersion != 1 || rotation.ActiveVersion != 2 || rotation.Reencrypted < 6 || rotation.Status.NeedsReencryption || rotation.Status.CiphertextsByVersion[2] < 6 {
		t.Fatalf("rotation = %#v", rotation)
	}
	if !slicesEqual(rotation.Status.RetainedVersions, []uint32{1, 2}) {
		t.Fatalf("retained versions = %#v", rotation.Status.RetainedVersions)
	}
	keyring, err := readMasterKeyring(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if keyring.active != 2 || len(keyring.keys) != 2 {
		t.Fatalf("keyring = %#v", keyring)
	}

	for _, check := range []struct {
		model any
		field string
		where string
		args  []any
	}{
		{&targetRow{}, "pin", "id = ?", []any{apple.ID}},
		{&targetRow{}, "matter_passcode", "id = ?", []any{matter.ID}},
		{&matterRuntimeKVRow{}, "value", "target_id = ? AND key = ?", []any{matter.ID, "fabric"}},
		{&mediaCredentialRow{}, "credential_blob_encrypted", "id = ?", []any{"credential-rotation"}},
		{&mediaRuntimeKVRow{}, "value_encrypted", "namespace = ? AND key = ?", []any{"camera-output/camera-rotation", "identity"}},
	} {
		var value string
		if err := store.orm.WithContext(ctx).Model(check.model).Select(check.field).Where(check.where, check.args...).Scan(&value).Error; err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(value, "enc:v2:2:") {
			t.Fatalf("rotated %s = %q", check.field, value)
		}
	}
	var providerRaw string
	if err := store.orm.WithContext(ctx).Model(&providerRow{}).Select("config_json").Where("id = ?", provider.ID).Scan(&providerRaw).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(providerRaw, "enc:v2:2:") || strings.Contains(providerRaw, "provider-secret-canary") {
		t.Fatalf("rotated provider config = %s", providerRaw)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	targets, err := store.ListTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if findTargetPIN(targets, apple.ID) != apple.Pin || findMatterPasscode(targets, matter.ID) != matter.MatterConfig.Passcode {
		t.Fatalf("decrypted targets = %#v", targets)
	}
	if value, found, err := store.GetMatterRuntimeValue(ctx, matter.ID, "fabric"); err != nil || !found || string(value) != "matter-secret-canary" {
		t.Fatalf("Matter runtime = %q, %v, %v", value, found, err)
	}
	if value, found, err := store.GetMediaCredential(ctx, "credential-rotation"); err != nil || !found || string(value.Blob) != "media-secret-canary" {
		t.Fatalf("media credential = %#v, %v, %v", value, found, err)
	}
	if value, found, err := store.GetMediaRuntimeValue(ctx, "camera-output/camera-rotation", "identity"); err != nil || !found || string(value) != "media-runtime-secret-canary" {
		t.Fatalf("media runtime = %q, %v, %v", value, found, err)
	}
	providers, err := store.ListProviders(ctx)
	if err != nil || !strings.Contains(string(providerConfigByID(providers, provider.ID)), "provider-secret-canary") {
		t.Fatalf("providers = %#v, %v", providers, err)
	}
}

func TestMasterKeyRotationReadsLegacyRawKeyAndCiphertext(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	path := t.TempDir() + "/legacy.key"
	if err := os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyring, err := readMasterKeyring(path)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := newSecretCodec(keyring)
	if err != nil {
		t.Fatal(err)
	}
	legacy := encryptLegacySecret(t, key, "target-pin:legacy", "12345678")
	plain, err := codec.decrypt("target-pin:legacy", legacy)
	if err != nil || plain != "12345678" {
		t.Fatalf("legacy decrypt = %q, %v", plain, err)
	}
	rotated, changed, err := codec.reencrypt("target-pin:legacy", legacy)
	if err != nil || !changed || !strings.HasPrefix(rotated, "enc:v2:1:") {
		t.Fatalf("legacy re-encrypt = %q, %v, %v", rotated, changed, err)
	}
	if plain, err = codec.decrypt("target-pin:legacy", rotated); err != nil || plain != "12345678" {
		t.Fatalf("modern decrypt = %q, %v", plain, err)
	}
}

func TestMasterKeyRotationKeepsBothKeysAndCanResumeAfterTransactionalFailure(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item := target.Config{ID: "resume-rotation", Type: "apple-hap", Name: "Resume", Address: ":51831", Pin: "34567890", SetupID: "RSME", StorePath: "./data/hap/resume-rotation"}
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatal(err)
	}
	// Simulate a corrupted row discovered only during a rotation. The normal
	// startup validation had already completed before this test-only injection.
	now := time.Now().UTC().UnixMilli()
	if err := store.orm.WithContext(ctx).Create(&providerRow{ID: "broken-rotation", Type: "virtual", Name: "Broken", ConfigJSON: "{", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.RotateMasterKey(ctx); err == nil || !strings.Contains(err.Error(), "safely resumed") {
		t.Fatalf("rotation error = %v", err)
	}
	keyring := store.secrets.keyring()
	if keyring.active != 2 || len(keyring.keys) != 2 {
		t.Fatalf("recoverable keyring = %#v", keyring)
	}
	targets, err := store.ListTargets(ctx)
	if err != nil || findTargetPIN(targets, item.ID) != item.Pin {
		t.Fatalf("old-key target remains readable = %#v, %v", targets, err)
	}
	if err := store.orm.WithContext(ctx).Model(&providerRow{}).Where("id = ?", "broken-rotation").Update("config_json", jsonDocument("{}")).Error; err != nil {
		t.Fatal(err)
	}
	result, err := store.ResumeMasterKeyRotation(ctx)
	if err != nil || result.ActiveVersion != 2 || result.PreviousVersion != 2 || result.Status.NeedsReencryption {
		t.Fatalf("resume = %#v, %v", result, err)
	}
	var encrypted string
	if err := store.orm.WithContext(ctx).Model(&targetRow{}).Select("pin").Where("id = ?", item.ID).Scan(&encrypted).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encrypted, "enc:v2:2:") {
		t.Fatalf("resumed target cipher = %q", encrypted)
	}
}

func encryptLegacySecret(t *testing.T, key []byte, scope, plaintext string) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{3}, aead.NonceSize())
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), []byte(scope))
	return legacyEncryptedPrefix + base64.RawStdEncoding.EncodeToString(sealed)
}

func findTargetPIN(items []target.Config, id string) string {
	for _, item := range items {
		if item.ID == id {
			return item.Pin
		}
	}
	return ""
}

func findMatterPasscode(items []target.Config, id string) string {
	for _, item := range items {
		if item.ID == id && item.MatterConfig != nil {
			return item.MatterConfig.Passcode
		}
	}
	return ""
}

func providerConfigByID(items []providerconfig.Config, id string) []byte {
	for _, item := range items {
		if item.ID == id {
			return item.Config
		}
	}
	return nil
}

func slicesEqual(left, right []uint32) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
