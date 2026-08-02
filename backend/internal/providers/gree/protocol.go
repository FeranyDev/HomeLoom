package gree

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	genericGreeDeviceKey    = "a3K8Bx%2r8Y7#xDh"
	genericGreeDeviceKeyGCM = "{yxAHAY_Lm6pbC/<"
	maximumGreePacket       = 64 * 1024
)

var greeGCMNonce = [...]byte{0x54, 0x40, 0x78, 0x44, 0x49, 0x67, 0x5a, 0x51, 0x6c, 0x5e, 0x63, 0x13}

const greeGCMAAD = "qualcomm-test"

// statusColumns follows the fields used by Gree Wi-Fi modules. TemSen and
// DwatSen are optional on the appliance; unsupported optional fields are
// simply absent from the returned values on affected firmware.
var statusColumns = []string{
	"Pow", "Mod", "SetTem", "WdSpd", "Air", "Blo", "Health", "SwhSlp", "Lig",
	"SwingLfRig", "SwUpDn", "Quiet", "Tur", "StHt", "TemUn", "HeatCoolType", "TemRec", "SvSt", "SlpMod",
	"TemSen", "DwatSen", "OutEnvTem", "AntiDirectBlow", "LigSen", "ErrCode",
}

// Transport is the narrow network boundary used by Provider. Keeping it
// injectable makes protocol and Provider tests deterministic without copying
// the production UDP implementation.
type Transport interface {
	Exchange(context.Context, string, int, []byte) ([]byte, error)
}

type udpTransport struct {
	timeout time.Duration
}

func newUDPTransport(timeout time.Duration) Transport {
	return &udpTransport{timeout: timeout}
}

func (t *udpTransport) Exchange(ctx context.Context, host string, port int, payload []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	remote, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("resolve Gree UDP endpoint: %w", err)
	}
	conn, err := net.DialUDP("udp4", nil, remote)
	if err != nil {
		return nil, fmt.Errorf("dial Gree UDP endpoint: %w", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(t.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set Gree UDP deadline: %w", err)
	}
	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("send Gree UDP packet: %w", err)
	}
	response := make([]byte, maximumGreePacket)
	count, err := conn.Read(response)
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("receive Gree UDP packet: %w", err)
	}
	return append([]byte(nil), response[:count]...), nil
}

func newAESBlock(key []byte) (cipher.Block, error) {
	if len(key) != aes.BlockSize {
		return nil, fmt.Errorf("Gree v1 AES key must be 16 bytes, got %d", len(key))
	}
	return aes.NewCipher(key)
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	return append(append([]byte(nil), data...), bytes.Repeat([]byte{byte(padding)}, padding)...)
}

func pkcs7Unpad(data []byte, blockSize int) []byte {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return bytes.TrimRight(data, "\x00\x0f")
	}
	padding := int(data[len(data)-1])
	if padding < 1 || padding > blockSize || padding > len(data) {
		return bytes.TrimRight(data, "\x00\x0f")
	}
	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return bytes.TrimRight(data, "\x00\x0f")
		}
	}
	return data[:len(data)-padding]
}

