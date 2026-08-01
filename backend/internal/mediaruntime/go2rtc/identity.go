package go2rtc

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const identityFilename = "homekit-identity.json"

type publisherIdentity struct {
	SchemaVersion int    `json:"schemaVersion"`
	PIN           string `json:"pin"`
	DeviceID      string `json:"deviceId"`
	DevicePrivate string `json:"devicePrivate"`
}

func ensureIdentity(directory, pinOverride, deviceIDOverride, privateOverride string) (publisherIdentity, error) {
	path := filepath.Join(directory, identityFilename)
	identity, found, err := readIdentity(path)
	if err != nil {
		return publisherIdentity{}, err
	}
	changed := false
	if !found {
		identity, err = newIdentity()
		if err != nil {
			return publisherIdentity{}, err
		}
		changed = true
	}
	if pinOverride != "" {
		if !validHAPPIN(pinOverride) {
			return publisherIdentity{}, errors.New("invalid HomeKit pairing PIN")
		}
		if identity.PIN != pinOverride {
			identity.PIN = pinOverride
			changed = true
		}
	}
	if deviceIDOverride != "" && identity.DeviceID != deviceIDOverride {
		identity.DeviceID = deviceIDOverride
		changed = true
	}
	if privateOverride != "" && identity.DevicePrivate != privateOverride {
		identity.DevicePrivate = privateOverride
		changed = true
	}
	if !validIdentity(identity) {
		return publisherIdentity{}, errors.New("invalid protected HomeKit identity")
	}
	if changed {
		if err := writeIdentity(path, identity); err != nil {
			return publisherIdentity{}, err
		}
	} else if err := os.Chmod(path, 0o600); err != nil {
		return publisherIdentity{}, err
	}
	return identity, nil
}

func readIdentity(path string) (publisherIdentity, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return publisherIdentity{}, false, nil
	}
	if err != nil {
		return publisherIdentity{}, false, fmt.Errorf("inspect HomeKit identity: %w", err)
	}
	if !info.Mode().IsRegular() {
		return publisherIdentity{}, false, errors.New("HomeKit identity path is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return publisherIdentity{}, false, fmt.Errorf("read HomeKit identity: %w", err)
	}
	var identity publisherIdentity
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&identity); err != nil {
		return publisherIdentity{}, false, errors.New("invalid HomeKit identity")
	}
	return identity, true, nil
}

func writeIdentity(path string, identity publisherIdentity) error {
	data, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write HomeKit identity: %w", err)
	}
	return os.Chmod(path, 0o600)
}

func newIdentity() (publisherIdentity, error) {
	pin, err := randomPIN()
	if err != nil {
		return publisherIdentity{}, err
	}
	mac := make([]byte, 6)
	if _, err := rand.Read(mac); err != nil {
		return publisherIdentity{}, err
	}
	mac[0] = (mac[0] | 0x02) & 0xFE
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return publisherIdentity{}, err
	}
	return publisherIdentity{SchemaVersion: 1, PIN: pin, DeviceID: fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]), DevicePrivate: hex.EncodeToString(private)}, nil
}

func randomPIN() (string, error) {
	for {
		value, err := rand.Int(rand.Reader, big.NewInt(90_000_000))
		if err != nil {
			return "", err
		}
		digits := fmt.Sprintf("%08d", value.Int64()+10_000_000)
		pin := digits[:3] + "-" + digits[3:5] + "-" + digits[5:]
		if validHAPPIN(pin) {
			return pin, nil
		}
	}
}

var hapPINPattern = regexp.MustCompile(`^[0-9]{3}-[0-9]{2}-[0-9]{3}$`)

func validHAPPIN(pin string) bool {
	if !hapPINPattern.MatchString(pin) {
		return false
	}
	digits := strings.ReplaceAll(pin, "-", "")
	if digits == "00000000" || digits == "12345678" || digits == "87654321" {
		return false
	}
	for _, digit := range digits[1:] {
		if digit != rune(digits[0]) {
			return true
		}
	}
	return false
}
func validIdentity(value publisherIdentity) bool {
	if value.SchemaVersion != 1 || !validHAPPIN(value.PIN) || !regexp.MustCompile(`^[0-9A-F]{2}(:[0-9A-F]{2}){5}$`).MatchString(value.DeviceID) {
		return false
	}
	private, err := hex.DecodeString(value.DevicePrivate)
	return err == nil && len(private) == ed25519.PrivateKeySize
}
