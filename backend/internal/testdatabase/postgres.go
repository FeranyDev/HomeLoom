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

const defaultURL = "postgres://homeloom:homeloom-dev@127.0.0.1:54329/homeloom?sslmode=disable"

// Credentials creates an isolated PostgreSQL schema and a private master-key
// path for one test. Tests share the server, never tables or encryption keys.
func Credentials(t testing.TB) (databaseURL, keyPath string) {
	t.Helper()
	baseURL := os.Getenv("HOMELOOM_TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = defaultURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
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
		return "PostgreSQL"
	}
	return fmt.Sprintf("PostgreSQL %s/%s", parsed.Host, strings.Trim(parsed.Path, "/"))
}
