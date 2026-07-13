package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

func TestConsistentBackupPreservesSchemaAndConfiguration(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	source := filepath.Join(directory, "source.db")
	store, err := Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveProvider(ctx, providerconfig.Config{ID: "backup-provider", Type: "virtual", Name: "Backup", Config: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "nested", "backup.db")
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
	backup, err := sql.Open("sqlite", destination)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var integrity string
	if err := backup.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity = %q, %v", integrity, err)
	}
	var providers, version int
	if err := backup.QueryRowContext(ctx, "SELECT COUNT(*) FROM providers WHERE id = 'backup-provider'").Scan(&providers); err != nil {
		t.Fatal(err)
	}
	if err := backup.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if providers != 1 || version == 0 {
		t.Fatalf("providers = %d, version = %d", providers, version)
	}
	if err := store.Backup(ctx, destination); err == nil {
		t.Fatal("existing backup was overwritten")
	}
	if err := store.Backup(ctx, source); err == nil {
		t.Fatal("database was backed up over itself")
	}
}

func TestTargetPINIsEncryptedAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "homeloom.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	item := target.Config{ID: "encrypted", Type: "apple-hap", Name: "Encrypted", Address: ":51827", Pin: "12345678", SetupID: "ENC1", StorePath: "data/hap/encrypted"}
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := store.database.QueryRowContext(ctx, "SELECT pin FROM targets WHERE id = ?", item.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == item.Pin || !strings.HasPrefix(stored, encryptedPrefix) {
		t.Fatalf("stored pin = %q", stored)
	}
	keyInfo, err := os.Stat(path + ".key")
	if err != nil || keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("master key mode = %v, %v", keyInfo, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
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

func TestOpenRejectsMissingMasterKeyForEncryptedPIN(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "homeloom.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTarget(ctx, target.Config{ID: "encrypted", Type: "apple-hap", Name: "Encrypted", Pin: "12345678"}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err := os.Remove(path + ".key"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "master key is missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestBackupCarriesMasterKeyNeededToRestoreTargetPIN(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.db")
	store, err := Open(ctx, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	item := target.Config{ID: "restore", Type: "apple-hap", Name: "Restore", Pin: "87654321"}
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "restore.db")
	if err := store.Backup(ctx, destination); err != nil {
		t.Fatal(err)
	}
	store.Close()
	restored, err := Open(ctx, destination)
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
	path := filepath.Join(t.TempDir(), "homeloom.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.database.ExecContext(ctx, "UPDATE targets SET pin = '11223344' WHERE id = 'apple-main'"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var raw string
	if err := reopened.database.QueryRowContext(ctx, "SELECT pin FROM targets WHERE id = 'apple-main'").Scan(&raw); err != nil {
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

func TestOpenRejectsDatabaseFromNewerVersion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "future.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL); INSERT INTO schema_migrations VALUES(999, 0)"); err != nil {
		t.Fatal(err)
	}
	database.Close()
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenForBackupDoesNotRunMigrations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL); INSERT INTO schema_migrations VALUES(1, 0)"); err != nil {
		t.Fatal(err)
	}
	database.Close()
	store, err := OpenForBackup(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.SchemaVersion(ctx)
	if err != nil || version != 1 {
		t.Fatalf("version = %d, %v", version, err)
	}
}

func TestRuntimeStateTableIsRemoved(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	var count int
	if err := store.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='property_states'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("property_states should not exist")
	}
}

func TestDatabaseOperationMetrics(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "metrics.db"))
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
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
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
	store, err := Open(ctx, filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %v", info.Mode().Perm())
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
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
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
	if err != nil || len(items) != 2 || string(items[1].Config) != `{"room":"test"}` {
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
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	item := target.Config{
		ID: "apple-second", Type: "apple-hap", Name: "Second", Enabled: false,
		Address: ":51827", Pin: "00203004", SetupID: "HLM2", StorePath: "./hap/second",
		DeviceIDs: []string{"virtual-switch-1"},
	}
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatalf("SaveTarget() error = %v", err)
	}
	items, err := store.ListTargets(ctx)
	if err != nil {
		t.Fatalf("ListTargets() error = %v", err)
	}
	if len(items) != 2 || len(items[1].DeviceIDs) != 1 || items[1].DeviceIDs[0] != "virtual-switch-1" {
		t.Fatalf("saved targets = %#v", items)
	}
	if err := store.DeleteTarget(ctx, item.ID); err != nil {
		t.Fatalf("DeleteTarget() error = %v", err)
	}
	items, err = store.ListTargets(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("targets after delete = %#v, %v", items, err)
	}
}

func TestMigrationSeedsDefaultTarget(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
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

func TestMissingTargetDelete(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
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
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
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
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
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
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
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
