package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"roost/internal/tlsmgr"
)

// Automatic HTTPS configuration. The panel stores the ACME settings and the
// binary builds a certificate manager from them at boot, so applying a change
// needs a restart — the UI says so explicitly.

// TLSSettings is the stored ACME configuration.
type TLSSettings struct {
	Enabled bool   `json:"enabled"`
	Domain  string `json:"domain"`
	Email   string `json:"email"`
	Staging bool   `json:"staging"`
}

func (a *API) TLSSettings() TLSSettings {
	return TLSSettings{
		Enabled: a.Store.Setting("tls:enabled", "") == "1",
		Domain:  a.Store.Setting("tls:domain", ""),
		Email:   a.Store.Setting("tls:email", ""),
		Staging: a.Store.Setting("tls:staging", "") == "1",
	}
}

// SetTLSManager lets main hand the live certificate manager to the API so the
// admin UI can report issuance status and force a certificate request.
func (a *API) SetTLSManager(m *tlsmgr.Manager) { a.tls = m }

func (a *API) routesTLS(mux *http.ServeMux) {
	h := a.requireAdmin

	mux.HandleFunc("GET /api/application/tls", h(func(w http.ResponseWriter, r *http.Request) {
		cfg := a.TLSSettings()
		out := map[string]any{
			"enabled": cfg.Enabled,
			"domain":  cfg.Domain,
			"email":   cfg.Email,
			"staging": cfg.Staging,
			"active":  a.tls != nil,
		}
		if a.tls != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			issued, notAfter, err := a.tls.Status(ctx)
			out["certificate_issued"] = issued
			if issued {
				out["expires_at"] = notAfter.UTC().Format(time.RFC3339)
				out["days_remaining"] = int(time.Until(notAfter).Hours() / 24)
			}
			if err != nil {
				out["error"] = err.Error()
			}
		}
		writeJSON(w, http.StatusOK, out)
	}))

	mux.HandleFunc("PUT /api/application/tls", h(func(w http.ResponseWriter, r *http.Request) {
		var body TLSSettings
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
			return
		}
		body.Domain = strings.TrimSpace(strings.ToLower(body.Domain))
		body.Email = strings.TrimSpace(body.Email)

		if body.Enabled {
			if err := tlsmgr.ValidateDomain(body.Domain); err != nil {
				writeError(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
			if err := tlsmgr.ValidateEmail(body.Email); err != nil {
				writeError(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
		}

		a.Store.SetSetting("tls:enabled", boolSetting(body.Enabled))
		a.Store.SetSetting("tls:domain", body.Domain)
		a.Store.SetSetting("tls:email", body.Email)
		a.Store.SetSetting("tls:staging", boolSetting(body.Staging))

		// Keep the panel URL (baked into wings tokens) in step with the
		// certificate, otherwise wings would be told to call an http:// panel.
		if body.Enabled {
			a.Store.SetSetting("app:url", "https://"+body.Domain)
		}

		a.activity(r, "admin:tls.update", map[string]any{"domain": body.Domain, "enabled": body.Enabled})
		writeJSON(w, http.StatusOK, map[string]any{
			"data":             body,
			"restart_required": true,
		})
	}))

	// Force an ACME order now rather than waiting for the first handshake, so
	// misconfiguration surfaces here instead of in a browser error.
	mux.HandleFunc("POST /api/application/tls/request", h(func(w http.ResponseWriter, r *http.Request) {
		if a.tls == nil {
			writeError(w, http.StatusBadRequest,
				"Automatic HTTPS is not running. Save the settings, then restart Roost with ports 80 and 443 available.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		if err := a.tls.Prime(ctx); err != nil {
			writeError(w, http.StatusBadGateway, "Certificate request failed: "+err.Error())
			return
		}
		issued, notAfter, _ := a.tls.Status(ctx)
		a.activity(r, "admin:tls.request", map[string]any{"domain": a.tls.Domain()})
		writeJSON(w, http.StatusOK, map[string]any{
			"certificate_issued": issued,
			"expires_at":         notAfter.UTC().Format(time.RFC3339),
		})
	}))
}

func boolSetting(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
