package api

import (
	"net/http"
	"strings"

	"roost/internal/auth"
	"roost/internal/store"
	"roost/internal/wings"
)

func (a *API) routesApplicationServers(mux *http.ServeMux) {
	h := a.requireAdmin

	mux.HandleFunc("GET /api/application/servers", h(func(w http.ResponseWriter, r *http.Request) {
		servers, _ := a.Store.Servers()
		filter := strings.ToLower(r.URL.Query().Get("filter"))
		rows := make([]map[string]any, 0, len(servers))
		for _, s := range servers {
			if filter != "" && !strings.Contains(strings.ToLower(s.Name+s.UUID+s.UUIDShort), filter) {
				continue
			}
			rows = append(rows, a.trServerApp(s))
		}
		writeList(w, r, "server", rows)
	}))

	mux.HandleFunc("GET /api/application/servers/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		s, err := a.Store.ServerByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Server not found.")
			return
		}
		attrs := a.trServerApp(s)
		// Include commonly-needed relationships for the admin UI.
		if owner, err := a.Store.UserByID(s.OwnerID); err == nil {
			attrs["relationships"] = map[string]any{
				"user": obj("user", trUser(owner)),
			}
		}
		writeItem(w, http.StatusOK, "server", attrs)
	}))

	mux.HandleFunc("POST /api/application/servers", h(a.handleAppCreateServer))

	mux.HandleFunc("PATCH /api/application/servers/{id}/details", h(func(w http.ResponseWriter, r *http.Request) {
		s, err := a.Store.ServerByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Server not found.")
			return
		}
		var body struct {
			ExternalID  *string `json:"external_id"`
			Name        *string `json:"name"`
			User        *int64  `json:"user"`
			Description *string `json:"description"`
		}
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
			return
		}
		if body.ExternalID != nil {
			s.ExternalID = body.ExternalID
		}
		if body.Name != nil {
			s.Name = *body.Name
		}
		if body.Description != nil {
			s.Description = *body.Description
		}
		if body.User != nil {
			if _, err := a.Store.UserByID(*body.User); err != nil {
				writeError(w, http.StatusUnprocessableEntity, "The requested owner does not exist.")
				return
			}
			s.OwnerID = *body.User
		}
		a.Store.UpdateServer(s)
		a.syncWings(s)
		a.activity(r, "admin:server.details", map[string]any{"name": s.Name}, [2]any{"server", s.ID})
		writeItem(w, http.StatusOK, "server", a.trServerApp(s))
	}))

	mux.HandleFunc("PATCH /api/application/servers/{id}/build", h(func(w http.ResponseWriter, r *http.Request) {
		s, err := a.Store.ServerByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Server not found.")
			return
		}
		var body struct {
			Allocation    *int64  `json:"allocation"`
			Memory        *int64  `json:"memory"`
			Swap          *int64  `json:"swap"`
			Disk          *int64  `json:"disk"`
			IO            *int64  `json:"io"`
			CPU           *int64  `json:"cpu"`
			Threads       *string `json:"threads"`
			OOMDisabled   *bool   `json:"oom_disabled"`
			FeatureLimits *struct {
				Databases   *int64 `json:"databases"`
				Allocations *int64 `json:"allocations"`
				Backups     *int64 `json:"backups"`
			} `json:"feature_limits"`
		}
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
			return
		}
		if body.Allocation != nil {
			al, err := a.Store.AllocationByID(*body.Allocation)
			if err != nil || (al.ServerID != nil && *al.ServerID != s.ID) {
				writeError(w, http.StatusUnprocessableEntity, "The requested allocation is not available.")
				return
			}
			al.ServerID = &s.ID
			a.Store.UpdateAllocation(al)
			s.AllocationID = body.Allocation
		}
		if body.Memory != nil {
			s.Memory = *body.Memory
		}
		if body.Swap != nil {
			s.Swap = *body.Swap
		}
		if body.Disk != nil {
			s.Disk = *body.Disk
		}
		if body.IO != nil {
			s.IO = *body.IO
		}
		if body.CPU != nil {
			s.CPU = *body.CPU
		}
		if body.Threads != nil {
			if *body.Threads == "" {
				s.Threads = nil
			} else {
				s.Threads = body.Threads
			}
		}
		if body.OOMDisabled != nil {
			s.OOMDisabled = *body.OOMDisabled
		}
		if fl := body.FeatureLimits; fl != nil {
			if fl.Databases != nil {
				s.DatabaseLimit = *fl.Databases
			}
			if fl.Allocations != nil {
				s.AllocationLimit = *fl.Allocations
			}
			if fl.Backups != nil {
				s.BackupLimit = *fl.Backups
			}
		}
		a.Store.UpdateServer(s)
		a.syncWings(s)
		a.activity(r, "admin:server.build", map[string]any{"name": s.Name}, [2]any{"server", s.ID})
		writeItem(w, http.StatusOK, "server", a.trServerApp(s))
	}))

	mux.HandleFunc("PATCH /api/application/servers/{id}/startup", h(func(w http.ResponseWriter, r *http.Request) {
		s, err := a.Store.ServerByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Server not found.")
			return
		}
		var body struct {
			Startup     *string           `json:"startup"`
			Environment map[string]string `json:"environment"`
			Egg         *int64            `json:"egg"`
			Image       *string           `json:"image"`
			SkipScripts *bool             `json:"skip_scripts"`
		}
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
			return
		}
		if body.Egg != nil {
			egg, err := a.Store.EggByID(*body.Egg)
			if err != nil {
				writeError(w, http.StatusUnprocessableEntity, "The requested egg does not exist.")
				return
			}
			s.EggID = egg.ID
			s.NestID = egg.NestID
		}
		if body.Startup != nil {
			s.Startup = *body.Startup
		}
		if body.Image != nil {
			s.Image = *body.Image
		}
		if body.SkipScripts != nil {
			s.SkipScripts = *body.SkipScripts
		}
		if body.Environment != nil {
			vars, _ := a.Store.EggVariables(s.EggID)
			for _, v := range vars {
				if val, ok := body.Environment[v.EnvVariable]; ok {
					a.Store.SetServerVariable(s.ID, v.ID, val)
				}
			}
		}
		a.Store.UpdateServer(s)
		a.syncWings(s)
		a.activity(r, "admin:server.startup", map[string]any{"name": s.Name}, [2]any{"server", s.ID})
		writeItem(w, http.StatusOK, "server", a.trServerApp(s))
	}))

	mux.HandleFunc("POST /api/application/servers/{id}/suspend", h(func(w http.ResponseWriter, r *http.Request) {
		a.setServerStatus(w, r, "suspended")
	}))
	mux.HandleFunc("POST /api/application/servers/{id}/unsuspend", h(func(w http.ResponseWriter, r *http.Request) {
		a.setServerStatus(w, r, "")
	}))
	mux.HandleFunc("POST /api/application/servers/{id}/reinstall", h(func(w http.ResponseWriter, r *http.Request) {
		s, err := a.Store.ServerByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Server not found.")
			return
		}
		status := "installing"
		s.Status = &status
		a.Store.UpdateServer(s)
		if node, err := a.Store.NodeByID(s.NodeID); err == nil {
			go wings.New(node).Reinstall(s.UUID)
		}
		a.activity(r, "admin:server.reinstall", nil, [2]any{"server", s.ID})
		w.WriteHeader(http.StatusAccepted)
	}))

	mux.HandleFunc("DELETE /api/application/servers/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		a.deleteServer(w, r, false)
	}))
	mux.HandleFunc("DELETE /api/application/servers/{id}/force", h(func(w http.ResponseWriter, r *http.Request) {
		a.deleteServer(w, r, true)
	}))

	// ---- admin: server databases ----
	mux.HandleFunc("GET /api/application/servers/{id}/databases", h(func(w http.ResponseWriter, r *http.Request) {
		dbs, _ := a.Store.DatabasesForServer(parseID(r, "id"))
		rows := make([]map[string]any, 0, len(dbs))
		for _, d := range dbs {
			rows = append(rows, trServerDatabaseApp(d))
		}
		writeList(w, r, "server_database", rows)
	}))
	mux.HandleFunc("POST /api/application/servers/{id}/databases", h(func(w http.ResponseWriter, r *http.Request) {
		s, err := a.Store.ServerByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Server not found.")
			return
		}
		var body struct {
			Database string `json:"database"`
			Remote   string `json:"remote"`
			Host     int64  `json:"host"`
		}
		if err := decode(r, &body); err != nil || body.Database == "" {
			writeError(w, http.StatusUnprocessableEntity, "A database name must be provided.")
			return
		}
		if body.Remote == "" {
			body.Remote = "%"
		}
		if body.Host == 0 {
			hosts, _ := a.Store.DatabaseHosts()
			if len(hosts) == 0 {
				writeError(w, http.StatusBadRequest, "No database hosts are configured.")
				return
			}
			body.Host = hosts[0].ID
		}
		d := &store.ServerDatabase{
			ServerID: s.ID, DatabaseHostID: body.Host,
			Database: "s" + r.PathValue("id") + "_" + body.Database,
			Username: "u" + r.PathValue("id") + "_" + auth.RandomAlnum(10),
			Remote:   body.Remote, Password: auth.RandomAlnum(24),
		}
		if err := a.Store.CreateServerDatabase(d); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeItem(w, http.StatusCreated, "server_database", trServerDatabaseApp(d))
	}))
	mux.HandleFunc("POST /api/application/servers/{id}/databases/{database}/reset-password", h(func(w http.ResponseWriter, r *http.Request) {
		d, err := a.Store.ServerDatabaseByID(parseID(r, "database"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Database not found.")
			return
		}
		d.Password = auth.RandomAlnum(24)
		a.Store.UpdateServerDatabase(d)
		writeNoContent(w)
	}))
	mux.HandleFunc("DELETE /api/application/servers/{id}/databases/{database}", h(func(w http.ResponseWriter, r *http.Request) {
		a.Store.DeleteServerDatabase(parseID(r, "database"))
		writeNoContent(w)
	}))
}

