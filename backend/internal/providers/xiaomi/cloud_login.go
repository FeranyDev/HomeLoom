package xiaomi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	cloudLoginChallengeTTL = 10 * time.Minute
	cloudLoginMaxAttempts  = 5
	cloudLoginMaxPending   = 128
)

type CloudLoginStartRequest struct {
	Region            string `json:"region"`
	Username          string `json:"username"`
	Password          string `json:"password"`
	RequestTimeoutSec int    `json:"requestTimeoutSeconds,omitempty"`
}

type CloudLoginVerifyRequest struct {
	ChallengeID string `json:"challengeId"`
	Code        string `json:"code"`
}

type CloudLoginResult struct {
	Status          string     `json:"status"`
	ChallengeID     string     `json:"challengeId,omitempty"`
	VerificationURL string     `json:"verificationUrl,omitempty"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	UserID          string     `json:"userId,omitempty"`
	Ssecurity       string     `json:"ssecurity,omitempty"`
	ServiceToken    string     `json:"serviceToken,omitempty"`
}

type cloudLoginChallenge struct {
	mu              sync.Mutex
	client          *httpMiotCloudClient
	verificationURL string
	expiresAt       time.Time
	attempts        int
}

type CloudLoginService struct {
	mu         sync.Mutex
	challenges map[string]*cloudLoginChallenge
	now        func() time.Time
	newClient  func(CloudConfig) *httpMiotCloudClient
}

func NewCloudLoginService() *CloudLoginService {
	return &CloudLoginService{
		challenges: make(map[string]*cloudLoginChallenge),
		now:        time.Now,
		newClient:  newHTTPMiotCloudClient,
	}
}

func (s *CloudLoginService) Start(ctx context.Context, request CloudLoginStartRequest) (CloudLoginResult, error) {
	config := CloudConfig{
		Region:            request.Region,
		Username:          request.Username,
		Password:          request.Password,
		RequestTimeoutSec: request.RequestTimeoutSec,
		Devices:           []DeviceConfig{},
	}
	config.applyDefaults()
	if strings.TrimSpace(config.Username) == "" {
		return CloudLoginResult{}, errors.New("Xiaomi account is required")
	}
	if config.Password == "" || config.Password == "********" {
		return CloudLoginResult{}, errors.New("a current Xiaomi account password is required")
	}
	if err := config.validate(); err != nil {
		return CloudLoginResult{}, err
	}
	client := s.newClient(config)
	if err := client.Login(ctx); err != nil {
		var verification *IdentityVerificationRequiredError
		if !errors.As(err, &verification) {
			return CloudLoginResult{}, err
		}
		id, idErr := randomCloudLoginID()
		if idErr != nil {
			return CloudLoginResult{}, idErr
		}
		now := s.now()
		challenge := &cloudLoginChallenge{client: client, verificationURL: verification.URL, expiresAt: now.Add(cloudLoginChallengeTTL)}
		s.mu.Lock()
		s.cleanupLocked(now)
		if len(s.challenges) >= cloudLoginMaxPending {
			s.mu.Unlock()
			client.config.Password = ""
			return CloudLoginResult{}, errors.New("too many pending Xiaomi identity verifications; try again later")
		}
		s.challenges[id] = challenge
		s.mu.Unlock()
		return CloudLoginResult{Status: "verification_required", ChallengeID: id, VerificationURL: verification.URL, ExpiresAt: &challenge.expiresAt}, nil
	}
	return cloudLoginSessionResult(client)
}

func (s *CloudLoginService) Verify(ctx context.Context, request CloudLoginVerifyRequest) (CloudLoginResult, error) {
	id, code := strings.TrimSpace(request.ChallengeID), strings.TrimSpace(request.Code)
	if id == "" || code == "" {
		return CloudLoginResult{}, errors.New("challengeId and verification code are required")
	}
	now := s.now()
	s.mu.Lock()
	s.cleanupLocked(now)
	challenge := s.challenges[id]
	s.mu.Unlock()
	if challenge == nil {
		return CloudLoginResult{}, errors.New("Xiaomi identity verification challenge is missing or expired; start login again")
	}
	challenge.mu.Lock()
	defer challenge.mu.Unlock()
	s.mu.Lock()
	active := s.challenges[id] == challenge
	s.mu.Unlock()
	if !active {
		return CloudLoginResult{}, errors.New("Xiaomi identity verification challenge is missing or expired; start login again")
	}
	if !s.now().Before(challenge.expiresAt) {
		s.delete(id, challenge)
		return CloudLoginResult{}, errors.New("Xiaomi identity verification challenge expired; start login again")
	}
	challenge.attempts++
	if challenge.attempts > cloudLoginMaxAttempts {
		s.delete(id, challenge)
		return CloudLoginResult{}, errors.New("too many Xiaomi identity verification attempts; start login again")
	}
	if err := challenge.client.VerifyIdentity(ctx, challenge.verificationURL, code); err != nil {
		if challenge.attempts >= cloudLoginMaxAttempts {
			s.delete(id, challenge)
		}
		return CloudLoginResult{}, err
	}
	result, err := cloudLoginSessionResult(challenge.client)
	s.delete(id, challenge)
	return result, err
}

func cloudLoginSessionResult(client *httpMiotCloudClient) (CloudLoginResult, error) {
	userID, ssecurity, serviceToken := client.session()
	if userID == "" || ssecurity == "" || serviceToken == "" {
		return CloudLoginResult{}, errors.New("Xiaomi cloud login completed without a full session")
	}
	return CloudLoginResult{Status: "verified", UserID: userID, Ssecurity: ssecurity, ServiceToken: serviceToken}, nil
}

func (s *CloudLoginService) cleanupLocked(now time.Time) {
	for id, challenge := range s.challenges {
		if !now.Before(challenge.expiresAt) {
			delete(s.challenges, id)
		}
	}
}

func (s *CloudLoginService) delete(id string, expected *cloudLoginChallenge) {
	s.mu.Lock()
	if s.challenges[id] == expected {
		expected.client.config.Password = ""
		delete(s.challenges, id)
	}
	s.mu.Unlock()
}

func randomCloudLoginID() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate Xiaomi login challenge: %w", err)
	}
	return hex.EncodeToString(value), nil
}
