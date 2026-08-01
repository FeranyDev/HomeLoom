package gormstore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func saveTestMediaSource(t testing.TB, ctx context.Context, store *Store, deviceID string) {
	t.Helper()
	err := store.SaveMediaSource(ctx, testMediaSource(deviceID))
	if err != nil {
		t.Fatalf("SaveMediaSource() error = %v", err)
	}
}

func testMediaSource(deviceID string) MediaSource {
	return MediaSource{
		DeviceID:         deviceID,
		ProviderID:       "virtual-main",
		ProviderDeviceID: "native-" + deviceID,
		Protocol:         "rtsp",
		ProfilesJSON:     []byte(`[{"schemaVersion":1,"id":"main","width":1920,"height":1080,"fps":25,"videoCodec":"h264","audioCodec":"aac"}]`),
		SourceConfigJSON: []byte(`{"host":"camera.local","port":554}`),
		Revision:         1,
		Enabled:          true,
	}
}

func TestMediaPersistenceCRUDEncryptsSecretsAndCascadesSourceChildren(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saveTestMediaSource(t, ctx, store, "camera-1")

	sources, err := store.ListMediaSources(ctx)
	if err != nil || len(sources) != 1 || sources[0].DeviceID != "camera-1" ||
		!json.Valid(sources[0].ProfilesJSON) || !json.Valid(sources[0].SourceConfigJSON) {
		t.Fatalf("media sources = %#v, %v", sources, err)
	}
	source, found, err := store.GetMediaSource(ctx, "camera-1")
	if err != nil || !found || source.Protocol != "rtsp" || source.Revision != 1 {
		t.Fatalf("media source = %#v, %v, %v", source, found, err)
	}

	credentialSecret := []byte("media-credential-secret-canary")
	if err := store.SaveMediaCredential(ctx, MediaCredential{
		ID: "credential-1", DeviceID: "camera-1", CredentialType: "static_password",
		Blob: credentialSecret, Version: 1, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	var credentialRaw string
	if err := store.orm.WithContext(ctx).Model(&mediaCredentialRow{}).
		Select("credential_blob_encrypted").Where("id = ?", "credential-1").
		Scan(&credentialRaw).Error; err != nil {
		t.Fatal(err)
	}
	if credentialRaw == string(credentialSecret) || !strings.HasPrefix(credentialRaw, encryptedPrefix) ||
		strings.Contains(credentialRaw, string(credentialSecret)) {
		t.Fatalf("stored credential = %q", credentialRaw)
	}
	credential, found, err := store.GetMediaCredential(ctx, "credential-1")
	if err != nil || !found || !bytes.Equal(credential.Blob, credentialSecret) ||
		credential.KeyVersion != 1 || credential.Version != 1 {
		t.Fatalf("media credential = %#v, %v, %v", credential, found, err)
	}

	if err := store.SaveMediaStream(ctx, MediaStream{
		ID: "stream-1", DeviceID: "camera-1", Protocol: "rtsp",
		CredentialRef: "credential-1", Profile: "main",
		Mode: "preload", AudioEnabled: true, OptionsJSON: []byte(`{"transport":"tcp"}`),
		Revision: 2, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	stream, found, err := store.GetMediaStream(ctx, "stream-1")
	if err != nil || !found || stream.DeviceID != "camera-1" ||
		stream.CredentialRef != "credential-1" || stream.Mode != "preload" ||
		!json.Valid(stream.OptionsJSON) {
		t.Fatalf("media stream = %#v, %v, %v", stream, found, err)
	}

	runtimeSecret := []byte{0, 1, 2, 3, 255}
	if err := store.PutMediaRuntimeValue(ctx, "camera-output/camera-1", "accessory-key", runtimeSecret, true); err != nil {
		t.Fatal(err)
	}
	var runtimeRaw string
	if err := store.orm.WithContext(ctx).Model(&mediaRuntimeKVRow{}).
		Select("value_encrypted").Where("namespace = ? AND key = ?", "camera-output/camera-1", "accessory-key").
		Scan(&runtimeRaw).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(runtimeRaw, encryptedPrefix) || strings.Contains(runtimeRaw, string(runtimeSecret)) {
		t.Fatalf("stored runtime value = %q", runtimeRaw)
	}
	runtimeValue, found, err := store.GetMediaRuntimeValue(ctx, "camera-output/camera-1", "accessory-key")
	if err != nil || !found || !bytes.Equal(runtimeValue, runtimeSecret) {
		t.Fatalf("media runtime value = %v, %v, %v", runtimeValue, found, err)
	}

	lease := MediaAuthLease{
		ID: "lease-1", WorkerID: "worker-1", WorkerInstanceID: "instance-1",
		DeviceID: "camera-1", Protocol: "rtsp", Purpose: "playback", Status: "claimed",
		ExpiresAt: time.Now().UTC().Add(time.Minute), RequestID: "request-1",
		RequestMaterialHash: "sha256:test", MaxUses: 1, ClaimedAt: time.Now().UTC(),
	}
	if err := store.SaveMediaAuthLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	storedLease, found, err := store.GetMediaAuthLease(ctx, lease.ID)
	if err != nil || !found || storedLease.WorkerInstanceID != lease.WorkerInstanceID ||
		storedLease.RequestMaterialHash != lease.RequestMaterialHash {
		t.Fatalf("media lease = %#v, %v, %v", storedLease, found, err)
	}

	audit, err := store.AppendMediaAuthAudit(ctx, MediaAuthAudit{
		WorkerID: "worker-1", DeviceID: "camera-1", Provider: "virtual-main",
		Action: "acquire", Result: "succeeded",
	})
	if err != nil || audit.ID == 0 {
		t.Fatalf("media auth audit = %#v, %v", audit, err)
	}
	audits, err := store.ListMediaAuthAudits(ctx, "camera-1", 10)
	if err != nil || len(audits) != 1 || audits[0].ID != audit.ID {
		t.Fatalf("media auth audits = %#v, %v", audits, err)
	}

	if err := store.DeleteMediaSource(ctx, "camera-1"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.GetMediaCredential(ctx, "credential-1"); err != nil || found {
		t.Fatalf("credential after source deletion = %v, %v", found, err)
	}
	if _, found, err := store.GetMediaStream(ctx, "stream-1"); err != nil || found {
		t.Fatalf("stream after source deletion = %v, %v", found, err)
	}
	if _, found, err := store.GetMediaAuthLease(ctx, "lease-1"); err != nil || found {
		t.Fatalf("lease after source deletion = %v, %v", found, err)
	}
	if value, found, err := store.GetMediaRuntimeValue(ctx, "camera-output/camera-1", "accessory-key"); err != nil || !found || !bytes.Equal(value, runtimeSecret) {
		t.Fatalf("runtime identity should outlive source configuration = %v, %v, %v", value, found, err)
	}
	if audits, err := store.ListMediaAuthAudits(ctx, "camera-1", 10); err != nil || len(audits) != 1 {
		t.Fatalf("authorization audit should outlive source configuration = %#v, %v", audits, err)
	}
}

func TestMediaModelConstraintsAcrossConfiguredDialect(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saveTestMediaSource(t, ctx, store, "camera-constraints")
	now := time.Now().UTC().UnixMilli()

	tests := map[string]any{
		"credential type": &mediaCredentialRow{
			ID: "bad-credential", DeviceID: "camera-constraints", CredentialType: "unknown",
			CredentialBlobEncrypted: "ciphertext", KeyVersion: 1, Version: 1, Status: "active",
			CreatedAt: now, UpdatedAt: now,
		},
		"credential status": &mediaCredentialRow{
			ID: "bad-status", DeviceID: "camera-constraints", CredentialType: "static_password",
			CredentialBlobEncrypted: "ciphertext", KeyVersion: 1, Version: 1, Status: "unknown",
			CreatedAt: now, UpdatedAt: now,
		},
		"stream mode": &mediaStreamRow{
			ID: "bad-stream", DeviceID: "camera-constraints", Protocol: "rtsp", Mode: "eager",
			OptionsJSON: "{}", CreatedAt: now, UpdatedAt: now,
		},
		"lease status": &mediaAuthLeaseRow{
			ID: "bad-lease", WorkerID: "worker", WorkerInstanceID: "instance",
			DeviceID: "camera-constraints", Protocol: "rtsp", Purpose: "playback",
			Status: "issued", ExpiresAt: now + 1000, RequestID: "request",
			MaxUses: 1, CreatedAt: now,
		},
		"lease use count": &mediaAuthLeaseRow{
			ID: "overused-lease", WorkerID: "worker", WorkerInstanceID: "instance",
			DeviceID: "camera-constraints", Protocol: "rtsp", Purpose: "playback",
			Status: "claimed", ExpiresAt: now + 1000, RequestID: "overused-request",
			MaxUses: 1, UseCount: 2, CreatedAt: now,
		},
		"missing source foreign key": &mediaStreamRow{
			ID: "orphan-stream", DeviceID: "missing-camera", Protocol: "rtsp", Mode: "preload",
			OptionsJSON: "{}", CreatedAt: now, UpdatedAt: now,
		},
	}
	for name, row := range tests {
		t.Run(name, func(t *testing.T) {
			if err := store.orm.WithContext(ctx).Create(row).Error; err == nil {
				t.Fatalf("%s constraint failure was accepted by %s", name, store.databaseKind)
			}
		})
	}

	first := mediaAuthLeaseRow{
		ID: "lease-first", WorkerID: "worker", WorkerInstanceID: "same-instance",
		DeviceID: "camera-constraints", Protocol: "rtsp", Purpose: "playback",
		Status: "claimed", ExpiresAt: now + 1000, RequestID: "same-request",
		MaxUses: 1, CreatedAt: now,
	}
	if err := store.orm.WithContext(ctx).Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	first.ID = "lease-duplicate"
	if err := store.orm.WithContext(ctx).Create(&first).Error; err == nil {
		t.Fatalf("duplicate worker-instance request was accepted by %s", store.databaseKind)
	}
}

func TestMediaJSONColumnsUseConfiguredDialectNativeType(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if !store.orm.WithContext(ctx).Migrator().HasColumn(&mediaStreamRow{}, "credential_ref") {
		t.Fatalf("%s media_streams.credential_ref column is missing", store.databaseKind)
	}
	var types []string
	if store.databaseKind == databasePostgreSQL {
		err = store.orm.WithContext(ctx).Raw(`
			SELECT data_type
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND (
			    (table_name = 'media_sources' AND column_name IN ('profiles_json', 'source_config_json'))
			    OR (table_name = 'media_streams' AND column_name = 'options_json')
			  )
			ORDER BY table_name, column_name
		`).Scan(&types).Error
	} else {
		err = store.orm.WithContext(ctx).Raw(`
			SELECT type FROM pragma_table_info('media_sources')
			WHERE name IN ('profiles_json', 'source_config_json')
			UNION ALL
			SELECT type FROM pragma_table_info('media_streams')
			WHERE name = 'options_json'
		`).Scan(&types).Error
	}
	if err != nil {
		t.Fatal(err)
	}
	expected := "TEXT"
	if store.databaseKind == databasePostgreSQL {
		expected = "jsonb"
	}
	if len(types) != 3 {
		t.Fatalf("%s media JSON column types = %#v", store.databaseKind, types)
	}
	for _, dataType := range types {
		if dataType != expected {
			t.Fatalf("%s media JSON column type = %q, want %q", store.databaseKind, dataType, expected)
		}
	}
}

func TestMediaPersistenceRejectsSecretsInLogicalConfiguration(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := MediaSource{
		DeviceID:         "camera-secret-config",
		ProviderID:       "virtual-main",
		ProviderDeviceID: "native-camera-secret-config",
		Protocol:         "rtsp",
		ProfilesJSON:     []byte(`[{"schemaVersion":1,"id":"main","width":1920,"height":1080,"fps":25,"videoCodec":"h264","audioCodec":"aac"}]`),
		SourceConfigJSON: []byte(`{"password":"plaintext-canary"}`),
		Revision:         1,
		Enabled:          true,
	}
	if err := store.SaveMediaSource(ctx, source); err == nil || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("plaintext source credential was accepted: %v", err)
	}
	source.SourceConfigJSON = []byte(`{"host":"camera.local"}`)
	if err := store.SaveMediaSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMediaStream(ctx, MediaStream{
		ID: "stream-secret-config", DeviceID: source.DeviceID, Protocol: "rtsp",
		Profile: "main", Mode: "preload", AudioEnabled: true,
		OptionsJSON: []byte(`{"accessToken":"plaintext-canary"}`), Revision: 1, Enabled: true,
	}); err == nil || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("plaintext stream credential was accepted: %v", err)
	}
}

func TestMediaSecretsSurviveRestartAndRequireMasterKey(t *testing.T) {
	for _, test := range []struct {
		name string
		save func(context.Context, *Store) error
	}{
		{
			name: "credential",
			save: func(ctx context.Context, store *Store) error {
				if err := store.SaveMediaSource(ctx, testMediaSource("camera-key")); err != nil {
					return err
				}
				return store.SaveMediaCredential(ctx, MediaCredential{
					ID: "credential-key", DeviceID: "camera-key", CredentialType: "device_secret",
					Blob: []byte("credential-key-secret"), Version: 1, Status: "active",
				})
			},
		},
		{
			name: "runtime value",
			save: func(ctx context.Context, store *Store) error {
				return store.PutMediaRuntimeValue(ctx, "camera-output/camera-key", "identity", []byte("runtime-key-secret"), true)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			databaseURL, keyPath := testCredentials(t)
			store, err := Open(ctx, databaseURL, keyPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.save(ctx, store); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, databaseURL, keyPath)
			if err != nil {
				t.Fatalf("reopen with media master key: %v", err)
			}
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(keyPath); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(ctx, databaseURL, keyPath); err == nil || !strings.Contains(err.Error(), "master key is missing") {
				t.Fatalf("Open without media master key = %v", err)
			}
		})
	}
}

func TestMediaTablesAreIncludedInLogicalSnapshotAndRestore(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	saveTestMediaSource(t, ctx, store, "camera-backup")
	if err := store.SaveMediaCredential(ctx, MediaCredential{
		ID: "credential-backup", DeviceID: "camera-backup", CredentialType: "static_password",
		Blob: []byte("backup-media-secret-canary"), Version: 1, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMediaStream(ctx, MediaStream{
		ID: "stream-backup", DeviceID: "camera-backup", Protocol: "rtsp",
		CredentialRef: "credential-backup", Profile: "main",
		Mode: "on_demand", OptionsJSON: []byte(`{}`), Revision: 1, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutMediaRuntimeValue(ctx, "camera-output/camera-backup", "identity", []byte("backup-runtime-secret-canary"), true); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMediaAuthLease(ctx, MediaAuthLease{
		ID: "lease-backup", WorkerID: "worker", WorkerInstanceID: "instance",
		DeviceID: "camera-backup", Protocol: "rtsp", Purpose: "playback", Status: "ended",
		ExpiresAt: time.Now().UTC().Add(time.Minute), RequestID: "request-backup",
		MaxUses: 1, UseCount: 1, EndedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMediaAuthAudit(ctx, MediaAuthAudit{
		WorkerID: "worker", DeviceID: "camera-backup", Provider: "virtual-main",
		Action: "acquire", Result: "succeeded",
	}); err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(t.TempDir(), "media-backup.json")
	if err := store.Backup(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("backup-media-secret-canary")) ||
		bytes.Contains(payload, []byte("backup-runtime-secret-canary")) {
		t.Fatalf("logical snapshot contains plaintext media secret: %s", payload)
	}
	snapshot, err := readSnapshot(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.MediaSources) != 1 || len(snapshot.MediaCredentials) != 1 ||
		len(snapshot.MediaStreams) != 1 || len(snapshot.MediaRuntimeValues) != 1 ||
		len(snapshot.MediaAuthLeases) != 1 || len(snapshot.MediaAuthAudits) != 1 ||
		len(snapshot.MediaConfigState) != 1 {
		t.Fatalf("media snapshot rows = sources:%d credentials:%d streams:%d runtime:%d leases:%d audits:%d state:%d",
			len(snapshot.MediaSources), len(snapshot.MediaCredentials), len(snapshot.MediaStreams),
			len(snapshot.MediaRuntimeValues), len(snapshot.MediaAuthLeases), len(snapshot.MediaAuthAudits),
			len(snapshot.MediaConfigState))
	}
	snapshotVersion := mediaConfigVersionFromRow(snapshot.MediaConfigState[0])
	if snapshot.MediaStreams[0].CredentialRef != "credential-backup" {
		t.Fatalf("snapshot stream credential ref = %q", snapshot.MediaStreams[0].CredentialRef)
	}
	if err := ValidateRestoreCandidate(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMediaSource(ctx, "camera-backup"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMediaRuntimeValue(ctx, "camera-output/camera-backup", "identity"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, backupPath, databaseURL, keyPath, true); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if source, found, err := restored.GetMediaSource(ctx, "camera-backup"); err != nil || !found || source.Revision != 1 {
		t.Fatalf("restored source = %#v, %v, %v", source, found, err)
	}
	if credential, found, err := restored.GetMediaCredential(ctx, "credential-backup"); err != nil || !found ||
		string(credential.Blob) != "backup-media-secret-canary" {
		t.Fatalf("restored credential = %#v, %v, %v", credential, found, err)
	}
	if stream, found, err := restored.GetMediaStream(ctx, "stream-backup"); err != nil || !found ||
		stream.CredentialRef != "credential-backup" {
		t.Fatalf("restored stream = %#v, %v, %v", stream, found, err)
	}
	if value, found, err := restored.GetMediaRuntimeValue(ctx, "camera-output/camera-backup", "identity"); err != nil ||
		!found || string(value) != "backup-runtime-secret-canary" {
		t.Fatalf("restored runtime value = %q, %v, %v", value, found, err)
	}
	if leases, err := restored.ListMediaAuthLeases(ctx, "camera-backup"); err != nil || len(leases) != 1 {
		t.Fatalf("restored leases = %#v, %v", leases, err)
	}
	if audits, err := restored.ListMediaAuthAudits(ctx, "camera-backup", 10); err != nil || len(audits) != 1 {
		t.Fatalf("restored audits = %#v, %v", audits, err)
	}
	if version, err := restored.GetMediaConfigVersion(ctx); err != nil ||
		version.Generation != snapshotVersion.Generation || version.Revision != snapshotVersion.Revision {
		t.Fatalf("restored media config version = %#v, want %#v, %v", version, snapshotVersion, err)
	}
}
