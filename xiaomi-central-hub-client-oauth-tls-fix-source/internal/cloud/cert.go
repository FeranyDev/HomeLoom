package cloud

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // required by Xiaomi certificate subject format
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const CentralGatewayCAPEM = `-----BEGIN CERTIFICATE-----
MIIBazCCAQ+gAwIBAgIEA/UKYDAMBggqhkjOPQQDAgUAMCIxEzARBgNVBAoTCk1p
amlhIFJvb3QxCzAJBgNVBAYTAkNOMCAXDTE2MTEyMzAxMzk0NVoYDzIwNjYxMTEx
MDEzOTQ1WjAiMRMwEQYDVQQKEwpNaWppYSBSb290MQswCQYDVQQGEwJDTjBZMBMG
ByqGSM49AgEGCCqGSM49AwEHA0IABL71iwLa4//4VBqgRI+6xE23xpovqPCxtv96
2VHbZij61/Ag6jmi7oZ/3Xg/3C+whglcwoUEE6KALGJ9vccV9PmjLzAtMAwGA1Ud
EwQFMAMBAf8wHQYDVR0OBBYEFJa3onw5sblmM6n40QmyAGDI5sURMAwGCCqGSM49
BAMCBQADSAAwRQIgchciK9h6tZmfrP8Ka6KziQ4Lv3hKfrHtAZXMHPda4IYCIQCG
az93ggFcbrG9u2wixjx1HKW4DUA5NXZG0wWQTpJTbQ==
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIBjzCCATWgAwIBAgIBATAKBggqhkjOPQQDAjAiMRMwEQYDVQQKEwpNaWppYSBS
b290MQswCQYDVQQGEwJDTjAgFw0yMjA2MDkxNDE0MThaGA8yMDcyMDUyNzE0MTQx
OFowLDELMAkGA1UEBhMCQ04xHTAbBgNVBAoMFE1JT1QgQ0VOVFJBTCBHQVRFV0FZ
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEdYrzbnp/0x/cZLZnuEDXTFf8mhj4
CVpZPwgj9e9Ve5r3K7zvu8Jjj7JF1JjQYvEC6yhp1SzBgglnK4L8xQzdiqNQME4w
HQYDVR0OBBYEFCf9+YBU7pXDs6K6CAQPRhlGJ+cuMB8GA1UdIwQYMBaAFJa3onw5
sblmM6n40QmyAGDI5sURMAwGA1UdEwQFMAMBAf8wCgYIKoZIzj0EAwIDSAAwRQIh
AKUv+c8v98vypkGMTzMwckGjjVqTef8xodsy6PhcSCq+AiA/n9mDs62hAo5zXyJy
Bs1s7mqXPf1XgieoxIvs1MqyiA==
-----END CERTIFICATE-----
`

