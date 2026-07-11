package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"roost/internal/auth"
	"roost/internal/store"
)

// External identity via OpenID Connect (authorization-code flow). This is a
// single generic OIDC provider — configure it against any issuer, or use the
// BundID preset for the German federal identity system. On callback we verify
// the ID token, then find-or-link the local account by OIDC subject (and,
// optionally, by verified email) and issue a normal panel session.

type oidcSettings struct {
	Enabled      bool
	Issuer       string
	ClientID     string
	ClientSecret string
	Scopes       string
	Label        string
	LinkByEmail  bool
}

func (a *API) oidcSettings() oidcSettings {
	s := a.Store
	return oidcSettings{
		Enabled:      s.Setting("oidc:enabled", "") == "1",
		Issuer:       s.Setting("oidc:issuer", ""),
		ClientID:     s.Setting("oidc:client_id", ""),
		ClientSecret: s.Setting("oidc:client_secret", ""),
		Scopes:       s.Setting("oidc:scopes", "openid email profile"),
		Label:        s.Setting("oidc:label", "Sign in with SSO"),
		LinkByEmail:  s.Setting("oidc:link_by_email", "1") == "1",
	}
}

func (c oidcSettings) ready() bool {
	return c.Enabled && c.Issuer != "" && c.ClientID != "" && c.ClientSecret != ""
}

// providerCache memoises OIDC discovery (which makes a network call) per issuer.
var (
	oidcProviderMu    sync.Mutex
	oidcProviderCache = map[string]*oidc.Provider{}
)

func (a *API) oidcProvider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	oidcProviderMu.Lock()
	p, ok := oidcProviderCache[issuer]
	oidcProviderMu.Unlock()
	if ok {
		return p, nil
	}
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	oidcProviderMu.Lock()
	oidcProviderCache[issuer] = p
	oidcProviderMu.Unlock()
	return p, nil
}

func (a *API) oauth2Config(p *oidc.Provider, cfg oidcSettings) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  strings.TrimRight(a.PanelURL(), "/") + "/auth/oidc/callback",
		Scopes:       strings.Fields(cfg.Scopes),
	}
}

func (a *API) routesOIDC(mux *http.ServeMux) {
	// Public: does the login page show an SSO button, and what does it say?
	mux.HandleFunc("GET /api/sso", func(w http.ResponseWriter, r *http.Request) {
		cfg := a.oidcSettings()
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": cfg.ready(),
			"label":   cfg.Label,
		})
	})
	mux.HandleFunc("GET /auth/oidc/login", a.handleOIDCLogin)
	mux.HandleFunc("GET /auth/oidc/callback", a.handleOIDCCallback)

	// Admin config.
	mux.HandleFunc("GET /api/application/sso", a.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		cfg := a.oidcSettings()
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":        cfg.Enabled,
			"issuer":         cfg.Issuer,
			"client_id":      cfg.ClientID,
			"client_secret":  maskSecret(cfg.ClientSecret),
			"scopes":         cfg.Scopes,
			"label":          cfg.Label,
			"link_by_email":  cfg.LinkByEmail,
			"redirect_uri":   strings.TrimRight(a.PanelURL(), "/") + "/auth/oidc/callback",
			"secret_present": cfg.ClientSecret != "",
		})
	}))
	mux.HandleFunc("PUT /api/application/sso", a.requireAdmin(a.handleOIDCSave))
}

func (a *API) handleOIDCSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled      bool   `json:"enabled"`
		Issuer       string `json:"issuer"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Scopes       string `json:"scopes"`
		Label        string `json:"label"`
		LinkByEmail  bool   `json:"link_by_email"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	s := a.Store
	if body.Enabled {
		s.SetSetting("oidc:enabled", "1")
	} else {
		s.SetSetting("oidc:enabled", "0")
	}
	s.SetSetting("oidc:issuer", strings.TrimRight(strings.TrimSpace(body.Issuer), "/"))
	s.SetSetting("oidc:client_id", strings.TrimSpace(body.ClientID))
	// Only overwrite the secret when a new (non-masked) value is supplied.
	if body.ClientSecret != "" && !isMasked(body.ClientSecret) {
		s.SetSetting("oidc:client_secret", body.ClientSecret)
	}
	if body.Scopes != "" {
		s.SetSetting("oidc:scopes", body.Scopes)
	}
	if body.Label != "" {
		s.SetSetting("oidc:label", body.Label)
	}
	if body.LinkByEmail {
		s.SetSetting("oidc:link_by_email", "1")
	} else {
		s.SetSetting("oidc:link_by_email", "0")
	}
	// Discovery may have changed — drop the cached provider.
	oidcProviderMu.Lock()
	oidcProviderCache = map[string]*oidc.Provider{}
	oidcProviderMu.Unlock()
	writeNoContent(w)
}

