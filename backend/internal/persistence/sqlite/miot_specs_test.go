package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMIoTSpecCacheLoadsByTypeAndModel(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "spec-cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fetchedAt := time.Now().UTC().Truncate(time.Millisecond)
	document := []byte(`{"type":"urn:test","services":[]}`)
	if err := store.SaveMIoTSpec(ctx, "urn:test", "vendor.model", document, fetchedAt); err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []struct{ specType, model string }{{specType: "urn:test"}, {model: "vendor.model"}} {
		loaded, resolvedType, loadedAt, found, err := store.LoadMIoTSpec(ctx, lookup.specType, lookup.model)
		if err != nil || !found || resolvedType != "urn:test" || string(loaded) != string(document) || !loadedAt.Equal(fetchedAt) {
			t.Fatalf("LoadMIoTSpec(%q,%q)=(%q,%q,%v,%v,%v)", lookup.specType, lookup.model, loaded, resolvedType, loadedAt, found, err)
		}
	}
}
