package hub

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config contains only local gateway connection material. Cloud OAuth and
// certificate issuance are deliberately outside this package.
type Config struct {
	Host               string `json:"host"`
	Port               int    `json:"port"`
	ClientID           string `json:"client_id"`
	CAFile             string `json:"ca_file"`
	CertFile           string `json:"cert_file"`
	KeyFile            string `json:"key_file"`
	ServerName         string `json:"server_name,omitempty"`
	VerifyServerName   bool   `json:"verify_server_name,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
	RequestTimeoutSec  int    `json:"request_timeout_seconds,omitempty"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config JSON: %w", err)
	}
	base := filepath.Dir(path)
	cfg.CAFile = resolvePath(base, cfg.CAFile)
	cfg.CertFile = resolvePath(base, cfg.CertFile)
	cfg.KeyFile = resolvePath(base, cfg.KeyFile)
	if cfg.Port == 0 {
		cfg.Port = 8883
	}
	if cfg.RequestTimeoutSec == 0 {
		cfg.RequestTimeoutSec = 10
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func resolvePath(base, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(base, value)
}

func (c Config) Validate() error {
	if c.Host == "" {
		return errors.New("config.host is required")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("config.port is invalid: %d", c.Port)
	}
	if c.ClientID == "" {
		return errors.New("config.client_id is required")
	}
	if c.CAFile == "" || c.CertFile == "" || c.KeyFile == "" {
		return errors.New("config.ca_file, cert_file and key_file are required")
	}
	if c.VerifyServerName && c.ServerName == "" {
		return errors.New("config.server_name is required when verify_server_name is true")
	}
	if c.InsecureSkipVerify && c.VerifyServerName {
		return errors.New("config.insecure_skip_verify and verify_server_name cannot both be true")
	}
	if c.RequestTimeoutSec < 1 || c.RequestTimeoutSec > 300 {
		return errors.New("config.request_timeout_seconds must be between 1 and 300")
	}
	return nil
}

func (c Config) RequestTimeout() time.Duration {
	return time.Duration(c.RequestTimeoutSec) * time.Second
}

func (c Config) TLSConfig() (*tls.Config, error) {
	caPEM, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("CA file contains no valid PEM certificate")
	}
	certificate, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load client certificate/key: %w", err)
	}

	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      roots,
		Certificates: []tls.Certificate{certificate},
		ServerName:   c.ServerName,
	}

	if c.InsecureSkipVerify {
		// Full verification bypass. This is retained only as an explicit
		// diagnostic escape hatch and must not be used for normal operation.
		cfg.InsecureSkipVerify = true //nolint:gosec
		return cfg, nil
	}

	if c.VerifyServerName {
		// Standard Go TLS verification: certificate chain plus DNS/IP name.
		return cfg, nil
	}

	// Xiaomi central hubs commonly present a CA-signed server certificate with
	// no DNS/IP SAN at all. Standard hostname verification therefore cannot
	// succeed. Match Xiaomi's official client behavior while retaining the
	// security property that matters here: verify the complete server
	// certificate chain against the configured Xiaomi CA and require the
	// certificate to be valid for TLS server authentication.
	cfg.InsecureSkipVerify = true //nolint:gosec // verified safely in VerifyConnection below
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("gateway returned no server certificate")
		}
		intermediates := x509.NewCertPool()
		for _, cert := range state.PeerCertificates[1:] {
			intermediates.AddCert(cert)
		}
		_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{
			Roots:         roots,
			Intermediates: intermediates,
			CurrentTime:   time.Now(),
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		if err != nil {
			return fmt.Errorf("verify gateway certificate chain: %w", err)
		}
		return nil
	}
	return cfg, nil
}
