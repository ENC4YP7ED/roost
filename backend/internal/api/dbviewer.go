package api

import (
	"net/http"
	"strings"
	"time"

	dbapi "roost/internal/dbviewer/api"
	"roost/internal/dbviewer/session"
	"roost/internal/store"
)

// The built-in database viewer (vendored from GoTypeMyAdmin) is mounted at
// /dbviewer — its SPA under /dbviewer/ and its REST API under /dbviewer/api.
// Both are gated behind Roost's root-admin auth: the viewer opens raw MySQL /
// MariaDB connections, so it must never be reachable by a normal panel user.

// DBViewer wires the vendored viewer into Roost's router.
type DBViewer struct {
	api      http.Handler
	sessions *session.Store
}

// NewDBViewer builds the viewer with a session TTL and an optional allowlist
// of database hosts it may connect to (empty = any, an SSRF trade-off that
// only root admins can exercise).
func NewDBViewer(ttl time.Duration, allowHosts []string) *DBViewer {
	sessions := session.NewStore(ttl)
	return &DBViewer{
		api:      dbapi.New(sessions, dbapi.Config{AllowHosts: allowHosts}),
		sessions: sessions,
	}
}

func (d *DBViewer) Close() {
	if d.sessions != nil {
		d.sessions.Close()
	}
}

// Mount registers the viewer's API and SPA behind the admin gate. staticFS
// serves the viewer's built assets.
func (a *API) MountDBViewer(mux *http.ServeMux, viewer *DBViewer, staticFS http.Handler) {
	if viewer == nil {
		return
	}
	// The vendored API's own mux expects paths rooted at "/", e.g. /databases.
	mux.Handle("/dbviewer/api/", a.requireAdmin(
		http.StripPrefix("/dbviewer/api", viewer.api).ServeHTTP))

	mux.HandleFunc("/dbviewer/", a.requireAdminPage(func(w http.ResponseWriter, r *http.Request) {
		staticFS.ServeHTTP(w, r)
	}))
}

// requireAdminPage gates an HTML page rather than a JSON endpoint: an
// unauthenticated browser is redirected to the panel login instead of being
// handed a 401 body it cannot render.
func (a *API) requireAdminPage(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.authenticate(r, store.KeyTypeAccount)
		if u == nil || !u.RootAdmin {
			// Assets are fetched by an already-loaded page; only redirect
			// navigations, otherwise the SPA sees HTML where it wants JS.
			if strings.Contains(r.Header.Get("Accept"), "text/html") {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			writeError(w, http.StatusForbidden, "Administrative access is required for the database viewer.")
			return
		}
		next(w, r)
	}
}
