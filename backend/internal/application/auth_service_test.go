package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type authMemorySession struct {
	csrfHash  string
	expiresAt time.Time
}

type authMemoryStore struct {
	username     string
	passwordHash string
	sessions     map[string]authMemorySession
}

func (s *authMemoryStore) AdminInitialized(context.Context) (bool, error) {
	return s.username != "", nil
}
func (s *authMemoryStore) CreateAdmin(_ context.Context, username, passwordHash string, _ time.Time) error {
	if s.username != "" {
		return errors.New("already initialized")
	}
	s.username, s.passwordHash = username, passwordHash
	return nil
}
func (s *authMemoryStore) AdminPasswordHash(_ context.Context, username string) (string, bool, error) {
	return s.passwordHash, s.username == username, nil
}
func (s *authMemoryStore) SaveAdminSession(_ context.Context, tokenHash, csrfHash string, _ time.Time, expiresAt time.Time) error {
	if s.sessions == nil {
		s.sessions = make(map[string]authMemorySession)
	}
	s.sessions[tokenHash] = authMemorySession{csrfHash: csrfHash, expiresAt: expiresAt}
	return nil
}
func (s *authMemoryStore) AdminSession(_ context.Context, tokenHash string, now time.Time) (string, string, time.Time, bool, error) {
	session, found := s.sessions[tokenHash]
	if !found || !session.expiresAt.After(now) {
		return "", "", time.Time{}, false, nil
	}
	return s.username, session.csrfHash, session.expiresAt, true, nil
}
func (s *authMemoryStore) TouchAdminSession(context.Context, string, time.Time) error { return nil }
func (s *authMemoryStore) DeleteAdminSession(_ context.Context, tokenHash string) error {
	delete(s.sessions, tokenHash)
	return nil
}
func (s *authMemoryStore) DeleteExpiredAdminSessions(_ context.Context, now time.Time) error {
	for key, session := range s.sessions {
		if !session.expiresAt.After(now) {
			delete(s.sessions, key)
		}
	}
	return nil
}

func TestAuthServiceSetupLoginCSRFAndLogout(t *testing.T) {
	ctx := context.Background()
	store := &authMemoryStore{}
	service := NewAuthService(store)
	service.bcryptCost = bcrypt.MinCost
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	sequence := 0
	service.random = func(int) (string, error) { sequence++; return string(rune('a' + sequence)), nil }

	if _, err := service.Setup(ctx, "ad min", "short"); err == nil {
		t.Fatal("invalid credentials accepted")
	}
	session, err := service.Setup(ctx, "admin", "a-long-password")
	if err != nil || session.Token == "" || session.CSRFToken == "" {
		t.Fatalf("setup session = %#v, %v", session, err)
	}
	if store.passwordHash == "a-long-password" {
		t.Fatal("password was stored as plaintext")
	}
	if _, found := store.sessions[session.Token]; found {
		t.Fatal("raw session token was stored")
	}
	status, err := service.Status(ctx, session.Token)
	if err != nil || !status.Initialized || !status.Authenticated || status.Username != "admin" {
		t.Fatalf("status = %#v, %v", status, err)
	}
	if err := service.VerifyCSRF(session, "wrong"); !errors.Is(err, ErrInvalidCSRF) {
		t.Fatalf("csrf error = %v", err)
	}
	if err := service.VerifyCSRF(session, session.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(ctx, "admin", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login error = %v", err)
	}
	if _, err := service.Login(ctx, "admin", "a-long-password"); err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("authenticate after logout = %v", err)
	}
}

func TestAuthServiceExpiresSessions(t *testing.T) {
	ctx := context.Background()
	store := &authMemoryStore{}
	service := NewAuthService(store)
	service.bcryptCost = bcrypt.MinCost
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	session, err := service.Setup(ctx, "admin", "a-long-password")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(25 * time.Hour)
	if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired session error = %v", err)
	}
}
