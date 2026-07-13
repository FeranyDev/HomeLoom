package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	database *sql.DB
	path     string
}

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	database.SetMaxOpenConns(1)

	store := &Store{database: database, path: path}
	if err := store.initialize(ctx); err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}

// OpenForBackup opens an existing HomeLoom database without running migrations.
// This preserves the pre-upgrade schema in the resulting snapshot.
func OpenForBackup(ctx context.Context, path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil { return nil, fmt.Errorf("open backup source: %w", err) }
	database, err := sql.Open("sqlite", path)
	if err != nil { return nil, fmt.Errorf("open database for backup: %w", err) }
	database.SetMaxOpenConns(1)
	if _, err := database.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil { database.Close(); return nil, fmt.Errorf("initialize backup connection: %w", err) }
	return &Store{database: database, path: path}, nil
}

func (s *Store) initialize(ctx context.Context) error {
	statements := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	}
	for _, statement := range statements {
		if _, err := s.database.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize database: %w", err)
		}
	}
	return s.migrate(ctx)
}

func (s *Store) Close() error {
	return s.database.Close()
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	if err := s.database.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func (s *Store) Backup(ctx context.Context, destination string) error {
	if destination == "" {
		return fmt.Errorf("backup destination is required")
	}
	sourcePath, _ := filepath.Abs(s.path)
	destinationPath, _ := filepath.Abs(destination)
	if sourcePath == destinationPath {
		return fmt.Errorf("backup destination must differ from database path")
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("backup destination already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if _, err := s.database.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return fmt.Errorf("backup database: %w", err)
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		return fmt.Errorf("secure backup permissions: %w", err)
	}
	return nil
}
