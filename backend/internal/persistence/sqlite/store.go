package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	database          *sql.DB
	path              string
	keyPath           string
	secrets           *secretCodec
	operationCount    atomic.Uint64
	totalLatencyNanos atomic.Uint64
	maxLatencyNanos   atomic.Uint64
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

	store := &Store{database: database, path: path, keyPath: path + ".key"}
	if err := store.initialize(ctx); err != nil {
		database.Close()
		return nil, err
	}
	if err := store.initializeSecrets(ctx); err != nil {
		database.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		database.Close()
		return nil, fmt.Errorf("secure database permissions: %w", err)
	}
	return store, nil
}

// OpenForBackup opens an existing HomeLoom database without running migrations.
// This preserves the pre-upgrade schema in the resulting snapshot.
func OpenForBackup(ctx context.Context, path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("open backup source: %w", err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database for backup: %w", err)
	}
	database.SetMaxOpenConns(1)
	if _, err := database.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		database.Close()
		return nil, fmt.Errorf("initialize backup connection: %w", err)
	}
	return &Store{database: database, path: path, keyPath: path + ".key"}, nil
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

func (s *Store) HealthCheck(ctx context.Context) error {
	defer s.observe(time.Now())
	if err := s.database.PingContext(ctx); err != nil {
		return fmt.Errorf("check database health: %w", err)
	}
	return nil
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	defer s.observe(time.Now())
	var version int
	if err := s.database.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func (s *Store) Backup(ctx context.Context, destination string) error {
	defer s.observe(time.Now())
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
	destinationKey := destination + ".key"
	keyInfo, keyErr := os.Lstat(s.keyPath)
	if keyErr == nil {
		if keyInfo.Mode()&os.ModeSymlink != 0 || !keyInfo.Mode().IsRegular() {
			return fmt.Errorf("source master key must be a regular file")
		}
		if _, destinationErr := os.Stat(destinationKey); destinationErr == nil {
			return fmt.Errorf("backup key destination already exists")
		} else if !os.IsNotExist(destinationErr) {
			return fmt.Errorf("check backup key destination: %w", destinationErr)
		}
	} else if !os.IsNotExist(keyErr) {
		return fmt.Errorf("check source master key: %w", keyErr)
	} else if encrypted, inspectErr := s.hasEncryptedTargetPINs(ctx); inspectErr != nil {
		return inspectErr
	} else if encrypted {
		return fmt.Errorf("master key is missing for encrypted target secrets")
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
	if err := copyPrivateFile(s.keyPath, destinationKey); err != nil {
		return fmt.Errorf("backup master key: %w", err)
	}
	return nil
}

func (s *Store) observe(started time.Time) {
	elapsed := uint64(time.Since(started))
	s.operationCount.Add(1)
	s.totalLatencyNanos.Add(elapsed)
	for {
		current := s.maxLatencyNanos.Load()
		if elapsed <= current || s.maxLatencyNanos.CompareAndSwap(current, elapsed) {
			break
		}
	}
}

func (s *Store) DatabaseOperationMetrics() (uint64, time.Duration, time.Duration) {
	count := s.operationCount.Load()
	average := time.Duration(0)
	if count > 0 {
		average = time.Duration(s.totalLatencyNanos.Load() / count)
	}
	return count, average, time.Duration(s.maxLatencyNanos.Load())
}
