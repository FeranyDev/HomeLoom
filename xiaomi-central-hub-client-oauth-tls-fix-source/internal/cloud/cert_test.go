package cloud

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"path/filepath"
	"testing"
)

func TestCreateCSR(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "client.key")
	key, err := EnsurePrivateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != ed25519.PrivateKeySize {
		t.Fatalf("key length = %d, want %d", len(key), ed25519.PrivateKeySize)
	}

	const uid = "1234567890"
	const virtualDID = "9876543210"
	csrPEM, err := CreateCSR(uid, virtualDID, key)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		t.Fatal("CSR has no PEM block")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Fatalf("CSR signature: %v", err)
	}
	wantCN, err := CertificateCommonName(uid, virtualDID)
	if err != nil {
		t.Fatal(err)
	}
	if csr.Subject.CommonName != wantCN {
		t.Errorf("common name = %q, want %q", csr.Subject.CommonName, wantCN)
	}
	if len(csr.Subject.Country) != 1 || csr.Subject.Country[0] != "CN" {
		t.Errorf("country = %#v, want [CN]", csr.Subject.Country)
	}
	if len(csr.Subject.Organization) != 1 || csr.Subject.Organization[0] != "Mijia Device" {
		t.Errorf("organization = %#v, want [Mijia Device]", csr.Subject.Organization)
	}
	if _, ok := csr.PublicKey.(ed25519.PublicKey); !ok {
		t.Fatalf("CSR public key type = %T, want Ed25519", csr.PublicKey)
	}
}

func TestEnsurePrivateKeyReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.key")
	first, err := EnsurePrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsurePrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(second) {
		t.Fatal("reloaded key differs from generated key")
	}
}
