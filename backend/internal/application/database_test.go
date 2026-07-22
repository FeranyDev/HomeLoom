package application_test

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/persistence/gormstore"
	"github.com/feranydev/homeloom/backend/internal/testdatabase"
)

func openTestStore(t testing.TB, ctx context.Context) (*gormstore.Store, error) {
	t.Helper()
	databaseURL, keyPath := testStoreCredentials(t)
	return gormstore.Open(ctx, databaseURL, keyPath)
}

func testStoreCredentials(t testing.TB) (string, string) {
	t.Helper()
	return testdatabase.Credentials(t)
}
