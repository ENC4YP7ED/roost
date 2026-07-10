package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"roost/internal/auth"
	"roost/internal/store"
)

// The remote API is what wings polls. Shapes follow Pterodactyl's
// api-remote.php routes so a stock wings daemon can pair with this panel.

func (a *API) routesRemote(mux *http.ServeMux) {
	h := a.requireNode

	// Paginated list of all servers on the requesting node, as full wings
	// configuration documents.
	mux.HandleFunc("GET /api/remote/servers", h(func(w http.ResponseWriter, r *http.Request) {
		node := nodeFrom(r)
		servers, err := a.Store.ServersForNode(node.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage < 1 {
			perPage = 50
		}
		start := (page - 1) * perPage
		if start > len(servers) {
			start = len(servers)
		}
		end := start + perPage
		if end > len(servers) {
			end = len(servers)
		}
		data := []map[string]any{}
		for _, s := range servers[start:end] {
			settings, err := a.wingsServerConfiguration(s)
			if err != nil {
				continue
			}
			process, _ := a.wingsProcessConfiguration(s)
			data = append(data, map[string]any{
				"uuid":                  s.UUID,
				"settings":              settings,
				"process_configuration": process,
			})
		}
		lastPage := (len(servers) + perPage - 1) / perPage
		if lastPage < 1 {
			lastPage = 1
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data":  data,
			"links": map[string]any{},
			"meta": map[string]any{
				"current_page": page,
				"from":         start + 1,
				"last_page":    lastPage,
				"per_page":     perPage,
				"to":           end,
				"total":        len(servers),
			},
		})
	}))

	// Wings calls this on boot to clear stale transfer/restore states.
	mux.HandleFunc("POST /api/remote/servers/reset", h(func(w http.ResponseWriter, r *http.Request) {
		node := nodeFrom(r)
		servers, _ := a.Store.ServersForNode(node.ID)
		for _, s := range servers {
			if s.Status != nil && (*s.Status == "restoring_backup") {
				s.Status = nil
				a.Store.UpdateServer(s)
			}
		}
		writeNoContent(w)
	}))

	mux.HandleFunc("GET /api/remote/servers/{uuid}", h(func(w http.ResponseWriter, r *http.Request) {
		s := a.remoteServer(w, r)
		if s == nil {
			return
		}
		settings, err := a.wingsServerConfiguration(s)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		process, _ := a.wingsProcessConfiguration(s)
		writeJSON(w, http.StatusOK, map[string]any{
			"settings":              settings,
			"process_configuration": process,
		})
	}))

	// Install script + status callbacks.
	mux.HandleFunc("GET /api/remote/servers/{uuid}/install", h(func(w http.ResponseWriter, r *http.Request) {
		s := a.remoteServer(w, r)
		if s == nil {
			return
		}
		egg, err := a.Store.EggByID(s.EggID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Egg missing for server.")
			return
		}
		env, _ := a.serverEnvironment(s)
		envAny := map[string]any{}
		for k, v := range env {
			envAny[k] = v
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"container_image": egg.ScriptContainer,
			"entrypoint":      egg.ScriptEntry,
			"script":          egg.ScriptInstall,
			"env":             envAny,
		})
	}))

	mux.HandleFunc("POST /api/remote/servers/{uuid}/install", h(func(w http.ResponseWriter, r *http.Request) {
		s := a.remoteServer(w, r)
		if s == nil {
			return
		}
		var body struct {
			Successful bool `json:"successful"`
			Reinstall  bool `json:"reinstall"`
		}
		decode(r, &body)
		if body.Successful {
			s.Status = nil
			ts := nowISO()
			s.InstalledAt = &ts
		} else if !body.Reinstall {
			status := "install_failed"
			s.Status = &status
		}
		a.Store.UpdateServer(s)
		writeNoContent(w)
	}))

	// SFTP credential check: username is "<user>.<server-short-uuid>".
	mux.HandleFunc("POST /api/remote/sftp/auth", h(func(w http.ResponseWriter, r *http.Request) {
		node := nodeFrom(r)
		var body struct {
			Type          string `json:"type"`
			Username      string `json:"username"`
			Password      string `json:"password"`
			IP            string `json:"ip"`
			SessionID     string `json:"session_id"`
			ClientVersion string `json:"client_version"`
		}
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		userPart, serverPart, ok := strings.Cut(body.Username, ".")
		if !ok {
			writeError(w, http.StatusForbidden, "Invalid username format; expected username.server-id.")
			return
		}
		u, err := a.Store.UserByUsername(userPart)
		if err != nil {
			writeError(w, http.StatusForbidden, "Invalid credentials.")
			return
		}
		srv, err := a.Store.ServerByIdentifier(serverPart)
		if err != nil || srv.NodeID != node.ID {
			writeError(w, http.StatusForbidden, "Invalid credentials.")
			return
		}

		if body.Type == "public_key" {
			keys, _ := a.Store.SSHKeysForUser(u.ID)
			matched := false
			for _, k := range keys {
				if strings.TrimSpace(k.PublicKey) == strings.TrimSpace(body.Password) {
					matched = true
					break
				}
			}
			if !matched {
				writeError(w, http.StatusForbidden, "Invalid credentials.")
				return
			}
		} else if !auth.CheckPassword(u.Password, body.Password) {
			writeError(w, http.StatusForbidden, "Invalid credentials.")
			return
		}

		perms := a.userPermissions(u, srv)
		if len(perms) == 0 || (!contains(perms, "*") && !contains(perms, "file.sftp")) {
			writeError(w, http.StatusForbidden, "You do not have permission to access SFTP for this server.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"server":      srv.UUID,
			"user":        u.UUID,
			"permissions": perms,
		})
	}))

	// Activity ingest from wings (power events, SFTP, etc.).
	mux.HandleFunc("POST /api/remote/activity", h(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Data []struct {
				Server    string          `json:"server"`
				Event     string          `json:"event"`
				Metadata  json.RawMessage `json:"metadata"`
				IP        string          `json:"ip"`
				User      *string         `json:"user"`
				Timestamp string          `json:"timestamp"`
			} `json:"data"`
		}
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		for _, entry := range body.Data {
			srv, err := a.Store.ServerByUUID(entry.Server)
			if err != nil {
				continue
			}
			props := "{}"
			if len(entry.Metadata) > 0 {
				props = string(entry.Metadata)
			}
			log := &store.ActivityLog{
				Event: entry.Event, IP: entry.IP, Properties: props, Timestamp: entry.Timestamp,
			}
			if entry.User != nil {
				if u, err := a.userByUUID(*entry.User); err == nil {
					log.ActorID = &u.ID
				}
			}
			a.Store.LogActivity(log, [2]any{"server", srv.ID})
		}
		writeNoContent(w)
	}))

	// Backup status callbacks.
	mux.HandleFunc("POST /api/remote/backups/{backup}", h(func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Store.BackupByUUID(r.PathValue("backup"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Backup not found.")
			return
		}
		var body struct {
			Checksum     string `json:"checksum"`
			ChecksumType string `json:"checksum_type"`
			Size         int64  `json:"size"`
			Successful   bool   `json:"successful"`
		}
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		checksum := body.ChecksumType + ":" + body.Checksum
		ts := nowISO()
		b.IsSuccessful = body.Successful
		b.Checksum = &checksum
		b.Bytes = body.Size
		b.CompletedAt = &ts
		a.Store.UpdateBackup(b)
		writeNoContent(w)
	}))

	mux.HandleFunc("POST /api/remote/backups/{backup}/restore", h(func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Store.BackupByUUID(r.PathValue("backup"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Backup not found.")
			return
		}
		srv, err := a.Store.ServerByID(b.ServerID)
		if err == nil && srv.Status != nil && *srv.Status == "restoring_backup" {
			srv.Status = nil
			a.Store.UpdateServer(srv)
		}
		writeNoContent(w)
	}))
}

func (a *API) remoteServer(w http.ResponseWriter, r *http.Request) *store.Server {
	node := nodeFrom(r)
	s, err := a.Store.ServerByUUID(r.PathValue("uuid"))
	if err != nil || s.NodeID != node.ID {
		writeError(w, http.StatusNotFound, "Server not found on this node.")
		return nil
	}
	return s
}

func (a *API) userByUUID(uuid string) (*store.User, error) {
	users, err := a.Store.Users("")
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.UUID == uuid {
			return u, nil
		}
	}
	return nil, store.ErrNotFound
}