func encryptPack(key []byte, plaintext []byte) (string, error) {
	block, err := newAESBlock(key)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad(plaintext, block.BlockSize())
	ciphertext := make([]byte, len(padded))
	for offset := 0; offset < len(padded); offset += block.BlockSize() {
		block.Encrypt(ciphertext[offset:offset+block.BlockSize()], padded[offset:offset+block.BlockSize()])
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptPack(key []byte, encoded string) ([]byte, error) {
	block, err := newAESBlock(key)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode Gree pack: %w", err)
	}
	if len(ciphertext) == 0 || len(ciphertext)%block.BlockSize() != 0 {
		return nil, errors.New("Gree encrypted pack is not block aligned")
	}
	plaintext := make([]byte, len(ciphertext))
	for offset := 0; offset < len(ciphertext); offset += block.BlockSize() {
		block.Decrypt(plaintext[offset:offset+block.BlockSize()], ciphertext[offset:offset+block.BlockSize()])
	}
	return pkcs7Unpad(plaintext, block.BlockSize()), nil
}

func newAESGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != aes.BlockSize {
		return nil, fmt.Errorf("Gree v2 AES key must be 16 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("Gree v2 AES-GCM key: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create Gree v2 AES-GCM: %w", err)
	}
	if gcm.NonceSize() != len(greeGCMNonce) {
		return nil, fmt.Errorf("Gree v2 AES-GCM nonce size is %d, want %d", gcm.NonceSize(), len(greeGCMNonce))
	}
	return gcm, nil
}

func encryptGCM(key []byte, plaintext []byte) (pack, tag string, err error) {
	gcm, err := newAESGCM(key)
	if err != nil {
		return "", "", err
	}
	sealed := gcm.Seal(nil, greeGCMNonce[:], plaintext, []byte(greeGCMAAD))
	ciphertext := sealed[:len(sealed)-gcm.Overhead()]
	tagBytes := sealed[len(sealed)-gcm.Overhead():]
	return base64.StdEncoding.EncodeToString(ciphertext), base64.StdEncoding.EncodeToString(tagBytes), nil
}

func decryptGCM(key []byte, encodedPack, encodedTag string) ([]byte, error) {
	gcm, err := newAESGCM(key)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(encodedTag) == "" {
		return nil, errors.New("Gree v2 envelope has no authentication tag")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encodedPack)
	if err != nil {
		return nil, fmt.Errorf("decode Gree v2 pack: %w", err)
	}
	tag, err := base64.StdEncoding.DecodeString(encodedTag)
	if err != nil {
		return nil, fmt.Errorf("decode Gree v2 tag: %w", err)
	}
	if len(tag) != gcm.Overhead() {
		return nil, fmt.Errorf("Gree v2 authentication tag has length %d, want %d", len(tag), gcm.Overhead())
	}
	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)
	plaintext, err := gcm.Open(nil, greeGCMNonce[:], sealed, []byte(greeGCMAAD))
	if err != nil {
		return nil, fmt.Errorf("verify Gree v2 authentication tag: %w", err)
	}
	return plaintext, nil
}

type greeEnvelope struct {
	CID  string `json:"cid"`
	I    int    `json:"i"`
	Pack string `json:"pack"`
	T    string `json:"t"`
	TCID string `json:"tcid"`
	UID  int64  `json:"uid"`
	Tag  string `json:"tag,omitempty"`
}

type bindRequest struct {
	CID string `json:"cid,omitempty"`
	MAC string `json:"mac"`
	T   string `json:"t"`
	UID int    `json:"uid"`
}

type statusRequest struct {
	Cols []string `json:"cols"`
	MAC  string   `json:"mac"`
	T    string   `json:"t"`
}

type commandRequest struct {
	Opt []string `json:"opt"`
	P   []any    `json:"p"`
	T   string   `json:"t"`
	Sub string   `json:"sub,omitempty"`
}

func protocolEncryptionVersion(versions []int) (int, error) {
	if len(versions) > 1 {
		return 0, errors.New("Gree encryption version must be specified at most once")
	}
	if len(versions) == 0 || versions[0] == 0 {
		return 1, nil
	}
	if versions[0] != 1 && versions[0] != 2 {
		return 0, fmt.Errorf("unsupported Gree encryption version %d", versions[0])
	}
	return versions[0], nil
}

func makeEnvelope(key []byte, sequence int, tcid string, uid int64, inner any, versions ...int) ([]byte, error) {
	version, err := protocolEncryptionVersion(versions)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(inner)
	if err != nil {
		return nil, fmt.Errorf("encode Gree inner packet: %w", err)
	}
	envelope := greeEnvelope{CID: "app", I: sequence, T: "pack", TCID: tcid, UID: uid}
	if version == 1 {
		envelope.Pack, err = encryptPack(key, plain)
	} else {
		envelope.Pack, envelope.Tag, err = encryptGCM(key, plain)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope)
}

func buildBindPacket(mac string, versions ...int) ([]byte, error) {
	version, err := protocolEncryptionVersion(versions)
	if err != nil {
		return nil, err
	}
	inner := bindRequest{MAC: mac, T: "bind", UID: 0}
	key := []byte(genericGreeDeviceKey)
	if version == 2 {
		inner.CID = mac
		key = []byte(genericGreeDeviceKeyGCM)
	}
	return makeEnvelope(key, 1, mac, 0, inner, version)
}

func buildStatusPacket(key []byte, subMAC, mainMAC string, uid int64, versions ...int) ([]byte, error) {
	return makeEnvelope(key, 0, mainMAC, uid, statusRequest{Cols: append([]string(nil), statusColumns...), MAC: subMAC, T: "status"}, versions...)
}

func buildCommandPacket(key []byte, subMAC, mainMAC string, uid int64, options []string, values []any, versions ...int) ([]byte, error) {
	if len(options) == 0 || len(options) != len(values) {
		return nil, errors.New("Gree command options and values must have the same non-zero length")
	}
	return makeEnvelope(key, 0, mainMAC, uid, commandRequest{Opt: append([]string(nil), options...), P: append([]any(nil), values...), T: "cmd", Sub: subMAC}, versions...)
}

