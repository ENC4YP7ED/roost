package api

import (
	"net/http"
	"strconv"
	"strings"

	"roost/internal/auth"
	"roost/internal/store"
	"roost/internal/wings"
)

func (a *API) routesApplication(mux *http.ServeMux) {
	h := a.requireAdmin

	// ---- overview ----
	mux.HandleFunc("GET /api/application/overview", h(func(w http.ResponseWriter, r *http.Request) {
		users, _ := a.Store.CountUsers()
		servers, _ := a.Store.CountServers()
		nodes, _ := a.Store.Nodes()
		locations, _ := a.Store.Locations()
		writeJSON(w, http.StatusOK, map[string]any{
			"users": users, "servers": servers,
			"nodes": len(nodes), "locations": len(locations),
			"version": "1.0.0-roost",
		})
	}))

	// ---- settings ----
	mux.HandleFunc("GET /api/application/settings", h(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"app:name": a.AppName(),
			"app:url":  a.PanelURL(),
		})
	}))
	mux.HandleFunc("PATCH /api/application/settings", h(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
			return
		}
		for k, v := range body {
			if k == "app:name" || k == "app:url" {
				a.Store.SetSetting(k, strings.TrimRight(v, "/"))
			}
		}
		writeNoContent(w)
	}))

	// ---- users ----
	mux.HandleFunc("GET /api/application/users", h(func(w http.ResponseWriter, r *http.Request) {
		users, _ := a.Store.Users(r.URL.Query().Get("filter[email]") + r.URL.Query().Get("filter"))
		rows := make([]map[string]any, 0, len(users))
		for _, u := range users {
			rows = append(rows, trUser(u))
		}
		writeList(w, r, "user", rows)
	}))
	mux.HandleFunc("GET /api/application/users/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		u, err := a.Store.UserByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "User not found.")
			return
		}
		attrs := trUser(u)
		servers, _ := a.Store.ServersOwnedBy(u.ID)
		srvRows := []item{}
		for _, s := range servers {
			srvRows = append(srvRows, obj("server", a.trServerApp(s)))
		}
		attrs["relationships"] = map[string]any{"servers": map[string]any{"object": "list", "data": srvRows}}
		writeItem(w, http.StatusOK, "user", attrs)
	}))
	mux.HandleFunc("GET /api/application/users/external/{external_id}", h(func(w http.ResponseWriter, r *http.Request) {
		u, err := a.Store.UserByExternalID(r.PathValue("external_id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "User not found.")
			return
		}
		writeItem(w, http.StatusOK, "user", trUser(u))
	}))
	mux.HandleFunc("POST /api/application/users", h(a.handleAppCreateUser))
	mux.HandleFunc("PATCH /api/application/users/{id}", h(a.handleAppUpdateUser))
	mux.HandleFunc("DELETE /api/application/users/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.DeleteUser(parseID(r, "id")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeNoContent(w)
	}))

	// ---- locations ----
	mux.HandleFunc("GET /api/application/locations", h(func(w http.ResponseWriter, r *http.Request) {
		locations, _ := a.Store.Locations()
		rows := make([]map[string]any, 0, len(locations))
		for _, l := range locations {
			rows = append(rows, trLocation(l))
		}
		writeList(w, r, "location", rows)
	}))
	mux.HandleFunc("GET /api/application/locations/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		l, err := a.Store.LocationByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Location not found.")
			return
		}
		writeItem(w, http.StatusOK, "location", trLocation(l))
	}))
	mux.HandleFunc("POST /api/application/locations", h(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Short, Long string }
		if err := decode(r, &body); err != nil || body.Short == "" {
			writeError(w, http.StatusUnprocessableEntity, "A short code must be provided.")
			return
		}
		l := &store.Location{Short: body.Short, Long: body.Long}
		if err := a.Store.CreateLocation(l); err != nil {
			writeError(w, http.StatusConflict, "That short code is already in use.")
			return
		}
		writeItem(w, http.StatusCreated, "location", trLocation(l))
	}))
	mux.HandleFunc("PATCH /api/application/locations/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		l, err := a.Store.LocationByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Location not found.")
			return
		}
		var body struct{ Short, Long *string }
		decode(r, &body)
		if body.Short != nil {
			l.Short = *body.Short
		}
		if body.Long != nil {
			l.Long = *body.Long
		}
		a.Store.UpdateLocation(l)
		writeItem(w, http.StatusOK, "location", trLocation(l))
	}))
	mux.HandleFunc("DELETE /api/application/locations/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.DeleteLocation(parseID(r, "id")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeNoContent(w)
	}))

	// ---- nodes ----
	mux.HandleFunc("GET /api/application/nodes", h(func(w http.ResponseWriter, r *http.Request) {
		nodes, _ := a.Store.Nodes()
		rows := make([]map[string]any, 0, len(nodes))
		for _, n := range nodes {
			rows = append(rows, a.trNode(n))
		}
		writeList(w, r, "node", rows)
	}))
	mux.HandleFunc("GET /api/application/nodes/deployable", h(func(w http.ResponseWriter, r *http.Request) {
		nodes, _ := a.Store.Nodes()
		rows := []map[string]any{}
		for _, n := range nodes {
			if n.MaintenanceMode {
				continue
			}
			if _, err := a.Store.FreeAllocation(n.ID); err == nil {
				rows = append(rows, a.trNode(n))
			}
		}
		writeList(w, r, "node", rows)
	}))
	mux.HandleFunc("GET /api/application/nodes/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		n, err := a.Store.NodeByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Node not found.")
			return
		}
		writeItem(w, http.StatusOK, "node", a.trNode(n))
	}))
	mux.HandleFunc("GET /api/application/nodes/{id}/configuration", h(func(w http.ResponseWriter, r *http.Request) {
		n, err := a.Store.NodeByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Node not found.")
			return
		}
		writeJSON(w, http.StatusOK, a.wingsNodeConfiguration(n))
	}))
	mux.HandleFunc("GET /api/application/nodes/{id}/system", h(func(w http.ResponseWriter, r *http.Request) {
		n, err := a.Store.NodeByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Node not found.")
			return
		}
		info, err := wings.New(n).SystemInformation()
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, info)
	}))
	mux.HandleFunc("POST /api/application/nodes", h(a.handleAppCreateNode))
	mux.HandleFunc("PATCH /api/application/nodes/{id}", h(a.handleAppUpdateNode))
	mux.HandleFunc("DELETE /api/application/nodes/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.DeleteNode(parseID(r, "id")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeNoContent(w)
	}))

	// ---- node allocations ----
	mux.HandleFunc("GET /api/application/nodes/{id}/allocations", h(func(w http.ResponseWriter, r *http.Request) {
		allocs, _ := a.Store.AllocationsForNode(parseID(r, "id"))
		rows := make([]map[string]any, 0, len(allocs))
		for _, al := range allocs {
			rows = append(rows, trAllocation(al))
		}
		writeList(w, r, "allocation", rows)
	}))
	mux.HandleFunc("POST /api/application/nodes/{id}/allocations", h(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IP    string   `json:"ip"`
			Alias string   `json:"alias"`
			Ports []string `json:"ports"`
		}
		if err := decode(r, &body); err != nil || body.IP == "" || len(body.Ports) == 0 {
			writeError(w, http.StatusUnprocessableEntity, "An IP address and at least one port are required.")
			return
		}
		var ports []int
		for _, p := range body.Ports {
			if lo, hi, ok := strings.Cut(p, "-"); ok {
				a1, e1 := strconv.Atoi(strings.TrimSpace(lo))
				a2, e2 := strconv.Atoi(strings.TrimSpace(hi))
				if e1 != nil || e2 != nil || a1 > a2 || a2-a1 > 1000 {
					writeError(w, http.StatusUnprocessableEntity, "Invalid port range: "+p)
					return
				}
				for v := a1; v <= a2; v++ {
					ports = append(ports, v)
				}
			} else {
				v, err := strconv.Atoi(strings.TrimSpace(p))
				if err != nil {
					writeError(w, http.StatusUnprocessableEntity, "Invalid port: "+p)
					return
				}
				ports = append(ports, v)
			}
		}
		var alias *string
		if body.Alias != "" {
			alias = &body.Alias
		}
		if _, err := a.Store.CreateAllocations(parseID(r, "id"), body.IP, alias, ports); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeNoContent(w)
	}))
	mux.HandleFunc("DELETE /api/application/nodes/{id}/allocations/{allocation}", h(func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.DeleteAllocation(parseID(r, "allocation")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeNoContent(w)
	}))

	a.routesApplicationServers(mux)
	a.routesApplicationNests(mux)
	a.routesApplicationExtras(mux)
}

