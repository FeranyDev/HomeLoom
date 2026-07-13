package sqlite

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.database.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY,
        applied_at INTEGER NOT NULL
    )`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)
	supported := 0
	for _, name := range files {
		version, versionErr := migrationVersion(name)
		if versionErr != nil {
			return versionErr
		}
		if version > supported {
			supported = version
		}
	}
	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if current > supported {
		return fmt.Errorf("database schema version %d is newer than supported version %d", current, supported)
	}
	for _, name := range files {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		var applied int
		err = s.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if applied > 0 {
			continue
		}
		content, err := migrationFiles.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %d: %w", version, err)
		}
		transaction, err := s.database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err := transaction.ExecContext(ctx, string(content)); err != nil {
			transaction.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := transaction.ExecContext(
			ctx,
			"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)",
			version,
			time.Now().UTC().UnixMilli(),
		); err != nil {
			transaction.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return nil
}

func migrationVersion(name string) (int, error) {
	base := filepath.Base(name)
	prefix, _, found := strings.Cut(base, "_")
	if !found {
		return 0, fmt.Errorf("invalid migration filename %q", base)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("invalid migration version in %q: %w", base, err)
	}
	return version, nil
}
