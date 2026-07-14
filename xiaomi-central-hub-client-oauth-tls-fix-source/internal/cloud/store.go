package cloud

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AuthState contains the long-lived OAuth identity, account identity and token
// set. OAuthDeviceID is used only for Xiaomi OAuth. VirtualDID is the MQTT
// client ID and is also bound into the central-gateway certificate subject.
type AuthState struct {
	Version       int    `json:"version"`
	ClientID      string `json:"client_id"`
	Region        string `json:"region"`
	RedirectURL   string `json:"redirect_url"`
	OAuthUUID     string `json:"oauth_uuid"`
	OAuthDeviceID string `json:"oauth_device_id"`
	VirtualDID    string `json:"virtual_did"`
	UID           string `json:"uid"`
	Token         Token  `json:"token"`
}

// NewOAuthUUID returns the stable, 32-lowercase-hex identifier used in the
// OAuth device_id "ha.<uuid>". The value must be persisted and reused.
func NewOAuthUUID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate OAuth UUID: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// NewVirtualDID returns the decimal uint64 identity used by the local MIPS
// MQTT client. It must be persisted because the certificate is bound to it.
func NewVirtualDID() (string, error) {
	var raw [8]byte
	for {
		if _, err := rand.Read(raw[:]); err != nil {
			return "", fmt.Errorf("generate virtual DID: %w", err)
		}
		value := binary.BigEndian.Uint64(raw[:])
		if value != 0 {
			return strconv.FormatUint(value, 10), nil
		}
	}
}

func OAuthDeviceID(uuid string) string {
	return "ha." + strings.TrimSpace(uuid)
}

func LoadAuthState(path string) (AuthState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AuthState{}, fmt.Errorf("read auth state: %w", err)
	}
	var state AuthState
	if err := json.Unmarshal(data, &state); err != nil {
		return AuthState{}, fmt.Errorf("parse auth state: %w", err)
	}
	if err := state.Validate(); err != nil {
		return AuthState{}, err
	}
	return state, nil
}

func (s AuthState) Validate() error {
	if s.ClientID == "" || s.Region == "" || s.RedirectURL == "" {
		return errors.New("auth state is missing OAuth application fields")
	}
	if len(s.OAuthUUID) != 32 {
		return errors.New("auth state has an invalid OAuth UUID")
	}
	if s.OAuthDeviceID != OAuthDeviceID(s.OAuthUUID) {
		return errors.New("auth state OAuth device ID does not match its UUID")
	}
	if _, err := strconv.ParseUint(s.VirtualDID, 10, 64); err != nil || s.VirtualDID == "0" {
		return errors.New("auth state has an invalid virtual DID")
	}
	if strings.TrimSpace(s.UID) == "" {
		return errors.New("auth state is missing the Xiaomi account UID")
	}
	if s.Token.AccessToken == "" || s.Token.RefreshToken == "" {
		return errors.New("auth state is missing OAuth tokens")
	}
	return nil
}

func SaveAuthState(path string, state AuthState) error {
	if state.Version == 0 {
		state.Version = 2
	}
	if err := state.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode auth state: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create auth state directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write auth state: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("secure auth state permissions: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace auth state: %w", err)
	}
	return nil
}
