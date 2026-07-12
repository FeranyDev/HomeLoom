package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

func TestRuntimeStateTableIsRemoved(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	var count int
	if err := store.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='property_states'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("property_states should not exist")
	}
}

func TestTargetSaveBindingsAndDelete(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	item := target.Config{
		ID: "apple-second", Type: "apple-hap", Name: "Second", Enabled: false,
		Address: ":51827", Pin: "00203004", SetupID: "HLM2", StorePath: "./hap/second",
		DeviceIDs: []string{"virtual-switch-1"},
	}
	if err := store.SaveTarget(ctx, item); err != nil {
		t.Fatalf("SaveTarget() error = %v", err)
	}
	items, err := store.ListTargets(ctx)
	if err != nil {
		t.Fatalf("ListTargets() error = %v", err)
	}
	if len(items) != 2 || len(items[1].DeviceIDs) != 1 || items[1].DeviceIDs[0] != "virtual-switch-1" {
		t.Fatalf("saved targets = %#v", items)
	}
	if err := store.DeleteTarget(ctx, item.ID); err != nil {
		t.Fatalf("DeleteTarget() error = %v", err)
	}
	items, err = store.ListTargets(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("targets after delete = %#v, %v", items, err)
	}
}

func TestMigrationSeedsDefaultTarget(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	targets, err := store.ListTargets(ctx)
	if err != nil {
		t.Fatalf("ListTargets() error = %v", err)
	}
	if len(targets) != 1 || targets[0].ID != "apple-main" || targets[0].Type != "apple-hap" {
		t.Fatalf("default targets = %#v", targets)
	}
}

func TestMissingTargetDelete(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.DeleteTarget(ctx, "missing"); err == nil {
		t.Fatal("DeleteTarget() accepted missing target")
	}
}
