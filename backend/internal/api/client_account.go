package api

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"roost/internal/auth"
	"roost/internal/store"
)

func (a *API) accountAttributes(u *store.User) map[string]any {
	sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(u.Email))))
	return map[string]any{
		"id":          u.ID,
		"uuid":        u.UUID,
		"admin":       u.RootAdmin,
		"username":    u.Username,
		"email":       u.Email,
		"first_name":  u.NameFirst,
		"last_name":   u.NameLast,
		"language":    u.Language,
		"image":       "https://gravatar.com/avatar/" + hex.EncodeToString(sum[:]),
		"2fa_enabled": u.UseTOTP,
		"created_at":  u.CreatedAt,
		"updated_at":  u.UpdatedAt,
	}
}

func (a *API) routesClientAccount(mux *http.ServeMux) {
	h := a.requireUser
	mux.HandleFunc("GET /api/client/account", h(func(w http.ResponseWriter, r *http.Request) {
		writeItem(w, http.StatusOK, "user", a.accountAttributes(userFrom(r)))
	}))

	mux.HandleFunc("PUT /api/client/account/email", h(a.handleUpdateEmail))
	mux.HandleFunc("PUT /api/client/account/password", h(a.handleUpdatePassword))

	mux.HandleFunc("GET /api/client/account/two-factor", h(a.handleTwoFactorSetup))
	mux.HandleFunc("POST /api/client/account/two-factor", h(a.handleTwoFactorEnable))
	mux.HandleFunc("POST /api/client/account/two-factor/disable", h(a.handleTwoFactorDisable))

	mux.HandleFunc("GET /api/client/account/activity", h(func(w http.ResponseWriter, r *http.Request) {
		logs, _ := a.Store.ActivityForActor(userFrom(r).ID, 100)
		rows := make([]map[string]any, 0, len(logs))
		for _, l := range logs {
			rows = append(rows, trActivity(l))
		}
		writeList(w, r, "activity_log", rows)
	}))

	mux.HandleFunc("GET /api/client/account/api-keys", h(func(w http.ResponseWriter, r *http.Request) {
		keys, _ := a.Store.APIKeysForUser(userFrom(r).ID, store.KeyTypeAccount)
		rows := make([]map[string]any, 0, len(keys))
		for _, k := range keys {
			rows = append(rows, trAPIKey(k))
		}
		writeList(w, r, "api_key", rows)
	}))
	mux.HandleFunc("POST /api/client/account/api-keys", h(a.handleCreateAccountKey))
	mux.HandleFunc("DELETE /api/client/account/api-keys/{identifier}", h(func(w http.ResponseWriter, r *http.Request) {
		a.Store.DeleteAPIKey(userFrom(r).ID, r.PathValue("identifier"), store.KeyTypeAccount)
		a.activity(r, "user:api-key.delete", map[string]any{"identifier": r.PathValue("identifier")})
		writeNoContent(w)
	}))

	mux.HandleFunc("GET /api/client/account/ssh-keys", h(func(w http.ResponseWriter, r *http.Request) {
		keys, _ := a.Store.SSHKeysForUser(userFrom(r).ID)
		rows := make([]map[string]any, 0, len(keys))
		for _, k := range keys {
			rows = append(rows, trSSHKey(k))
		}
		writeList(w, r, "ssh_key", rows)
	}))
	mux.HandleFunc("POST /api/client/account/ssh-keys", h(a.handleCreateSSHKey))
	mux.HandleFunc("POST /api/client/account/ssh-keys/remove", h(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Fingerprint string `json:"fingerprint"`
		}
		decode(r, &body)
		a.Store.DeleteSSHKeyByFingerprint(userFrom(r).ID, body.Fingerprint)
		a.activity(r, "user:ssh-key.delete", map[string]any{"fingerprint": body.Fingerprint})
		writeNoContent(w)
	}))
}

func (a *API) handleUpdateEmail(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil || !strings.Contains(body.Email, "@") {
		writeError(w, http.StatusUnprocessableEntity, "A valid email address must be provided.")
		return
	}
	if !auth.CheckPassword(u.Password, body.Password) {
		writeError(w, http.StatusBadRequest, "The password provided was invalid for this account.")
		return
	}
	old := u.Email
	u.Email = body.Email
	if err := a.Store.UpdateUser(u); err != nil {
		writeError(w, http.StatusConflict, "That email address is already in use.")
		return
	}
	a.activity(r, "user:account.email-changed", map[string]any{"old": old, "new": body.Email})
	writeNoContent(w)
}

