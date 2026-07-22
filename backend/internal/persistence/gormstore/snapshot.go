package gormstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	snapshotFormatVersion       = 1
	pendingRestoreFormatVersion = 1
)

type databaseSnapshot struct {
	FormatVersion         int                      `json:"formatVersion"`
	SchemaVersion         int                      `json:"schemaVersion"`
	CreatedAt             time.Time                `json:"createdAt"`
	Providers             []providerRow            `json:"providers"`
	Targets               []targetRow              `json:"targets"`
	TargetVirtualDevices  []targetVirtualDeviceRow `json:"targetVirtualDevices"`
	HomeKitAccessoryIDs   []homeKitAccessoryIDRow  `json:"homeKitAccessoryIds"`
	HomeKitIIDs           []homeKitIIDRow          `json:"homeKitIids"`
	SystemSettings        []systemSettingRow       `json:"systemSettings"`
	DevicePreferences     []devicePreferenceRow    `json:"devicePreferences"`
	AuditEvents           []auditEventRow          `json:"auditEvents"`
	MappingProfiles       []mappingProfileRow      `json:"mappingProfiles"`
	MappingBindings       []mappingBindingRow      `json:"mappingBindings"`
	CustomModelProperties []customModelPropertyRow `json:"customModelProperties"`
	AdminUsers            []adminUserRow           `json:"adminUsers"`
	AdminSessions         []adminSessionRow        `json:"adminSessions"`
	MIoTSpecCache         []miotSpecCacheRow       `json:"miotSpecCache"`
	CustomUnifiedModels   []customUnifiedModelRow  `json:"customUnifiedModels"`
}

type pendingRestoreMarker struct {
	FormatVersion int       `json:"formatVersion"`
	CreatedAt     time.Time `json:"createdAt"`
}

func (s *Store) Backup(ctx context.Context, destination string) error {
	defer s.observe(time.Now())
	if destination == "" {
		return fmt.Errorf("backup destination is required")
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("backup destination already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check backup destination: %w", err)
	}
	if _, err := os.Stat(destination + ".key"); err == nil {
		return fmt.Errorf("backup key destination already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check backup key destination: %w", err)
	}
	if err := requireRegularFile(s.keyPath, "database master key"); err != nil {
		return err
	}
	snapshot, err := s.readSnapshot(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode database snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if err := writePrivateFile(destination, payload); err != nil {
		return fmt.Errorf("write database snapshot: %w", err)
	}
	if err := copyPrivateFile(s.keyPath, destination+".key"); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("backup master key: %w", err)
	}
	return nil
}

