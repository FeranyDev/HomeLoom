package lan

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" // #nosec G501 -- Sonoff LAN derives its AES key with MD5.
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Codec implements the Sonoff LAN payload format. A DIY codec leaves JSON in
// clear text; a normal LAN codec uses AES-128-CBC with PKCS#7 padding and an
// AES key equal to MD5(devicekey).
type Codec struct {
	DeviceKey string
	DIY       bool
}

func NewCodec(deviceKey string, diy bool) Codec {
	return Codec{DeviceKey: deviceKey, DIY: diy}
}

// Encode returns the value suitable for the request or TXT data field. In
// encrypted mode the returned value is standard base64 ciphertext.
//
// The optional diy argument exists for callers that prefer a stateless helper:
// when omitted, DIY is false and the payload is encrypted.
func Encode(deviceKey string, plaintext []byte, iv []byte, diy ...bool) (string, error) {
	clearText := len(diy) > 0 && diy[0]
	return NewCodec(deviceKey, clearText).Encode(plaintext, iv)
}

// Decode accepts the base64 ciphertext used by encrypted Sonoff messages or
// clear JSON when diy is true.
func Decode(deviceKey string, encoded string, iv []byte, diy ...bool) ([]byte, error) {
	clearText := len(diy) > 0 && diy[0]
	return NewCodec(deviceKey, clearText).Decode(encoded, iv)
}

func (c Codec) Encode(plaintext, iv []byte) (string, error) {
	if c.DIY {
		return string(plaintext), nil
	}
	ciphertext, err := Encrypt(c.DeviceKey, plaintext, iv)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (c Codec) Decode(encoded string, iv []byte) ([]byte, error) {
	if c.DIY {
		return []byte(encoded), nil
	}
	decoded, err := decodeBase64(encoded)
	if err != nil {
		return nil, err
	}
	return Decrypt(c.DeviceKey, decoded, iv)
}

// Encrypt performs AES-128-CBC encryption with strict IV validation. The
// device key itself is never used directly as an AES key.
func Encrypt(deviceKey string, plaintext, iv []byte) ([]byte, error) {
	key := md5.Sum([]byte(deviceKey)) // #nosec G401 -- required by Sonoff LAN.
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create Sonoff AES cipher: %w", err)
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("Sonoff AES IV must be %d bytes, got %d", aes.BlockSize, len(iv))
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	return ciphertext, nil
}

// Decrypt performs AES-128-CBC decryption and rejects malformed PKCS#7
// padding rather than silently returning corrupted JSON.
func Decrypt(deviceKey string, ciphertext, iv []byte) ([]byte, error) {
	key := md5.Sum([]byte(deviceKey)) // #nosec G401 -- required by Sonoff LAN.
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create Sonoff AES cipher: %w", err)
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("Sonoff AES IV must be %d bytes, got %d", aes.BlockSize, len(iv))
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("Sonoff ciphertext is not a non-empty AES block sequence")
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
	return pkcs7Unpad(plaintext, aes.BlockSize)
}

func pkcs7Pad(input []byte, blockSize int) []byte {
	padding := blockSize - len(input)%blockSize
	output := make([]byte, len(input)+padding)
	copy(output, input)
	for index := len(input); index < len(output); index++ {
		output[index] = byte(padding)
	}
	return output
}

func pkcs7Unpad(input []byte, blockSize int) ([]byte, error) {
	if len(input) == 0 || len(input)%blockSize != 0 {
		return nil, errors.New("Sonoff plaintext is not a complete AES block sequence")
	}
	padding := int(input[len(input)-1])
	if padding == 0 || padding > blockSize || padding > len(input) {
		return nil, errors.New("invalid Sonoff PKCS#7 padding")
	}
	for _, value := range input[len(input)-padding:] {
		if int(value) != padding {
			return nil, errors.New("invalid Sonoff PKCS#7 padding")
		}
	}
	return append([]byte(nil), input[:len(input)-padding]...), nil
}

// ParseIV accepts the base64 wire representation used by zeroconf and also a
// raw 16-byte IV, which is convenient when constructing test requests.
func ParseIV(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("Sonoff AES IV is required")
	}
	if decoded, err := decodeBase64(value); err == nil && len(decoded) == aes.BlockSize {
		return decoded, nil
	}
	if len(value) == aes.BlockSize {
		return []byte(value), nil
	}
	return nil, errors.New("Sonoff AES IV must be base64 encoded or 16 raw bytes")
}

func EncodeIV(iv []byte) (string, error) {
	if len(iv) != aes.BlockSize {
		return "", fmt.Errorf("Sonoff AES IV must be %d bytes, got %d", aes.BlockSize, len(iv))
	}
	return base64.StdEncoding.EncodeToString(iv), nil
}

func randomIV() ([]byte, error) {
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("generate Sonoff AES IV: %w", err)
	}
	return iv, nil
}

func decodeBase64(value string) ([]byte, error) {
	value = string(bytes.TrimSpace([]byte(value)))
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid Sonoff base64 payload")
}