func (a *API) handleUpdatePassword(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body struct {
		Current  string `json:"current_password"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil || len(body.Password) < 8 {
		writeError(w, http.StatusUnprocessableEntity, "New password must be at least 8 characters long.")
		return
	}
	if !auth.CheckPassword(u.Password, body.Current) {
		writeError(w, http.StatusBadRequest, "The password provided was invalid for this account.")
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to hash password.")
		return
	}
	u.Password = hash
	a.Store.UpdateUser(u)
	a.activity(r, "user:account.password-changed", nil)
	writeNoContent(w)
}

// handleTwoFactorSetup returns a fresh secret + provisioning URI. The secret
// is kept server-side (unconfirmed) until the user verifies a code.
func (a *API) handleTwoFactorSetup(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	secret := auth.NewTOTPSecret()
	a.Store.SetSetting(fmt.Sprintf("totp_pending:%d", u.ID), secret)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"image_url_data": auth.TOTPUri(a.AppName(), u.Email, secret),
			"secret":         secret,
		},
	})
}

func (a *API) handleTwoFactorEnable(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	secret := a.Store.Setting(fmt.Sprintf("totp_pending:%d", u.ID), "")
	if secret == "" || !auth.VerifyTOTP(secret, body.Code) {
		writeError(w, http.StatusBadRequest, "The token provided is not valid.")
		return
	}
	// Recovery tokens are shown once and stored hashed.
	tokens := make([]string, 10)
	hashes := make([]string, 10)
	for i := range tokens {
		raw := sha256.Sum256([]byte(auth.RandomHex(16)))
		tokens[i] = strings.ToUpper(base64.RawURLEncoding.EncodeToString(raw[:6]))
		hashes[i] = auth.SHA256Hex(tokens[i])
	}
	a.Store.ReplaceRecoveryTokens(u.ID, hashes)

	ts := nowISO()
	u.UseTOTP = true
	u.TOTPSecret = &secret
	u.TOTPAuthenticatedAt = &ts
	a.Store.UpdateUser(u)
	a.Store.SetSetting(fmt.Sprintf("totp_pending:%d", u.ID), "")
	a.activity(r, "user:account.two-factor.create", nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"object":     "recovery_tokens",
		"attributes": map[string]any{"tokens": tokens},
	})
}

func (a *API) handleTwoFactorDisable(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil || !auth.CheckPassword(u.Password, body.Password) {
		writeError(w, http.StatusBadRequest, "The password provided was invalid for this account.")
		return
	}
	u.UseTOTP = false
	u.TOTPSecret = nil
	a.Store.UpdateUser(u)
	a.Store.ReplaceRecoveryTokens(u.ID, nil)
	a.activity(r, "user:account.two-factor.delete", nil)
	writeNoContent(w)
}

func (a *API) handleCreateAccountKey(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body struct {
		Description string   `json:"description"`
		AllowedIPs  []string `json:"allowed_ips"`
	}
	if err := decode(r, &body); err != nil || body.Description == "" {
		writeError(w, http.StatusUnprocessableEntity, "A description for the key must be provided.")
		return
	}
	keys, _ := a.Store.APIKeysForUser(u.ID, store.KeyTypeAccount)
	if len(keys) >= 25 {
		writeError(w, http.StatusBadRequest, "You have reached the account limit for number of API keys.")
		return
	}
	identifier := "ptlc_" + auth.RandomAlnum(11) // 16 chars total
	secret := auth.RandomAlnum(32)
	ips, _ := json.Marshal(orEmpty(body.AllowedIPs))
	key := &store.APIKey{
		UserID: u.ID, KeyType: store.KeyTypeAccount, Identifier: identifier,
		TokenHash: auth.SHA256Hex(secret), Memo: body.Description, AllowedIPs: string(ips),
	}
	if err := a.Store.CreateAPIKey(key); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create key.")
		return
	}
	a.activity(r, "user:api-key.create", map[string]any{"identifier": identifier})
	attrs := trAPIKey(key)
	writeJSON(w, http.StatusOK, map[string]any{
		"object":     "api_key",
		"attributes": attrs,
		"meta":       map[string]any{"secret_token": identifier + secret},
	})
}

func (a *API) handleCreateSSHKey(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if err := decode(r, &body); err != nil || body.Name == "" || body.PublicKey == "" {
		writeError(w, http.StatusUnprocessableEntity, "A name and public key must be provided.")
		return
	}
	fields := strings.Fields(body.PublicKey)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "ssh-") && !strings.HasPrefix(fields[0], "ecdsa-") {
		writeError(w, http.StatusUnprocessableEntity, "The public key provided is not valid.")
		return
	}
	raw, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "The public key provided is not valid.")
		return
	}
	sum := sha256.Sum256(raw)
	fingerprint := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	key := &store.SSHKey{UserID: u.ID, Name: body.Name, Fingerprint: fingerprint, PublicKey: body.PublicKey}
	if err := a.Store.CreateSSHKey(key); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to store key.")
		return
	}
	a.activity(r, "user:ssh-key.create", map[string]any{"fingerprint": fingerprint})
	writeItem(w, http.StatusOK, "ssh_key", trSSHKey(key))
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
