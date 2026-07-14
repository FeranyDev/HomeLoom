package hub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testPKI struct {
	caPath     string
	clientCert string
	clientKey  string
	serverCert *x509.Certificate
}

func makeTestPKI(t *testing.T) testPKI {
	t.Helper()
	dir := t.TempDir()
	now := time.Now()

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}

	makeLeaf := func(serial int64, cn string, usages []x509.ExtKeyUsage) (*x509.Certificate, []byte, *ecdsa.PrivateKey) {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    now.Add(-time.Hour),
			NotAfter:     now.Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  usages,
			// Deliberately no DNSNames or IPAddresses. Xiaomi hub server
			// certificates can have no SAN extension.
		}
		der, err := x509.CreateCertificate(rand.Reader, template, rootCert, &key.PublicKey, rootKey)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return cert, der, key
	}

	serverCert, _, _ := makeLeaf(2, "gateway", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	_, clientDER, clientKey := makeLeaf(3, "client", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})

	caPath := filepath.Join(dir, "ca.pem")
	clientCertPath := filepath.Join(dir, "client.crt")
	clientKeyPath := filepath.Join(dir, "client.key")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clientCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clientKeyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return testPKI{caPath: caPath, clientCert: clientCertPath, clientKey: clientKeyPath, serverCert: serverCert}
}

func TestTLSConfigVerifiesChainWithoutHostname(t *testing.T) {
	pki := makeTestPKI(t)
	cfg := Config{
		Host:              "192.0.2.1",
		Port:              8883,
		ClientID:          "test-client",
		CAFile:            pki.caPath,
		CertFile:          pki.clientCert,
		KeyFile:           pki.clientKey,
		ServerName:        "gateway.local",
		RequestTimeoutSec: 10,
	}
	tlsCfg, err := cfg.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !tlsCfg.InsecureSkipVerify || tlsCfg.VerifyConnection == nil {
		t.Fatal("expected custom chain-only certificate verification")
	}
	if err := tlsCfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{pki.serverCert}}); err != nil {
		t.Fatalf("certificate without SAN should pass chain-only verification: %v", err)
	}
}

func TestTLSConfigStrictHostnameMode(t *testing.T) {
	pki := makeTestPKI(t)
	cfg := Config{
		Host:              "192.0.2.1",
		Port:              8883,
		ClientID:          "test-client",
		CAFile:            pki.caPath,
		CertFile:          pki.clientCert,
		KeyFile:           pki.clientKey,
		ServerName:        "gateway.local",
		VerifyServerName:  true,
		RequestTimeoutSec: 10,
	}
	tlsCfg, err := cfg.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	if tlsCfg.InsecureSkipVerify || tlsCfg.VerifyConnection != nil {
		t.Fatal("expected standard TLS hostname verification")
	}
}

func TestConfigRejectsConflictingVerificationModes(t *testing.T) {
	cfg := Config{
		Host:               "192.0.2.1",
		Port:               8883,
		ClientID:           "test-client",
		CAFile:             "ca.pem",
		CertFile:           "client.crt",
		KeyFile:            "client.key",
		ServerName:         "gateway.local",
		VerifyServerName:   true,
		InsecureSkipVerify: true,
		RequestTimeoutSec:  10,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected conflicting verification mode error")
	}
}
