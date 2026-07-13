package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrAdminAlreadyInitialized = errors.New("administrator already initialized")
	ErrInvalidCredentials      = errors.New("invalid credentials")
	ErrInvalidSession          = errors.New("invalid session")
	ErrInvalidCSRF             = errors.New("invalid CSRF token")
)

var dummyPasswordHash = func() []byte {
	hash, _ := bcrypt.GenerateFromPassword([]byte("homeloom-dummy-password"), bcrypt.DefaultCost)
	return hash
}()

type AuthStore interface {
	AdminInitialized(context.Context) (bool, error)
	CreateAdmin(context.Context, string, string, time.Time) error
	AdminPasswordHash(context.Context, string) (string, bool, error)
	SaveAdminSession(context.Context, string, string, time.Time, time.Time) error
	AdminSession(context.Context, string, time.Time) (string, string, time.Time, bool, error)
	TouchAdminSession(context.Context, string, time.Time) error
	DeleteAdminSession(context.Context, string) error
	DeleteExpiredAdminSessions(context.Context, time.Time) error
}

type AuthSession struct {
	Username  string    `json:"username"`
	Token     string    `json:"-"`
	CSRFToken string    `json:"-"`
	ExpiresAt time.Time `json:"expiresAt"`
	csrfHash  string
}

type AuthStatus struct {
	Initialized   bool   `json:"initialized"`
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
}

type AuthService struct {
	store      AuthStore
	now        func() time.Time
	random     func(int) (string, error)
	sessionTTL time.Duration
	bcryptCost int
}

func NewAuthService(store AuthStore) *AuthService {
	return &AuthService{store: store, now: time.Now, random: randomToken, sessionTTL: 24 * time.Hour, bcryptCost: bcrypt.DefaultCost}
}

func (s *AuthService) Status(ctx context.Context, token string) (AuthStatus, error) {
	initialized, err := s.store.AdminInitialized(ctx)
	if err != nil || !initialized || token == "" {
		return AuthStatus{Initialized: initialized}, err
	}
	session, err := s.Authenticate(ctx, token)
	if errors.Is(err, ErrInvalidSession) {
		return AuthStatus{Initialized: true}, nil
	}
	if err != nil {
		return AuthStatus{}, err
	}
	return AuthStatus{Initialized: true, Authenticated: true, Username: session.Username}, nil
}

func (s *AuthService) Setup(ctx context.Context, username, password string) (AuthSession, error) {
	username, fields := validateCredentials(username, password)
	if len(fields) > 0 {
		return AuthSession{}, NewValidationError("invalid administrator credentials", fields)
	}
	initialized, err := s.store.AdminInitialized(ctx)
	if err != nil {
		return AuthSession{}, err
	}
	if initialized {
		return AuthSession{}, ErrAdminAlreadyInitialized
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return AuthSession{}, fmt.Errorf("hash administrator password: %w", err)
	}
	now := s.now().UTC()
	if err := s.store.CreateAdmin(ctx, username, string(hash), now); err != nil {
		initialized, checkErr := s.store.AdminInitialized(ctx)
		if checkErr != nil {
			return AuthSession{}, err
		}
		if initialized {
			return AuthSession{}, ErrAdminAlreadyInitialized
		}
		return AuthSession{}, err
	}
	return s.newSession(ctx, username, now)
}

func (s *AuthService) Login(ctx context.Context, username, password string) (AuthSession, error) {
	username = strings.TrimSpace(username)
	hash, found, err := s.store.AdminPasswordHash(ctx, username)
	if err != nil {
		return AuthSession{}, err
	}
	comparisonHash := []byte(hash)
	if !found {
		comparisonHash = dummyPasswordHash
	}
	if bcrypt.CompareHashAndPassword(comparisonHash, []byte(password)) != nil || !found {
		return AuthSession{}, ErrInvalidCredentials
	}
	now := s.now().UTC()
	return s.newSession(ctx, username, now)
}

func (s *AuthService) Authenticate(ctx context.Context, token string) (AuthSession, error) {
	if token == "" {
		return AuthSession{}, ErrInvalidSession
	}
	now := s.now().UTC()
	tokenHash := hashToken(token)
	username, csrfHash, expiresAt, found, err := s.store.AdminSession(ctx, tokenHash, now)
	if err != nil {
		return AuthSession{}, err
	}
	if !found {
		return AuthSession{}, ErrInvalidSession
	}
	if err := s.store.TouchAdminSession(ctx, tokenHash, now); err != nil {
		return AuthSession{}, err
	}
	return AuthSession{Username: username, Token: token, ExpiresAt: expiresAt, csrfHash: csrfHash}, nil
}

func (s *AuthService) VerifyCSRF(session AuthSession, csrfToken string) error {
	provided := hashToken(csrfToken)
	if csrfToken == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(session.csrfHash)) != 1 {
		return ErrInvalidCSRF
	}
	return nil
}

func (s *AuthService) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteAdminSession(ctx, hashToken(token))
}

func (s *AuthService) newSession(ctx context.Context, username string, now time.Time) (AuthSession, error) {
	token, err := s.random(32)
	if err != nil {
		return AuthSession{}, fmt.Errorf("generate session token: %w", err)
	}
	csrf, err := s.random(32)
	if err != nil {
		return AuthSession{}, fmt.Errorf("generate CSRF token: %w", err)
	}
	expiresAt := now.Add(s.sessionTTL)
	if err := s.store.DeleteExpiredAdminSessions(ctx, now); err != nil {
		return AuthSession{}, err
	}
	if err := s.store.SaveAdminSession(ctx, hashToken(token), hashToken(csrf), now, expiresAt); err != nil {
		return AuthSession{}, err
	}
	return AuthSession{Username: username, Token: token, CSRFToken: csrf, ExpiresAt: expiresAt, csrfHash: hashToken(csrf)}, nil
}

func validateCredentials(username, password string) (string, map[string]string) {
	username = strings.TrimSpace(username)
	fields := map[string]string{}
	if len(username) < 3 || len(username) > 64 {
		fields["username"] = "用户名长度应为 3–64 个字符"
	} else {
		for _, character := range username {
			if unicode.IsControl(character) || unicode.IsSpace(character) {
				fields["username"] = "用户名不能包含空白或控制字符"
				break
			}
		}
	}
	if len(password) < 12 || len(password) > 128 {
		fields["password"] = "密码长度应为 12–128 个字符"
	}
	return username, fields
}

func randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
