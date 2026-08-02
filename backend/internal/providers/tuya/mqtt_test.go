package tuya

import (
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDecodeMQTTMessageVerifiesAndDecryptsEncryptedPayload(t *testing.T) {
	accessKey := "0123456789abcdefghijklmnop"
	body := []byte(`{"bizCode":"device","eventType":"dp_report","data":{"devId":"device-1","status":[{"code":"switch","value":true}]}}`)
	ciphertext := encryptTuyaTestPayload(t, body, accessKey)
	payload := base64.StdEncoding.EncodeToString(ciphertext)
	version, encryptType, timestamp := "1.0", "aes_ecb", int64(1588918073598)
	signText := fmt.Sprintf("encryptPayload=%s||encryptType=%s||t=%d||v=%s||%s", payload, encryptType, timestamp, version, accessKey)
	digest := md5.Sum([]byte(signText))
	envelope, err := json.Marshal(map[string]any{
		"encryptPayload": payload,
		"encryptType":    encryptType,
		"t":              timestamp,
		"v":              version,
		"sign":           hex.EncodeToString(digest[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := DecodeMQTTMessage(envelope, accessKey)
	if err != nil {
		t.Fatal(err)
	}
	if event.DeviceID != "device-1" || event.EventType != "dp_report" || len(event.Status) != 1 || event.Status[0].Code != "switch" || event.Status[0].Value != true {
		t.Fatalf("event=%#v", event)
	}
	if strings.Contains(string(envelope), string(body)) {
		t.Fatal("encrypted envelope exposed plaintext body")
	}

	bad := append([]byte(nil), envelope...)
	bad[len(bad)-3] ^= 1
	if _, err := DecodeMQTTMessage(bad, accessKey); err == nil {
		t.Fatal("accepted tampered MQTT message")
	}
}

func encryptTuyaTestPayload(t *testing.T, plaintext []byte, accessKey string) []byte {
	t.Helper()
	key, err := middleAESKey(accessKey)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	padding := block.BlockSize() - len(plaintext)%block.BlockSize()
	plaintext = append(append([]byte(nil), plaintext...), make([]byte, padding)...)
	for index := len(plaintext) - padding; index < len(plaintext); index++ {
		plaintext[index] = byte(padding)
	}
	ciphertext := make([]byte, len(plaintext))
	for offset := 0; offset < len(plaintext); offset += block.BlockSize() {
		block.Encrypt(ciphertext[offset:offset+block.BlockSize()], plaintext[offset:offset+block.BlockSize()])
	}
	return ciphertext
}
