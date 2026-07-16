package postgres

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func TestAuditEventsArePersistedNewestFirst(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for index, correlationID := range []string{"trace-old", "trace-new"} {
		event, err := store.AppendAuditEvent(ctx, domainaudit.Event{CorrelationID: correlationID, Actor: "local-api", Action: "put", ResourceType: "device", ResourceID: "switch-1", Method: "PUT", Route: "/api/v1/devices/:id/enabled", Status: 200, Outcome: domainaudit.OutcomeSucceeded, CreatedAt: time.Unix(int64(index+1), 0).UTC()})
		if err != nil || event.ID == 0 {
			t.Fatalf("append = %#v, %v", event, err)
		}
	}
	events, err := store.ListAuditEvents(ctx, 1)
	if err != nil || len(events) != 1 || events[0].CorrelationID != "trace-new" || events[0].CreatedAt.Unix() != 2 {
		t.Fatalf("events = %#v, %v", events, err)
	}
}

func TestConsistentBackupPreservesSchemaAndConfiguration(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveProvider(ctx, providerconfig.Config{ID: "backup-provider", Type: "virtual", Name: "Backup", Config: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "nested", "backup.json")
	if err := store.Backup(ctx, destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o", info.Mode().Perm())
	}
	keyInfo, err := os.Stat(destination + ".key")
	if err != nil || keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("backup key mode = %v, %v", keyInfo, err)
	}
	if err := ValidateRestoreCandidate(ctx, destination); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readSnapshot(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshotHasProvider(snapshot, "backup-provider") || snapshot.SchemaVersion != currentSchemaVersion {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if err := store.Backup(ctx, destination); err == nil {
		t.Fatal("existing backup was overwritten")
	}
}

func snapshotHasProvider(snapshot databaseSnapshot, id string) bool {
	for _, provider := range snapshot.Providers {
		if provider.ID == id {
			return true
		}
	}
	return false
}

func TestTargetPINIsEncryptedAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	item := target.Config{ID: "encrypted", Type: "apple-hap", Name: "Encrypted", Address: ":51827", Pin: "12345678", SetupID: "ENC1", StorePath: "data/hap/encrypted"}
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := store.orm.WithContext(ctx).Model(&targetRow{}).Select("pin").Where("id = ?", item.ID).Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored == item.Pin || !strings.HasPrefix(stored, encryptedPrefix) {
		t.Fatalf("stored pin = %q", stored)
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil || keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("master key mode = %v, %v", keyInfo, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	items, err := reopened.ListTargets(ctx)
	reopened.Close()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, current := range items {
		if current.ID == item.ID {
			found = current.Pin == item.Pin
		}
	}
	if !found {
		t.Fatalf("decrypted target not found: %#v", items)
	}
}

func TestProviderSecretsAreEncryptedAndSurviveRestart(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	item := providerconfig.Config{ID: "mqtt-encrypted", Type: "mqtt", Name: "Encrypted MQTT", Enabled: true, Config: []byte(`{"brokerUrl":"mqtt://localhost:1883","username":"reader","password":"broker-password","ssecurity":"miot-security","tls":{"certFile":"client.pem","privateKey":"key-material"}}`)}
	if err := store.SaveProvider(ctx, item); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := store.orm.WithContext(ctx).Model(&providerRow{}).Select("config_json").Where("id = ?", item.ID).Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "broker-password") || strings.Contains(stored, "key-material") || strings.Contains(stored, "miot-security") || !strings.Contains(stored, encryptedPrefix) || !strings.Contains(stored, "client.pem") {
		t.Fatalf("stored provider config = %s", stored)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	items, err := reopened.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range items {
		if current.ID == item.ID {
			config := string(current.Config)
			if !strings.Contains(config, `"password":"broker-password"`) || !strings.Contains(config, `"privateKey":"key-material"`) || !strings.Contains(config, `"ssecurity":"miot-security"`) || !strings.Contains(config, `"certFile":"client.pem"`) {
				t.Fatalf("decrypted provider config = %s", config)
			}
			return
		}
	}
	t.Fatalf("decrypted provider not found: %#v", items)
}

func TestOpenEncryptsLegacyPlaintextProviderSecrets(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.orm.WithContext(ctx).Model(&providerRow{}).Where("id = ?", "virtual-main").Update("config_json", `{"password":"legacy-password","public":"visible"}`).Error; err != nil {
		t.Fatal(err)
	}
	store.Close()
	reopened, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var raw string
	if err := reopened.orm.WithContext(ctx).Model(&providerRow{}).Select("config_json").Where("id = ?", "virtual-main").Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "legacy-password") || !strings.Contains(raw, encryptedPrefix) || (!strings.Contains(raw, `"public":"visible"`) && !strings.Contains(raw, `"public": "visible"`)) {
		t.Fatalf("migrated provider config = %s", raw)
	}
	items, err := reopened.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == "virtual-main" && strings.Contains(string(item.Config), `"password":"legacy-password"`) {
			return
		}
	}
	t.Fatalf("decrypted migrated provider missing: %#v", items)
}

func TestOpenRejectsMissingMasterKeyForEncryptedProviderSecret(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProvider(ctx, providerconfig.Config{ID: "mqtt-secret", Type: "mqtt", Config: []byte(`{"password":"secret"}`)}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, databaseURL, keyPath); err == nil || !strings.Contains(err.Error(), "master key is missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenRejectsMissingMasterKeyForEncryptedPIN(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTarget(ctx, target.Config{ID: "encrypted", Type: "apple-hap", Name: "Encrypted", Pin: "12345678"}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, databaseURL, keyPath); err == nil || !strings.Contains(err.Error(), "master key is missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestBackupCarriesMasterKeyNeededToRestoreTargetPIN(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	item := target.Config{ID: "restore", Type: "apple-hap", Name: "Restore", Pin: "87654321"}
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "restore.json")
	if err := store.Backup(ctx, destination); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, err := Restore(ctx, destination, databaseURL, keyPath, true); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	items, err := restored.ListTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range items {
		if current.ID == item.ID && current.Pin == item.Pin {
			return
		}
	}
	t.Fatalf("restored target pin missing: %#v", items)
}

func TestOpenEncryptsLegacyPlaintextPINWithExistingKey(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.orm.WithContext(ctx).Model(&targetRow{}).Where("id = ?", "apple-main").Update("pin", "11223344").Error; err != nil {
		t.Fatal(err)
	}
	store.Close()
	reopened, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var raw string
	if err := reopened.orm.WithContext(ctx).Model(&targetRow{}).Select("pin").Where("id = ?", "apple-main").Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, encryptedPrefix) {
		t.Fatalf("legacy pin was not encrypted: %q", raw)
	}
	items, err := reopened.ListTargets(ctx)
	if err != nil || len(items) == 0 || items[0].Pin != "11223344" {
		t.Fatalf("targets = %#v, %v", items, err)
	}
}

func TestOpenForBackupDoesNotSynchronizeModels(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := OpenForBackup(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.orm.Migrator().HasTable(&providerRow{}) {
		t.Fatal("backup inspection unexpectedly synchronized GORM models")
	}
	if _, err := store.SchemaVersion(ctx); err == nil {
		t.Fatal("incomplete inspection database reported a current schema")
	}
}

func TestRuntimeStateTableIsRemoved(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	if store.orm.Migrator().HasTable("property_states") {
		t.Fatal("property_states should not exist")
	}
}

func TestGORMInitializesCurrentModelsWithoutMigrationTable(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, model := range currentModels() {
		if !store.orm.Migrator().HasTable(model) {
			t.Fatalf("missing GORM model table for %T", model)
		}
	}
	if store.orm.Migrator().HasTable("schema_migrations") {
		t.Fatal("legacy schema_migrations table should not be created")
	}
	version, err := store.SchemaVersion(ctx)
	if err != nil || version != currentSchemaVersion {
		t.Fatalf("schema version = %d, %v", version, err)
	}
}

func TestDatabaseOperationMetrics(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	before, _, _ := store.DatabaseOperationMetrics()
	if _, err := store.ListProviders(ctx); err != nil {
		t.Fatal(err)
	}
	after, average, maximum := store.DatabaseOperationMetrics()
	if after <= before || average <= 0 || maximum < average {
		t.Fatalf("metrics = %d -> %d, %v, %v", before, after, average, maximum)
	}
}

func TestDeviceDisabledPreferencePersistsAndCanBeCleared(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetDeviceDisabled(ctx, "switch-1", true); err != nil {
		t.Fatal(err)
	}
	ids, err := store.ListDisabledDeviceIDs(ctx)
	if err != nil || len(ids) != 1 || ids[0] != "switch-1" {
		t.Fatalf("disabled ids = %#v, %v", ids, err)
	}
	if err := store.SetDeviceDisabled(ctx, "switch-1", false); err != nil {
		t.Fatal(err)
	}
	ids, err = store.ListDisabledDeviceIDs(ctx)
	if err != nil || len(ids) != 0 {
		t.Fatalf("cleared ids = %#v, %v", ids, err)
	}
}

func TestSystemSettingsCRUD(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(store.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("master key mode = %v", info.Mode().Perm())
	}
	if value, exists, err := store.GetSetting(ctx, "command_timeout_seconds"); err != nil || exists || value != "" {
		t.Fatalf("missing setting = %q, %v, %v", value, exists, err)
	}
	if err := store.SaveSetting(ctx, "command_timeout_seconds", "15"); err != nil {
		t.Fatal(err)
	}
	if value, exists, err := store.GetSetting(ctx, "command_timeout_seconds"); err != nil || !exists || value != "15" {
		t.Fatalf("saved setting = %q, %v, %v", value, exists, err)
	}
	if err := store.SaveSetting(ctx, "command_timeout_seconds", "20"); err != nil {
		t.Fatal(err)
	}
	if value, _, _ := store.GetSetting(ctx, "command_timeout_seconds"); value != "20" {
		t.Fatalf("updated setting = %q", value)
	}
}

func TestProviderSeedCRUD(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	items, err := store.ListProviders(ctx)
	if err != nil || len(items) != 1 || items[0].ID != "virtual-main" || !items[0].Enabled {
		t.Fatalf("seed providers = %#v, %v", items, err)
	}
	item := providerconfig.Config{ID: "virtual-second", Type: "virtual", Name: "Second", Config: []byte(`{"room":"test"}`)}
	if err := store.SaveProvider(ctx, item); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListProviders(ctx)
	if err != nil || len(items) != 2 || (!strings.Contains(string(items[1].Config), `"room":"test"`) && !strings.Contains(string(items[1].Config), `"room": "test"`)) {
		t.Fatalf("providers = %#v, %v", items, err)
	}
	if err := store.DeleteProvider(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProvider(ctx, item.ID); err == nil {
		t.Fatal("missing provider delete was accepted")
	}
}

func TestTargetSaveBindingsAndDelete(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	item := target.Config{
		ID: "apple-second", Type: "apple-hap", Name: "Second", Enabled: false,
		Address: ":51827", Pin: "00203004", SetupID: "HLM2", StorePath: "./hap/second",
		Devices: []target.VirtualDevice{{ID: "living-switch", Name: "客厅开关", Type: device.TypeSwitch, SourceDeviceID: "virtual-switch-1", Enabled: true}},
	}
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatalf("SaveTarget() error = %v", err)
	}
	items, err := store.ListTargets(ctx)
	if err != nil {
		t.Fatalf("ListTargets() error = %v", err)
	}
	if len(items) != 2 || len(items[1].Devices) != 1 || items[1].Devices[0].ID != "living-switch" || items[1].Devices[0].SourceDeviceID != "virtual-switch-1" || len(items[1].DeviceIDs) != 1 || items[1].DeviceIDs[0] != "virtual-switch-1" {
		t.Fatalf("saved targets = %#v", items)
	}
	binding := mapping.Binding{ID: "living-switch-on", Stage: mapping.StageConsumer, ProviderID: "virtual-main", DeviceID: "virtual-switch-1", DeviceType: device.TypeSwitch, ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power", TargetID: item.ID, ConsumerDeviceID: "living-switch", ConsumerID: "homekit", ConsumerProperty: "Switch.On", Enabled: true}
	if err := store.SaveMappingBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	item.Devices[0].SourceDeviceID = "virtual-switch-2"
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatal(err)
	}
	bindings, err := store.ListMappingBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range bindings {
		if current.ID == binding.ID {
			t.Fatal("mapping for the previous virtual-device source was retained")
		}
	}
	binding.ID, binding.DeviceID = "living-switch-on-new", "virtual-switch-2"
	if err := store.SaveMappingBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTarget(ctx, item.ID); err != nil {
		t.Fatalf("DeleteTarget() error = %v", err)
	}
	items, err = store.ListTargets(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("targets after delete = %#v, %v", items, err)
	}
	bindings, err = store.ListMappingBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range bindings {
		if current.ID == binding.ID {
			t.Fatal("target-scoped mapping survived target deletion")
		}
	}
}

func TestGORMInitializationSeedsDefaultTarget(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	targets, err := store.ListTargets(ctx)
	if err != nil {
		t.Fatalf("ListTargets() error = %v", err)
	}
	if len(targets) != 1 || targets[0].ID != "apple-main" || targets[0].Type != "apple-hap" {
		t.Fatalf("default targets = %#v", targets)
	}
}

func TestGORMInitializationDoesNotAddDefaultsToPopulatedTables(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	orm, err := openGORM(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := orm.WithContext(ctx).AutoMigrate(currentModels()...); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixMilli()
	if err := orm.WithContext(ctx).Create(&providerRow{ID: "existing-provider", Type: "virtual", Name: "Existing", ConfigJSON: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := orm.WithContext(ctx).Create(&targetRow{ID: "existing-target", Type: "apple-hap", Name: "Existing", Enabled: true, Address: ":51826", PIN: "12345678", SetupID: "EXST", StorePath: "data/hap/existing", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	database, err := orm.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	providers, err := store.ListProviders(ctx)
	if err != nil || len(providers) != 1 || providers[0].ID != "existing-provider" {
		t.Fatalf("providers = %#v, %v", providers, err)
	}
	targets, err := store.ListTargets(ctx)
	if err != nil || len(targets) != 1 || targets[0].ID != "existing-target" {
		t.Fatalf("targets = %#v, %v", targets, err)
	}
}

func TestMissingTargetDelete(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.DeleteTarget(ctx, "missing"); err == nil {
		t.Fatal("DeleteTarget() accepted missing target")
	}
}

func TestHomeKitAccessoryAIDIsStableAndNeverReordered(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, err := store.HomeKitAccessoryAID(ctx, "apple-main", "device-z")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.HomeKitAccessoryAID(ctx, "apple-main", "device-a")
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.HomeKitAccessoryAID(ctx, "apple-main", "device-z")
	if err != nil {
		t.Fatal(err)
	}
	third, err := store.HomeKitAccessoryAID(ctx, "apple-main", "device-0")
	if err != nil {
		t.Fatal(err)
	}
	if first != 2 || second != 3 || again != first || third != 4 {
		t.Fatalf("AIDs = %d %d %d %d", first, second, again, third)
	}
}

func TestHomeKitAccessoryAIDRequiresExistingTarget(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.HomeKitAccessoryAID(ctx, "missing", "device"); err == nil {
		t.Fatal("foreign-key violation was accepted")
	}
}

func TestHomeKitIIDIsStableWhenNewResourcesAreInserted(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	serviceIID, err := store.HomeKitIID(ctx, "apple-main", "device", "service:switch")
	if err != nil {
		t.Fatal(err)
	}
	onIID, err := store.HomeKitIID(ctx, "apple-main", "device", "characteristic:on")
	if err != nil {
		t.Fatal(err)
	}
	newIID, err := store.HomeKitIID(ctx, "apple-main", "device", "characteristic:fault")
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.HomeKitIID(ctx, "apple-main", "device", "service:switch")
	if err != nil {
		t.Fatal(err)
	}
	if serviceIID != 1 || onIID != 2 || newIID != 3 || again != serviceIID {
		t.Fatalf("IIDs = %d %d %d %d", serviceIID, onIID, newIID, again)
	}
}