func (s *Store) readSnapshot(ctx context.Context) (databaseSnapshot, error) {
	result := databaseSnapshot{FormatVersion: snapshotFormatVersion, SchemaVersion: currentSchemaVersion, CreatedAt: time.Now().UTC()}
	transaction := func(tx *gorm.DB) error {
		queries := []struct {
			label string
			out   any
		}{
			{"providers", &result.Providers}, {"targets", &result.Targets},
			{"target virtual devices", &result.TargetVirtualDevices}, {"HomeKit accessory IDs", &result.HomeKitAccessoryIDs},
			{"HomeKit IIDs", &result.HomeKitIIDs}, {"system settings", &result.SystemSettings},
			{"device preferences", &result.DevicePreferences}, {"audit events", &result.AuditEvents},
			{"mapping profiles", &result.MappingProfiles}, {"mapping bindings", &result.MappingBindings},
			{"custom model properties", &result.CustomModelProperties}, {"administrator users", &result.AdminUsers},
			{"administrator sessions", &result.AdminSessions}, {"MIoT spec cache", &result.MIoTSpecCache},
			{"custom unified models", &result.CustomUnifiedModels},
		}
		for _, query := range queries {
			if err := tx.Find(query.out).Error; err != nil {
				return fmt.Errorf("snapshot %s: %w", query.label, err)
			}
		}
		return nil
	}
	var err error
	if s.databaseKind == databasePostgreSQL {
		err = s.orm.WithContext(ctx).Transaction(transaction, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	} else {
		err = s.orm.WithContext(ctx).Transaction(transaction, &sql.TxOptions{ReadOnly: true})
	}
	if err != nil {
		return databaseSnapshot{}, fmt.Errorf("create consistent database snapshot: %w", err)
	}
	return result, nil
}

func readSnapshot(path string) (databaseSnapshot, error) {
	if err := requireRegularFile(path, "database snapshot"); err != nil {
		return databaseSnapshot{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return databaseSnapshot{}, fmt.Errorf("read database snapshot: %w", err)
	}
	var snapshot databaseSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return databaseSnapshot{}, fmt.Errorf("decode database snapshot: %w", err)
	}
	if snapshot.FormatVersion != snapshotFormatVersion || snapshot.SchemaVersion != currentSchemaVersion {
		return databaseSnapshot{}, fmt.Errorf("unsupported database snapshot format %d schema %d", snapshot.FormatVersion, snapshot.SchemaVersion)
	}
	return snapshot, nil
}

func ValidateRestoreCandidate(_ context.Context, path string) error {
	snapshot, err := readSnapshot(path)
	if err != nil {
		return err
	}
	if err := requireRegularFile(path+".key", "restore master key"); err != nil {
		return err
	}
	key, err := readMasterKey(path + ".key")
	if err != nil {
		return fmt.Errorf("validate restore master key: %w", err)
	}
	codec, err := newSecretCodec(key)
	if err != nil {
		return err
	}
	for _, target := range snapshot.Targets {
		if _, err := codec.decrypt("target-pin:"+target.ID, target.PIN); err != nil {
			return fmt.Errorf("validate target %q secret: %w", target.ID, err)
		}
	}
	validator := &Store{secrets: codec}
	for _, provider := range snapshot.Providers {
		if _, _, err := validator.transformProviderConfigSecrets(provider.ID, []byte(provider.ConfigJSON), false); err != nil {
			return fmt.Errorf("validate provider %q secrets: %w", provider.ID, err)
		}
	}
	return nil
}

func Restore(ctx context.Context, source, databaseURL, keyPath string, replace bool) (string, error) {
	if !replace {
		return "", fmt.Errorf("explicit database replacement confirmation is required")
	}
	if err := ValidateRestoreCandidate(ctx, source); err != nil {
		return "", err
	}
	store, err := Open(ctx, databaseURL, keyPath)
	if err != nil {
		return "", err
	}
	defer store.Close()
	recoveryFile, err := os.CreateTemp(filepath.Dir(source), "homeloom-pre-restore-*.json")
	if err != nil {
		return "", fmt.Errorf("create pre-restore snapshot path: %w", err)
	}
	recoveryPath := recoveryFile.Name()
	if err := recoveryFile.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(recoveryPath); err != nil {
		return "", err
	}
	if err := store.Backup(ctx, recoveryPath); err != nil {
		return "", fmt.Errorf("preserve current database data: %w", err)
	}
	if err := store.applySnapshotFile(ctx, source); err != nil {
		return recoveryPath, err
	}
	return recoveryPath, nil
}

func (s *Store) applySnapshotFile(ctx context.Context, source string) error {
	snapshot, err := readSnapshot(source)
	if err != nil {
		return err
	}
	if err := ValidateRestoreCandidate(ctx, source); err != nil {
		return err
	}
	current, err := s.readSnapshot(ctx)
	if err != nil {
		return err
	}
	if err := s.replaceRows(ctx, snapshot); err != nil {
		return err
	}
	newKey, err := os.ReadFile(source + ".key")
	if err == nil {
		err = replacePrivateFile(s.keyPath, newKey)
	}
	if err != nil {
		if rollbackErr := s.replaceRows(ctx, current); rollbackErr != nil {
			return fmt.Errorf("activate restored master key: %v; rollback database data: %w", err, rollbackErr)
		}
		return fmt.Errorf("activate restored master key: %w", err)
	}
	return nil
}

func (s *Store) replaceRows(ctx context.Context, snapshot databaseSnapshot) error {
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		unscoped := tx.Session(&gorm.Session{AllowGlobalUpdate: true})
		deleteOrder := []any{
			&adminSessionRow{}, &homeKitIIDRow{}, &homeKitAccessoryIDRow{}, &targetVirtualDeviceRow{},
			&mappingBindingRow{}, &mappingProfileRow{}, &customModelPropertyRow{}, &customUnifiedModelRow{},
			&miotSpecCacheRow{}, &auditEventRow{}, &devicePreferenceRow{}, &systemSettingRow{},
			&providerRow{}, &targetRow{}, &adminUserRow{},
		}
		for _, model := range deleteOrder {
			if err := unscoped.Delete(model).Error; err != nil {
				return fmt.Errorf("clear database table %T: %w", model, err)
			}
		}
		insertOrder := []struct {
			label string
			rows  any
		}{
			{"providers", &snapshot.Providers}, {"targets", &snapshot.Targets}, {"administrator users", &snapshot.AdminUsers},
			{"system settings", &snapshot.SystemSettings}, {"device preferences", &snapshot.DevicePreferences},
			{"audit events", &snapshot.AuditEvents}, {"mapping profiles", &snapshot.MappingProfiles},
			{"mapping bindings", &snapshot.MappingBindings}, {"custom model properties", &snapshot.CustomModelProperties},
			{"custom unified models", &snapshot.CustomUnifiedModels}, {"MIoT spec cache", &snapshot.MIoTSpecCache},
			{"target virtual devices", &snapshot.TargetVirtualDevices}, {"HomeKit accessory IDs", &snapshot.HomeKitAccessoryIDs},
			{"HomeKit IIDs", &snapshot.HomeKitIIDs},
		}
		for _, item := range insertOrder {
			if err := createSnapshotRows(tx, item.rows); err != nil {
				return fmt.Errorf("restore %s: %w", item.label, err)
			}
		}
		// Restored browser sessions are intentionally invalidated.
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&adminSessionRow{}).Error; err != nil {
			return fmt.Errorf("invalidate restored administrator sessions: %w", err)
		}
		if s.databaseKind == databasePostgreSQL {
			if err := tx.Exec(`SELECT setval(pg_get_serial_sequence('audit_events', 'id'), COALESCE((SELECT MAX(id) FROM audit_events), 1), EXISTS(SELECT 1 FROM audit_events))`).Error; err != nil {
				return fmt.Errorf("reset audit event sequence: %w", err)
			}
		}
		return nil
	})
}