func (a *API) setServerStatus(w http.ResponseWriter, r *http.Request, status string) {
	s, err := a.Store.ServerByID(parseID(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Server not found.")
		return
	}
	if status == "" {
		s.Status = nil
	} else {
		s.Status = &status
	}
	a.Store.UpdateServer(s)
	a.syncWings(s)
	event := "admin:server.unsuspend"
	if status != "" {
		event = "admin:server." + status
	}
	a.activity(r, event, nil, [2]any{"server", s.ID})
	writeNoContent(w)
}

func (a *API) deleteServer(w http.ResponseWriter, r *http.Request, force bool) {
	s, err := a.Store.ServerByID(parseID(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Server not found.")
		return
	}
	if node, err := a.Store.NodeByID(s.NodeID); err == nil {
		if err := wings.New(node).DeleteServer(s.UUID); err != nil && !force {
			writeError(w, http.StatusBadGateway,
				"Wings reported an error removing the server; use the force option to delete anyway. "+err.Error())
			return
		}
	}
	if err := a.Store.DeleteServer(s.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.activity(r, "admin:server.delete", map[string]any{"name": s.Name})
	writeNoContent(w)
}

func (a *API) handleAppCreateServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExternalID  *string           `json:"external_id"`
		Name        string            `json:"name"`
		Description string            `json:"description"`
		User        int64             `json:"user"`
		Egg         int64             `json:"egg"`
		DockerImage string            `json:"docker_image"`
		Startup     string            `json:"startup"`
		Environment map[string]string `json:"environment"`
		SkipScripts bool              `json:"skip_scripts"`
		OOMDisabled *bool             `json:"oom_disabled"`
		Limits      *struct {
			Memory  int64   `json:"memory"`
			Swap    int64   `json:"swap"`
			Disk    int64   `json:"disk"`
			IO      int64   `json:"io"`
			CPU     int64   `json:"cpu"`
			Threads *string `json:"threads"`
		} `json:"limits"`
		FeatureLimits struct {
			Databases   int64 `json:"databases"`
			Allocations int64 `json:"allocations"`
			Backups     int64 `json:"backups"`
		} `json:"feature_limits"`
		Allocation struct {
			Default    int64   `json:"default"`
			Additional []int64 `json:"additional"`
		} `json:"allocation"`
		StartOnCompletion bool `json:"start_on_completion"`
	}
	if err := decode(r, &body); err != nil || body.Name == "" || body.User == 0 || body.Egg == 0 {
		writeError(w, http.StatusUnprocessableEntity, "name, user and egg are required fields.")
		return
	}
	if _, err := a.Store.UserByID(body.User); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "The requested owner does not exist.")
		return
	}
	egg, err := a.Store.EggByID(body.Egg)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "The requested egg does not exist.")
		return
	}
	alloc, err := a.Store.AllocationByID(body.Allocation.Default)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "The requested default allocation does not exist.")
		return
	}
	if alloc.ServerID != nil {
		writeError(w, http.StatusUnprocessableEntity, "The requested allocation is already assigned.")
		return
	}

	if body.DockerImage == "" {
		for _, v := range jsonObj(egg.DockerImages) {
			if s, ok := v.(string); ok {
				body.DockerImage = s
				break
			}
		}
	}
	if body.Startup == "" {
		body.Startup = egg.Startup
	}

	status := "installing"
	srv := &store.Server{
		ExternalID: body.ExternalID,
		UUID:       auth.UUID(),
		UUIDShort:  auth.RandomHex(4),
		NodeID:     alloc.NodeID,
		Name:       body.Name, Description: body.Description,
		Status: &status, SkipScripts: body.SkipScripts,
		OwnerID: body.User,
		Memory:  512, Swap: 0, Disk: 1024, IO: 500, CPU: 0,
		NestID: egg.NestID, EggID: egg.ID,
		Startup: body.Startup, Image: body.DockerImage,
		DatabaseLimit: body.FeatureLimits.Databases, AllocationLimit: body.FeatureLimits.Allocations,
		BackupLimit: body.FeatureLimits.Backups,
		OOMDisabled: true,
	}
	if body.Limits != nil {
		srv.Memory = body.Limits.Memory
		srv.Swap = body.Limits.Swap
		srv.Disk = body.Limits.Disk
		srv.IO = body.Limits.IO
		srv.CPU = body.Limits.CPU
		srv.Threads = body.Limits.Threads
	}
	if body.OOMDisabled != nil {
		srv.OOMDisabled = *body.OOMDisabled
	}
	if err := a.Store.CreateServer(srv); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Assign allocations.
	alloc.ServerID = &srv.ID
	a.Store.UpdateAllocation(alloc)
	srv.AllocationID = &alloc.ID
	for _, extra := range body.Allocation.Additional {
		if al, err := a.Store.AllocationByID(extra); err == nil && al.ServerID == nil && al.NodeID == alloc.NodeID {
			al.ServerID = &srv.ID
			a.Store.UpdateAllocation(al)
		}
	}
	a.Store.UpdateServer(srv)

	// Store egg variable values (defaults merged with provided environment).
	vars, _ := a.Store.EggVariables(egg.ID)
	for _, v := range vars {
		val := v.DefaultValue
		if got, ok := body.Environment[v.EnvVariable]; ok {
			val = got
		}
		a.Store.SetServerVariable(srv.ID, v.ID, val)
	}

	// Ask wings to install; failures leave the server in "installing" until
	// the daemon comes back and polls.
	if node, err := a.Store.NodeByID(srv.NodeID); err == nil {
		go wings.New(node).CreateServer(srv.UUID, body.StartOnCompletion)
	}

	a.activity(r, "admin:server.create", map[string]any{"name": srv.Name}, [2]any{"server", srv.ID})
	writeItem(w, http.StatusCreated, "server", a.trServerApp(srv))
}
