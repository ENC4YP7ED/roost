package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"roost/internal/auth"
	"roost/internal/store"
	"roost/internal/wings"
)

func (a *API) routesClientFeatures(mux *http.ServeMux) {
	h := a.requireUser
	base := "/api/client/servers/{server}"

	// ---- databases ----
	mux.HandleFunc("GET "+base+"/databases", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		dbs, _ := a.Store.DatabasesForServer(srv.ID)
		rows := make([]map[string]any, 0, len(dbs))
		includePassword := strings.Contains(r.URL.Query().Get("include"), "password")
		for _, d := range dbs {
			rows = append(rows, a.trClientDatabase(d, includePassword))
		}
		writeList(w, r, "server_database", rows)
	}, "database.read")))

	mux.HandleFunc("POST "+base+"/databases", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		var body struct {
			Database string `json:"database"`
			Remote   string `json:"remote"`
		}
		if err := decode(r, &body); err != nil || body.Database == "" {
			writeError(w, http.StatusUnprocessableEntity, "A database name must be provided.")
			return
		}
		count, _ := a.Store.CountDatabasesForServer(srv.ID)
		if srv.DatabaseLimit == 0 || count >= srv.DatabaseLimit {
			writeError(w, http.StatusBadRequest, "This server has reached its database limit.")
			return
		}
		hosts, _ := a.Store.DatabaseHosts()
		if len(hosts) == 0 {
			writeError(w, http.StatusBadRequest, "No database hosts are configured on this panel.")
			return
		}
		if body.Remote == "" {
			body.Remote = "%"
		}
		d := &store.ServerDatabase{
			ServerID:       srv.ID,
			DatabaseHostID: hosts[0].ID,
			Database:       fmt.Sprintf("s%d_%s", srv.ID, body.Database),
			Username:       fmt.Sprintf("u%d_%s", srv.ID, auth.RandomAlnum(10)),
			Remote:         body.Remote,
			Password:       auth.RandomAlnum(24),
		}
		if err := a.Store.CreateServerDatabase(d); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.activity(r, "server:database.create", map[string]any{"name": d.Database}, [2]any{"server", srv.ID})
		writeItem(w, http.StatusOK, "server_database", a.trClientDatabase(d, true))
	}, "database.create")))

	mux.HandleFunc("POST "+base+"/databases/{database}/rotate-password", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		d, err := a.Store.ServerDatabaseByID(parseID(r, "database"))
		if err != nil || d.ServerID != srv.ID {
			writeError(w, http.StatusNotFound, "Database not found.")
			return
		}
		d.Password = auth.RandomAlnum(24)
		a.Store.UpdateServerDatabase(d)
		a.activity(r, "server:database.rotate-password", map[string]any{"name": d.Database}, [2]any{"server", srv.ID})
		writeItem(w, http.StatusOK, "server_database", a.trClientDatabase(d, true))
	}, "database.update")))

	mux.HandleFunc("DELETE "+base+"/databases/{database}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		d, err := a.Store.ServerDatabaseByID(parseID(r, "database"))
		if err != nil || d.ServerID != srv.ID {
			writeError(w, http.StatusNotFound, "Database not found.")
			return
		}
		a.Store.DeleteServerDatabase(d.ID)
		a.activity(r, "server:database.delete", map[string]any{"name": d.Database}, [2]any{"server", srv.ID})
		writeNoContent(w)
	}, "database.delete")))

	// ---- schedules ----
	mux.HandleFunc("GET "+base+"/schedules", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		schedules, _ := a.Store.SchedulesForServer(srv.ID)
		rows := make([]map[string]any, 0, len(schedules))
		for _, sc := range schedules {
			tasks, _ := a.Store.TasksForSchedule(sc.ID)
			rows = append(rows, trSchedule(sc, tasks))
		}
		writeList(w, r, "server_schedule", rows)
	}, "schedule.read")))

	mux.HandleFunc("POST "+base+"/schedules", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		a.upsertSchedule(w, r, nil)
	}, "schedule.create")))

	mux.HandleFunc("GET "+base+"/schedules/{schedule}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		sc, err := a.Store.ScheduleByID(parseID(r, "schedule"))
		if err != nil || sc.ServerID != srv.ID {
			writeError(w, http.StatusNotFound, "Schedule not found.")
			return
		}
		tasks, _ := a.Store.TasksForSchedule(sc.ID)
		writeItem(w, http.StatusOK, "server_schedule", trSchedule(sc, tasks))
	}, "schedule.read")))

	mux.HandleFunc("POST "+base+"/schedules/{schedule}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		sc, err := a.Store.ScheduleByID(parseID(r, "schedule"))
		if err != nil || sc.ServerID != srv.ID {
			writeError(w, http.StatusNotFound, "Schedule not found.")
			return
		}
		a.upsertSchedule(w, r, sc)
	}, "schedule.update")))

	mux.HandleFunc("POST "+base+"/schedules/{schedule}/execute", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		sc, err := a.Store.ScheduleByID(parseID(r, "schedule"))
		if err != nil || sc.ServerID != srv.ID {
			writeError(w, http.StatusNotFound, "Schedule not found.")
			return
		}
		go a.runSchedule(sc)
		a.activity(r, "server:schedule.execute", map[string]any{"name": sc.Name}, [2]any{"server", srv.ID})
		w.WriteHeader(http.StatusAccepted)
	}, "schedule.update")))

	mux.HandleFunc("DELETE "+base+"/schedules/{schedule}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		sc, err := a.Store.ScheduleByID(parseID(r, "schedule"))
		if err != nil || sc.ServerID != srv.ID {
			writeError(w, http.StatusNotFound, "Schedule not found.")
			return
		}
		a.Store.DeleteSchedule(sc.ID)
		a.activity(r, "server:schedule.delete", map[string]any{"name": sc.Name}, [2]any{"server", srv.ID})
		writeNoContent(w)
	}, "schedule.delete")))

	mux.HandleFunc("POST "+base+"/schedules/{schedule}/tasks", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		a.upsertTask(w, r, nil)
	}, "schedule.update")))

	mux.HandleFunc("POST "+base+"/schedules/{schedule}/tasks/{task}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		t, err := a.Store.TaskByID(parseID(r, "task"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Task not found.")
			return
		}
		a.upsertTask(w, r, t)
	}, "schedule.update")))

	mux.HandleFunc("DELETE "+base+"/schedules/{schedule}/tasks/{task}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		a.Store.DeleteTask(parseID(r, "task"))
		writeNoContent(w)
	}, "schedule.update")))

	// ---- network / allocations ----
	mux.HandleFunc("GET "+base+"/network/allocations", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		allocs, _ := a.Store.AllocationsForServer(srv.ID)
		rows := make([]map[string]any, 0, len(allocs))
		for _, al := range allocs {
			at := trAllocation(al)
			at["is_default"] = srv.AllocationID != nil && *srv.AllocationID == al.ID
			delete(at, "assigned")
			rows = append(rows, at)
		}
		writeList(w, r, "allocation", rows)
	}, "allocation.read")))

	mux.HandleFunc("POST "+base+"/network/allocations", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		existing, _ := a.Store.AllocationsForServer(srv.ID)
		if int64(len(existing)) >= srv.AllocationLimit {
			writeError(w, http.StatusBadRequest, "This server has reached its allocation limit.")
			return
		}
		free, err := a.Store.FreeAllocation(srv.NodeID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "No additional allocations are available on this node.")
			return
		}
		free.ServerID = &srv.ID
		a.Store.UpdateAllocation(free)
		a.syncWings(srv)
		a.activity(r, "server:allocation.create", map[string]any{"allocation": fmt.Sprintf("%s:%d", free.IP, free.Port)}, [2]any{"server", srv.ID})
		at := trAllocation(free)
		at["is_default"] = false
		delete(at, "assigned")
		writeItem(w, http.StatusOK, "allocation", at)
	}, "allocation.create")))

	mux.HandleFunc("POST "+base+"/network/allocations/{allocation}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		al, err := a.Store.AllocationByID(parseID(r, "allocation"))
		if err != nil || al.ServerID == nil || *al.ServerID != srv.ID {
			writeError(w, http.StatusNotFound, "Allocation not found.")
			return
		}
		var body struct {
			Notes *string `json:"notes"`
		}
		decode(r, &body)
		al.Notes = body.Notes
		a.Store.UpdateAllocation(al)
		at := trAllocation(al)
		at["is_default"] = srv.AllocationID != nil && *srv.AllocationID == al.ID
		delete(at, "assigned")
		writeItem(w, http.StatusOK, "allocation", at)
	}, "allocation.update")))

	mux.HandleFunc("POST "+base+"/network/allocations/{allocation}/primary", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		al, err := a.Store.AllocationByID(parseID(r, "allocation"))
		if err != nil || al.ServerID == nil || *al.ServerID != srv.ID {
			writeError(w, http.StatusNotFound, "Allocation not found.")
			return
		}
		srv.AllocationID = &al.ID
		a.Store.UpdateServer(srv)
		a.syncWings(srv)
		a.activity(r, "server:allocation.primary", map[string]any{"allocation": fmt.Sprintf("%s:%d", al.IP, al.Port)}, [2]any{"server", srv.ID})
		at := trAllocation(al)
		at["is_default"] = true
		delete(at, "assigned")
		writeItem(w, http.StatusOK, "allocation", at)
	}, "allocation.update")))

	mux.HandleFunc("DELETE "+base+"/network/allocations/{allocation}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		al, err := a.Store.AllocationByID(parseID(r, "allocation"))
		if err != nil || al.ServerID == nil || *al.ServerID != srv.ID {
			writeError(w, http.StatusNotFound, "Allocation not found.")
			return
		}
		if srv.AllocationID != nil && *srv.AllocationID == al.ID {
			writeError(w, http.StatusBadRequest, "You cannot delete the primary allocation for this server.")
			return
		}
		al.ServerID = nil
		al.Notes = nil
		a.Store.UpdateAllocation(al)
		a.syncWings(srv)
		a.activity(r, "server:allocation.delete", map[string]any{"allocation": fmt.Sprintf("%s:%d", al.IP, al.Port)}, [2]any{"server", srv.ID})
		writeNoContent(w)
	}, "allocation.delete")))

	// ---- subusers ----
	mux.HandleFunc("GET "+base+"/users", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		subs, _ := a.Store.SubusersForServer(srv.ID)
		rows := make([]map[string]any, 0, len(subs))
		for _, sub := range subs {
			if at := a.trSubuser(sub); at != nil {
				rows = append(rows, at)
			}
		}
		writeList(w, r, "server_subuser", rows)
	}, "user.read")))

	mux.HandleFunc("POST "+base+"/users", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		var body struct {
			Email       string   `json:"email"`
			Permissions []string `json:"permissions"`
		}
		if err := decode(r, &body); err != nil || body.Email == "" {
			writeError(w, http.StatusUnprocessableEntity, "An email address must be provided.")
			return
		}
		target, err := a.Store.UserByEmail(body.Email)
		if err != nil {
			// Pterodactyl auto-creates an account for unknown emails.
			username := strings.Split(body.Email, "@")[0] + "_" + auth.RandomAlnum(4)
			pw, _ := auth.HashPassword(auth.RandomAlnum(24))
			target = &store.User{
				UUID: auth.UUID(), Username: username, Email: body.Email, Password: pw, Language: "en",
			}
			if err := a.Store.CreateUser(target); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if target.ID == srv.OwnerID {
			writeError(w, http.StatusBadRequest, "You cannot add the server owner as a subuser.")
			return
		}
		perms, _ := json.Marshal(append(uniqueValid(body.Permissions), "websocket.connect"))
		sub := &store.Subuser{UserID: target.ID, ServerID: srv.ID, Permissions: string(perms)}
		if err := a.Store.CreateSubuser(sub); err != nil {
			writeError(w, http.StatusConflict, "That user is already a subuser on this server.")
			return
		}
		a.activity(r, "server:subuser.create", map[string]any{"email": body.Email}, [2]any{"server", srv.ID})
		writeItem(w, http.StatusOK, "server_subuser", a.trSubuser(sub))
	}, "user.create")))

	mux.HandleFunc("GET "+base+"/users/{user}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		sub := a.subuserByUUID(w, r)
		if sub == nil {
			return
		}
		writeItem(w, http.StatusOK, "server_subuser", a.trSubuser(sub))
	}, "user.read")))

	mux.HandleFunc("POST "+base+"/users/{user}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		sub := a.subuserByUUID(w, r)
		if sub == nil {
			return
		}
		var body struct {
			Permissions []string `json:"permissions"`
		}
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
			return
		}
		perms, _ := json.Marshal(append(uniqueValid(body.Permissions), "websocket.connect"))
		sub.Permissions = string(perms)
		a.Store.UpdateSubuser(sub)
		a.activity(r, "server:subuser.update", nil, [2]any{"server", sub.ServerID})
		writeItem(w, http.StatusOK, "server_subuser", a.trSubuser(sub))
	}, "user.update")))

	mux.HandleFunc("DELETE "+base+"/users/{user}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		sub := a.subuserByUUID(w, r)
		if sub == nil {
			return
		}
		a.Store.DeleteSubuser(sub.ServerID, sub.UserID)
		a.activity(r, "server:subuser.delete", nil, [2]any{"server", sub.ServerID})
		writeNoContent(w)
	}, "user.delete")))

	// ---- backups ----
	mux.HandleFunc("GET "+base+"/backups", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		backups, _ := a.Store.BackupsForServer(srv.ID)
		rows := make([]map[string]any, 0, len(backups))
		for _, b := range backups {
			rows = append(rows, trBackup(b))
		}
		count, _ := a.Store.CountBackupsForServer(srv.ID)
		page := map[string]any{"backup_count": count}
		_ = page
		writeList(w, r, "backup", rows)
	}, "backup.read")))

	mux.HandleFunc("POST "+base+"/backups", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		var body struct {
			Name     string `json:"name"`
			Ignored  string `json:"ignored"`
			IsLocked bool   `json:"is_locked"`
		}
		decode(r, &body)
		count, _ := a.Store.CountBackupsForServer(srv.ID)
		if srv.BackupLimit == 0 || count >= srv.BackupLimit {
			writeError(w, http.StatusBadRequest, "This server has reached its backup limit.")
			return
		}
		if body.Name == "" {
			body.Name = "Backup at " + nowISO()
		}
		ignored, _ := json.Marshal(splitLines(body.Ignored))
		b := &store.Backup{
			ServerID: srv.ID, UUID: auth.UUID(), Name: body.Name,
			IgnoredFiles: string(ignored), IsLocked: body.IsLocked, Disk: "wings",
		}
		if err := a.Store.CreateBackup(b); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		node, _ := a.Store.NodeByID(srv.NodeID)
		if err := wings.New(node).Backup(srv.UUID, map[string]any{
			"adapter": "wings", "uuid": b.UUID, "ignore": body.Ignored,
		}); err != nil {
			// Leave the row; wings will report status when reachable again.
		}
		a.activity(r, "server:backup.start", map[string]any{"name": b.Name}, [2]any{"server", srv.ID})
		writeItem(w, http.StatusOK, "backup", trBackup(b))
	}, "backup.create")))

	mux.HandleFunc("GET "+base+"/backups/{backup}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		b := a.backupFor(w, r)
		if b == nil {
			return
		}
		writeItem(w, http.StatusOK, "backup", trBackup(b))
	}, "backup.read")))

	mux.HandleFunc("GET "+base+"/backups/{backup}/download", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		u, srv := userFrom(r), serverFrom(r)
		b := a.backupFor(w, r)
		if b == nil {
			return
		}
		node, _ := a.Store.NodeByID(srv.NodeID)
		link, err := wings.BackupDownloadURL(a.PanelURL(), node, srv, u, b.UUID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.activity(r, "server:backup.download", map[string]any{"name": b.Name}, [2]any{"server", srv.ID})
		writeItem(w, http.StatusOK, "signed_url", map[string]any{"url": link})
	}, "backup.download")))

	mux.HandleFunc("POST "+base+"/backups/{backup}/lock", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		b := a.backupFor(w, r)
		if b == nil {
			return
		}
		b.IsLocked = !b.IsLocked
		a.Store.UpdateBackup(b)
		writeItem(w, http.StatusOK, "backup", trBackup(b))
	}, "backup.delete")))

	mux.HandleFunc("POST "+base+"/backups/{backup}/restore", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		b := a.backupFor(w, r)
		if b == nil {
			return
		}
		var body struct {
			TruncateDirectory bool `json:"truncate"`
		}
		decode(r, &body)
		node, _ := a.Store.NodeByID(srv.NodeID)
		status := "restoring_backup"
		srv.Status = &status
		a.Store.UpdateServer(srv)
		if err := wings.New(node).RestoreBackup(srv.UUID, b.UUID, map[string]any{
			"adapter": "wings", "truncate_directory": body.TruncateDirectory,
		}); err != nil {
			srv.Status = nil
			a.Store.UpdateServer(srv)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		a.activity(r, "server:backup.restore", map[string]any{"name": b.Name}, [2]any{"server", srv.ID})
		writeNoContent(w)
	}, "backup.restore")))

	mux.HandleFunc("DELETE "+base+"/backups/{backup}", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		b := a.backupFor(w, r)
		if b == nil {
			return
		}
		if b.IsLocked {
			writeError(w, http.StatusBadRequest, "Cannot delete a locked backup.")
			return
		}
		node, _ := a.Store.NodeByID(srv.NodeID)
		wings.New(node).DeleteBackup(srv.UUID, b.UUID)
		ts := nowISO()
		b.DeletedAt = &ts
		a.Store.UpdateBackup(b)
		a.activity(r, "server:backup.delete", map[string]any{"name": b.Name}, [2]any{"server", srv.ID})
		writeNoContent(w)
	}, "backup.delete")))

	// ---- startup ----
	mux.HandleFunc("GET "+base+"/startup", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		vars, _ := a.Store.EggVariables(srv.EggID)
		values, _ := a.Store.ServerVariableValues(srv.ID)
		rows := make([]map[string]any, 0, len(vars))
		for _, v := range vars {
			if !v.UserViewable {
				continue
			}
			at := trEggVariable(v)
			delete(at, "id")
			delete(at, "egg_id")
			delete(at, "user_viewable")
			at["is_editable"] = v.UserEditable
			delete(at, "user_editable")
			if val, ok := values[v.ID]; ok {
				at["server_value"] = val
			} else {
				at["server_value"] = v.DefaultValue
			}
			rows = append(rows, at)
		}
		egg, _ := a.Store.EggByID(srv.EggID)
		images := map[string]any{}
		if egg != nil {
			images = jsonObj(egg.DockerImages)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data":   toItems("egg_variable", rows),
			"meta": map[string]any{
				"startup_command":     a.renderStartup(srv),
				"raw_startup_command": srv.Startup,
				"docker_images":       images,
			},
		})
	}, "startup.read")))

	mux.HandleFunc("PUT "+base+"/startup/variable", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		var body struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := decode(r, &body); err != nil || body.Key == "" {
			writeError(w, http.StatusUnprocessableEntity, "A variable key must be provided.")
			return
		}
		vars, _ := a.Store.EggVariables(srv.EggID)
		for _, v := range vars {
			if v.EnvVariable != body.Key {
				continue
			}
			if !v.UserEditable || !v.UserViewable {
				writeError(w, http.StatusBadRequest, "This variable cannot be edited.")
				return
			}
			a.Store.SetServerVariable(srv.ID, v.ID, body.Value)
			a.activity(r, "server:startup.edit", map[string]any{"variable": body.Key, "value": body.Value}, [2]any{"server", srv.ID})
			at := trEggVariable(v)
			delete(at, "id")
			delete(at, "egg_id")
			delete(at, "user_viewable")
			at["is_editable"] = v.UserEditable
			delete(at, "user_editable")
			at["server_value"] = body.Value
			writeItem(w, http.StatusOK, "egg_variable", at)
			return
		}
		writeError(w, http.StatusNotFound, "The requested variable was not found.")
	}, "startup.update")))

	// ---- settings ----
	mux.HandleFunc("POST "+base+"/settings/rename", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		var body struct {
			Name        string  `json:"name"`
			Description *string `json:"description"`
		}
		if err := decode(r, &body); err != nil || body.Name == "" {
			writeError(w, http.StatusUnprocessableEntity, "A server name must be provided.")
			return
		}
		old := srv.Name
		srv.Name = body.Name
		if body.Description != nil {
			srv.Description = *body.Description
		}
		a.Store.UpdateServer(srv)
		a.syncWings(srv)
		a.activity(r, "server:settings.rename", map[string]any{"old": old, "new": body.Name}, [2]any{"server", srv.ID})
		writeNoContent(w)
	}, "settings.rename")))

	mux.HandleFunc("POST "+base+"/settings/reinstall", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		status := "installing"
		srv.Status = &status
		a.Store.UpdateServer(srv)
		node, _ := a.Store.NodeByID(srv.NodeID)
		if err := wings.New(node).Reinstall(srv.UUID); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		a.activity(r, "server:settings.reinstall", nil, [2]any{"server", srv.ID})
		w.WriteHeader(http.StatusAccepted)
	}, "settings.reinstall")))

	mux.HandleFunc("PUT "+base+"/settings/docker-image", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		srv := serverFrom(r)
		var body struct {
			DockerImage string `json:"docker_image"`
		}
		if err := decode(r, &body); err != nil || body.DockerImage == "" {
			writeError(w, http.StatusUnprocessableEntity, "A docker image must be provided.")
			return
		}
		egg, _ := a.Store.EggByID(srv.EggID)
		allowed := false
		if egg != nil {
			for _, img := range jsonObj(egg.DockerImages) {
				if s, ok := img.(string); ok && s == body.DockerImage {
					allowed = true
				}
			}
		}
		if !allowed {
			writeError(w, http.StatusBadRequest, "The requested image is not allowed for this egg.")
			return
		}
		srv.Image = body.DockerImage
		a.Store.UpdateServer(srv)
		a.syncWings(srv)
		a.activity(r, "server:startup.image", map[string]any{"image": body.DockerImage}, [2]any{"server", srv.ID})
		writeNoContent(w)
	}, "startup.docker-image")))
}

