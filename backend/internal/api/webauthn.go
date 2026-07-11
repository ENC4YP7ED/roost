package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"roost/internal/auth"
	"roost/internal/store"
)

// Passkeys (WebAuthn). Registration is done by a signed-in user adding an
// authenticator to their account; login is passwordless and discoverable, so
// the browser offers whichever passkey matches this relying party. The same
// ceremony doubles as a strong second factor.

// webauthnChallenge is a short-lived ceremony state (the server's expected
// challenge), keyed by a random token handed to the browser and returned on
// finish. Kept in memory like pendingLogins — single-binary, restart clears.
type webauthnChallenge struct {
	data    webauthn.SessionData
	expires time.Time
}

var (
	waChallengeMu sync.Mutex
	waChallenges  = map[string]webauthnChallenge{}
)

func putChallenge(sd *webauthn.SessionData) string {
	token := auth.RandomAlnum(40)
	waChallengeMu.Lock()
	waChallenges[token] = webauthnChallenge{data: *sd, expires: time.Now().Add(5 * time.Minute)}
	// Opportunistically evict expired ceremonies.
	for k, v := range waChallenges {
		if time.Now().After(v.expires) {
			delete(waChallenges, k)
		}
	}
	waChallengeMu.Unlock()
	return token
}

func takeChallenge(token string) (webauthn.SessionData, bool) {
	waChallengeMu.Lock()
	defer waChallengeMu.Unlock()
	c, ok := waChallenges[token]
	if ok {
		delete(waChallenges, token)
	}
	if !ok || time.Now().After(c.expires) {
		return webauthn.SessionData{}, false
	}
	return c.data, true
}

// webauthnInstance builds the relying-party config from the panel URL. RPID is
// the bare host; the origin is the scheme+host+port the browser will send.
func (a *API) webauthnInstance() (*webauthn.WebAuthn, error) {
	u, err := url.Parse(a.PanelURL())
	if err != nil {
		return nil, err
	}
	return webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: a.AppName(),
		RPOrigins:     []string{strings.TrimRight(u.Scheme+"://"+u.Host, "/")},
	})
}

// webauthnUser adapts a store user + its credentials to the library interface.
type webauthnUser struct {
	u     *store.User
	creds []webauthn.Credential
}

func (a *API) webauthnUser(u *store.User) *webauthnUser {
	rows, _ := a.Store.WebAuthnCredentials(u.ID)
	creds := make([]webauthn.Credential, 0, len(rows))
	for _, c := range rows {
		creds = append(creds, toWebauthnCredential(c))
	}
	return &webauthnUser{u: u, creds: creds}
}

func (w *webauthnUser) WebAuthnID() []byte   { return []byte(w.u.UUID) }
func (w *webauthnUser) WebAuthnName() string { return w.u.Username }
func (w *webauthnUser) WebAuthnDisplayName() string {
	return strings.TrimSpace(w.u.NameFirst + " " + w.u.NameLast)
}
func (w *webauthnUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

func toWebauthnCredential(c *store.WebAuthnCredential) webauthn.Credential {
	var transports []protocol.AuthenticatorTransport
	var names []string
	if json.Unmarshal([]byte(c.Transports), &names) == nil {
		for _, n := range names {
			transports = append(transports, protocol.AuthenticatorTransport(n))
		}
	}
	return webauthn.Credential{
		ID:              c.CredentialID,
		PublicKey:       c.PublicKey,
		AttestationType: c.Attestation,
		Transport:       transports,
		Flags: webauthn.CredentialFlags{
			BackupEligible: c.BackupEligible,
			BackupState:    c.BackupState,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:    c.AAGUID,
			SignCount: c.SignCount,
		},
	}
}

func (a *API) routesWebAuthn(mux *http.ServeMux) {
	h := a.requireUser

	// --- account: manage passkeys ---
	mux.HandleFunc("GET /api/client/account/passkeys", h(func(w http.ResponseWriter, r *http.Request) {
		rows, _ := a.Store.WebAuthnCredentials(userFrom(r).ID)
		out := make([]map[string]any, 0, len(rows))
		for _, c := range rows {
			out = append(out, trPasskey(c))
		}
		writeList(w, r, "passkey", out)
	}))
	mux.HandleFunc("POST /api/client/account/passkeys/register/begin", h(a.handlePasskeyRegisterBegin))
	mux.HandleFunc("POST /api/client/account/passkeys/register/finish", h(a.handlePasskeyRegisterFinish))
	mux.HandleFunc("PUT /api/client/account/passkeys/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		decode(r, &body)
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if strings.TrimSpace(body.Name) == "" {
			writeError(w, http.StatusUnprocessableEntity, "A name is required.")
			return
		}
		if err := a.Store.RenameWebAuthnCredential(userFrom(r).ID, id, body.Name); err != nil {
			writeError(w, http.StatusNotFound, "Passkey not found.")
			return
		}
		writeNoContent(w)
	}))
	mux.HandleFunc("DELETE /api/client/account/passkeys/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err := a.Store.DeleteWebAuthnCredential(userFrom(r).ID, id); err != nil {
			writeError(w, http.StatusNotFound, "Passkey not found.")
			return
		}
		a.activity(r, "user:passkey.delete", map[string]any{"id": id})
		writeNoContent(w)
	}))

	// --- public: passwordless login ---
	mux.HandleFunc("POST /auth/passkey/login/begin", throttle(loginLimiter, a.handlePasskeyLoginBegin))
	mux.HandleFunc("POST /auth/passkey/login/finish", throttle(loginLimiter, a.handlePasskeyLoginFinish))
}

