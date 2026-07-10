package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"roost/internal/dbviewer/db"
	"roost/internal/dbviewer/session"
)

// sessionKey is the context key under which the active session is stashed.
type ctxKey int

const sessionCtxKey ctxKey = 0

// withSession resolves the bearer token to a live session before delegating.
func (a *API) withSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			writeErr(w, http.StatusUnauthorized, "missing session token")
			return
		}
		sess, ok := a.sessions.Get(token)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "session expired or invalid")
			return
		}
		ctx := context.WithValue(r.Context(), sessionCtxKey, sess)
		next(w, r.WithContext(ctx))
	}
}

func sess(r *http.Request) *session.Session {
	return r.Context().Value(sessionCtxKey).(*session.Session)
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	// Fall back to a ?token= query param so the browser can stream downloads
	// (export links) directly to disk, where it can't set an Authorization
	// header. The token isn't an ambient credential, so this doesn't open CSRF.
	return r.URL.Query().Get("token")
}

// ---- connection ------------------------------------------------------------

type connectReq struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

func (a *API) handleConnect(w http.ResponseWriter, r *http.Request) {
	if !a.connLimiter.allow(clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many connection attempts; slow down")
		return
	}
	var req connectReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Host == "" {
		req.Host = "127.0.0.1"
	}
	if req.Port == 0 {
		req.Port = 3306
	}
	if !a.hostAllowed(req.Host) {
		writeErr(w, http.StatusForbidden, "connections to this host are not permitted")
		return
	}

	pool, err := db.Open(r.Context(), req.Host, req.Port, req.User, req.Password)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "connection failed: "+err.Error())
		return
	}

	server := req.Host + ":" + strconv.Itoa(req.Port)
	s := a.sessions.Create(pool, "mysql", req.Host, req.Port, req.User, server)
	info, _ := db.Info(r.Context(), pool)

	writeJSON(w, http.StatusOK, map[string]any{
		"token":  s.Token,
		"server": server,
		"user":   req.User,
		"info":   info,
	})
}

func (a *API) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	a.sessions.Destroy(sess(r).Token)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) handleSession(w http.ResponseWriter, r *http.Request) {
	s := sess(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"server": s.Server,
		"user":   s.User,
	})
}

// ---- server / databases ----------------------------------------------------

func (a *API) handleServerInfo(w http.ResponseWriter, r *http.Request) {
	info, err := db.Info(r.Context(), sess(r).DB)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *API) handleDatabases(w http.ResponseWriter, r *http.Request) {
	list, err := db.Databases(r.Context(), sess(r).DB)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": list})
}

type createDBReq struct {
	Name      string `json:"name"`
	Charset   string `json:"charset"`
	Collation string `json:"collation"`
}

func (a *API) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	var req createDBReq
	if err := decode(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "database name required")
		return
	}
	stmt := "CREATE DATABASE " + db.QuoteIdent(req.Name)
	if req.Charset != "" {
		stmt += " CHARACTER SET " + db.QuoteIdent(req.Charset)
	}
	if req.Collation != "" {
		stmt += " COLLATE " + db.QuoteIdent(req.Collation)
	}
	if _, err := db.Exec(r.Context(), sess(r).DB, "", stmt, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) handleDropDatabase(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("db")
	if _, err := db.Exec(r.Context(), sess(r).DB, "", "DROP DATABASE "+db.QuoteIdent(name), 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- tables ----------------------------------------------------------------

func (a *API) handleTables(w http.ResponseWriter, r *http.Request) {
	list, err := db.Tables(r.Context(), sess(r).DB, r.PathValue("db"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": list})
}

func (a *API) handleColumns(w http.ResponseWriter, r *http.Request) {
	list, err := db.Columns(r.Context(), sess(r).DB, r.PathValue("db"), r.PathValue("table"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"columns": list})
}

func (a *API) handleIndexes(w http.ResponseWriter, r *http.Request) {
	list, err := db.Indexes(r.Context(), sess(r).DB, r.PathValue("db"), r.PathValue("table"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"indexes": list})
}

func (a *API) handleBrowse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := atoiDefault(q.Get("limit"), 50)
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	offset := atoiDefault(q.Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	rs, total, err := db.Browse(r.Context(), sess(r).DB,
		r.PathValue("db"), r.PathValue("table"), limit, offset, q.Get("orderBy"), q.Get("dir"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result": rs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (a *API) handleDDL(w http.ResponseWriter, r *http.Request) {
	ddl, err := db.CreateTableSQL(r.Context(), sess(r).DB, r.PathValue("db"), r.PathValue("table"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ddl": ddl})
}

func (a *API) handleDropTable(w http.ResponseWriter, r *http.Request) {
	schema := r.PathValue("db")
	table := r.PathValue("table")
	stmt := "DROP TABLE " + db.QuoteIdent(schema) + "." + db.QuoteIdent(table)
	if _, err := db.Exec(r.Context(), sess(r).DB, "", stmt, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- arbitrary SQL ---------------------------------------------------------

type queryReq struct {
	Database string `json:"database"`
	SQL      string `json:"sql"`
}

func (a *API) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req queryReq
	if err := decode(r, &req); err != nil || strings.TrimSpace(req.SQL) == "" {
		writeErr(w, http.StatusBadRequest, "sql statement required")
		return
	}
	rs, err := db.Exec(r.Context(), sess(r).DB, req.Database, req.SQL, queryRowCap)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": rs})
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
