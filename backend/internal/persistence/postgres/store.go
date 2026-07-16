package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const currentSchemaVersion = 1

type Store struct {
	orm               *gorm.DB
	database          *sql.DB
	databaseURL       string
	keyPath           string
	secrets           *secretCodec
	operationCount    atomic.Uint64
	totalLatencyNanos atomic.Uint64
	maxLatencyNanos   atomic.Uint64
}

func Open(ctx context.Context, databaseURL, keyPath string) (*Store, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("PostgreSQL database URL is required")
	}
	if keyPath == "" {
		return nil, fmt.Errorf("database master key path is required")
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o750); err != nil {
		return nil, fmt.Errorf("create master key directory: %w", err)
	}

	orm, err := openGORM(databaseURL)
	if err != nil {
		return nil, err
	}
	database, err := orm.DB()
	if err != nil {
		return nil, fmt.Errorf("access PostgreSQL connection pool: %w", err)
	}
	configurePool(database)
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}

	store := &Store{orm: orm, database: database, databaseURL: databaseURL, keyPath: keyPath}
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

func openGORM(databaseURL string) (*gorm.DB, error) {
	orm, err := gorm.Open(gormpostgres.New(gormpostgres.Config{
		DSN:                  databaseURL,
		PreferSimpleProtocol: true,
	}), &gorm.Config{
		SkipDefaultTransaction: true,
		Logger:                 logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	return orm, nil
}

func configurePool(database *sql.DB) {
	database.SetMaxOpenConns(25)
	database.SetMaxIdleConns(10)
	database.SetConnMaxIdleTime(5 * time.Minute)
	database.SetConnMaxLifetime(30 * time.Minute)
}

// OpenForBackup opens an existing HomeLoom database without synchronizing GORM
// models. Logical snapshot reads therefore never alter the source schema.
func OpenForBackup(ctx context.Context, databaseURL, keyPath string) (*Store, error) {
	orm, err := openGORM(databaseURL)
	if err != nil {
		return nil, err
	}
	database, err := orm.DB()
	if err != nil {
		return nil, fmt.Errorf("access PostgreSQL backup connection pool: %w", err)
	}
	configurePool(database)
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("connect PostgreSQL for backup: %w", err)
	}
	return &Store{orm: orm, database: database, databaseURL: databaseURL, keyPath: keyPath}, nil
}

func (s *Store) initialize(ctx context.Context) error {
	if err := s.orm.WithContext(ctx).AutoMigrate(currentModels()...); err != nil {
		return fmt.Errorf("synchronize PostgreSQL models: %w", err)
	}
	return s.seedDefaults(ctx)
}

func (s *Store) seedDefaults(ctx context.Context) error {
	now := time.Now().UTC().UnixMilli()
	defaults := []struct {
		model any
		value any
	}{
		{&providerRow{}, &providerRow{ID: "virtual-main", Type: "virtual", Name: "Virtual Provider", Enabled: true, ConfigJSON: "{}", CreatedAt: now, UpdatedAt: now}},
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
