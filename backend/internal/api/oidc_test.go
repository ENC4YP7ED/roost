package api

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// mockIdP is a throwaway OpenID Connect provider: discovery doc, JWKS, and a
// token endpoint that returns a pre-signed ID token. It lets us drive the full
// authorization-code callback without any real network identity provider.
type mockIdP struct {
	server  *httptest.Server
	key     *rsa.PrivateKey
	idToken string
}

func newMockIdP(t *testing.T, clientID, nonce string, claims map[string]any) *mockIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	m := &mockIdP{key: key}

	mux := http.NewServeMux()
	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)

	// Sign the ID token now that we know the issuer (the server URL).
	full := map[string]any{
		"iss":   m.server.URL,
		"aud":   clientID,
		"sub":   "sub-123",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
		"nonce": nonce,
	}
	for k, v := range claims {
		full[k] = v
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "test"}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(full)
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	m.idToken, _ = jws.CompactSerialize()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                m.server.URL,
			"authorization_endpoint":                m.server.URL + "/authorize",
			"token_endpoint":                        m.server.URL + "/token",
			"jwks_uri":                              m.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"},
		}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "id_token": m.idToken,
		})
	})
	return m
}

func (h *harness) configureOIDC(issuer, clientID string) {
	h.st.SetSetting("oidc:enabled", "1")
	h.st.SetSetting("oidc:issuer", issuer)
	h.st.SetSetting("oidc:client_id", clientID)
	h.st.SetSetting("oidc:client_secret", "shhh")
	h.st.SetSetting("oidc:label", "Sign in with BundID")
}

func TestOIDCConfigAndGuards(t *testing.T) {
	h := newHarness(t)
	h.mkUser("admin", "admin@example.com", "adminpass1", true)
	cookie := h.login("admin", "adminpass1")

	// Public SSO status: disabled until configured.
	if res := h.do("GET", "/api/sso", nil); res.json()["enabled"] != false {
		t.Errorf("sso should be disabled initially: %v", res.json())
	}

	// Save config as admin; the secret must be write-only (masked on read).
	save := h.do("PUT", "/api/application/sso", map[string]any{
		"enabled": true, "issuer": "https://id.example", "client_id": "abc",
		"client_secret": "topsecret", "label": "Sign in with BundID",
	}, withCookie(cookie))
	if save.Code != http.StatusNoContent {
		t.Fatalf("save sso = %d: %s", save.Code, save.Body.String())
	}
	get := h.do("GET", "/api/application/sso", nil, withCookie(cookie))
	if get.json()["client_secret"] == "topsecret" {
		t.Error("client secret must not be returned verbatim")
	}
	if get.json()["secret_present"] != true {
		t.Error("secret_present should be true after saving a secret")
	}
	// Re-saving with the masked secret must not wipe the stored one.
	h.do("PUT", "/api/application/sso", map[string]any{
		"enabled": true, "issuer": "https://id.example", "client_id": "abc",
		"client_secret": get.json()["client_secret"],
	}, withCookie(cookie))
	if h.st.Setting("oidc:client_secret", "") != "topsecret" {
		t.Error("masked re-save should preserve the existing secret")
	}

	// Public status now enabled with the configured label.
	if res := h.do("GET", "/api/sso", nil); res.json()["enabled"] != true || res.json()["label"] != "Sign in with BundID" {
		t.Errorf("sso status wrong: %v", res.json())
	}

	// Non-admins cannot read the config.
	h.mkUser("joe", "joe@example.com", "joepass123", false)
	uc := h.login("joe", "joepass123")
	if res := h.do("GET", "/api/application/sso", nil, withCookie(uc)); res.Code != http.StatusForbidden {
		t.Errorf("non-admin read sso = %d, want 403", res.Code)
	}

	// Callback with a mismatched state redirects back with an error.
	cb := h.do("GET", "/auth/oidc/callback?state=x", nil, withCookie(&http.Cookie{Name: "oidc_state", Value: "y"}))
	if cb.Code != http.StatusFound || cb.Header().Get("Location") == "/" {
		t.Errorf("bad-state callback should redirect with error, got %d -> %s", cb.Code, cb.Header().Get("Location"))
	}
}

func TestOIDCFullCallback(t *testing.T) {
	h := newHarness(t)
	const clientID = "roost-client"
	const nonce = "test-nonce-value"
	idp := newMockIdP(t, clientID, nonce, map[string]any{
		"email": "Sso.User@Example.com", "email_verified": true,
		"given_name": "Sso", "family_name": "User", "preferred_username": "ssouser",
	})
	h.configureOIDC(idp.server.URL, clientID)

	// Drive the callback directly with matching state/nonce cookies.
	cb := h.do("GET", "/auth/oidc/callback?state=st&code=authcode", nil,
		withCookie(&http.Cookie{Name: "oidc_state", Value: "st"}),
		withCookie(&http.Cookie{Name: "oidc_nonce", Value: nonce}),
	)
	if cb.Code != http.StatusFound || cb.Header().Get("Location") != "/" {
		t.Fatalf("callback = %d -> %q\n%s", cb.Code, cb.Header().Get("Location"), cb.Body.String())
	}
	// A session cookie must have been issued.
	var hasSession bool
	for _, c := range cb.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			hasSession = true
		}
	}
	if !hasSession {
		t.Error("SSO callback should issue a session cookie")
	}

	// A local account was provisioned, linked by OIDC subject, non-admin.
	u, err := h.st.UserByEmail("sso.user@example.com")
	if err != nil {
		t.Fatalf("SSO user not created: %v", err)
	}
	if u.ExternalID == nil || *u.ExternalID != "oidc:sub-123" {
		t.Errorf("external id not linked: %v", u.ExternalID)
	}
	if u.RootAdmin {
		t.Error("SSO users must be non-admin")
	}

	// A second callback with the same subject reuses the account, not a dupe.
	h.do("GET", "/auth/oidc/callback?state=st&code=authcode", nil,
		withCookie(&http.Cookie{Name: "oidc_state", Value: "st"}),
		withCookie(&http.Cookie{Name: "oidc_nonce", Value: nonce}),
	)
	all, _ := h.st.Users("")
	n := 0
	for _, x := range all {
		if x.Email == "sso.user@example.com" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("expected exactly one SSO account, got %d", n)
	}
}
