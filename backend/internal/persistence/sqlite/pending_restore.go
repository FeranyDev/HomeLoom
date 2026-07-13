package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const pendingRestoreFormatVersion = 1

type pendingRestoreMarker struct {
	FormatVersion int       `json:"formatVersion"`
	CreatedAt     time.Time `json:"createdAt"`
}

func PendingRestorePaths(databasePath string) (database, key, marker string) {
	return databasePath + ".restore-pending.db", databasePath + ".restore-pending.db.key", databasePath + ".restore-pending.json"
}

func ValidateRestoreCandidate(ctx context.Context, path string) error {
	return validateRestoreCandidate(ctx, path)
}

func WritePendingRestoreMarker(databasePath string, createdAt time.Time) error {
	_, _, markerPath := PendingRestorePaths(databasePath)
	if _, err := os.Lstat(markerPath); err == nil {
		return fmt.Errorf("a database restore is already pending")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect pending restore marker: %w", err)
	}
	payload, err := json.Marshal(pendingRestoreMarker{FormatVersion: pendingRestoreFormatVersion, CreatedAt: createdAt.UTC()})
	if err != nil {
		return fmt.Errorf("encode pending restore marker: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(markerPath), ".homeloom-restore-marker-")
	if err != nil {
		return fmt.Errorf("create pending restore marker: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure pending restore marker: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		return fmt.Errorf("write pending restore marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync pending restore marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close pending restore marker: %w", err)
	}
	if err := os.Rename(temporaryPath, markerPath); err != nil {
		return fmt.Errorf("activate pending restore marker: %w", err)
	}
	return nil
}

func ApplyPendingRestore(ctx context.Context, databasePath string) (recoveryPath string, applied bool, err error) {
	stagedDatabase, stagedKey, markerPath := PendingRestorePaths(databasePath)
	payload, err := os.ReadFile(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read pending restore marker: %w", err)
	}
	if err := requireRegularFile(markerPath, "pending restore marker"); err != nil {
		return "", false, err
	}
	var marker pendingRestoreMarker
	if err := json.Unmarshal(payload, &marker); err != nil || marker.FormatVersion != pendingRestoreFormatVersion {
		return "", false, fmt.Errorf("invalid pending restore marker")
	}
	if err := requireRegularFile(stagedDatabase, "pending restore database"); err != nil {
		return "", false, err
	}
	if err := requireRegularFile(stagedKey, "pending restore master key"); err != nil {
		return "", false, err
	}
	recoveryPath, err = Restore(ctx, stagedDatabase, databasePath, true)
	if err != nil {
		return "", false, fmt.Errorf("apply pending database restore: %w", err)
	}
	for _, path := range []string{markerPath, stagedDatabase, stagedKey} {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return recoveryPath, true, fmt.Errorf("clean applied restore staging: %w", removeErr)
		}
	}
	return recoveryPath, true, nil
}

func DiscardPendingRestore(databasePath string) error {
	stagedDatabase, stagedKey, markerPath := PendingRestorePaths(databasePath)
	for _, path := range []string{markerPath, stagedDatabase, stagedKey} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("discard pending restore: %w", err)
		}
	}
	return nil
}
