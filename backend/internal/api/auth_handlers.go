package api

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"roost/internal/auth"
	"roost/internal/store"
)

const sessionTTL = 12 * time.Hour

func (a *API) routesAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/login", throttle(loginLimiter, a.handleLogin))
	mux.HandleFunc("POST /auth/login/checkpoint", throttle(loginLimiter, a.handleLoginCheckpoint))
	mux.HandleFunc("POST /auth/logout", a.handleLogout)
	mux.HandleFunc("POST /auth/password", a.handleForgotPassword)
	mux.HandleFunc("POST /auth/password/reset", a.handleResetPassword)
}

// pendingLogins holds users who passed password auth but still owe a TOTP
// code, keyed by a one-time confirmation token.
type pendingLogin struct {
	userID  int64
	expires time.Time
}

var (
	pendingMu     sync.Mutex
	pendingLogins = map[string]pendingLogin{}
)

func (a *API) issueSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	token := auth.RandomAlnum(40)
	a.Store.CreateSession(&store.Session{
		TokenHash: auth.SHA256Hex(token),
		UserID:    u.ID,
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
		ExpiresAt: time.Now().UTC().Add(sessionTTL).Format(time.RFC3339),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User          string            `json:"user"`
		Password      string            `json:"password"`
		CaptchaTokens map[string]string `json:"captcha_tokens"`
	}
	if err := decode(r, &body); err != nil || body.User == "" || body.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "A username or email and password must be provided.")
		return
	}
	if err := a.verifyCaptchaLayers(body.CaptchaTokens, clientIP(r)); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	u, err := a.Store.UserByEmail(body.User)
	if err != nil {
		u, err = a.Store.UserByUsername(body.User)
	}
	if err != nil || !auth.CheckPassword(u.Password, body.Password) {
		a.Store.LogActivity(&store.ActivityLog{Event: "auth:fail", IP: clientIP(r), Properties: `{"using":"password"}`})
		writeError(w, http.StatusUnauthorized, "These credentials do not match our records.")
		return
	}

	if u.UseTOTP {
		token := auth.RandomAlnum(64)
		pendingMu.Lock()
		pendingLogins[token] = pendingLogin{userID: u.ID, expires: time.Now().Add(5 * time.Minute)}
		pendingMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"complete":           false,
				"confirmation_token": token,
			},
		})
		return
	}

	a.completeLogin(w, r, u)
}

func (a *API) handleLoginCheckpoint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConfirmationToken  string `json:"confirmation_token"`
		AuthenticationCode string `json:"authentication_code"`
		RecoveryToken      string `json:"recovery_token"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	pendingMu.Lock()
	pending, ok := pendingLogins[body.ConfirmationToken]
	pendingMu.Unlock()
	if !ok || time.Now().After(pending.expires) {
		writeError(w, http.StatusUnauthorized, "The provided confirmation token is invalid or has expired.")
		return
	}
	u, err := a.Store.UserByID(pending.userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Account not found.")
		return
	}

	authenticated := false
	if body.RecoveryToken != "" {
		authenticated, _ = a.Store.ConsumeRecoveryToken(u.ID, auth.SHA256Hex(body.RecoveryToken))
	} else if u.TOTPSecret != nil {
		authenticated = auth.VerifyTOTP(*u.TOTPSecret, body.AuthenticationCode)
	}
	if !authenticated {
		a.Store.LogActivity(&store.ActivityLog{Event: "auth:fail", IP: clientIP(r), ActorID: &u.ID, Properties: `{"using":"two_factor"}`})
		writeError(w, http.StatusUnauthorized, "The provided two-factor authentication token was not valid.")
		return
	}
	pendingMu.Lock()
	delete(pendingLogins, body.ConfirmationToken)
	pendingMu.Unlock()
	a.completeLogin(w, r, u)
}

func (a *API) completeLogin(w http.ResponseWriter, r *http.Request, u *store.User) {
	a.issueSession(w, r, u)
	a.Store.LogActivity(&store.ActivityLog{Event: "auth:success", IP: clientIP(r), ActorID: &u.ID, Properties: "{}"})
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"complete": true,
			"intended": "/",
			"user":     a.accountAttributes(u),
		},
	})
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		a.Store.DeleteSession(auth.SHA256Hex(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	writeNoContent(w)
}

// handleForgotPassword mints a reset token. Without an SMTP relay the token
// is written to the panel's log so an operator can hand it to the user.
func (a *API) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	decode(r, &body)
	if u, err := a.Store.UserByEmail(body.Email); err == nil {
		token := auth.RandomAlnum(48)
		a.Store.SetSetting("password_reset:"+u.Email, auth.SHA256Hex(token)+"|"+time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
		log.Printf("password reset requested for %s — token: %s", u.Email, token)
	}
	// Always answer the same way to avoid account enumeration.
	writeJSON(w, http.StatusOK, map[string]any{"status": "If that account exists, a reset token has been issued (check the panel log)."})
}

func (a *API) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil || len(body.Password) < 8 {
		writeError(w, http.StatusUnprocessableEntity, "Password must be at least 8 characters.")
		return
	}
	stored := a.Store.Setting("password_reset:"+body.Email, "")
	if stored == "" {
		writeError(w, http.StatusUnauthorized, "The provided reset token is invalid.")
		return
	}
	hash, expiry, _ := strings.Cut(stored, "|")
	exp, err := time.Parse(time.RFC3339, expiry)
	if err != nil || time.Now().After(exp) || auth.SHA256Hex(body.Token) != hash {
		writeError(w, http.StatusUnauthorized, "The provided reset token is invalid or has expired.")
		return
	}
	u, err := a.Store.UserByEmail(body.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "The provided reset token is invalid.")
		return
	}
	pw, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to hash password.")
		return
	}
	u.Password = pw
	a.Store.UpdateUser(u)
	a.Store.SetSetting("password_reset:"+body.Email, "")
	a.issueSession(w, r, u)
	writeJSON(w, http.StatusOK, map[string]any{"redirect_to": "/"})
}
