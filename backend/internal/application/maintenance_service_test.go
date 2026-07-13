package application_test

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/persistence/sqlite"
)

func TestMaintenanceServiceBacksUpStagesAndAppliesRestore(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "homeloom.db")
	store, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProvider(ctx, providerconfig.Config{ID: "preserved", Type: "virtual", Name: "Preserved", Config: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	service := application.NewMaintenanceService(store, databasePath, sqlite.ValidateRestoreCandidate, sqlite.PendingRestorePaths, sqlite.WritePendingRestoreMarker)
	if _, err := service.Backup(ctx, "wrong"); err == nil {
		t.Fatal("backup accepted missing confirmation")
	}
	artifact, err := service.Backup(ctx, application.BackupConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Cleanup()
	archive, err := zip.OpenReader(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(archive.File))
	for _, entry := range archive.File {
		names = append(names, entry.Name)
	}
	archive.Close()
	sort.Strings(names)
	if len(names) != 2 || names[0] != "homeloom.db" || names[1] != "homeloom.db.key" {
		t.Fatalf("archive entries = %#v", names)
	}

	upload, err := os.Open(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := service.StageRestore(ctx, upload, application.RestoreConfirmation)
	upload.Close()
	if err != nil || !pending.Staged || !pending.RequiresRestart || pending.SchemaVersion == 0 {
		t.Fatalf("pending restore = %#v, %v", pending, err)
	}
	if _, err := service.StageRestore(ctx, nil, application.RestoreConfirmation); err == nil {
		t.Fatal("second pending restore was accepted")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	recovery, applied, err := sqlite.ApplyPendingRestore(ctx, databasePath)
	if err != nil || !applied || recovery == "" {
		t.Fatalf("apply pending restore = %q, %v, %v", recovery, applied, err)
	}
	restored, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	providers, err := restored.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, provider := range providers {
		found = found || provider.ID == "preserved"
	}
	if !found {
		t.Fatalf("restored providers = %#v", providers)
	}
}
