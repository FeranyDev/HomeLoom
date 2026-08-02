package tuya

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	tuyaapi "github.com/feranydev/homeloom/backend/internal/providers/tuya/api"
)

const sharingLoginSessionTTL = 5 * time.Minute

// SharingLoginStartRequest matches Home Assistant's first Tuya config-flow
// step. User Code is shown in the Tuya Smart or Smart Life app under Me →
// Settings → Account and Security.
type SharingLoginStartRequest struct {
	UserCode string `json:"userCode"`
}

type SharingLoginStartResult struct {
	State     string    `json:"state"`
	QRData    string    `json:"qrData"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type SharingLoginPollRequest struct {
	State string `json:"state"`
}

type SharingLoginPollResult struct {
	Status       string    `json:"status"`
	Message      string    `json:"message,omitempty"`
	AccessToken  string    `json:"accessToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	UID          string    `json:"uid,omitempty"`
	Endpoint     string    `json:"endpoint,omitempty"`
	TerminalID   string    `json:"terminalId,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
}

type sharingLoginSession struct {
	userCode  string
	qrToken   string
	state     string
	expiresAt time.Time
}

// SharingLoginService owns short-lived QR login sessions. User codes and QR
// tokens are never returned after the session is completed and are kept only
// in memory while a user is actively pairing the app.
type SharingLoginService struct {
	mu         sync.Mutex
	sessions   map[string]sharingLoginSession
	now        func() time.Time
	random     func([]byte) (int, error)
	httpClient func() *http.Client
}

func NewSharingLoginService() *SharingLoginService {
	return &SharingLoginService{sessions: make(map[string]sharingLoginSession), now: time.Now, random: rand.Read}
}

func (s *SharingLoginService) Start(ctx context.Context, request SharingLoginStartRequest) (SharingLoginStartResult, error) {
	userCode := strings.TrimSpace(request.UserCode)
	if userCode == "" {
		return SharingLoginStartResult{}, errors.New("Tuya User Code is required")
	}
	state, err := s.newState()
	if err != nil {
		return SharingLoginStartResult{}, err
	}
	query := url.Values{}
	query.Set("clientid", tuyaapi.HomeAssistantClientID)
	query.Set("usercode", userCode)
	query.Set("schema", tuyaapi.HomeAssistantSchema)
	response, err := s.do(ctx, http.MethodPost, "/v1.0/m/life/home-assistant/qrcode/tokens", query)
	if err != nil {
		return SharingLoginStartResult{}, err
	}
	var payload struct {
		Success bool            `json:"success"`
		Code    json.RawMessage `json:"code"`
		Msg     string          `json:"msg"`
		Result  struct {
			QRCode string `json:"qrcode"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		return SharingLoginStartResult{}, fmt.Errorf("decode Tuya QR login response: %w", err)
	}
	if !payload.Success || strings.TrimSpace(payload.Result.QRCode) == "" {
		code := strings.Trim(string(payload.Code), `"`)
		return SharingLoginStartResult{}, fmt.Errorf("Tuya QR login failed (%s): %s", code, strings.TrimSpace(payload.Msg))
	}
	now := s.currentTime()
	expiresAt := now.Add(sharingLoginSessionTTL)
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = make(map[string]sharingLoginSession)
	}
	s.cleanupLocked(now)
	s.sessions[state] = sharingLoginSession{userCode: userCode, qrToken: payload.Result.QRCode, state: state, expiresAt: expiresAt}
	s.mu.Unlock()
	return SharingLoginStartResult{State: state, QRData: "tuyaSmart--qrLogin?token=" + strings.TrimSpace(payload.Result.QRCode), ExpiresAt: expiresAt}, nil
}

