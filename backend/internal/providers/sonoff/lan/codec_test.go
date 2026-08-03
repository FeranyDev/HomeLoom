package lan

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" // #nosec G501 -- test vector for the Sonoff-required derivation.
	"encoding/base64"
	"testing"
)

func TestEncodeDecodeUsesMD5DeviceKeyAndPKCS7(t *testing.T) {
	deviceKey := "device-key"
	plaintext := []byte(`{"switch":"on","brightness":0}`)
	iv := []byte("0123456789abcdef")

	encoded, err := Encode(deviceKey, plaintext, iv)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 ciphertext: %v", err)
	}
	key := md5.Sum([]byte(deviceKey)) // #nosec G401 -- matches the wire protocol.
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	expected := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(expected, padded)
	if !bytes.Equal(ciphertext, expected) {
		t.Fatalf("ciphertext does not use MD5(devicekey) AES key: got %x want %x", ciphertext, expected)
	}

	decoded, err := Decode(deviceKey, encoded, iv)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !bytes.Equal(decoded, plaintext) {
		t.Fatalf("decoded = %q, want %q", decoded, plaintext)
	}
}

func TestCodecDIYLeavesPayloadPlain(t *testing.T) {
	plaintext := []byte(`{"switch":"off"}`)
	encoded, err := NewCodec("unused", true).Encode(plaintext, nil)
	if err != nil || encoded != string(plaintext) {
		t.Fatalf("DIY Encode() = %q, %v", encoded, err)
	}
	decoded, err := NewCodec("unused", true).Decode(encoded, nil)
	if err != nil || !bytes.Equal(decoded, plaintext) {
		t.Fatalf("DIY Decode() = %q, %v", decoded, err)
	}
}

func TestDecodeRejectsInvalidIVAndPadding(t *testing.T) {
	if _, err := Encrypt("key", []byte("data"), []byte("short")); err == nil {
		t.Fatal("Encrypt() accepted a short IV")
	}
	iv := []byte("0123456789abcdef")
	ciphertext, err := Encrypt("key", []byte("data"), iv)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := Decrypt("key", ciphertext, iv); err == nil {
		t.Fatal("Decrypt() accepted invalid PKCS#7 padding")
	}
}