func decodeEnvelope(payload []byte, key []byte, versions ...int) (map[string]any, error) {
	version, err := protocolEncryptionVersion(versions)
	if err != nil {
		return nil, err
	}
	var envelope greeEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode Gree envelope: %w", err)
	}
	if envelope.Pack == "" {
		return nil, errors.New("Gree envelope has no pack")
	}
	var plain []byte
	if version == 1 {
		plain, err = decryptPack(key, envelope.Pack)
	} else {
		plain, err = decryptGCM(key, envelope.Pack, envelope.Tag)
	}
	if err != nil {
		return nil, err
	}
	var inner map[string]any
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.UseNumber()
	if err := decoder.Decode(&inner); err != nil {
		return nil, fmt.Errorf("decode Gree inner packet: %w", err)
	}
	if err := validateResponseCode(inner); err != nil {
		return nil, err
	}
	return inner, nil
}

func validateResponseCode(inner map[string]any) error {
	if value, ok := inner["r"]; ok {
		code, valid := numberFromAny(value)
		if valid && code != 0 && code != 200 {
			return fmt.Errorf("Gree device rejected request with response code %d", int(code))
		}
	}
	if value, ok := inner["ret"]; ok {
		code, valid := numberFromAny(value)
		if valid && code != 0 && code != 200 {
			return fmt.Errorf("Gree device returned error %d", int(code))
		}
	}
	return nil
}

func parseBindResponse(payload []byte, versions ...int) ([]byte, error) {
	version, err := protocolEncryptionVersion(versions)
	if err != nil {
		return nil, err
	}
	genericKey := []byte(genericGreeDeviceKey)
	if version == 2 {
		genericKey = []byte(genericGreeDeviceKeyGCM)
	}
	inner, err := decodeEnvelope(payload, genericKey, version)
	if err != nil {
		return nil, err
	}
	keyText, ok := inner["key"].(string)
	if !ok || strings.TrimSpace(keyText) == "" {
		return nil, errors.New("Gree bind response did not contain a device key")
	}
	deviceKey := []byte(strings.TrimSpace(keyText))
	if version == 1 {
		_, err = newAESBlock(deviceKey)
	} else {
		_, err = newAESGCM(deviceKey)
	}
	if err != nil {
		return nil, fmt.Errorf("Gree bind response key: %w", err)
	}
	return deviceKey, nil
}

func parseStatusResponse(payload []byte, key []byte, versions ...int) (map[string]any, error) {
	inner, err := decodeEnvelope(payload, key, versions...)
	if err != nil {
		return nil, err
	}
	return statusValues(inner)
}

func statusValues(inner map[string]any) (map[string]any, error) {
	result := make(map[string]any)
	if raw, ok := inner["dat"]; ok {
		switch value := raw.(type) {
		case map[string]any:
			for key, item := range value {
				result[key] = item
			}
		case []any:
			if len(value) > 0 {
				if object, ok := value[0].(map[string]any); ok {
					for key, item := range object {
						result[key] = item
					}
				} else if columns, ok := stringSlice(inner["cols"]); ok {
					for index, column := range columns {
						if index < len(value) {
							result[column] = value[index]
						}
					}
				}
			}
		case json.Number, float64, float32, int, int64, int32, string, bool:
			if columns, ok := stringSlice(inner["cols"]); ok && len(columns) == 1 {
				result[columns[0]] = value
			}
		}
	}
	if len(result) == 0 {
		// A few modules return the columns directly instead of wrapping them in
		// dat. Preserve that shape as a useful fallback for status polling.
		for key, value := range inner {
			if key != "t" && key != "r" && key != "ret" && key != "cols" {
				result[key] = value
			}
		}
	}
	if len(result) == 0 {
		return nil, errors.New("Gree status response contained no values")
	}
	return result, nil
}

func stringSlice(value any) ([]string, bool) {
	if items, ok := value.([]string); ok {
		return append([]string(nil), items...), true
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		result = append(result, text)
	}
	return result, true
}

func numberFromAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		result, err := typed.Float64()
		return result, err == nil
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case string:
		result, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return result, err == nil
	default:
		return 0, false
	}
}

func integerFromAny(value any) (int, bool) {
	result, ok := numberFromAny(value)
	return int(result), ok
}

func boolFromAny(value any) (bool, bool) {
	if result, ok := value.(bool); ok {
		return result, true
	}
	number, ok := numberFromAny(value)
	return number != 0, ok
}
