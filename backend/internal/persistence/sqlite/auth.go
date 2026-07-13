package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) AdminInitialized(ctx context.Context) (bool, error) {
	defer s.observe(time.Now())
	var count int
	if err := s.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_users").Scan(&count); err != nil {
		return false, fmt.Errorf("check administrator initialization: %w", err)
	}
	return count > 0, nil
}

func (s *Store) CreateAdmin(ctx context.Context, username, passwordHash string, createdAt time.Time) error {
	defer s.observe(time.Now())
	result, err := s.database.ExecContext(ctx, `INSERT INTO admin_users(id, username, password_hash, created_at, updated_at)
        SELECT 1, ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM admin_users)`, username, passwordHash, createdAt.UnixMilli(), createdAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("create administrator: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read administrator creation result: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("administrator already initialized")
	}
	return nil
}

func (s *Store) AdminPasswordHash(ctx context.Context, username string) (string, bool, error) {
	defer s.observe(time.Now())
	var passwordHash string
	err := s.database.QueryRowContext(ctx, "SELECT password_hash FROM admin_users WHERE username = ?", username).Scan(&passwordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read administrator credentials: %w", err)
	}
	return passwordHash, true, nil
}

func (s *Store) SaveAdminSession(ctx context.Context, tokenHash, csrfHash string, createdAt, expiresAt time.Time) error {
	defer s.observe(time.Now())
	_, err := s.database.ExecContext(ctx, `INSERT INTO admin_sessions(
        token_hash, admin_id, csrf_hash, created_at, expires_at, last_seen_at
    ) VALUES (?, 1, ?, ?, ?, ?)`, tokenHash, csrfHash, createdAt.UnixMilli(), expiresAt.UnixMilli(), createdAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("save administrator session: %w", err)
	}
	return nil
}

func (s *Store) AdminSession(ctx context.Context, tokenHash string, now time.Time) (username, csrfHash string, expiresAt time.Time, found bool, err error) {
	defer s.observe(time.Now())
	var expiresAtMillis int64
	err = s.database.QueryRowContext(ctx, `SELECT users.username, sessions.csrf_hash, sessions.expires_at
        FROM admin_sessions sessions JOIN admin_users users ON users.id = sessions.admin_id
        WHERE sessions.token_hash = ? AND sessions.expires_at > ?`, tokenHash, now.UnixMilli()).Scan(&username, &csrfHash, &expiresAtMillis)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", time.Time{}, false, nil
	}
	if err != nil {
		return "", "", time.Time{}, false, fmt.Errorf("read administrator session: %w", err)
	}
	return username, csrfHash, time.UnixMilli(expiresAtMillis).UTC(), true, nil
}

func (s *Store) TouchAdminSession(ctx context.Context, tokenHash string, seenAt time.Time) error {
	defer s.observe(time.Now())
	if _, err := s.database.ExecContext(ctx, "UPDATE admin_sessions SET last_seen_at = ? WHERE token_hash = ?", seenAt.UnixMilli(), tokenHash); err != nil {
		return fmt.Errorf("touch administrator session: %w", err)
	}
	return nil
}

func (s *Store) DeleteAdminSession(ctx context.Context, tokenHash string) error {
	defer s.observe(time.Now())
	if _, err := s.database.ExecContext(ctx, "DELETE FROM admin_sessions WHERE token_hash = ?", tokenHash); err != nil {
		return fmt.Errorf("delete administrator session: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredAdminSessions(ctx context.Context, now time.Time) error {
	defer s.observe(time.Now())
	if _, err := s.database.ExecContext(ctx, "DELETE FROM admin_sessions WHERE expires_at <= ?", now.UnixMilli()); err != nil {
		return fmt.Errorf("delete expired administrator sessions: %w", err)
	}
	return nil
}
