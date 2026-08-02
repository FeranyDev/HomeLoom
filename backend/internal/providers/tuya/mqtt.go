package tuya

import (
	"bytes"
	"crypto/aes"
	"crypto/md5" // #nosec G501 -- Tuya's documented message authentication uses MD5.
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MQTTEvent is the provider-neutral result of decoding a Tuya cloud status
// message. It is also useful for replay tests and diagnostic tooling.
type MQTTEvent struct {
	DeviceID   string
	ProductID  string
	EventType  string
	Name       string
	Online     *bool
	Status     []DPStatus
	ObservedAt time.Time
}

type mqttEnvelope struct {
	EncryptPayload string          `json:"encryptPayload"`
	Sign           string          `json:"sign"`
	EncryptType    string          `json:"encryptType"`
	Version        string          `json:"v"`
	Timestamp      int64           `json:"t"`
	Protocol       json.RawMessage `json:"protocol"`
	Data           json.RawMessage `json:"data"`
	BizCode        string          `json:"bizCode"`
	EventType      string          `json:"eventType"`
}

// DecodeMQTTMessage verifies and decrypts a Tuya legacy cloud message. Tuya
// documents AES-ECB with the middle 16 characters of the Access Secret for
// this message format. Plain JSON is accepted as a useful compatibility path
// for test brokers and newer deployments that already terminate encryption.
func DecodeMQTTMessage(payload []byte, accessKey string) (MQTTEvent, error) {
	var envelope mqttEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return MQTTEvent{}, fmt.Errorf("decode tuya mqtt envelope: %w", err)
	}
	body := envelope.Data
	if envelope.EncryptPayload != "" {
		if err := verifyMQTTSignature(envelope, accessKey); err != nil {
			return MQTTEvent{}, err
		}
		decoded, err := base64.StdEncoding.DecodeString(envelope.EncryptPayload)
		if err != nil {
			return MQTTEvent{}, fmt.Errorf("decode tuya mqtt payload: %w", err)
		}
		body, err = decryptTuyaPayload(decoded, accessKey)
		if err != nil {
			return MQTTEvent{}, err
		}
	}
	if len(body) == 0 {
		body = payload
	}
	var message struct {
		BizCode    string          `json:"bizCode"`
		EventType  string          `json:"eventType"`
		Data       json.RawMessage `json:"data"`
		DevID      string          `json:"devId"`
		ProductID  string          `json:"productId"`
		DeviceName string          `json:"deviceName"`
		Status     []struct {
			Code  string          `json:"code"`
			Value json.RawMessage `json:"value"`
			Time  int64           `json:"t"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &message); err != nil {
		return MQTTEvent{}, fmt.Errorf("decode tuya mqtt body: %w", err)
	}
	if len(message.Data) > 0 && string(message.Data) != "null" {
		var nested struct {
			DevID      string `json:"devId"`
			ProductID  string `json:"productId"`
			DeviceName string `json:"deviceName"`
			Status     []struct {
				Code  string          `json:"code"`
				Value json.RawMessage `json:"value"`
				Time  int64           `json:"t"`
			} `json:"status"`
		}
		if err := json.Unmarshal(message.Data, &nested); err == nil {
			if nested.DevID != "" {
				message.DevID = nested.DevID
			}
			if nested.ProductID != "" {
				message.ProductID = nested.ProductID
			}
			if nested.DeviceName != "" {
				message.DeviceName = nested.DeviceName
			}
			if len(nested.Status) > 0 {
				message.Status = nested.Status
			}
		}
	}
	result := MQTTEvent{DeviceID: message.DevID, ProductID: message.ProductID, EventType: message.EventType, Name: message.DeviceName}
	if result.EventType == "" {
		result.EventType = message.BizCode
	}
	for _, item := range message.Status {
		var value any
		if err := json.Unmarshal(item.Value, &value); err != nil {
			return MQTTEvent{}, fmt.Errorf("decode tuya mqtt status %q: %w", item.Code, err)
		}
		observedAt := time.Time{}
		if item.Time > 0 {
			observedAt = time.UnixMilli(item.Time).UTC()
			if observedAt.After(result.ObservedAt) {
				result.ObservedAt = observedAt
			}
		}
		result.Status = append(result.Status, TuyaStatus{Code: item.Code, Value: value})
	}
	if result.EventType == "online" {
		value := true
		result.Online = &value
	} else if result.EventType == "offline" {
		value := false
		result.Online = &value
	}
	if result.DeviceID == "" {
		return MQTTEvent{}, errors.New("tuya mqtt message has no device id")
	}
	if result.ObservedAt.IsZero() {
		result.ObservedAt = time.Now().UTC()
	}
	return result, nil
}

func verifyMQTTSignature(envelope mqttEnvelope, accessKey string) error {
	if envelope.Sign == "" {
		return errors.New("tuya mqtt message signature is missing")
	}
	if strings.TrimSpace(accessKey) == "" {
		return errors.New("tuya mqtt access key is required for encrypted messages")
	}
	fields := map[string]string{
		"encryptPayload": envelope.EncryptPayload,
		"encryptType":    envelope.EncryptType,
		"t":              strconv.FormatInt(envelope.Timestamp, 10),
		"v":              envelope.Version,
	}
	keys := make([]string, 0, len(fields))
	for key, value := range fields {
		if value != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+fields[key])
	}
	message := strings.Join(parts, "||") + "||" + accessKey
	digest := md5.Sum([]byte(message)) // #nosec G401 -- protocol mandated.
	expected := hex.EncodeToString(digest[:])
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(expected)), []byte(strings.ToLower(strings.TrimSpace(envelope.Sign)))) != 1 {
		return errors.New("tuya mqtt message signature is invalid")
	}
	return nil
}

func decryptTuyaPayload(payload []byte, accessKey string) ([]byte, error) {
	key, err := middleAESKey(accessKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create tuya mqtt cipher: %w", err)
	}
	if len(payload) == 0 || len(payload)%block.BlockSize() != 0 {
		return nil, errors.New("tuya mqtt ciphertext is not AES block aligned")
	}
	plain := make([]byte, len(payload))
	for offset := 0; offset < len(payload); offset += block.BlockSize() {
		block.Decrypt(plain[offset:offset+block.BlockSize()], payload[offset:offset+block.BlockSize()])
	}
	plain, err = unpadPKCS7(plain, block.BlockSize())
	if err != nil {
		return nil, fmt.Errorf("unpad tuya mqtt payload: %w", err)
	}
	return plain, nil
}

func middleAESKey(accessKey string) ([]byte, error) {
	accessKey = strings.TrimSpace(accessKey)
	if len(accessKey) < aes.BlockSize {
		return nil, errors.New("tuya mqtt access key must contain at least 16 characters")
	}
	start := (len(accessKey) - aes.BlockSize) / 2
	return []byte(accessKey[start : start+aes.BlockSize]), nil
}

func unpadPKCS7(value []byte, size int) ([]byte, error) {
	if len(value) == 0 || size <= 0 || len(value)%size != 0 {
		return nil, errors.New("invalid PKCS#7 payload")
	}
	padding := int(value[len(value)-1])
	if padding == 0 || padding > size || padding > len(value) {
		return nil, errors.New("invalid PKCS#7 padding")
	}
	if !bytes.Equal(value[len(value)-padding:], bytes.Repeat([]byte{byte(padding)}, padding)) {
		return nil, errors.New("invalid PKCS#7 padding")
	}
	return value[:len(value)-padding], nil
}
