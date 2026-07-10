package api

import (
	"net/http"
	"strconv"
	"time"

	"roost/internal/store"
	"roost/internal/wings"
)

func nowISO() string { return time.Now().UTC().Format(time.RFC3339) }

func (a *API) routesClient(mux *http.ServeMux) {
	a.routesClientAccount(mux)
	a.routesClientFiles(mux)
	a.routesClientFeatures(mux)

	h := a.requireUser

	// GET /api/client — servers visible to the acting user.
	mux.HandleFunc("GET /api/client", h(func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r)
		var (
			servers []*store.Server
			err     error
		)
		switch r.URL.Query().Get("type") {
		case "admin", "admin-all":
			if !u.RootAdmin {
				writeList(w, r, "server", nil)
				return
			}
			servers, err = a.Store.Servers()
		case "owner":
			servers, err = a.Store.ServersOwnedBy(u.ID)
		default:
			servers, err = a.Store.ServersForUser(u.ID)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows := make([]map[string]any, 0, len(servers))
		for _, s := range servers {
			rows = append(rows, a.trServerClient(s, u))
		}
		writeList(w, r, "server", rows)
	}))

	mux.HandleFunc("GET /api/client/permissions", h(func(w http.ResponseWriter, r *http.Request) {
		writeItem(w, http.StatusOK, "system_permissions", map[string]any{
			"permissions": AllPermissions,
		})
	}))

	mux.HandleFunc("GET /api/client/servers/{server}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		u, srv := userFrom(r), serverFrom(r)
		attrs := a.trServerClient(srv, u)
		writeJSON(w, http.StatusOK, map[string]any{
			"object":     "server",
			"attributes": attrs,
			"meta": map[string]any{
				"is_server_owner":  srv.OwnerID == u.ID,
				"user_permissions": a.userPermissions(u, srv),
			},
		})
	})))

	// Websocket credentials: a JWT the browser presents directly to wings.
	mux.HandleFunc("GET /api/client/servers/{server}/websocket", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		u, srv := userFrom(r), serverFrom(r)
		node, err := a.Store.NodeByID(srv.NodeID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Node for this server is missing.")
			return
		}
		perms := a.userPermissions(u, srv)
		token, socket, err := wings.WebsocketToken(a.PanelURL(), node, srv, u, perms)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{"token": token, "socket": socket},
		})
	}, "websocket.connect")))

	// Live resource utilisation, proxied from wings.
	mux.HandleFunc("GET /api/client/servers/{server}/resources", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		node, err := a.Store.NodeByID(srv.NodeID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Node for this server is missing.")
			return
		}
		suspended := srv.Status != nil && *srv.Status == "suspended"
		var details struct {
			State       string `json:"state"`
			Utilization struct {
				Memory  int64   `json:"memory_bytes"`
				Disk    int64   `json:"disk_bytes"`
				CPU     float64 `json:"cpu_absolute"`
				Network struct {
					Rx int64 `json:"rx_bytes"`
					Tx int64 `json:"tx_bytes"`
				} `json:"network"`
				Uptime int64 `json:"uptime"`
			} `json:"utilization"`
		}
		if err := wings.New(node).Do("GET", "/api/servers/"+srv.UUID, nil, &details); err != nil {
			// Wings offline: report the server as offline rather than erroring
			// so the dashboard stays usable.
			writeItem(w, http.StatusOK, "stats", map[string]any{
				"current_state": "offline",
				"is_suspended":  suspended,
				"resources": map[string]any{
					"memory_bytes": 0, "cpu_absolute": 0, "disk_bytes": 0,
					"network_rx_bytes": 0, "network_tx_bytes": 0, "uptime": 0,
				},
			})
			return
		}
		writeItem(w, http.StatusOK, "stats", map[string]any{
			"current_state": details.State,
			"is_suspended":  suspended,
			"resources": map[string]any{
				"memory_bytes":     details.Utilization.Memory,
				"cpu_absolute":     details.Utilization.CPU,
				"disk_bytes":       details.Utilization.Disk,
				"network_rx_bytes": details.Utilization.Network.Rx,
				"network_tx_bytes": details.Utilization.Network.Tx,
				"uptime":           details.Utilization.Uptime,
			},
		})
	})))

	mux.HandleFunc("GET /api/client/servers/{server}/activity", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		logs, _ := a.Store.ActivityForSubject("server", srv.ID, 100)
		rows := make([]map[string]any, 0, len(logs))
		for _, l := range logs {
			rows = append(rows, trActivity(l))
		}
		writeList(w, r, "activity_log", rows)
	}, "activity.read")))

	mux.HandleFunc("POST /api/client/servers/{server}/command", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		var body struct {
			Command string `json:"command"`
		}
		if err := decode(r, &body); err != nil || body.Command == "" {
			writeError(w, http.StatusUnprocessableEntity, "A command must be provided.")
			return
		}
		node, _ := a.Store.NodeByID(srv.NodeID)
		if err := wings.New(node).SendCommands(srv.UUID, []string{body.Command}); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		a.activity(r, "server:console.command", map[string]any{"command": body.Command}, [2]any{"server", srv.ID})
		writeNoContent(w)
	}, "control.console")))

	mux.HandleFunc("POST /api/client/servers/{server}/power", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		var body struct {
			Signal string `json:"signal"`
		}
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
			return
		}
		switch body.Signal {
		case "start", "stop", "restart", "kill":
		default:
			writeError(w, http.StatusUnprocessableEntity, "Signal must be one of start, stop, restart, kill.")
			return
		}
		node, _ := a.Store.NodeByID(srv.NodeID)
		if err := wings.New(node).SendPower(srv.UUID, body.Signal); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		a.activity(r, "server:power."+body.Signal, nil, [2]any{"server", srv.ID})
		writeNoContent(w)
	}, "control.start")))
}

// parseID is a tolerant path-value → int64 helper.
func parseID(r *http.Request, key string) int64 {
	v, _ := strconv.ParseInt(r.PathValue(key), 10, 64)
	return v
}
