// Package api exposes the REST surface consumed by the TypeScript frontend.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"roost/internal/dbviewer/session"
)

// maxBodyBytes caps request bodies (large .sql imports still fit comfortably)
// to prevent a single request from exhausting server memory.
const maxBodyBytes = 64 << 20 // 64 MiB

// queryRowCap bounds the number of rows an ad-hoc /query result loads into
// memory, so a `SELECT *` on a huge table can't OOM the backend.
const queryRowCap = 100_000

// Config tunes API-level security knobs.
type Config struct {
	// AllowHosts, when non-empty, restricts which database hosts a client may
	// connect to (mitigates using the server as an SSRF/port-scan proxy).
	AllowHosts []string
}

// API holds shared dependencies for the handlers.
type API struct {
	sessions    *session.Store
	mux         *http.ServeMux
	allowHosts  map[string]bool
	connLimiter *rateLimiter
}

// New builds the API handler with all routes registered.
func New(sessions *session.Store, cfg Config) *API {
	allow := make(map[string]bool, len(cfg.AllowHosts))
	for _, h := range cfg.AllowHosts {
		allow[h] = true
	}
	a := &API{
		sessions:    sessions,
		mux:         http.NewServeMux(),
		allowHosts:  allow,
		connLimiter: newRateLimiter(12, time.Minute), // 12 connect attempts / IP / min
	}
	a.routes()
	return a
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Cap request bodies to bound memory — except /import, which is streamed
	// statement-by-statement and may be an arbitrarily large dump.
	if r.URL.Path != "/import" {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	}
	a.mux.ServeHTTP(w, r)
}

// hostAllowed reports whether a client may open a connection to host. An empty
// allowlist permits any host (documented SSRF trade-off for self-hosting).
func (a *API) hostAllowed(host string) bool {
	if len(a.allowHosts) == 0 {
		return true
	}
	return a.allowHosts[host]
}

func (a *API) routes() {
	// Connection lifecycle.
	a.mux.HandleFunc("POST /connect", a.handleConnect)
	a.mux.HandleFunc("POST /disconnect", a.withSession(a.handleDisconnect))
	a.mux.HandleFunc("GET /session", a.withSession(a.handleSession))

	// Server-level.
	a.mux.HandleFunc("GET /server/info", a.withSession(a.handleServerInfo))
	a.mux.HandleFunc("GET /databases", a.withSession(a.handleDatabases))
	a.mux.HandleFunc("POST /databases", a.withSession(a.handleCreateDatabase))
	a.mux.HandleFunc("DELETE /databases/{db}", a.withSession(a.handleDropDatabase))

	// Schema-level.
	a.mux.HandleFunc("GET /databases/{db}/tables", a.withSession(a.handleTables))
	a.mux.HandleFunc("GET /databases/{db}/export", a.withSession(a.handleExportDatabase))
	a.mux.HandleFunc("GET /databases/{db}/tables/{table}/columns", a.withSession(a.handleColumns))
	a.mux.HandleFunc("GET /databases/{db}/tables/{table}/indexes", a.withSession(a.handleIndexes))
	a.mux.HandleFunc("GET /databases/{db}/tables/{table}/foreign-keys", a.withSession(a.handleForeignKeys))
	a.mux.HandleFunc("GET /databases/{db}/tables/{table}/rows", a.withSession(a.handleBrowse))
	a.mux.HandleFunc("POST /databases/{db}/tables/{table}/search", a.withSession(a.handleSearch))
	a.mux.HandleFunc("GET /databases/{db}/tables/{table}/ddl", a.withSession(a.handleDDL))
	a.mux.HandleFunc("GET /databases/{db}/tables/{table}/export", a.withSession(a.handleExportTable))
	a.mux.HandleFunc("DELETE /databases/{db}/tables/{table}", a.withSession(a.handleDropTable))

	// Row mutations.
	a.mux.HandleFunc("POST /databases/{db}/tables/{table}/insert", a.withSession(a.handleInsertRow))
	a.mux.HandleFunc("POST /databases/{db}/tables/{table}/update", a.withSession(a.handleUpdateRow))
	a.mux.HandleFunc("POST /databases/{db}/tables/{table}/delete", a.withSession(a.handleDeleteRow))

	// Users & privileges.
	a.mux.HandleFunc("GET /users", a.withSession(a.handleUsers))
	a.mux.HandleFunc("POST /users", a.withSession(a.handleCreateUser))
	a.mux.HandleFunc("GET /users/{user}/{host}/grants", a.withSession(a.handleUserGrants))
	a.mux.HandleFunc("DELETE /users/{user}/{host}", a.withSession(a.handleDropUser))

	// Arbitrary SQL & import.
	a.mux.HandleFunc("POST /query", a.withSession(a.handleQuery))
	a.mux.HandleFunc("POST /import", a.withSession(a.handleImport))
}

// ---- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
