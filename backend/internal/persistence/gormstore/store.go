package gormstore

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ncruces/go-sqlite3/gormlite"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const currentSchemaVersion = 1

type databaseKind string

const (
	databasePostgreSQL databaseKind = "postgres"
	databaseSQLite     databaseKind = "sqlite"
)

type Store struct {
	orm               *gorm.DB
	database          *sql.DB
	databaseURL       string
	keyPath           string
	databaseKind      databaseKind
	secrets           *secretCodec
	operationCount    atomic.Uint64
	totalLatencyNanos atomic.Uint64
	maxLatencyNanos   atomic.Uint64
}

func Open(ctx context.Context, databaseURL, keyPath string) (*Store, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is required")
	}
	if keyPath == "" {
		return nil, fmt.Errorf("database master key path is required")
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o750); err != nil {
		return nil, fmt.Errorf("create master key directory: %w", err)
	}

	orm, kind, err := openGORM(databaseURL)
	if err != nil {
		return nil, err
	}
	database, err := orm.DB()
	if err != nil {
		return nil, fmt.Errorf("access %s connection pool: %w", databaseLabel(kind), err)
	}
	configurePool(database, kind)
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("connect %s: %w", databaseLabel(kind), err)
	}

	store := &Store{orm: orm, database: database, databaseURL: databaseURL, keyPath: keyPath, databaseKind: kind}
	if err := store.initialize(ctx); err != nil {
		database.Close()
		return nil, err
	}
	if err := store.initializeSecrets(ctx); err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}

func openGORM(databaseURL string) (*gorm.DB, databaseKind, error) {
	var (
		dialector gorm.Dialector
		kind      databaseKind
	)
	switch {
	case strings.HasPrefix(databaseURL, "postgres://"), strings.HasPrefix(databaseURL, "postgresql://"):
		dialector = gormpostgres.New(gormpostgres.Config{DSN: databaseURL, PreferSimpleProtocol: true})
		kind = databasePostgreSQL
	case strings.HasPrefix(databaseURL, "sqlite:"):
		dsn, path, err := sqliteDSN(databaseURL)
		if err != nil {
			return nil, "", err
		}
		if path != "" {
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return nil, "", fmt.Errorf("create SQLite database directory: %w", err)
			}
		}
		dialector = gormlite.Open(dsn)
		kind = databaseSQLite
	default:
		return nil, "", fmt.Errorf("unsupported database URL scheme")
	}
	orm, err := gorm.Open(dialector, &gorm.Config{
		SkipDefaultTransaction: true,
		Logger:                 logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", databaseLabel(kind), err)
	}
	return orm, kind, nil
}

func sqliteDSN(databaseURL string) (dsn, path string, err error) {
	raw := strings.TrimPrefix(databaseURL, "sqlite:")
	if strings.HasPrefix(raw, "//") {
		raw = strings.TrimPrefix(raw, "//")
	}
	if raw == "" {
		return "", "", fmt.Errorf("SQLite database path is required")
	}
	if raw == ":memory:" {
		raw = "file:homeloom-memory?mode=memory&cache=shared"
	} else if !strings.HasPrefix(raw, "file:") {
		path = strings.SplitN(raw, "?", 2)[0]
		raw = "file:" + raw
	} else if parsed, parseErr := url.Parse(raw); parseErr == nil && parsed.Query().Get("mode") != "memory" {
		path = parsed.Path
	}
	parsed, parseErr := url.Parse(raw)
	if parseErr != nil {
		return "", "", fmt.Errorf("parse SQLite database URL: %w", parseErr)
	}
	query := parsed.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	if query.Get("mode") != "memory" {
		query.Add("_pragma", "journal_mode(WAL)")
		query.Add("_pragma", "synchronous(NORMAL)")
	}
	query.Set("_txlock", "immediate")
	parsed.RawQuery = query.Encode()
	return parsed.String(), path, nil
}

