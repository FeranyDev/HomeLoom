package gormstore

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteURLConfiguresPureGoDatabase(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "nested directory")
	databasePath := filepath.Join(directory, "homeloom.db")
	keyPath := filepath.Join(directory, "homeloom.key")
	store, err := Open(context.Background(), "sqlite://"+filepath.ToSlash(databasePath), keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.databaseKind != databaseSQLite || store.orm.Dialector.Name() != "sqlite" {
		t.Fatalf("database kind = %q, dialector = %q", store.databaseKind, store.orm.Dialector.Name())
	}
	for _, test := range []struct {
		name string
		want int
	}{{"foreign_keys", 1}, {"busy_timeout", 5000}} {
		var value int
		if err := store.orm.Raw("PRAGMA " + test.name).Scan(&value).Error; err != nil || value != test.want {
			t.Fatalf("PRAGMA %s = %d, %v", test.name, value, err)
		}
	}
	var journalMode string
	if err := store.orm.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil || journalMode != "wal" {
		t.Fatalf("journal_mode = %q, %v", journalMode, err)
	}
	type columnInfo struct {
		Name string
		Type string
	}
	var columns []columnInfo
	if err := store.orm.Raw("PRAGMA table_info(providers)").Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		if column.Name == "config_json" {
			if column.Type != "TEXT" {
				t.Fatalf("SQLite config_json type = %q", column.Type)
			}
			return
		}
	}
	t.Fatal("SQLite providers.config_json column not found")
}

func TestSQLiteMemoryURLIsSupported(t *testing.T) {
	store, err := Open(context.Background(), "sqlite::memory:", filepath.Join(t.TempDir(), "homeloom.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.SchemaVersion(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsUnsupportedDatabaseScheme(t *testing.T) {
	if _, err := Open(context.Background(), "mysql://localhost/homeloom", filepath.Join(t.TempDir(), "homeloom.key")); err == nil {
		t.Fatal("unsupported database scheme was accepted")
	}
}