func (a *API) handleAppCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExternalID *string `json:"external_id"`
		Email      string  `json:"email"`
		Username   string  `json:"username"`
		FirstName  string  `json:"first_name"`
		LastName   string  `json:"last_name"`
		Password   string  `json:"password"`
		RootAdmin  bool    `json:"root_admin"`
		Language   string  `json:"language"`
	}
	if err := decode(r, &body); err != nil || body.Email == "" || body.Username == "" {
		writeError(w, http.StatusUnprocessableEntity, "An email and username must be provided.")
		return
	}
	if body.Password == "" {
		body.Password = auth.RandomAlnum(24)
	}
	if body.Language == "" {
		body.Language = "en"
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to hash password.")
		return
	}
	u := &store.User{
		ExternalID: body.ExternalID, UUID: auth.UUID(), Username: body.Username,
		Email: body.Email, NameFirst: body.FirstName, NameLast: body.LastName,
		Password: hash, Language: body.Language, RootAdmin: body.RootAdmin,
	}
	if err := a.Store.CreateUser(u); err != nil {
		writeError(w, http.StatusConflict, "That email or username is already in use.")
		return
	}
	a.activity(r, "admin:user.create", map[string]any{"email": u.Email})
	writeItem(w, http.StatusCreated, "user", trUser(u))
}

