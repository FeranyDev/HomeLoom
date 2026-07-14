package xiaomi

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

type testCertificate struct {
	certificate *x509.Certificate
	privateKey  ed25519.PrivateKey
	der         []byte
}

func issueTestCertificate(t *testing.T, serial int64, template x509.Certificate, parent *testCertificate) testCertificate {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template.SerialNumber = big.NewInt(serial)
	issuer, signer := &template, privateKey
	if parent != nil {
		issuer, signer = parent.certificate, parent.privateKey
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, issuer, publicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCertificate{certificate: certificate, privateKey: privateKey, der: der}
}

func testXiaomiChain(t *testing.T, now time.Time, leafNotBefore, leafNotAfter time.Time, usages []x509.ExtKeyUsage) (testCertificate, testCertificate, testCertificate, []byte) {
	t.Helper()
	root := issueTestCertificate(t, 1, x509.Certificate{Subject: pkix.Name{CommonName: "Xiaomi Test Root"}, NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}, nil)
	intermediate := issueTestCertificate(t, 2, x509.Certificate{Subject: pkix.Name{CommonName: "Xiaomi Test Intermediate"}, NotBefore: now.Add(-12 * time.Hour), NotAfter: now.Add(12 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}, &root)
	leaf := issueTestCertificate(t, 3, x509.Certificate{Subject: pkix.Name{CommonName: "unrelated.example"}, DNSNames: []string{"unrelated.example"}, NotBefore: leafNotBefore, NotAfter: leafNotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usages}, &intermediate)
	bundle := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intermediate.der})...)
	return root, intermediate, leaf, bundle
}

func TestXiaomiTLSVerifiesCAChainAndIgnoresSAN(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	_, _, leaf, bundle := testXiaomiChain(t, now, now.Add(-time.Hour), now.Add(time.Hour), []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	roots, intermediates, err := xiaomiCAPools(bundle)
	if err != nil {
		t.Fatal(err)
	}
	// Only the leaf is sent by the gateway here; the configured Xiaomi CA bundle
	// supplies the intermediate. Its SAN deliberately does not match the LAN host.
	if err := verifyXiaomiServerCertificate(roots, intermediates, func() time.Time { return now })(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf.certificate}}); err != nil {
		t.Fatalf("valid Xiaomi chain was rejected: %v", err)
	}
}

func TestBundledXiaomiCAContainsRootAndIntermediate(t *testing.T) {
	roots, intermediates, err := xiaomiCAPools([]byte(CentralGatewayCAPEM))
	if err != nil {
		t.Fatal(err)
	}
	if len(roots.Subjects()) != 1 || len(intermediates.Subjects()) != 1 {
		t.Fatalf("bundled Xiaomi CA subjects: roots=%d intermediates=%d", len(roots.Subjects()), len(intermediates.Subjects()))
	}
}

func TestXiaomiTLSRejectsExpiredServerCertificate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	_, intermediate, leaf, bundle := testXiaomiChain(t, now, now.Add(-2*time.Hour), now.Add(-time.Hour), []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	roots, intermediates, err := xiaomiCAPools(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyXiaomiServerCertificate(roots, intermediates, func() time.Time { return now })(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf.certificate, intermediate.certificate}}); err == nil {
		t.Fatal("expired Xiaomi server certificate was accepted")
	}
}

func TestXiaomiTLSRequiresServerAuth(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	_, _, leaf, bundle := testXiaomiChain(t, now, now.Add(-time.Hour), now.Add(time.Hour), []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	roots, intermediates, err := xiaomiCAPools(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyXiaomiServerCertificate(roots, intermediates, func() time.Time { return now })(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf.certificate}}); err == nil {
		t.Fatal("client-auth-only certificate was accepted as a Xiaomi server")
	}
}

func TestXiaomiTLSRejectsUnknownCA(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	_, _, _, trustedBundle := testXiaomiChain(t, now, now.Add(-time.Hour), now.Add(time.Hour), []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	_, unknownIntermediate, unknownLeaf, _ := testXiaomiChain(t, now, now.Add(-time.Hour), now.Add(time.Hour), []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	roots, intermediates, err := xiaomiCAPools(trustedBundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyXiaomiServerCertificate(roots, intermediates, func() time.Time { return now })(tls.ConnectionState{PeerCertificates: []*x509.Certificate{unknownLeaf.certificate, unknownIntermediate.certificate}}); err == nil {
		t.Fatal("server certificate from an unknown CA was accepted")
	}
}
