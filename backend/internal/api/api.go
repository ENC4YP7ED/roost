// Package api implements the panel's HTTP surface: the Pterodactyl-compatible
// client (/api/client), application (/api/application) and remote
// (/api/remote) APIs, plus session auth for the SPA.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"roost/internal/auth"
	"roost/internal/store"
	"roost/internal/tlsmgr"
)

type API struct {
	Store *store.Store
	tls   *tlsmgr.Manager // nil unless automatic HTTPS is running
}

func New(s *store.Store) *API { return &API{Store: s} }

// PanelURL is what wings and JWT audiences see as this panel's address.
func (a *API) PanelURL() string {
	return a.Store.Setting("app:url", "http://localhost:8090")
}

func (a *API) AppName() string {
	return a.Store.Setting("app:name", "Roost")
}

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxAPIKey
	ctxServer
	ctxNode
)

func userFrom(r *http.Request) *store.User {
	u, _ := r.Context().Value(ctxUser).(*store.User)
	return u
}

func serverFrom(r *http.Request) *store.Server {
	s, _ := r.Context().Value(ctxServer).(*store.Server)
	return s
}

func nodeFrom(r *http.Request) *store.Node {
	n, _ := r.Context().Value(ctxNode).(*store.Node)
	return n
}

const sessionCookie = "roost_session"

// authenticate resolves the acting user from the session cookie or a bearer
// API key of the given type. Returns nil when unauthenticated.
func (a *API) authenticate(r *http.Request, keyType int) *store.User {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token := strings.TrimPrefix(h, "Bearer ")
		// API tokens look like ptlc_<random> / ptla_<random> with a leading
		// identifier segment, mirroring Pterodactyl's "identifier + token".
		if len(token) > 16 {
			ident := token[:16]
			key, err := a.Store.APIKeyByIdentifier(ident)
			if err == nil && key.KeyType == keyType &&
				auth.SHA256Hex(token[16:]) == key.TokenHash {
				if u, err := a.Store.UserByID(key.UserID); err == nil {
					a.Store.TouchAPIKey(key.ID)
					return u
				}
			}
		}
		return nil
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	u, err := a.Store.SessionUser(auth.SHA256Hex(cookie.Value))
	if err != nil {
		return nil
	}
	return u
}

// requireUser gates client-API routes: session cookie or ptlc_ API key.
func (a *API) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.authenticate(r, store.KeyTypeAccount)
		if u == nil {
			writeError(w, http.StatusUnauthorized, "Unauthenticated.")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxUser, u)))
	}
}

// requireAdmin gates application-API routes: root admin session or ptla_ key.
func (a *API) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.authenticate(r, store.KeyTypeApplication)
		if u == nil {
			// Admin SPA uses the session cookie, which authenticate() treats
			// as account-scope; accept it as long as the user is an admin.
			u = a.authenticate(r, store.KeyTypeAccount)
		}
		if u == nil {
			writeError(w, http.StatusUnauthorized, "Unauthenticated.")
			return
		}
		if !u.RootAdmin {
			writeError(w, http.StatusForbidden, "This action requires administrative privileges.")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxUser, u)))
	}
}

// requireNode gates the remote API wings calls with "Bearer <id>.<token>".
func (a *API) requireNode(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		id, token, ok := strings.Cut(h, ".")
		if !ok {
			writeError(w, http.StatusUnauthorized, "Invalid node token.")
			return
		}
		node, err := a.Store.NodeByTokenID(id)
		if err != nil || node.DaemonToken != token {
			writeError(w, http.StatusUnauthorized, "Invalid node token.")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxNode, node)))
	}
}