func configurePool(database *sql.DB, kind databaseKind) {
	if kind == databaseSQLite {
		database.SetMaxOpenConns(4)
		database.SetMaxIdleConns(4)
		return
	}
	database.SetMaxOpenConns(25)
	database.SetMaxIdleConns(10)
	database.SetConnMaxIdleTime(5 * time.Minute)
	database.SetConnMaxLifetime(30 * time.Minute)
}

// OpenForBackup opens an existing HomeLoom database without synchronizing GORM
// models. Logical snapshot reads therefore never alter the source schema.
func OpenForBackup(ctx context.Context, databaseURL, keyPath string) (*Store, error) {
	orm, kind, err := openGORM(databaseURL)
	if err != nil {
		return nil, err
	}
	database, err := orm.DB()
	if err != nil {
		return nil, fmt.Errorf("access %s backup connection pool: %w", databaseLabel(kind), err)
	}
	configurePool(database, kind)
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("connect %s for backup: %w", databaseLabel(kind), err)
	}
	return &Store{orm: orm, database: database, databaseURL: databaseURL, keyPath: keyPath, databaseKind: kind}, nil
}

func databaseLabel(kind databaseKind) string {
	if kind == databaseSQLite {
		return "SQLite"
	}
	return "PostgreSQL"
}

func (s *Store) initialize(ctx context.Context) error {
	// Provider routes are one-to-many from a source property. Remove the old
	// source-side unique index through GORM before synchronizing the current
	// non-unique lookup index; the model-side unique index remains authoritative
	// for deterministic reverse writes.
	migrator := s.orm.WithContext(ctx).Migrator()
	if migrator.HasIndex(&mappingBindingRow{}, "mapping_provider_source_unique") {
		if err := migrator.DropIndex(&mappingBindingRow{}, "mapping_provider_source_unique"); err != nil {
			return fmt.Errorf("update Provider mapping source index: %w", err)
		}
	}
	if err := s.orm.WithContext(ctx).AutoMigrate(currentModels()...); err != nil {
		return fmt.Errorf("synchronize %s models: %w", databaseLabel(s.databaseKind), err)
	}
	return s.seedDefaults(ctx)
}

func (s *Store) seedDefaults(ctx context.Context) error {
	now := time.Now().UTC().UnixMilli()
	defaults := []struct {
		model any
		value any
	}{
		{&providerRow{}, &providerRow{ID: "virtual-main", Type: "virtual", Name: "Virtual Provider", Enabled: true, ConfigJSON: jsonDocument("{}"), CreatedAt: now, UpdatedAt: now}},
		{&targetRow{}, &targetRow{ID: "apple-main", Type: "apple-hap", Name: "HomeLoom 主桥", Enabled: true, Address: ":51826", PIN: "00102003", SetupID: "HLM1", StorePath: "./data/hap/apple-main", CreatedAt: now, UpdatedAt: now}},
	}
	for _, item := range defaults {
		var count int64
		if err := s.orm.WithContext(ctx).Model(item.model).Count(&count).Error; err != nil {
			return fmt.Errorf("inspect default database records: %w", err)
		}
		if count != 0 {
			continue
		}
		if err := s.orm.WithContext(ctx).Create(item.value).Error; err != nil {
			return fmt.Errorf("initialize default database records: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.database.Close()
}

func (s *Store) HealthCheck(ctx context.Context) error {
	defer s.observe(time.Now())
	var value int
	if err := s.orm.WithContext(ctx).Raw("SELECT 1").Scan(&value).Error; err != nil {
		return fmt.Errorf("check database health: %w", err)
	}
	if value != 1 {
		return fmt.Errorf("check database health: unexpected response %d", value)
	}
	return nil
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	defer s.observe(time.Now())
	for _, model := range currentModels() {
		if !s.orm.WithContext(ctx).Migrator().HasTable(model) {
			return 0, fmt.Errorf("current database model is incomplete")
		}
	}
	return currentSchemaVersion, nil
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
