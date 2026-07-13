package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Restore copies and validates a backup before it becomes the configured
// database. Replacing an existing database requires replace=true and leaves a
// paired pre-restore snapshot beside it for manual rollback.
func Restore(ctx context.Context, source, destination string, replace bool) (string, error) {
	if source == "" || destination == "" {
		return "", fmt.Errorf("restore source and destination are required")
	}
	sourcePath, sourceErr := filepath.Abs(source)
	destinationPath, destinationErr := filepath.Abs(destination)
	if sourceErr != nil || destinationErr != nil {
		return "", fmt.Errorf("resolve restore paths")
	}
	if sourcePath == destinationPath {
		return "", fmt.Errorf("restore source must differ from database path")
	}
	if err := requireRegularFile(sourcePath, "restore source"); err != nil {
		return "", err
	}
	sourceKey := sourcePath + ".key"
	if _, err := os.Lstat(sourceKey); err == nil {
		if err := requireRegularFile(sourceKey, "restore master key"); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect restore master key: %w", err)
	}

	destinationExists := false
	if _, err := os.Lstat(destinationPath); err == nil {
		destinationExists = true
		if err := requireRegularFile(destinationPath, "database destination"); err != nil {
			return "", err
		}
		if !replace {
			return "", fmt.Errorf("database destination already exists; explicit replacement confirmation is required")
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect database destination: %w", err)
	}
	if _, err := os.Lstat(destinationPath + ".key"); err == nil {
		if err := requireRegularFile(destinationPath+".key", "destination master key"); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect destination master key: %w", err)
	}
	if !destinationExists {
		if _, err := os.Lstat(destinationPath + ".key"); err == nil {
			return "", fmt.Errorf("orphan destination master key already exists")
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect destination master key: %w", err)
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(destinationPath + suffix); err == nil {
			return "", fmt.Errorf("database appears active or was not cleanly stopped: %s exists", filepath.Base(destinationPath+suffix))
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect database sidecar: %w", err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o750); err != nil {
		return "", fmt.Errorf("create restore directory: %w", err)
	}
	stagingDirectory, err := os.MkdirTemp(filepath.Dir(destinationPath), ".homeloom-restore-")
	if err != nil {
		return "", fmt.Errorf("create restore staging directory: %w", err)
	}
	defer os.RemoveAll(stagingDirectory)
	stagedDatabase := filepath.Join(stagingDirectory, "homeloom.db")
	if err := copyPrivateFile(sourcePath, stagedDatabase); err != nil {
		return "", fmt.Errorf("stage restore database: %w", err)
	}
	if _, err := os.Stat(sourceKey); err == nil {
		if err := copyPrivateFile(sourceKey, stagedDatabase+".key"); err != nil {
			return "", fmt.Errorf("stage restore master key: %w", err)
		}
	}
	if err := validateRestoreCandidate(ctx, stagedDatabase); err != nil {
		return "", err
	}

	recoveryPath := ""
	if destinationExists {
		recoveryPath = destinationPath + ".pre-restore-" + time.Now().UTC().Format("20060102T150405.000000000Z")
		if _, err := os.Lstat(recoveryPath); err == nil {
			return "", fmt.Errorf("pre-restore snapshot destination already exists")
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect pre-restore snapshot destination: %w", err)
		}
		if err := os.Rename(destinationPath, recoveryPath); err != nil {
			return "", fmt.Errorf("preserve current database: %w", err)
		}
		if _, err := os.Lstat(destinationPath + ".key"); err == nil {
			if err := os.Rename(destinationPath+".key", recoveryPath+".key"); err != nil {
				_ = os.Rename(recoveryPath, destinationPath)
				return "", fmt.Errorf("preserve current master key: %w", err)
			}
		} else if !os.IsNotExist(err) {
			_ = os.Rename(recoveryPath, destinationPath)
			return "", fmt.Errorf("inspect current master key: %w", err)
		}
	}
	if err := os.Rename(stagedDatabase, destinationPath); err != nil {
		rollbackRestore(destinationPath, recoveryPath)
		return "", fmt.Errorf("activate restored database: %w", err)
	}
	if err := os.Rename(stagedDatabase+".key", destinationPath+".key"); err != nil {
		_ = os.Remove(destinationPath)
		rollbackRestore(destinationPath, recoveryPath)
		return "", fmt.Errorf("activate restored master key: %w", err)
	}
	if err := os.Chmod(destinationPath, 0o600); err != nil {
		return recoveryPath, fmt.Errorf("secure restored database: %w", err)
	}
	if err := os.Chmod(destinationPath+".key", 0o600); err != nil {
		return recoveryPath, fmt.Errorf("secure restored master key: %w", err)
	}
	return recoveryPath, nil
}

func validateRestoreCandidate(ctx context.Context, path string) error {
	inspection, err := OpenForBackup(ctx, path)
	if err != nil {
		return fmt.Errorf("inspect restore candidate: %w", err)
	}
	var integrity string
	if err := inspection.database.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		_ = inspection.Close()
		if err != nil {
			return fmt.Errorf("check restore integrity: %w", err)
		}
		return fmt.Errorf("check restore integrity: %s", integrity)
	}
	if _, err := inspection.SchemaVersion(ctx); err != nil {
		_ = inspection.Close()
		return fmt.Errorf("inspect restore schema: %w", err)
	}
	if err := inspection.Close(); err != nil {
		return fmt.Errorf("close restore inspection: %w", err)
	}
	validated, err := Open(ctx, path)
	if err != nil {
		return fmt.Errorf("validate restore compatibility and secrets: %w", err)
	}
	if _, err := validated.database.ExecContext(ctx, "DELETE FROM admin_sessions"); err != nil {
		_ = validated.Close()
		return fmt.Errorf("invalidate restored administrator sessions: %w", err)
	}
	if _, err := validated.database.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = validated.Close()
		return fmt.Errorf("checkpoint restored database: %w", err)
	}
	if err := validated.Close(); err != nil {
		return fmt.Errorf("close validated restore: %w", err)
	}
	return nil
}

func requireRegularFile(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file", label)
	}
	return nil
}

func rollbackRestore(destination, recovery string) {
	if recovery == "" {
		return
	}
	_ = os.Rename(recovery, destination)
	if _, err := os.Lstat(recovery + ".key"); err == nil {
		_ = os.Rename(recovery+".key", destination+".key")
	}
}