// withServer resolves {server} (short or long uuid) and checks the acting
// user can access it. Admins and owners get every permission; subusers get
// their stored set.
func (a *API) withServer(next http.HandlerFunc, perms ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r)
		srv, err := a.Store.ServerByIdentifier(r.PathValue("server"))
		if err != nil {
			writeError(w, http.StatusNotFound, "The requested server was not found.")
			return
		}
		if !u.RootAdmin && srv.OwnerID != u.ID {
			sub, err := a.Store.Subuser(srv.ID, u.ID)
			if err != nil {
				writeError(w, http.StatusNotFound, "The requested server was not found.")
				return
			}
			if len(perms) > 0 {
				var have []string
				json.Unmarshal([]byte(sub.Permissions), &have)
				for _, p := range perms {
					if !contains(have, p) {
						writeError(w, http.StatusForbidden, "You do not have permission to perform this action: "+p)
						return
					}
				}
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxServer, srv)))
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// userPermissions lists what the acting user can do on a server.
func (a *API) userPermissions(u *store.User, srv *store.Server) []string {
	if u.RootAdmin || srv.OwnerID == u.ID {
		all := make([]string, 0, len(AllPermissions)+2)
		all = append(all, "*")
		if u.RootAdmin {
			all = append(all, "admin.websocket.errors", "admin.websocket.install", "admin.websocket.transfer")
		}
		return all
	}
	sub, err := a.Store.Subuser(srv.ID, u.ID)
	if err != nil {
		return nil
	}
	var have []string
	json.Unmarshal([]byte(sub.Permissions), &have)
	return have
}

// AllPermissions mirrors Pterodactyl's Permission model.
var AllPermissions = []string{
	"websocket.connect",
	"control.console", "control.start", "control.stop", "control.restart",
	"user.create", "user.read", "user.update", "user.delete",
	"file.create", "file.read", "file.read-content", "file.update", "file.delete", "file.archive", "file.sftp",
	"backup.create", "backup.read", "backup.delete", "backup.download", "backup.restore",
	"allocation.read", "allocation.create", "allocation.update", "allocation.delete",
	"startup.read", "startup.update", "startup.docker-image",
	"database.create", "database.read", "database.update", "database.delete",
	"schedule.create", "schedule.read", "schedule.update", "schedule.delete",
	"settings.rename", "settings.reinstall",
	"activity.read",
}

// clientIP strips the port from RemoteAddr.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// decode reads a JSON body into dst with a size cap.
func decode(r *http.Request, dst any) error {
	defer io.Copy(io.Discard, r.Body)
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst)
}

// activity logs an event with the acting user attached.
func (a *API) activity(r *http.Request, event string, props map[string]any, subjects ...[2]any) {
	u := userFrom(r)
	raw, _ := json.Marshal(props)
	if props == nil {
		raw = []byte("{}")
	}
	log := &store.ActivityLog{Event: event, IP: clientIP(r), Properties: string(raw)}
	payload := map[string]any{"ip": log.IP, "properties": props}
	if u != nil {
		log.ActorID = &u.ID
		payload["actor"] = map[string]any{"id": u.ID, "username": u.Username, "email": u.Email}
	}
	a.Store.LogActivity(log, subjects...)
	a.dispatchWebhooks(event, payload)
}

// Mux assembles every route.
func (a *API) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	a.routesAuth(mux)
	a.routesClient(mux)
	a.routesApplication(mux)
	a.routesRemote(mux)
	a.routesExtras(mux)
	a.routesCaptcha(mux)
	a.routesTLS(mux)
	a.routesBilling(mux)
	a.routesStorefront(mux)
	return mux
}

// WrapExternal dispatches GET /api/application/servers/external/{id} ahead of
// the mux — its shape conflicts with /servers/{id}/databases under net/http
// pattern precedence, so it cannot be registered normally.
func (a *API) WrapExternal(next http.Handler) http.Handler {
	const prefix = "/api/application/servers/external/"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, prefix) {
			ext := strings.TrimPrefix(r.URL.Path, prefix)
			if ext != "" && !strings.Contains(ext, "/") {
				a.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
					s, err := a.Store.ServerByExternalID(ext)
					if err != nil {
						writeError(w, http.StatusNotFound, "Server not found.")
						return
					}
					writeItem(w, http.StatusOK, "server", a.trServerApp(s))
				})(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// StartScheduler runs schedule processing until ctx is cancelled.
func (a *API) StartScheduler(ctx context.Context) {
	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				a.processSchedules()
			}
		}
	}()
}

var errBadRequest = errors.New("bad request")
