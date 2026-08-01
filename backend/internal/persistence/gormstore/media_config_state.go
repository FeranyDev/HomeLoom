package gormstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const mediaConfigStateID uint8 = 1

type MediaConfigVersion struct {
	Generation uint64
	Revision   uint64
	UpdatedAt  time.Time
}

func (s *Store) GetMediaConfigVersion(ctx context.Context) (MediaConfigVersion, error) {
	defer s.observe(time.Now())
	var row mediaConfigStateRow
	err := s.orm.WithContext(ctx).Where("id = ?", mediaConfigStateID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MediaConfigVersion{}, errors.New("media config state is missing")
	}
	if err != nil {
		return MediaConfigVersion{}, fmt.Errorf("read media config state: %w", err)
	}
	return mediaConfigVersionFromRow(row), nil
}

func (s *Store) BumpMediaConfigRevision(ctx context.Context) (MediaConfigVersion, error) {
	defer s.observe(time.Now())
	var result MediaConfigVersion
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = s.bumpMediaConfigRevisionTx(tx)
		return err
	})
	return result, err
}

func (s *Store) BumpMediaConfigGeneration(ctx context.Context) (MediaConfigVersion, error) {
	defer s.observe(time.Now())
	var result MediaConfigVersion
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := s.lockMediaConfigState(tx)
		if err != nil {
			return err
		}
		if row.Generation == math.MaxUint64 {
			return errors.New("media config generation overflow")
		}
		row.Generation++
		row.Revision = 1
		row.UpdatedAt = time.Now().UTC().UnixMilli()
		if err := tx.Model(&mediaConfigStateRow{}).Where("id = ?", mediaConfigStateID).
			Updates(map[string]any{
				"generation": row.Generation,
				"revision":   row.Revision,
				"updated_at": row.UpdatedAt,
			}).Error; err != nil {
			return fmt.Errorf("bump media config generation: %w", err)
		}
		result = mediaConfigVersionFromRow(row)
		return nil
	})
	return result, err
}

// CompareAndSwapMediaConfigVersion atomically applies one legal transition:
// either the next revision in the same generation or the first revision of the
// next generation. It is suitable for callers that computed a configuration
// snapshot outside a database transaction.
func (s *Store) CompareAndSwapMediaConfigVersion(
	ctx context.Context,
	expected MediaConfigVersion,
	next MediaConfigVersion,
) (bool, error) {
	defer s.observe(time.Now())
	if err := validateMediaConfigTransition(expected, next); err != nil {
		return false, err
	}
	now := time.Now().UTC().UnixMilli()
	result := s.orm.WithContext(ctx).Model(&mediaConfigStateRow{}).
		Where("id = ? AND generation = ? AND revision = ?", mediaConfigStateID, expected.Generation, expected.Revision).
		Updates(map[string]any{"generation": next.Generation, "revision": next.Revision, "updated_at": now})
	if result.Error != nil {
		return false, fmt.Errorf("compare and swap media config state: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

func validateMediaConfigTransition(expected, next MediaConfigVersion) error {
	if expected.Generation == 0 || expected.Revision == 0 {
		return errors.New("expected media config generation and revision must be positive")
	}
	if next.Generation == expected.Generation {
		if expected.Revision == math.MaxUint64 || next.Revision != expected.Revision+1 {
			return errors.New("next media config revision must advance exactly once")
		}
		return nil
	}
	if expected.Generation != math.MaxUint64 &&
		next.Generation == expected.Generation+1 &&
		next.Revision == 1 {
		return nil
	}
	return errors.New("next media config generation must advance exactly once and reset revision to one")
}

func (s *Store) bumpMediaConfigRevisionTx(tx *gorm.DB) (MediaConfigVersion, error) {
	row, err := s.lockMediaConfigState(tx)
	if err != nil {
		return MediaConfigVersion{}, err
	}
	if row.Revision == math.MaxUint64 {
		return MediaConfigVersion{}, errors.New("media config revision overflow")
	}
	row.Revision++
	row.UpdatedAt = time.Now().UTC().UnixMilli()
	if err := tx.Model(&mediaConfigStateRow{}).Where("id = ?", mediaConfigStateID).
		Updates(map[string]any{"revision": row.Revision, "updated_at": row.UpdatedAt}).Error; err != nil {
		return MediaConfigVersion{}, fmt.Errorf("bump media config revision: %w", err)
	}
	return mediaConfigVersionFromRow(row), nil
}

func (s *Store) lockMediaConfigState(tx *gorm.DB) (mediaConfigStateRow, error) {
	var row mediaConfigStateRow
	query := tx.Where("id = ?", mediaConfigStateID)
	if s.databaseKind == databasePostgreSQL {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return mediaConfigStateRow{}, errors.New("media config state is missing")
	}
	if err != nil {
		return mediaConfigStateRow{}, fmt.Errorf("lock media config state: %w", err)
	}
	return row, nil
}

func mediaConfigVersionFromRow(row mediaConfigStateRow) MediaConfigVersion {
	return MediaConfigVersion{
		Generation: row.Generation,
		Revision:   row.Revision,
		UpdatedAt:  time.UnixMilli(row.UpdatedAt).UTC(),
	}
}
