package httpapi

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/persistence/postgres"
	"github.com/feranydev/homeloom/backend/internal/testdatabase"
)

func openTestStore(t testing.TB, ctx context.Context) (*postgres.Store, error) {
	t.Helper()
	databaseURL, keyPath := testStoreCredentials(t)
	return postgres.Open(ctx, databaseURL, keyPath)
}

func testStoreCredentials(t testing.TB) (string, string) {
	t.Helper()
	return testdatabase.Credentials(t)
}