func (a *API) handleAppUpdateUser(w http.ResponseWriter, r *http.Request) {
	u, err := a.Store.UserByID(parseID(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "User not found.")
		return
	}
	var body struct {
		ExternalID *string `json:"external_id"`
		Email      *string `json:"email"`
		Username   *string `json:"username"`
		FirstName  *string `json:"first_name"`
		LastName   *string `json:"last_name"`
		Password   *string `json:"password"`
		RootAdmin  *bool   `json:"root_admin"`
		Language   *string `json:"language"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	if body.ExternalID != nil {
		u.ExternalID = body.ExternalID
	}
	if body.Email != nil {
		u.Email = *body.Email
	}
	if body.Username != nil {
		u.Username = *body.Username
	}
	if body.FirstName != nil {
		u.NameFirst = *body.FirstName
	}
	if body.LastName != nil {
		u.NameLast = *body.LastName
	}
	if body.Language != nil {
		u.Language = *body.Language
	}
	if body.RootAdmin != nil {
		if !*body.RootAdmin && u.ID == userFrom(r).ID {
			writeError(w, http.StatusBadRequest, "You cannot remove your own administrative status.")
			return
		}
		u.RootAdmin = *body.RootAdmin
	}
	if body.Password != nil && *body.Password != "" {
		hash, err := auth.HashPassword(*body.Password)
		if err == nil {
			u.Password = hash
		}
	}
	if err := a.Store.UpdateUser(u); err != nil {
		writeError(w, http.StatusConflict, "That email or username is already in use.")
		return
	}
	a.activity(r, "admin:user.update", map[string]any{"email": u.Email})
	writeItem(w, http.StatusOK, "user", trUser(u))
}

type nodePayload struct {
	Name               *string `json:"name"`
	Description        *string `json:"description"`
	LocationID         *int64  `json:"location_id"`
	Public             *bool   `json:"public"`
	FQDN               *string `json:"fqdn"`
	Scheme             *string `json:"scheme"`
	BehindProxy        *bool   `json:"behind_proxy"`
	MaintenanceMode    *bool   `json:"maintenance_mode"`
	Memory             *int64  `json:"memory"`
	MemoryOverallocate *int64  `json:"memory_overallocate"`
	Disk               *int64  `json:"disk"`
	DiskOverallocate   *int64  `json:"disk_overallocate"`
	UploadSize         *int64  `json:"upload_size"`
	DaemonListen       *int    `json:"daemon_listen"`
	DaemonSFTP         *int    `json:"daemon_sftp"`
	DaemonBase         *string `json:"daemon_base"`
}

func applyNodePayload(n *store.Node, p *nodePayload) {
	if p.Name != nil {
		n.Name = *p.Name
	}
	if p.Description != nil {
		n.Description = *p.Description
	}
	if p.LocationID != nil {
		n.LocationID = *p.LocationID
	}
	if p.Public != nil {
		n.Public = *p.Public
	}
	if p.FQDN != nil {
		n.FQDN = *p.FQDN
	}
	if p.Scheme != nil {
		n.Scheme = *p.Scheme
	}
	if p.BehindProxy != nil {
		n.BehindProxy = *p.BehindProxy
	}
	if p.MaintenanceMode != nil {
		n.MaintenanceMode = *p.MaintenanceMode
	}
	if p.Memory != nil {
		n.Memory = *p.Memory
	}
	if p.MemoryOverallocate != nil {
		n.MemoryOverallocate = *p.MemoryOverallocate
	}
	if p.Disk != nil {
		n.Disk = *p.Disk
	}
	if p.DiskOverallocate != nil {
		n.DiskOverallocate = *p.DiskOverallocate
	}
	if p.UploadSize != nil {
		n.UploadSize = *p.UploadSize
	}
	if p.DaemonListen != nil {
		n.DaemonListen = *p.DaemonListen
	}
	if p.DaemonSFTP != nil {
		n.DaemonSFTP = *p.DaemonSFTP
	}
	if p.DaemonBase != nil {
		n.DaemonBase = *p.DaemonBase
	}
}

func (a *API) handleAppCreateNode(w http.ResponseWriter, r *http.Request) {
	var body nodePayload
	if err := decode(r, &body); err != nil || body.Name == nil || body.FQDN == nil || body.LocationID == nil {
		writeError(w, http.StatusUnprocessableEntity, "A name, fqdn and location_id must be provided.")
		return
	}
	if _, err := a.Store.LocationByID(*body.LocationID); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "The requested location does not exist.")
		return
	}
	n := &store.Node{
		UUID: auth.UUID(), Public: true, Scheme: "https",
		Memory: 0, Disk: 0, UploadSize: 100,
		DaemonTokenID: auth.RandomAlnum(16), DaemonToken: auth.RandomAlnum(64),
		DaemonListen: 8080, DaemonSFTP: 2022, DaemonBase: "/var/lib/pterodactyl/volumes",
	}
	applyNodePayload(n, &body)
	if err := a.Store.CreateNode(n); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.activity(r, "admin:node.create", map[string]any{"name": n.Name})
	writeItem(w, http.StatusCreated, "node", a.trNode(n))
}

func (a *API) handleAppUpdateNode(w http.ResponseWriter, r *http.Request) {
	n, err := a.Store.NodeByID(parseID(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Node not found.")
		return
	}
	var body nodePayload
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	applyNodePayload(n, &body)
	if err := a.Store.UpdateNode(n); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.activity(r, "admin:node.update", map[string]any{"name": n.Name})
	writeItem(w, http.StatusOK, "node", a.trNode(n))
}

// resetNodeToken regenerates daemon credentials (extension endpoint).
func (a *API) routesApplicationExtras(mux *http.ServeMux) {
	h := a.requireAdmin

	mux.HandleFunc("POST /api/application/nodes/{id}/reset-token", h(func(w http.ResponseWriter, r *http.Request) {
		n, err := a.Store.NodeByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Node not found.")
			return
		}
		// Direct SQL-free update: recreate credentials then persist via raw update.
		n.DaemonTokenID = auth.RandomAlnum(16)
		n.DaemonToken = auth.RandomAlnum(64)
		if err := a.Store.ResetNodeToken(n); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeItem(w, http.StatusOK, "node", a.trNode(n))
	}))

	// ---- database hosts (admin UI extension) ----
	mux.HandleFunc("GET /api/application/database-hosts", h(func(w http.ResponseWriter, r *http.Request) {
		hosts, _ := a.Store.DatabaseHosts()
		rows := make([]map[string]any, 0, len(hosts))
		for _, host := range hosts {
			rows = append(rows, trDatabaseHost(host))
		}
		writeList(w, r, "database_host", rows)
	}))
	mux.HandleFunc("POST /api/application/database-hosts", h(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name     string `json:"name"`
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Username string `json:"username"`
			Password string `json:"password"`
			NodeID   *int64 `json:"node_id"`
		}
		if err := decode(r, &body); err != nil || body.Name == "" || body.Host == "" {
			writeError(w, http.StatusUnprocessableEntity, "A name and host must be provided.")
			return
		}
		if body.Port == 0 {
			body.Port = 3306
		}
		hst := &store.DatabaseHost{Name: body.Name, Host: body.Host, Port: body.Port,
			Username: body.Username, Password: body.Password, NodeID: body.NodeID}
		if err := a.Store.CreateDatabaseHost(hst); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeItem(w, http.StatusCreated, "database_host", trDatabaseHost(hst))
	}))
	mux.HandleFunc("PATCH /api/application/database-hosts/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		hst, err := a.Store.DatabaseHostByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Database host not found.")
			return
		}
		var body struct {
			Name     *string `json:"name"`
			Host     *string `json:"host"`
			Port     *int    `json:"port"`
			Username *string `json:"username"`
			Password *string `json:"password"`
			NodeID   *int64  `json:"node_id"`
		}
		decode(r, &body)
		if body.Name != nil {
			hst.Name = *body.Name
		}
		if body.Host != nil {
			hst.Host = *body.Host
		}
		if body.Port != nil {
			hst.Port = *body.Port
		}
		if body.Username != nil {
			hst.Username = *body.Username
		}
		if body.Password != nil && *body.Password != "" {
			hst.Password = *body.Password
		}
		if body.NodeID != nil {
			hst.NodeID = body.NodeID
		}
		a.Store.UpdateDatabaseHost(hst)
		writeItem(w, http.StatusOK, "database_host", trDatabaseHost(hst))
	}))
	mux.HandleFunc("DELETE /api/application/database-hosts/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.DeleteDatabaseHost(parseID(r, "id")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeNoContent(w)
	}))

	// ---- mounts (admin UI extension) ----
	mux.HandleFunc("GET /api/application/mounts", h(func(w http.ResponseWriter, r *http.Request) {
		mounts, _ := a.Store.Mounts()
		rows := make([]map[string]any, 0, len(mounts))
		for _, m := range mounts {
			rows = append(rows, trMount(m))
		}
		writeList(w, r, "mount", rows)
	}))
	mux.HandleFunc("POST /api/application/mounts", h(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name          string `json:"name"`
			Description   string `json:"description"`
			Source        string `json:"source"`
			Target        string `json:"target"`
			ReadOnly      bool   `json:"read_only"`
			UserMountable bool   `json:"user_mountable"`
		}
		if err := decode(r, &body); err != nil || body.Name == "" || body.Source == "" || body.Target == "" {
			writeError(w, http.StatusUnprocessableEntity, "A name, source and target must be provided.")
			return
		}
		m := &store.Mount{UUID: auth.UUID(), Name: body.Name, Description: body.Description,
			Source: body.Source, Target: body.Target, ReadOnly: body.ReadOnly, UserMountable: body.UserMountable}
		if err := a.Store.CreateMount(m); err != nil {
			writeError(w, http.StatusConflict, "A mount with that name already exists.")
			return
		}
		writeItem(w, http.StatusCreated, "mount", trMount(m))
	}))
	mux.HandleFunc("PATCH /api/application/mounts/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		m, err := a.Store.MountByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Mount not found.")
			return
		}
		var body struct {
			Name          *string `json:"name"`
			Description   *string `json:"description"`
			Source        *string `json:"source"`
			Target        *string `json:"target"`
			ReadOnly      *bool   `json:"read_only"`
			UserMountable *bool   `json:"user_mountable"`
		}
		decode(r, &body)
		if body.Name != nil {
			m.Name = *body.Name
		}
		if body.Description != nil {
			m.Description = *body.Description
		}
		if body.Source != nil {
			m.Source = *body.Source
		}
		if body.Target != nil {
			m.Target = *body.Target
		}
		if body.ReadOnly != nil {
			m.ReadOnly = *body.ReadOnly
		}
		if body.UserMountable != nil {
			m.UserMountable = *body.UserMountable
		}
		a.Store.UpdateMount(m)
		writeItem(w, http.StatusOK, "mount", trMount(m))
	}))
	mux.HandleFunc("DELETE /api/application/mounts/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		a.Store.DeleteMount(parseID(r, "id"))
		writeNoContent(w)
	}))
}
