package gormstore

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/testdatabase"
)

func testCredentials(t testing.TB) (string, string) {
	t.Helper()
	return testdatabase.Credentials(t)
}

func openTestStore(t testing.TB, ctx context.Context) (*Store, error) {
	t.Helper()
	databaseURL, keyPath := testCredentials(t)
	return Open(ctx, databaseURL, keyPath)
}