func createSnapshotRows(tx *gorm.DB, rows any) error {
	value := reflect.ValueOf(rows)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Len() == 0 {
		return nil
	}
	return tx.Omit(clause.Associations).Create(rows).Error
}

func PendingRestorePaths(keyPath string) (snapshot, key, marker string) {
	snapshot = keyPath + ".restore-pending.json"
	return snapshot, snapshot + ".key", keyPath + ".restore-pending.marker"
}

func WritePendingRestoreMarker(keyPath string, createdAt time.Time) error {
	_, _, markerPath := PendingRestorePaths(keyPath)
	if _, err := os.Lstat(markerPath); err == nil {
		return fmt.Errorf("a database restore is already pending")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect pending restore marker: %w", err)
	}
	payload, err := json.Marshal(pendingRestoreMarker{FormatVersion: pendingRestoreFormatVersion, CreatedAt: createdAt.UTC()})
	if err != nil {
		return fmt.Errorf("encode pending restore marker: %w", err)
	}
	return writePrivateFile(markerPath, payload)
}

func ApplyPendingRestore(ctx context.Context, databaseURL, keyPath string) (recoveryPath string, applied bool, err error) {
	stagedSnapshot, stagedKey, markerPath := PendingRestorePaths(keyPath)
	payload, err := os.ReadFile(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read pending restore marker: %w", err)
	}
	var marker pendingRestoreMarker
	if err := json.Unmarshal(payload, &marker); err != nil || marker.FormatVersion != pendingRestoreFormatVersion {
		return "", false, fmt.Errorf("invalid pending restore marker")
	}
	if err := requireRegularFile(stagedSnapshot, "pending database snapshot"); err != nil {
		return "", false, err
	}
	if err := requireRegularFile(stagedKey, "pending restore master key"); err != nil {
		return "", false, err
	}
	recoveryPath, err = Restore(ctx, stagedSnapshot, databaseURL, keyPath, true)
	if err != nil {
		return "", false, fmt.Errorf("apply pending database restore: %w", err)
	}
	for _, path := range []string{markerPath, stagedSnapshot, stagedKey} {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return recoveryPath, true, fmt.Errorf("clean applied restore staging: %w", removeErr)
		}
	}
	return recoveryPath, true, nil
}

func DiscardPendingRestore(keyPath string) error {
	stagedSnapshot, stagedKey, markerPath := PendingRestorePaths(keyPath)
	for _, path := range []string{markerPath, stagedSnapshot, stagedKey} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("discard pending restore: %w", err)
		}
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

func writePrivateFile(path string, payload []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(payload); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func replacePrivateFile(path string, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".homeloom-key-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