func (a *API) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	cfg := a.oidcSettings()
	if !cfg.ready() {
		writeError(w, http.StatusServiceUnavailable, "Single sign-on is not configured.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	provider, err := a.oidcProvider(ctx, cfg.Issuer)
	if err != nil {
		writeError(w, http.StatusBadGateway, "The identity provider could not be reached.")
		return
	}
	state := auth.RandomAlnum(32)
	nonce := auth.RandomAlnum(32)
	setShortCookie(w, "oidc_state", state)
	setShortCookie(w, "oidc_nonce", nonce)
	url := a.oauth2Config(provider, cfg).AuthCodeURL(state, oidc.Nonce(nonce))
	http.Redirect(w, r, url, http.StatusFound)
}

func (a *API) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	cfg := a.oidcSettings()
	if !cfg.ready() {
		oidcFail(w, r, "Single sign-on is not configured.")
		return
	}
	// CSRF: the state we set must come back unchanged.
	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || r.URL.Query().Get("state") != stateCookie.Value {
		oidcFail(w, r, "The sign-in request could not be validated.")
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		oidcFail(w, r, "The identity provider reported an error: "+e)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	provider, err := a.oidcProvider(ctx, cfg.Issuer)
	if err != nil {
		oidcFail(w, r, "The identity provider could not be reached.")
		return
	}
	oauth2Cfg := a.oauth2Config(provider, cfg)
	token, err := oauth2Cfg.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		oidcFail(w, r, "The authorization code could not be exchanged.")
		return
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		oidcFail(w, r, "The identity provider did not return an ID token.")
		return
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	idToken, err := verifier.Verify(ctx, rawID)
	if err != nil {
		oidcFail(w, r, "The ID token could not be verified.")
		return
	}
	nonceCookie, err := r.Cookie("oidc_nonce")
	if err != nil || idToken.Nonce != nonceCookie.Value {
		oidcFail(w, r, "The sign-in nonce did not match.")
		return
	}

	var claims struct {
		Sub               string `json:"sub"`
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		Name              string `json:"name"`
		GivenName         string `json:"given_name"`
		FamilyName        string `json:"family_name"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := idToken.Claims(&claims); err != nil || claims.Sub == "" {
		oidcFail(w, r, "The identity provider returned no usable profile.")
		return
	}

	user, err := a.resolveOIDCUser(cfg, &claims)
	if err != nil {
		oidcFail(w, r, err.Error())
		return
	}
	clearShortCookie(w, "oidc_state")
	clearShortCookie(w, "oidc_nonce")
	a.issueSession(w, r, user)
	a.Store.LogActivity(&store.ActivityLog{Event: "auth:sso", IP: clientIP(r), ActorID: &user.ID, Properties: "{}"})
	http.Redirect(w, r, "/", http.StatusFound)
}

// oidcClaims is the minimal profile we read from the ID token.
type oidcClaims = struct {
	Sub               string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	Name              string `json:"name"`
	GivenName         string `json:"given_name"`
	FamilyName        string `json:"family_name"`
	PreferredUsername string `json:"preferred_username"`
}

// resolveOIDCUser links the assertion to a local account: by OIDC subject
// first, then (if enabled) by verified email, otherwise it provisions a new
// non-admin account.
func (a *API) resolveOIDCUser(cfg oidcSettings, claims *oidcClaims) (*store.User, error) {
	extID := "oidc:" + claims.Sub
	if u, err := a.Store.UserByExternalID(extID); err == nil {
		return u, nil
	}

	email := strings.TrimSpace(strings.ToLower(claims.Email))
	if cfg.LinkByEmail && email != "" && claims.EmailVerified {
		if u, err := a.Store.UserByEmail(email); err == nil {
			u.ExternalID = &extID
			a.Store.UpdateUser(u)
			return u, nil
		}
	}

	if email == "" {
		return nil, errConfig("the identity provider did not supply an email address")
	}
	username := claims.PreferredUsername
	if username == "" {
		username = strings.Split(email, "@")[0]
	}
	username = uniqueUsername(a.Store, sanitizeUsername(username))

	pw, _ := auth.HashPassword(auth.RandomAlnum(32))
	u := &store.User{
		ExternalID: &extID, UUID: auth.UUID(), Username: username, Email: email,
		NameFirst: firstOr(claims.GivenName, claims.Name), NameLast: claims.FamilyName,
		Password: pw, Language: "en",
	}
	if err := a.Store.CreateUser(u); err != nil {
		return nil, errConfig("could not create a local account for this identity")
	}
	return u, nil
}

// oidcFail redirects back to the login page with an error banner rather than
// dumping a raw error, since this endpoint is reached via the browser.
func oidcFail(w http.ResponseWriter, r *http.Request, msg string) {
	clearShortCookie(w, "oidc_state")
	clearShortCookie(w, "oidc_nonce")
	http.Redirect(w, r, "/?sso_error="+url.QueryEscape(msg), http.StatusFound)
}

func setShortCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})
}

func clearShortCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return "••••••••"
}

func isMasked(s string) bool { return strings.Trim(s, "•") == "" }

func firstOr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func sanitizeUsername(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		out = "user"
	}
	return out
}

// uniqueUsername appends a numeric suffix until the username is free.
func uniqueUsername(s *store.Store, base string) string {
	if _, err := s.UserByUsername(base); err != nil {
		return base
	}
	for i := 1; ; i++ {
		cand := base + strconv.Itoa(i)
		if _, err := s.UserByUsername(cand); err != nil {
			return cand
		}
	}
}
