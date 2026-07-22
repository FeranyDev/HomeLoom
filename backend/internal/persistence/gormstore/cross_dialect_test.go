package gormstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

func TestPostgreSQLUsesJSONBAndRestoresThroughSQLite(t *testing.T) {
	if os.Getenv("HOMELOOM_TEST_DATABASE_URL") == "" {
		t.Skip("HOMELOOM_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgresURL, postgresKey := testCredentials(t)
	postgresStore, err := Open(ctx, postgresURL, postgresKey)
	if err != nil {
		t.Fatal(err)
	}
	if postgresStore.databaseKind != databasePostgreSQL || postgresStore.orm.Dialector.Name() != "postgres" {
		t.Fatalf("database kind = %q, dialector = %q", postgresStore.databaseKind, postgresStore.orm.Dialector.Name())
	}
	var dataType string
	if err := postgresStore.orm.Raw(`SELECT data_type FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'providers' AND column_name = 'config_json'`).Scan(&dataType).Error; err != nil || dataType != "jsonb" {
		t.Fatalf("PostgreSQL providers.config_json type = %q, %v", dataType, err)
	}
	if err := postgresStore.SaveProvider(ctx, providerconfig.Config{ID: "cross-provider", Type: "mqtt", Name: "Cross Dialect", Config: []byte(`{"password":"cross-secret"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := postgresStore.SaveTarget(ctx, target.Config{ID: "cross-target", Type: "apple-hap", Name: "Cross Target", Pin: "12345678"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "postgres-backup.json")
	if err := postgresStore.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := postgresStore.Close(); err != nil {
		t.Fatal(err)
	}

	sqliteDirectory := t.TempDir()
	sqliteURL := "sqlite://" + filepath.ToSlash(filepath.Join(sqliteDirectory, "homeloom.db"))
	sqliteKey := filepath.Join(sqliteDirectory, "homeloom.key")
	if _, err := Restore(ctx, backup, sqliteURL, sqliteKey, true); err != nil {
		t.Fatal(err)
	}
	sqliteStore, err := Open(ctx, sqliteURL, sqliteKey)
	if err != nil {
		t.Fatal(err)
	}
	defer sqliteStore.Close()
	providers, err := sqliteStore.ListProviders(ctx)
	if err != nil || !hasProvider(providers, "cross-provider") {
		t.Fatalf("SQLite providers = %#v, %v", providers, err)
	}
	targets, err := sqliteStore.ListTargets(ctx)
	if err != nil || targetPIN(targets, "cross-target") != "12345678" {
		t.Fatalf("SQLite targets = %#v, %v", targets, err)
	}
	sqliteBackup := filepath.Join(t.TempDir(), "sqlite-backup.json")
	if err := sqliteStore.Backup(ctx, sqliteBackup); err != nil {
		t.Fatal(err)
	}
	secondPostgresURL, secondPostgresKey := testCredentials(t)
	if _, err := Restore(ctx, sqliteBackup, secondPostgresURL, secondPostgresKey, true); err != nil {
		t.Fatal(err)
	}
	secondPostgresStore, err := Open(ctx, secondPostgresURL, secondPostgresKey)
	if err != nil {
		t.Fatal(err)
	}
	defer secondPostgresStore.Close()
	providers, err = secondPostgresStore.ListProviders(ctx)
	if err != nil || !hasProvider(providers, "cross-provider") {
		t.Fatalf("restored PostgreSQL providers = %#v, %v", providers, err)
	}
	targets, err = secondPostgresStore.ListTargets(ctx)
	if err != nil || targetPIN(targets, "cross-target") != "12345678" {
		t.Fatalf("restored PostgreSQL targets = %#v, %v", targets, err)
	}
}
