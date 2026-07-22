package gormstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func TestRestoreValidatesSecretsAndPreservesPreviousDatabase(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
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
	now := time.Now().UTC()
	if err := store.CreateAdmin(ctx, "admin", "password-hash", now); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAdminSession(ctx, "old-session-hash", "old-csrf-hash", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.json")
	if err := store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProvider(ctx, providerconfig.Config{ID: "old-provider", Type: "virtual", Name: "Old", Config: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Restore(ctx, backup, databaseURL, keyPath, false); err == nil || !strings.Contains(err.Error(), "explicit database replacement") {
		t.Fatalf("error = %v", err)
	}
	recovery, err := Restore(ctx, backup, databaseURL, keyPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if recovery == "" {
		t.Fatal("missing pre-restore snapshot")
	}
	restored, err := Open(ctx, databaseURL, keyPath)
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
	if _, _, _, found, err := restored.AdminSession(ctx, "old-session-hash", now); err != nil || found {
		t.Fatalf("restored session found = %v, error = %v", found, err)
	}
	_ = restored.Close()

	if _, err := Restore(ctx, recovery, databaseURL, keyPath, true); err != nil {
		t.Fatal(err)
	}
	previous, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer previous.Close()
	providers, err := previous.ListProviders(ctx)
	if err != nil || !hasProvider(providers, "old-provider") {
		t.Fatalf("providers = %#v, error = %v", providers, err)
	}
}

func TestRestoreRejectsMissingKeyAndInvalidSnapshot(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testCredentials(t)
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTarget(ctx, target.Config{ID: "encrypted", Type: "apple-hap", Name: "Encrypted", Pin: "12345678"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.json")
	if err := store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	if err := os.Remove(backup + ".key"); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, backup, databaseURL, keyPath, true); err == nil || !strings.Contains(err.Error(), "master key") {
		t.Fatalf("error = %v", err)
	}

	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{"formatVersion":1`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, invalid, databaseURL, keyPath, true); err == nil || !strings.Contains(err.Error(), "decode database snapshot") {
		t.Fatalf("error = %v", err)
	}
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
