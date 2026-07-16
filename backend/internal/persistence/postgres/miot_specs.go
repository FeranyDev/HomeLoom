package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) LoadMIoTSpec(ctx context.Context, specType, model string) ([]byte, string, time.Time, bool, error) {
	defer s.observe(time.Now())
	query := s.orm.WithContext(ctx)
	if specType == "" {
		query = query.Where("model = ?", model).Order("fetched_at DESC")
	} else {
		query = query.Where("spec_type = ?", specType)
	}
	var row miotSpecCacheRow
	err := query.Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", time.Time{}, false, nil
		}
		return nil, "", time.Time{}, false, fmt.Errorf("load MIoT spec cache: %w", err)
	}
	return append([]byte(nil), row.DocumentJSON...), row.SpecType, time.UnixMilli(row.FetchedAt).UTC(), true, nil
}

func (s *Store) SaveMIoTSpec(ctx context.Context, specType, model string, document []byte, fetchedAt time.Time) error {
	defer s.observe(time.Now())
	row := miotSpecCacheRow{SpecType: specType, Model: model, DocumentJSON: append([]byte(nil), document...), FetchedAt: fetchedAt.UTC().UnixMilli()}
	err := s.orm.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "spec_type"}}, DoUpdates: clause.AssignmentColumns([]string{"model", "document_json", "fetched_at"})}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("save MIoT spec cache: %w", err)
	}
	return nil
}