func (s *SharingLoginService) Poll(ctx context.Context, request SharingLoginPollRequest) (SharingLoginPollResult, error) {
	state := strings.TrimSpace(request.State)
	if state == "" {
		return SharingLoginPollResult{}, errors.New("Tuya QR login state is required")
	}
	now := s.currentTime()
	s.mu.Lock()
	s.cleanupLocked(now)
	session, ok := s.sessions[state]
	s.mu.Unlock()
	if !ok || session.state != state {
		return SharingLoginPollResult{Status: "expired", Message: "Tuya QR login session expired; start again"}, nil
	}
	query := url.Values{}
	query.Set("clientid", tuyaapi.HomeAssistantClientID)
	query.Set("usercode", session.userCode)
	response, err := s.do(ctx, http.MethodGet, "/v1.0/m/life/home-assistant/qrcode/tokens/"+url.PathEscape(session.qrToken), query)
	if err != nil {
		return SharingLoginPollResult{}, err
	}
	var payload struct {
		Success bool            `json:"success"`
		Code    json.RawMessage `json:"code"`
		Msg     string          `json:"msg"`
		T       int64           `json:"t"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		return SharingLoginPollResult{}, fmt.Errorf("decode Tuya QR login result: %w", err)
	}
	if !payload.Success {
		// Tuya uses a negative/pending response until the app confirms the QR
		// code. Keep polling within the session TTL instead of making the user
		// restart after every pending response.
		return SharingLoginPollResult{Status: "pending", Message: strings.TrimSpace(payload.Msg)}, nil
	}
	var metadata struct {
		Endpoint      string `json:"endpoint"`
		TerminalID    string `json:"terminal_id"`
		TerminalIDAlt string `json:"terminalId"`
	}
	if err := json.Unmarshal(payload.Result, &metadata); err != nil {
		return SharingLoginPollResult{}, fmt.Errorf("decode Tuya QR login metadata: %w", err)
	}
	token, expiresAt, err := tuyaapi.DecodeSharingToken(payload.Result, payload.T)
	if err != nil {
		return SharingLoginPollResult{}, err
	}
	if strings.TrimSpace(metadata.Endpoint) == "" {
		return SharingLoginPollResult{}, errors.New("Tuya QR login did not return an API endpoint")
	}
	terminalID := strings.TrimSpace(metadata.TerminalID)
	if terminalID == "" {
		terminalID = strings.TrimSpace(metadata.TerminalIDAlt)
	}
	if terminalID == "" {
		return SharingLoginPollResult{}, errors.New("Tuya QR login did not return a terminal id")
	}
	s.mu.Lock()
	if _, stillActive := s.sessions[state]; !stillActive {
		s.mu.Unlock()
		return SharingLoginPollResult{Status: "expired", Message: "Tuya QR login session expired; start again"}, nil
	}
	delete(s.sessions, state)
	s.mu.Unlock()
	return SharingLoginPollResult{Status: "complete", AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, UID: token.UID, Endpoint: metadata.Endpoint, TerminalID: terminalID, ExpiresAt: expiresAt}, nil
}

func (s *SharingLoginService) QRData(state string) (string, bool) {
	state = strings.TrimSpace(state)
	if state == "" {
		return "", false
	}
	now := s.currentTime()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(now)
	session, ok := s.sessions[state]
	return "tuyaSmart--qrLogin?token=" + session.qrToken, ok && session.state == state
}

func (s *SharingLoginService) do(ctx context.Context, method, path string, query url.Values) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	base, err := url.Parse(tuyaapi.SharingLoginBaseURL)
	if err != nil {
		return nil, errors.New("Tuya QR login endpoint is invalid")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(path, "/")
	base.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, base.String(), nil)
	if err != nil {
		return nil, errors.New("create Tuya QR login request failed")
	}
	client := (*http.Client)(nil)
	if s.httpClient != nil {
		client = s.httpClient()
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("Tuya QR login request failed: %w", ctxErr)
		}
		return nil, errors.New("Tuya QR login request failed")
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read Tuya QR login response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Tuya QR login HTTP %d", response.StatusCode)
	}
	return payload, nil
}

func (s *SharingLoginService) newState() (string, error) {
	value := make([]byte, 24)
	random := s.random
	if random == nil {
		random = rand.Read
	}
	if _, err := random(value); err != nil {
		return "", errors.New("generate Tuya QR login state failed")
	}
	return hex.EncodeToString(value), nil
}

func (s *SharingLoginService) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *SharingLoginService) cleanupLocked(now time.Time) {
	for state, session := range s.sessions {
		if !now.Before(session.expiresAt) {
			delete(s.sessions, state)
		}
	}
}
