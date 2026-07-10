package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Extras that none of the ancestors (Pterodactyl) or siblings (Pelican,
// Pyrodactyl) ship in this combination: first-class webhooks, egg import
// straight from a URL, login throttling, and a public health probe.

// ---- login rate limiting ----

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
	limit   int
	window  time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{buckets: map[string][]time.Time{}, limit: limit, window: window}
}

// allow records an attempt for key and reports whether it is within limits.
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	kept := rl.buckets[key][:0]
	for _, t := range rl.buckets[key] {
		if now.Sub(t) < rl.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= rl.limit {
		rl.buckets[key] = kept
		return false
	}
	rl.buckets[key] = append(kept, now)
	return true
}

var loginLimiter = newRateLimiter(10, time.Minute)

// throttle wraps a handler with a per-IP request cap.
func throttle(rl *rateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "Too many attempts; please wait a minute and try again.")
			return
		}
		next(w, r)
	}
}

// ---- webhooks ----

type webhook struct {
	ID     int64    `json:"id"`
	URL    string   `json:"url"`
	Events []string `json:"events"` // prefixes; ["*"] = everything
}

func (a *API) webhooks() []webhook {
	var hooks []webhook
	json.Unmarshal([]byte(a.Store.Setting("webhooks", "[]")), &hooks)
	return hooks
}

// dispatchWebhooks posts the event to every matching endpoint, async.
func (a *API) dispatchWebhooks(event string, payload map[string]any) {
	hooks := a.webhooks()
	if len(hooks) == 0 {
		return
	}
	body, err := json.Marshal(map[string]any{
		"event":     event,
		"timestamp": nowISO(),
		"data":      payload,
	})
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, hook := range hooks {
		matched := false
		for _, ev := range hook.Events {
			if ev == "*" || ev == event || strings.HasPrefix(event, strings.TrimSuffix(ev, "*")) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		go func(url string) {
			req, err := http.NewRequest("POST", url, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "Roost-Webhook/1.0")
			if res, err := client.Do(req); err == nil {
				res.Body.Close()
			}
		}(hook.URL)
	}
}

func (a *API) routesExtras(mux *http.ServeMux) {
	h := a.requireAdmin

	// Public health probe (for uptime monitors / load balancers).
	mux.HandleFunc("GET /api/system/health", func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.Store.CountUsers(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "detail": "database unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "name": a.AppName()})
	})

	// Webhook management.
	mux.HandleFunc("GET /api/application/webhooks", h(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": a.webhooks()})
	}))
	mux.HandleFunc("PUT /api/application/webhooks", h(func(w http.ResponseWriter, r *http.Request) {
		var hooks []webhook
		if err := decode(r, &hooks); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
			return
		}
		for i := range hooks {
			hooks[i].ID = int64(i + 1)
			if !strings.HasPrefix(hooks[i].URL, "http://") && !strings.HasPrefix(hooks[i].URL, "https://") {
				writeError(w, http.StatusUnprocessableEntity, "Webhook URLs must be http(s).")
				return
			}
			if len(hooks[i].Events) == 0 {
				hooks[i].Events = []string{"*"}
			}
		}
		raw, _ := json.Marshal(hooks)
		a.Store.SetSetting("webhooks", string(raw))
		writeJSON(w, http.StatusOK, map[string]any{"data": hooks})
	}))

	// Import an egg directly from a URL (PTDL_v1/v2 JSON).
	mux.HandleFunc("POST /api/application/nests/{id}/eggs/import-url", h(func(w http.ResponseWriter, r *http.Request) {
		nest, err := a.Store.NestByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Nest not found.")
			return
		}
		var body struct {
			URL string `json:"url"`
		}
		if err := decode(r, &body); err != nil || !strings.HasPrefix(body.URL, "http") {
			writeError(w, http.StatusUnprocessableEntity, "A valid URL must be provided.")
			return
		}
		client := &http.Client{Timeout: 20 * time.Second}
		res, err := client.Get(body.URL)
		if err != nil {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("Could not fetch the egg: %v", err))
			return
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("The remote server responded with HTTP %d.", res.StatusCode))
			return
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, 5<<20))
		if err != nil {
			writeError(w, http.StatusBadGateway, "Failed to read the egg file.")
			return
		}
		var doc EggDocument
		if err := json.Unmarshal(raw, &doc); err != nil || doc.Name == "" {
			writeError(w, http.StatusUnprocessableEntity, "The fetched file is not a valid egg export.")
			return
		}
		egg, err := a.ImportEgg(nest.ID, &doc)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		a.activity(r, "admin:egg.import", map[string]any{"name": egg.Name, "url": body.URL})
		writeItem(w, http.StatusCreated, "egg", trEgg(egg))
	}))
}
