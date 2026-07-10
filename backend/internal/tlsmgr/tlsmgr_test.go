package tlsmgr

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

func TestValidateDomain(t *testing.T) {
	valid := []string{
		"example.com",
		"panel.example.com",
		"a.b.c.d.example.com",
		"xn--bcher-kva.example",  // punycode
		"my-panel.example.co.uk", // hyphen + multi-label TLD
		"1panel.example.com",     // leading digit is legal
	}
	for _, d := range valid {
		if err := ValidateDomain(d); err != nil {
			t.Errorf("ValidateDomain(%q) = %v, want nil", d, err)
		}
	}

	invalid := []struct{ domain, wantSubstr string }{
		{"", "required"},
		{"   ", "required"},
		{"1.2.3.4", "IP addresses"},
		{"::1", "IP addresses"},
		{"localhost", "localhost"},
		{"LOCALHOST", "localhost"},
		{"panel.local", ".local"},
		{"https://panel.example.com", "scheme or port"},
		{"panel.example.com:443", "scheme or port"},
		{"panel.example.com/path", "scheme or port"},
		{"nodots", "not a valid domain"},
		{"-leading.example.com", "not a valid domain"},
		{"trailing-.example.com", "not a valid domain"},
		{"double..dot.com", "not a valid domain"},
		{"under_score.com", "not a valid domain"},
	}
	for _, tc := range invalid {
		err := ValidateDomain(tc.domain)
		if err == nil {
			t.Errorf("ValidateDomain(%q) = nil, want an error", tc.domain)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantSubstr) {
			t.Errorf("ValidateDomain(%q) = %q, want it to mention %q", tc.domain, err, tc.wantSubstr)
		}
	}
}

func TestValidateEmail(t *testing.T) {
	for _, e := range []string{"a@b.co", "admin@example.com", "first.last+tag@sub.example.org"} {
		if err := ValidateEmail(e); err != nil {
			t.Errorf("ValidateEmail(%q) = %v, want nil", e, err)
		}
	}
	for _, e := range []string{"", "   ", "nope", "@example.com", "a@", "a@nodot", "a b@example.com "} {
		if err := ValidateEmail(e); err == nil {
			t.Errorf("ValidateEmail(%q) = nil, want an error", e)
		}
	}
}

func TestNewManagerUsesStagingDirectory(t *testing.T) {
	prod := New(Config{Domain: "example.com", Email: "a@b.co", CacheDir: t.TempDir()})
	if prod.m.Client != nil {
		t.Error("production manager should use autocert's default directory")
	}
	staging := New(Config{Domain: "example.com", Email: "a@b.co", CacheDir: t.TempDir(), Staging: true})
	if staging.m.Client == nil || staging.m.Client.DirectoryURL != LetsEncryptStaging {
		t.Error("staging manager does not point at the staging directory")
	}
	if got := staging.Domain(); got != "example.com" {
		t.Errorf("Domain() = %q", got)
	}
}

func TestTLSConfigEnforcesMinVersion(t *testing.T) {
	m := New(Config{Domain: "example.com", CacheDir: t.TempDir()})
	cfg := m.TLSConfig()
	if cfg.MinVersion < 0x0303 { // TLS 1.2
		t.Errorf("MinVersion = %#x, want at least TLS 1.2", cfg.MinVersion)
	}
	if cfg.GetCertificate == nil {
		t.Error("TLSConfig has no GetCertificate hook")
	}
}

func TestHostPolicyRejectsOtherDomains(t *testing.T) {
	m := New(Config{Domain: "panel.example.com", CacheDir: t.TempDir()})
	ctx := context.Background()
	if err := m.m.HostPolicy(ctx, "panel.example.com"); err != nil {
		t.Errorf("configured host rejected: %v", err)
	}
	if err := m.m.HostPolicy(ctx, "evil.example.com"); err == nil {
		t.Error("certificates would be issued for an unconfigured host")
	}
}

func TestStatusReportsNoCertificateInitially(t *testing.T) {
	m := New(Config{Domain: "example.com", CacheDir: t.TempDir()})
	issued, notAfter, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if issued {
		t.Error("reported a certificate for an empty cache")
	}
	if !notAfter.IsZero() {
		t.Error("expiry set with no certificate")
	}
}

func TestStatusParsesCachedCertificate(t *testing.T) {
	dir := t.TempDir()
	m := New(Config{Domain: "example.com", CacheDir: dir})

	want := time.Now().Add(60 * 24 * time.Hour).Truncate(time.Second)
	writeFakeBundle(t, dir, "example.com", want)

	issued, notAfter, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !issued {
		t.Fatal("cached certificate not detected")
	}
	if !notAfter.Equal(want.UTC()) {
		t.Errorf("notAfter = %v, want %v", notAfter, want.UTC())
	}
}

// The rsa-suffixed cache key must also be found.
func TestStatusFindsRSASuffixedKey(t *testing.T) {
	dir := t.TempDir()
	m := New(Config{Domain: "example.com", CacheDir: dir})
	want := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	writeFakeBundle(t, dir, "example.com+rsa", want)

	issued, _, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !issued {
		t.Error("rsa-suffixed cache entry not detected")
	}
}

func TestStatusRejectsCorruptBundle(t *testing.T) {
	dir := t.TempDir()
	m := New(Config{Domain: "example.com", CacheDir: dir})
	cache := autocert.DirCache(dir)
	if err := cache.Put(context.Background(), "example.com", []byte("not pem")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Status(context.Background()); err == nil {
		t.Error("corrupt cache entry reported as a valid certificate")
	}
}

func TestPrimeRespectsContextCancellation(t *testing.T) {
	m := New(Config{Domain: "example.com", CacheDir: t.TempDir(), Staging: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Prime(ctx); err == nil {
		t.Error("Prime ignored a cancelled context")
	}
}

func TestFirstCertificateSkipsKeyBlock(t *testing.T) {
	der, _ := makeCert(t, time.Now().Add(time.Hour))
	bundle := append(
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("fake")}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...,
	)
	cert, err := firstCertificate(bundle)
	if err != nil {
		t.Fatalf("firstCertificate: %v", err)
	}
	if cert.Subject.CommonName != "example.com" {
		t.Errorf("parsed the wrong block: %v", cert.Subject)
	}

	if _, err := firstCertificate([]byte("garbage")); err == nil {
		t.Error("garbage accepted")
	}
}

// ---- helpers ----

func makeCert(t *testing.T, notAfter time.Time) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		DNSNames:     []string{"example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der, key
}

func writeFakeBundle(t *testing.T, dir, key string, notAfter time.Time) {
	t.Helper()
	der, priv := makeCert(t, notAfter)
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	bundle := append(
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...,
	)
	if err := autocert.DirCache(dir).Put(context.Background(), key, bundle); err != nil {
		t.Fatal(err)
	}
	// Sanity: the file really landed where autocert looks for it.
	if _, err := autocert.DirCache(dir).Get(context.Background(), key); err != nil {
		t.Fatalf("cache round-trip failed for %s: %v", filepath.Join(dir, key), err)
	}
}