func (a *API) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	wa, err := a.webauthnInstance()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "WebAuthn is not configured.")
		return
	}
	user := a.webauthnUser(userFrom(r))
	// Require a resident (discoverable) key with user verification so it can be
	// used both passwordless and as a second factor.
	rv := protocol.ResidentKeyRequirementRequired
	creation, sd, err := wa.BeginRegistration(user, webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
		ResidentKey:      rv,
		UserVerification: protocol.VerificationPreferred,
	}))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	token := putChallenge(sd)
	writeJSON(w, http.StatusOK, map[string]any{"session": token, "publicKey": creation.Response})
}

func (a *API) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Session  string          `json:"session"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	sd, ok := takeChallenge(body.Session)
	if !ok {
		writeError(w, http.StatusBadRequest, "This registration attempt has expired. Please try again.")
		return
	}
	wa, err := a.webauthnInstance()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "WebAuthn is not configured.")
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(body.Response))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "The authenticator response could not be parsed.")
		return
	}
	user := a.webauthnUser(userFrom(r))
	cred, err := wa.CreateCredential(user, sd, parsed)
	if err != nil {
		writeError(w, http.StatusBadRequest, "The passkey could not be verified.")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Passkey"
	}
	if err := a.Store.CreateWebAuthnCredential(fromWebauthnCredential(userFrom(r).ID, name, cred)); err != nil {
		writeError(w, http.StatusConflict, "This passkey is already registered.")
		return
	}
	a.activity(r, "user:passkey.create", map[string]any{"name": name})
	rows, _ := a.Store.WebAuthnCredentials(userFrom(r).ID)
	for _, c := range rows {
		if bytes.Equal(c.CredentialID, cred.ID) {
			writeItem(w, http.StatusOK, "passkey", trPasskey(c))
			return
		}
	}
	writeNoContent(w)
}

func (a *API) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	wa, err := a.webauthnInstance()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "WebAuthn is not configured.")
		return
	}
	assertion, sd, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	token := putChallenge(sd)
	writeJSON(w, http.StatusOK, map[string]any{"session": token, "publicKey": assertion.Response})
}

func (a *API) handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Session  string          `json:"session"`
		Response json.RawMessage `json:"response"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	sd, ok := takeChallenge(body.Session)
	if !ok {
		writeError(w, http.StatusBadRequest, "This sign-in attempt has expired. Please try again.")
		return
	}
	wa, err := a.webauthnInstance()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "WebAuthn is not configured.")
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(body.Response))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "The authenticator response could not be parsed.")
		return
	}

	// The user handle in the assertion is the account UUID we set as WebAuthnID.
	var matched *store.User
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		u, err := a.Store.UserByUUID(string(userHandle))
		if err != nil {
			return nil, err
		}
		matched = u
		return a.webauthnUser(u), nil
	}
	cred, err := wa.ValidateDiscoverableLogin(handler, sd, parsed)
	if err != nil || matched == nil {
		a.Store.LogActivity(&store.ActivityLog{Event: "auth:fail", IP: clientIP(r), Properties: `{"using":"passkey"}`})
		writeError(w, http.StatusUnauthorized, "This passkey was not recognised.")
		return
	}
	// Record the new signature counter (clone detection / replay defence).
	a.Store.TouchWebAuthnCredential(cred.ID, cred.Authenticator.SignCount)
	a.completeLogin(w, r, matched)
}

func fromWebauthnCredential(userID int64, name string, cred *webauthn.Credential) *store.WebAuthnCredential {
	names := make([]string, 0, len(cred.Transport))
	for _, t := range cred.Transport {
		names = append(names, string(t))
	}
	transports, _ := json.Marshal(names)
	return &store.WebAuthnCredential{
		UserID:         userID,
		Name:           name,
		CredentialID:   cred.ID,
		PublicKey:      cred.PublicKey,
		Attestation:    cred.AttestationType,
		AAGUID:         cred.Authenticator.AAGUID,
		SignCount:      cred.Authenticator.SignCount,
		Transports:     string(transports),
		BackupEligible: cred.Flags.BackupEligible,
		BackupState:    cred.Flags.BackupState,
	}
}

func trPasskey(c *store.WebAuthnCredential) map[string]any {
	return map[string]any{
		"id":           c.ID,
		"name":         c.Name,
		"created_at":   c.CreatedAt,
		"last_used_at": c.LastUsedAt,
		"backed_up":    c.BackupState,
	}
}
