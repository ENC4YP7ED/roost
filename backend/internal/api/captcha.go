package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Pluggable CAPTCHA layer. Admins can enable any combination of providers —
// including several at once ("double-layering") or the same provider twice
// with different keys — and every enabled layer must be solved on login.
// Verification is uniform: all supported providers implement the same
// siteverify POST protocol.

type captchaProvider struct {
	ID       int    `json:"id"`
	Provider string `json:"provider"` // turnstile | recaptcha | hcaptcha
	Mode     string `json:"mode"`     // visible | invisible
	SiteKey  string `json:"site_key"`
	Secret   string `json:"secret,omitempty"`
}

var captchaVerifyURLs = map[string]string{
	"turnstile": "https://challenges.cloudflare.com/turnstile/v0/siteverify",
	"recaptcha": "https://www.google.com/recaptcha/api/siteverify",
	"hcaptcha":  "https://api.hcaptcha.com/siteverify",
}

func (a *API) captchaProviders() []captchaProvider {
	var providers []captchaProvider
	json.Unmarshal([]byte(a.Store.Setting("captcha:providers", "[]")), &providers)
	return providers
}

// verifyCaptchaLayers checks one token per configured layer, keyed by layer
// id. Every layer must pass for the request to proceed.
func (a *API) verifyCaptchaLayers(tokens map[string]string, ip string) error {
	providers := a.captchaProviders()
	if len(providers) == 0 {
		return nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, p := range providers {
		token := tokens[fmt.Sprintf("%d", p.ID)]
		if token == "" {
			return fmt.Errorf("captcha challenge %q was not completed", p.Provider)
		}
		endpoint, ok := captchaVerifyURLs[p.Provider]
		if !ok {
			return fmt.Errorf("unknown captcha provider %q", p.Provider)
		}
		form := url.Values{"secret": {p.Secret}, "response": {token}, "remoteip": {ip}}
		res, err := client.PostForm(endpoint, form)
		if err != nil {
			return fmt.Errorf("captcha provider %q unreachable: %w", p.Provider, err)
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
		res.Body.Close()
		var body struct {
			Success bool `json:"success"`
		}
		if json.Unmarshal(raw, &body) != nil || !body.Success {
			return fmt.Errorf("captcha verification failed for %q", p.Provider)
		}
	}
	return nil
}

func (a *API) routesCaptcha(mux *http.ServeMux) {
	// Public: what the login page must render. Secrets never leave the server.
	mux.HandleFunc("GET /auth/captcha", func(w http.ResponseWriter, r *http.Request) {
		providers := a.captchaProviders()
		public := make([]map[string]any, 0, len(providers))
		for _, p := range providers {
			public = append(public, map[string]any{
				"id": p.ID, "provider": p.Provider, "mode": p.Mode, "site_key": p.SiteKey,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": public})
	})

	// Admin management. GET returns secrets so the form can be re-edited;
	// this endpoint is root-admin only.
	mux.HandleFunc("GET /api/application/captcha", a.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": a.captchaProviders()})
	}))
	mux.HandleFunc("PUT /api/application/captcha", a.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		var providers []captchaProvider
		if err := decode(r, &providers); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
			return
		}
		for i := range providers {
			providers[i].ID = i + 1
			providers[i].Provider = strings.ToLower(strings.TrimSpace(providers[i].Provider))
			if _, ok := captchaVerifyURLs[providers[i].Provider]; !ok {
				writeError(w, http.StatusUnprocessableEntity,
					fmt.Sprintf("Unsupported provider %q — use turnstile, recaptcha or hcaptcha.", providers[i].Provider))
				return
			}
			if providers[i].SiteKey == "" || providers[i].Secret == "" {
				writeError(w, http.StatusUnprocessableEntity, "Every layer needs a site key and a secret key.")
				return
			}
			switch providers[i].Mode {
			case "", "visible":
				providers[i].Mode = "visible"
			case "invisible":
			default:
				writeError(w, http.StatusUnprocessableEntity, "Layer mode must be visible or invisible.")
				return
			}
		}
		raw, _ := json.Marshal(providers)
		a.Store.SetSetting("captcha:providers", string(raw))
		a.activity(r, "admin:captcha.update", map[string]any{"layers": len(providers)})
		writeJSON(w, http.StatusOK, map[string]any{"data": providers})
	}))
}