// ---- helpers ----

func toItems(object string, rows []map[string]any) []item {
	out := make([]item, 0, len(rows))
	for _, row := range rows {
		out = append(out, obj(object, row))
	}
	return out
}

func uniqueValid(perms []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range perms {
		if p == "websocket.connect" || seen[p] {
			continue
		}
		if contains(AllPermissions, p) {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func (a *API) subuserByUUID(w http.ResponseWriter, r *http.Request) *store.Subuser {
	srv := serverFrom(r)
	uuid := r.PathValue("user")
	subs, _ := a.Store.SubusersForServer(srv.ID)
	for _, sub := range subs {
		if u, err := a.Store.UserByID(sub.UserID); err == nil && u.UUID == uuid {
			return sub
		}
	}
	writeError(w, http.StatusNotFound, "The requested subuser was not found.")
	return nil
}

func (a *API) backupFor(w http.ResponseWriter, r *http.Request) *store.Backup {
	srv := serverFrom(r)
	b, err := a.Store.BackupByUUID(r.PathValue("backup"))
	if err != nil || b.ServerID != srv.ID || b.DeletedAt != nil {
		writeError(w, http.StatusNotFound, "The requested backup was not found.")
		return nil
	}
	return b
}

// syncWings tells the node to re-pull this server's configuration; failures
// are ignored (wings syncs on its own schedule too).
func (a *API) syncWings(srv *store.Server) {
	if node, err := a.Store.NodeByID(srv.NodeID); err == nil {
		go wings.New(node).Sync(srv.UUID)
	}
}