type CertFiles struct {
	CAFile   string `json:"ca_file"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

func EnsurePrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, errors.New("private key file has no PEM block")
		}
		keyAny, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes)
		if parseErr != nil {
			return nil, fmt.Errorf("parse private key: %w", parseErr)
		}
		switch key := keyAny.(type) {
		case ed25519.PrivateKey:
			return key, nil
		case *ed25519.PrivateKey:
			return *key, nil
		default:
			return nil, errors.New("private key is not Ed25519")
		}
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate Ed25519 key: %w", err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode private key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create certificate directory: %w", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0o600); err != nil {
		return nil, fmt.Errorf("write private key: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure private key permissions: %w", err)
	}
	return key, nil
}

func CertificateCommonName(uid, virtualDID string) (string, error) {
	uid = strings.TrimSpace(uid)
	virtualDID = strings.TrimSpace(virtualDID)
	if uid == "" || virtualDID == "" {
		return "", errors.New("account UID and virtual DID are required")
	}
	digest := sha1.Sum([]byte(virtualDID))
	return fmt.Sprintf("mips.%s.%s.2", uid, hex.EncodeToString(digest[:])), nil
}

func CreateCSR(uid, virtualDID string, key ed25519.PrivateKey) (string, error) {
	if len(key) != ed25519.PrivateKeySize {
		return "", errors.New("valid Ed25519 private key is required")
	}
	commonName, err := CertificateCommonName(uid, virtualDID)
	if err != nil {
		return "", err
	}
	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			Country:      []string{"CN"},
			Organization: []string{"Mijia Device"},
			CommonName:   commonName,
		},
		SignatureAlgorithm: x509.PureEd25519,
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return "", fmt.Errorf("create CSR: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), nil
}

func (c OAuthClient) RequestCentralCertificate(ctx context.Context, accessToken, csr string) (string, error) {
	if strings.TrimSpace(csr) == "" {
		return "", errors.New("CSR is required")
	}
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  struct {
			Cert string `json:"cert"`
		} `json:"result"`
	}
	if err := c.apiPost(ctx, accessToken, CentralCertPath, map[string]string{
		"csr": base64.StdEncoding.EncodeToString([]byte(csr)),
	}, &envelope); err != nil {
		return "", err
	}
	if envelope.Code != 0 {
		return "", fmt.Errorf("certificate API returned code %d: %s", envelope.Code, envelope.Message)
	}
	if !strings.Contains(envelope.Result.Cert, "BEGIN CERTIFICATE") {
		encoded, _ := json.Marshal(envelope)
		return "", fmt.Errorf("certificate API returned invalid certificate: %s", compactBody(encoded))
	}
	return envelope.Result.Cert, nil
}

func WriteCertificateFiles(dir, certificate, expectedCommonName string, key ed25519.PrivateKey) (CertFiles, error) {
	if len(key) != ed25519.PrivateKeySize {
		return CertFiles{}, errors.New("valid Ed25519 private key is required")
	}
	if err := VerifyCertificateKey(certificate, expectedCommonName, key); err != nil {
		return CertFiles{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return CertFiles{}, fmt.Errorf("create certificate directory: %w", err)
	}
	files := CertFiles{
		CAFile:   filepath.Join(dir, "ca.pem"),
		CertFile: filepath.Join(dir, "client.crt"),
		KeyFile:  filepath.Join(dir, "client.key"),
	}
	if err := os.WriteFile(files.CAFile, []byte(CentralGatewayCAPEM), 0o644); err != nil {
		return CertFiles{}, fmt.Errorf("write CA certificate: %w", err)
	}
	if !strings.HasSuffix(certificate, "\n") {
		certificate += "\n"
	}
	if err := os.WriteFile(files.CertFile, []byte(certificate), 0o644); err != nil {
		return CertFiles{}, fmt.Errorf("write client certificate: %w", err)
	}
	if _, err := os.Stat(files.KeyFile); err != nil {
		return CertFiles{}, fmt.Errorf("private key is missing: %w", err)
	}
	if err := os.Chmod(files.KeyFile, 0o600); err != nil {
		return CertFiles{}, fmt.Errorf("secure private key permissions: %w", err)
	}
	return files, nil
}

func VerifyCertificateKey(certificatePEM, expectedCommonName string, key ed25519.PrivateKey) error {
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("client certificate has no CERTIFICATE PEM block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse client certificate: %w", err)
	}
	publicKey, ok := certificate.PublicKey.(ed25519.PublicKey)
	if !ok {
		return errors.New("client certificate public key is not Ed25519")
	}
	expectedPublic, ok := key.Public().(ed25519.PublicKey)
	if !ok || !publicKey.Equal(expectedPublic) {
		return errors.New("client certificate does not match private key")
	}
	if certificate.Subject.CommonName != expectedCommonName {
		return fmt.Errorf("client certificate common name mismatch: got %q, want %q", certificate.Subject.CommonName, expectedCommonName)
	}
	if len(certificate.Subject.Country) == 0 || certificate.Subject.Country[0] != "CN" {
		return errors.New("client certificate country is not CN")
	}
	if len(certificate.Subject.Organization) == 0 || certificate.Subject.Organization[0] != "Mijia Device" {
		return errors.New("client certificate organization is not Mijia Device")
	}
	now := time.Now()
	if now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
		return fmt.Errorf("client certificate is not currently valid: %s to %s", certificate.NotBefore.Format(time.RFC3339), certificate.NotAfter.Format(time.RFC3339))
	}
	return nil
}
