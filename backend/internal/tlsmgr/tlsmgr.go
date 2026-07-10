// Package tlsmgr provides automatic HTTPS for the panel using Let's Encrypt
// (ACME). Certificates are obtained on first TLS handshake for the configured
// domain, cached on disk, and renewed automatically well before expiry.
package tlsmgr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// LetsEncryptStaging is the ACME directory used when staging mode is on. It
// issues untrusted certificates but has far higher rate limits, so operators
// can rehearse a setup without burning their weekly quota.
const LetsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"

type Config struct {
	Domain   string
	Email    string
	CacheDir string
	Staging  bool
}

type Manager struct {
	cfg Config
	m   *autocert.Manager
}

func New(cfg Config) *Manager {
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.Domain),
		Cache:      autocert.DirCache(cfg.CacheDir),
		Email:      cfg.Email,
	}
	if cfg.Staging {
		m.Client = &acme.Client{DirectoryURL: LetsEncryptStaging}
	}
	return &Manager{cfg: cfg, m: m}
}

func (m *Manager) Domain() string { return m.cfg.Domain }

// TLSConfig returns a config that fetches/renews certificates on demand.
func (m *Manager) TLSConfig() *tls.Config {
	cfg := m.m.TLSConfig()
	cfg.MinVersion = tls.VersionTLS12
	return cfg
}

// HTTPHandler wraps a plaintext handler so ACME HTTP-01 challenges are served.
// A nil fallback redirects every other request to HTTPS.
func (m *Manager) HTTPHandler(fallback http.Handler) http.Handler {
	return m.m.HTTPHandler(fallback)
}

// Prime requests the certificate immediately instead of waiting for the first
// handshake, so the admin UI can report success or a concrete ACME failure.
func (m *Manager) Prime(ctx context.Context) error {
	hello := &tls.ClientHelloInfo{
		ServerName:        m.cfg.Domain,
		SupportedProtos:   []string{"http/1.1"},
		CipherSuites:      []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256},
		SupportedVersions: []uint16{tls.VersionTLS12, tls.VersionTLS13},
	}
	hello.Conn = nil
	done := make(chan error, 1)
	go func() {
		_, err := m.m.GetCertificate(hello)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Status reports whether a cached certificate exists and when it expires.
func (m *Manager) Status(ctx context.Context) (issued bool, notAfter time.Time, err error) {
	// autocert stores the bundle under the host name, with an ecdsa/rsa suffix
	// for the non-default key type.
	for _, key := range []string{m.cfg.Domain, m.cfg.Domain + "+rsa"} {
		raw, cacheErr := m.m.Cache.Get(ctx, key)
		if errors.Is(cacheErr, autocert.ErrCacheMiss) {
			continue
		}
		if cacheErr != nil {
			return false, time.Time{}, cacheErr
		}
		cert, parseErr := firstCertificate(raw)
		if parseErr != nil {
			return false, time.Time{}, parseErr
		}
		return true, cert.NotAfter, nil
	}
	return false, time.Time{}, nil
}

// firstCertificate pulls the leaf out of a cached PEM bundle (the private key
// block precedes the certificate chain).
func firstCertificate(raw []byte) (*x509.Certificate, error) {
	for block, rest := pem.Decode(raw); block != nil; block, rest = pem.Decode(rest) {
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
	}
	return nil, errors.New("no certificate found in cached bundle")
}

var domainRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$`)

// ValidateDomain rejects the inputs Let's Encrypt can never issue for, so the
// operator gets a useful message instead of an opaque ACME error.
func ValidateDomain(domain string) error {
	domain = strings.TrimSpace(domain)
	switch {
	case domain == "":
		return errors.New("a domain name is required")
	// IP literals are checked before the punctuation rules so IPv6 addresses
	// ("::1") report the real reason rather than "looks like a port".
	case net.ParseIP(domain) != nil || net.ParseIP(strings.Trim(domain, "[]")) != nil:
		return errors.New("certificates cannot be issued for IP addresses")
	case strings.Contains(domain, "/") || strings.Contains(domain, ":"):
		return errors.New("enter a bare domain name, without a scheme or port")
	case strings.EqualFold(domain, "localhost"):
		return errors.New("certificates cannot be issued for localhost")
	case strings.HasSuffix(domain, ".local"):
		return errors.New("certificates cannot be issued for .local domains")
	case !domainRe.MatchString(domain):
		return fmt.Errorf("%q is not a valid domain name", domain)
	}
	return nil
}

// ValidateEmail checks the ACME account address.
func ValidateEmail(email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return errors.New("a contact email is required (Let's Encrypt uses it for expiry warnings)")
	}
	if strings.ContainsAny(email, " \t\r\n") {
		return fmt.Errorf("%q is not a valid email address", email)
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 || at == len(email)-1 || !strings.Contains(email[at+1:], ".") {
		return fmt.Errorf("%q is not a valid email address", email)
	}
	return nil
}
