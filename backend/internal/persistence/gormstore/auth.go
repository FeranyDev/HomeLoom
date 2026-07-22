package gormstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

func (s *Store) AdminInitialized(ctx context.Context) (bool, error) {
	defer s.observe(time.Now())
	var count int64
	if err := s.orm.WithContext(ctx).Model(&adminUserRow{}).Count(&count).Error; err != nil {
		return false, fmt.Errorf("check administrator initialization: %w", err)
	}
	return count > 0, nil
}

func (s *Store) CreateAdmin(ctx context.Context, username, passwordHash string, createdAt time.Time) error {
	defer s.observe(time.Now())
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&adminUserRow{}).Count(&count).Error; err != nil {
			return fmt.Errorf("check administrator initialization: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("administrator already initialized")
		}
		row := adminUserRow{ID: 1, Username: username, PasswordHash: passwordHash, CreatedAt: createdAt.UnixMilli(), UpdatedAt: createdAt.UnixMilli()}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("create administrator: %w", err)
		}
		return nil
	})
}

func (s *Store) AdminPasswordHash(ctx context.Context, username string) (string, bool, error) {
	defer s.observe(time.Now())
	var row adminUserRow
	err := s.orm.WithContext(ctx).Select("password_hash").Where("username = ?", username).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read administrator credentials: %w", err)
	}
	return row.PasswordHash, true, nil
}

func (s *Store) SaveAdminSession(ctx context.Context, tokenHash, csrfHash string, createdAt, expiresAt time.Time) error {
	defer s.observe(time.Now())
	row := adminSessionRow{TokenHash: tokenHash, AdminID: 1, CSRFHash: csrfHash, CreatedAt: createdAt.UnixMilli(), ExpiresAt: expiresAt.UnixMilli(), LastSeenAt: createdAt.UnixMilli()}
	err := s.orm.WithContext(ctx).Create(&row).Error
	if err != nil {
		return fmt.Errorf("save administrator session: %w", err)
	}
	return nil
}

func (s *Store) AdminSession(ctx context.Context, tokenHash string, now time.Time) (username, csrfHash string, expiresAt time.Time, found bool, err error) {
	defer s.observe(time.Now())
	var result struct {
		Username  string `gorm:"column:username"`
		CSRFHash  string `gorm:"column:csrf_hash"`
		ExpiresAt int64  `gorm:"column:expires_at"`
	}
	query := s.orm.WithContext(ctx).Table("admin_sessions AS sessions").
		Select("users.username, sessions.csrf_hash, sessions.expires_at").
		Joins("JOIN admin_users AS users ON users.id = sessions.admin_id").
		Where("sessions.token_hash = ? AND sessions.expires_at > ?", tokenHash, now.UnixMilli()).
		Take(&result)
	if errors.Is(query.Error, gorm.ErrRecordNotFound) {
		return "", "", time.Time{}, false, nil
	}
	if query.Error != nil {
		return "", "", time.Time{}, false, fmt.Errorf("read administrator session: %w", query.Error)
	}
	return result.Username, result.CSRFHash, time.UnixMilli(result.ExpiresAt).UTC(), true, nil
}

func (s *Store) TouchAdminSession(ctx context.Context, tokenHash string, seenAt time.Time) error {
	defer s.observe(time.Now())
	if err := s.orm.WithContext(ctx).Model(&adminSessionRow{}).Where("token_hash = ?", tokenHash).Update("last_seen_at", seenAt.UnixMilli()).Error; err != nil {
		return fmt.Errorf("touch administrator session: %w", err)
	}
	return nil
}

func (s *Store) DeleteAdminSession(ctx context.Context, tokenHash string) error {
	defer s.observe(time.Now())
	if err := s.orm.WithContext(ctx).Where("token_hash = ?", tokenHash).Delete(&adminSessionRow{}).Error; err != nil {
		return fmt.Errorf("delete administrator session: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredAdminSessions(ctx context.Context, now time.Time) error {
	defer s.observe(time.Now())
	if err := s.orm.WithContext(ctx).Where("expires_at <= ?", now.UnixMilli()).Delete(&adminSessionRow{}).Error; err != nil {
		return fmt.Errorf("delete expired administrator sessions: %w", err)
	}
	return nil
}
