package application

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	BackupConfirmation  = "BACKUP"
	RestoreConfirmation = "RESTORE"
	maxRestoreArchive   = 256 << 20
)

type MaintenanceStore interface {
	Backup(context.Context, string) error
	SchemaVersion(context.Context) (int, error)
}

type MaintenanceArtifact struct {
	Path     string
	Filename string
	cleanup  func()
}

func (a MaintenanceArtifact) Cleanup() {
	if a.cleanup != nil {
		a.cleanup()
	}
}

type PendingRestore struct {
	Staged          bool      `json:"staged"`
	RequiresRestart bool      `json:"requiresRestart"`
	CreatedAt       time.Time `json:"createdAt"`
	SchemaVersion   int       `json:"schemaVersion"`
}

type MaintenanceService struct {
	store         MaintenanceStore
	masterKeyPath string
	validate      func(context.Context, string) error
	pendingPaths  func(string) (string, string, string)
	writeMarker   func(string, time.Time) error
	now           func() time.Time
}

func NewMaintenanceService(store MaintenanceStore, masterKeyPath string, validate func(context.Context, string) error, pendingPaths func(string) (string, string, string), writeMarker func(string, time.Time) error) *MaintenanceService {
	return &MaintenanceService{store: store, masterKeyPath: masterKeyPath, validate: validate, pendingPaths: pendingPaths, writeMarker: writeMarker, now: time.Now}
}

func (s *MaintenanceService) Backup(ctx context.Context, confirmation string) (MaintenanceArtifact, error) {
	if confirmation != BackupConfirmation {
		return MaintenanceArtifact{}, NewValidationError("backup confirmation required", map[string]string{"confirmation": "type BACKUP to confirm"})
	}
	directory, err := os.MkdirTemp("", "homeloom-backup-")
	if err != nil {
		return MaintenanceArtifact{}, fmt.Errorf("create backup workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	snapshot := filepath.Join(directory, "homeloom-postgres.json")
	if err := s.store.Backup(ctx, snapshot); err != nil {
		cleanup()
		return MaintenanceArtifact{}, err
	}
	archivePath := filepath.Join(directory, "homeloom-backup.zip")
	if err := createBackupArchive(archivePath, snapshot); err != nil {
		cleanup()
		return MaintenanceArtifact{}, err
	}
	filename := "homeloom-backup-" + s.now().UTC().Format("20060102T150405Z") + ".zip"
	return MaintenanceArtifact{Path: archivePath, Filename: filename, cleanup: cleanup}, nil
}

func (s *MaintenanceService) StageRestore(ctx context.Context, archive io.Reader, confirmation string) (PendingRestore, error) {
	if confirmation != RestoreConfirmation {
		return PendingRestore{}, NewValidationError("restore confirmation required", map[string]string{"confirmation": "type RESTORE to confirm"})
	}
	if archive == nil {
		return PendingRestore{}, NewValidationError("restore archive is required", map[string]string{"file": "required"})
	}
	stagedSnapshot, stagedKey, markerPath := s.pendingPaths(s.masterKeyPath)
	for _, path := range []string{stagedSnapshot, stagedKey, markerPath} {
		if _, err := os.Lstat(path); err == nil {
			return PendingRestore{}, fmt.Errorf("a database restore is already pending")
		} else if !os.IsNotExist(err) {
			return PendingRestore{}, fmt.Errorf("inspect restore staging: %w", err)
		}
	}
	directory, err := os.MkdirTemp(filepath.Dir(s.masterKeyPath), ".homeloom-restore-upload-")
	if err != nil {
		return PendingRestore{}, fmt.Errorf("create restore upload workspace: %w", err)
	}
	defer os.RemoveAll(directory)
	archivePath := filepath.Join(directory, "backup.zip")
	if err := copyLimitedFile(archivePath, archive, maxRestoreArchive); err != nil {
		return PendingRestore{}, err
	}
	candidate := filepath.Join(directory, "homeloom-postgres.json")
	if err := extractBackupArchive(archivePath, candidate); err != nil {
		return PendingRestore{}, err
	}
	if err := s.validate(ctx, candidate); err != nil {
		return PendingRestore{}, err
	}
	version, err := s.store.SchemaVersion(ctx)
	if err != nil {
		return PendingRestore{}, err
	}
	if err := movePrivateFile(candidate, stagedSnapshot); err != nil {
		return PendingRestore{}, err
	}
	if err := movePrivateFile(candidate+".key", stagedKey); err != nil {
		_ = os.Remove(stagedSnapshot)
		return PendingRestore{}, err
	}
	createdAt := s.now().UTC()
	if err := s.writeMarker(s.masterKeyPath, createdAt); err != nil {
		_ = os.Remove(stagedSnapshot)
		_ = os.Remove(stagedKey)
		return PendingRestore{}, err
	}
	return PendingRestore{Staged: true, RequiresRestart: true, CreatedAt: createdAt, SchemaVersion: version}, nil
}

func createBackupArchive(archivePath, snapshotPath string) error {
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create backup archive: %w", err)
	}
	archive := zip.NewWriter(file)
	for _, item := range []struct{ source, name string }{{snapshotPath, "homeloom-postgres.json"}, {snapshotPath + ".key", "homeloom-master.key"}} {
		if err := addArchiveFile(archive, item.source, item.name); err != nil {
			archive.Close()
			file.Close()
			return err
		}
	}
	if err := archive.Close(); err != nil {
		file.Close()
		return fmt.Errorf("finish backup archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close backup archive: %w", err)
	}
	return nil
}

func addArchiveFile(archive *zip.Writer, source, name string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open backup component: %w", err)
	}
	defer input.Close()
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o600)
	output, err := archive.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create backup archive entry: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("write backup archive entry: %w", err)
	}
	return nil
}

func copyLimitedFile(path string, source io.Reader, limit int64) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create restore upload: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, limit+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("store restore upload: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close restore upload: %w", closeErr)
	}
	if written > limit {
		return fmt.Errorf("restore archive exceeds %d bytes", limit)
	}
	return nil
}

func extractBackupArchive(archivePath, candidate string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open restore archive: %w", err)
	}
	defer archive.Close()
	entries := map[string]string{"homeloom-postgres.json": candidate, "homeloom-master.key": candidate + ".key"}
	seen := make(map[string]bool)
	var totalSize uint64
	for _, entry := range archive.File {
		destination, ok := entries[entry.Name]
		totalSize += entry.UncompressedSize64
		if !ok || seen[entry.Name] || entry.FileInfo().IsDir() || entry.UncompressedSize64 > maxRestoreArchive || totalSize > maxRestoreArchive {
			return fmt.Errorf("invalid restore archive entry %q", entry.Name)
		}
		input, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open restore archive entry: %w", err)
		}
		err = copyLimitedFile(destination, input, maxRestoreArchive)
		input.Close()
		if err != nil {
			return err
		}
		seen[entry.Name] = true
	}
	for name := range entries {
		if !seen[name] {
			return fmt.Errorf("restore archive is missing %s", name)
		}
	}
	return nil
}

func movePrivateFile(source, destination string) error {
	if filepath.Dir(source) == filepath.Dir(destination) {
		if err := os.Rename(source, destination); err != nil {
			return fmt.Errorf("stage restore component: %w", err)
		}
	} else {
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		err = copyLimitedFile(destination, input, maxRestoreArchive)
		input.Close()
		if err != nil {
			return err
		}
		if err := os.Remove(source); err != nil {
			return err
		}
	}
	return os.Chmod(destination, 0o600)
}
