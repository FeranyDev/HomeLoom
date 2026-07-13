package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func TestRestoreValidatesSecretsAndPreservesPreviousDatabase(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	source := filepath.Join(directory, "source.db")
	store, err := Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTarget(ctx, target.Config{ID: "apple-restored", Type: "apple-hap", Name: "Restored", Pin: "87654321"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMappingProfile(ctx, mapping.Profile{SchemaVersion: 1, ID: "restored-profile", Version: 1, Kind: mapping.KindProvider, InputType: device.ValueTypeBool, OutputType: device.ValueTypeBool, Transforms: []mapping.Transform{{Type: mapping.TransformInvert}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMappingBinding(ctx, mapping.Binding{ID: "restored-binding", ProfileID: "restored-profile", ProviderID: "virtual-main", DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(directory, "backup.db")
	if err := store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(directory, "active.db")
	active, err := Open(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := active.SaveProvider(ctx, providerconfig.Config{ID: "old-provider", Type: "virtual", Name: "Old", Config: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, backup, destination, false); err == nil || !strings.Contains(err.Error(), "explicit replacement") {
		t.Fatalf("error = %v", err)
	}

	recovery, err := Restore(ctx, backup, destination, true)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == "" {
		t.Fatal("missing pre-restore snapshot")
	}
	restored, err := Open(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := restored.ListTargets(ctx)
	if err != nil || targetPIN(targets, "apple-restored") != "87654321" {
		t.Fatalf("targets = %#v, error = %v", targets, err)
	}
	profiles, err := restored.ListMappingProfiles(ctx)
	if err != nil || !hasProfile(profiles, "restored-profile") {
		t.Fatalf("profiles = %#v, error = %v", profiles, err)
	}
	bindings, err := restored.ListMappingBindings(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].ID != "restored-binding" {
		t.Fatalf("bindings = %#v, error = %v", bindings, err)
	}
	_ = restored.Close()

	previous, err := Open(ctx, recovery)
	if err != nil {
		t.Fatal(err)
	}
	providers, err := previous.ListProviders(ctx)
	if err != nil || !hasProvider(providers, "old-provider") {
		t.Fatalf("providers = %#v, error = %v", providers, err)
	}
	_ = previous.Close()
}

func targetPIN(items []target.Config, id string) string {
	for _, item := range items {
		if item.ID == id {
			return item.Pin
		}
	}
	return ""
}

func hasProvider(items []providerconfig.Config, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func hasProfile(items []mapping.Profile, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func TestRestoreRejectsMissingKeyAndActiveSidecars(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	source := filepath.Join(directory, "source.db")
	store, err := Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTarget(ctx, target.Config{ID: "encrypted", Type: "apple-hap", Name: "Encrypted", Pin: "12345678"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(directory, "backup.db")
	if err := store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	if err := os.Remove(backup + ".key"); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, backup, filepath.Join(directory, "missing-key.db"), false); err == nil || !strings.Contains(err.Error(), "master key is missing") {
		t.Fatalf("error = %v", err)
	}

	plain := filepath.Join(directory, "plain.db")
	plainStore, err := Open(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	_ = plainStore.Close()
	plainBackup := filepath.Join(directory, "plain-backup.db")
	plainSource, err := OpenForBackup(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := plainSource.Backup(ctx, plainBackup); err != nil {
		t.Fatal(err)
	}
	_ = plainSource.Close()
	destination := filepath.Join(directory, "destination.db")
	if err := os.WriteFile(destination+"-wal", []byte("active"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, plainBackup, destination, false); err == nil || !strings.Contains(err.Error(), "appears active") {
		t.Fatalf("error = %v", err)
	}
}
