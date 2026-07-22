package testdatabase

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Credentials creates an isolated SQLite database by default. When
// HOMELOOM_TEST_DATABASE_URL is set to PostgreSQL, it creates an isolated
// schema there instead. Every test receives separate tables and encryption key.
func Credentials(t testing.TB) (databaseURL, keyPath string) {
	t.Helper()
	baseURL := os.Getenv("HOMELOOM_TEST_DATABASE_URL")
	if baseURL == "" {
		directory := t.TempDir()
		return "sqlite://" + filepath.ToSlash(filepath.Join(directory, "homeloom.db")), filepath.Join(directory, "homeloom.key")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatalf("parse test PostgreSQL URL: %v", err)
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("generate test schema: %v", err)
	}
	schema := "homeloom_test_" + hex.EncodeToString(random)
	admin, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open test PostgreSQL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("connect test PostgreSQL at %s: %v", parsed.Host, err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quoteIdentifier(schema)); err != nil {
		admin.Close()
		t.Fatalf("create test PostgreSQL schema: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+quoteIdentifier(schema)+" CASCADE")
		_ = admin.Close()
	})
	return parsed.String(), filepath.Join(t.TempDir(), "homeloom.key")
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func RedactedHost(databaseURL string) string {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "database"
	}
	if parsed.Scheme == "sqlite" {
		return "SQLite"
	}
	return fmt.Sprintf("PostgreSQL %s/%s", parsed.Host, strings.Trim(parsed.Path, "/"))
}
