package api

import (
	"encoding/json"
	"strconv"

	"roost/internal/store"
)

// Transformers turn store records into Pterodactyl-shaped attribute maps.

func jsonArr(raw string) []any {
	var v []any
	if json.Unmarshal([]byte(raw), &v) != nil || v == nil {
		return []any{}
	}
	return v
}

func jsonObj(raw string) map[string]any {
	var v map[string]any
	if json.Unmarshal([]byte(raw), &v) != nil || v == nil {
		return map[string]any{}
	}
	return v
}

// ---- application API shapes ----

func trUser(u *store.User) map[string]any {
	return map[string]any{
		"id":          u.ID,
		"external_id": u.ExternalID,
		"uuid":        u.UUID,
		"username":    u.Username,
		"email":       u.Email,
		"first_name":  u.NameFirst,
		"last_name":   u.NameLast,
		"language":    u.Language,
		"root_admin":  u.RootAdmin,
		"2fa":         u.UseTOTP,
		"created_at":  u.CreatedAt,
		"updated_at":  u.UpdatedAt,
	}
}

func trLocation(l *store.Location) map[string]any {
	return map[string]any{
		"id":         l.ID,
		"short":      l.Short,
		"long":       l.Long,
		"created_at": l.CreatedAt,
		"updated_at": l.UpdatedAt,
	}
}

func (a *API) trNode(n *store.Node) map[string]any {
	mem, disk, count, _ := a.Store.NodeUsage(n.ID)
	return map[string]any{
		"id":                  n.ID,
		"uuid":                n.UUID,
		"public":              n.Public,
		"name":                n.Name,
		"description":         n.Description,
		"location_id":         n.LocationID,
		"fqdn":                n.FQDN,
		"scheme":              n.Scheme,
		"behind_proxy":        n.BehindProxy,
		"maintenance_mode":    n.MaintenanceMode,
		"memory":              n.Memory,
		"memory_overallocate": n.MemoryOverallocate,
		"disk":                n.Disk,
		"disk_overallocate":   n.DiskOverallocate,
		"upload_size":         n.UploadSize,
		"daemon_listen":       n.DaemonListen,
		"daemon_sftp":         n.DaemonSFTP,
		"daemon_base":         n.DaemonBase,
		"created_at":          n.CreatedAt,
		"updated_at":          n.UpdatedAt,
		"allocated_resources": map[string]any{"memory": mem, "disk": disk},
		"servers_count":       count,
	}
}

func trAllocation(al *store.Allocation) map[string]any {
	return map[string]any{
		"id":       al.ID,
		"ip":       al.IP,
		"alias":    al.IPAlias,
		"port":     al.Port,
		"notes":    al.Notes,
		"assigned": al.ServerID != nil,
	}
}

func trNest(n *store.Nest) map[string]any {
	return map[string]any{
		"id":          n.ID,
		"uuid":        n.UUID,
		"author":      n.Author,
		"name":        n.Name,
		"description": n.Description,
		"created_at":  n.CreatedAt,
		"updated_at":  n.UpdatedAt,
	}
}

func trEgg(e *store.Egg) map[string]any {
	images := jsonObj(e.DockerImages)
	first := ""
	for _, v := range images {
		if s, ok := v.(string); ok {
			first = s
			break
		}
	}
	return map[string]any{
		"id":            e.ID,
		"uuid":          e.UUID,
		"name":          e.Name,
		"nest":          e.NestID,
		"author":        e.Author,
		"description":   e.Description,
		"features":      jsonArr(e.Features),
		"docker_image":  first,
		"docker_images": images,
		"config": map[string]any{
			"files":         jsonObj(e.ConfigFiles),
			"startup":       jsonObj(e.ConfigStartup),
			"stop":          e.ConfigStop,
			"logs":          jsonObj(e.ConfigLogs),
			"file_denylist": jsonArr(e.FileDenylist),
			"extends":       e.ConfigFrom,
		},
		"startup": e.Startup,
		"script": map[string]any{
			"privileged": e.ScriptPrivileged,
			"install":    e.ScriptInstall,
			"entry":      e.ScriptEntry,
			"container":  e.ScriptContainer,
			"extends":    e.CopyScriptFrom,
		},
		"created_at": e.CreatedAt,
		"updated_at": e.UpdatedAt,
	}
}

func trEggVariable(v *store.EggVariable) map[string]any {
	return map[string]any{
		"id":            v.ID,
		"egg_id":        v.EggID,
		"name":          v.Name,
		"description":   v.Description,
		"env_variable":  v.EnvVariable,
		"default_value": v.DefaultValue,
		"user_viewable": v.UserViewable,
		"user_editable": v.UserEditable,
		"rules":         v.Rules,
		"created_at":    v.CreatedAt,
		"updated_at":    v.UpdatedAt,
	}
}

func serverLimits(s *store.Server) map[string]any {
	return map[string]any{
		"memory":       s.Memory,
		"swap":         s.Swap,
		"disk":         s.Disk,
		"io":           s.IO,
		"cpu":          s.CPU,
		"threads":      s.Threads,
		"oom_disabled": s.OOMDisabled,
	}
}

func serverFeatureLimits(s *store.Server) map[string]any {
	return map[string]any{
		"databases":   s.DatabaseLimit,
		"allocations": s.AllocationLimit,
		"backups":     s.BackupLimit,
	}
}

// trServerApp is the application-API server shape.
func (a *API) trServerApp(s *store.Server) map[string]any {
	env, _ := a.serverEnvironment(s)
	attrs := map[string]any{
		"id":             s.ID,
		"external_id":    s.ExternalID,
		"uuid":           s.UUID,
		"identifier":     s.UUIDShort,
		"name":           s.Name,
		"description":    s.Description,
		"status":         s.Status,
		"suspended":      s.Status != nil && *s.Status == "suspended",
		"limits":         serverLimits(s),
		"feature_limits": serverFeatureLimits(s),
		"user":           s.OwnerID,
		"node":           s.NodeID,
		"allocation":     s.AllocationID,
		"nest":           s.NestID,
		"egg":            s.EggID,
		"container": map[string]any{
			"startup_command": s.Startup,
			"image":           s.Image,
			"installed":       s.InstalledAt != nil,
			"environment":     env,
		},
		"created_at": s.CreatedAt,
		"updated_at": s.UpdatedAt,
	}
	return attrs
}

// trServerClient is the client-API server shape.
func (a *API) trServerClient(s *store.Server, u *store.User) map[string]any {
	node, _ := a.Store.NodeByID(s.NodeID)
	egg, _ := a.Store.EggByID(s.EggID)
	allocations, _ := a.Store.AllocationsForServer(s.ID)

	allocItems := []item{}
	for _, al := range allocations {
		at := trAllocation(al)
		at["is_default"] = s.AllocationID != nil && *s.AllocationID == al.ID
		delete(at, "assigned")
		allocItems = append(allocItems, obj("allocation", at))
	}

	varItems := []item{}
	if vars, err := a.Store.EggVariables(s.EggID); err == nil {
		values, _ := a.Store.ServerVariableValues(s.ID)
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
			varItems = append(varItems, obj("egg_variable", at))
		}
	}

	attrs := map[string]any{
		"server_owner":              s.OwnerID == u.ID,
		"identifier":                s.UUIDShort,
		"internal_id":               s.ID,
		"uuid":                      s.UUID,
		"name":                      s.Name,
		"description":               s.Description,
		"status":                    s.Status,
		"is_suspended":              s.Status != nil && *s.Status == "suspended",
		"is_installing":             s.Status != nil && (*s.Status == "installing" || *s.Status == "install_failed"),
		"is_transferring":           false,
		"is_node_under_maintenance": node != nil && node.MaintenanceMode,
		"limits":                    serverLimits(s),
		"invocation":                s.Startup,
		"docker_image":              s.Image,
		"egg_features":              []any{},
		"feature_limits":            serverFeatureLimits(s),
		"relationships": map[string]any{
			"allocations": map[string]any{"object": "list", "data": allocItems},
			"variables":   map[string]any{"object": "list", "data": varItems},
		},
	}
	if node != nil {
		attrs["node"] = node.Name
		attrs["sftp_details"] = map[string]any{"ip": node.FQDN, "port": node.DaemonSFTP}
	}
	if egg != nil {
		attrs["egg_features"] = jsonArr(egg.Features)
	}
	return attrs
}

func trSchedule(sc *store.Schedule, tasks []*store.ScheduleTask) map[string]any {
	taskItems := []item{}
	for _, t := range tasks {
		taskItems = append(taskItems, obj("schedule_task", trTask(t)))
	}
	return map[string]any{
		"id":   sc.ID,
		"name": sc.Name,
		"cron": map[string]any{
			"day_of_week":  sc.CronDayOfWeek,
			"day_of_month": sc.CronDayOfMonth,
			"month":        sc.CronMonth,
			"hour":         sc.CronHour,
			"minute":       sc.CronMinute,
		},
		"is_active":        sc.IsActive,
		"is_processing":    sc.IsProcessing,
		"only_when_online": sc.OnlyWhenOnline,
		"last_run_at":      sc.LastRunAt,
		"next_run_at":      sc.NextRunAt,
		"created_at":       sc.CreatedAt,
		"updated_at":       sc.UpdatedAt,
		"relationships": map[string]any{
			"tasks": map[string]any{"object": "list", "data": taskItems},
		},
	}
}

func trTask(t *store.ScheduleTask) map[string]any {
	return map[string]any{
		"id":                  t.ID,
		"sequence_id":         t.SequenceID,
		"action":              t.Action,
		"payload":             t.Payload,
		"time_offset":         t.TimeOffset,
		"is_queued":           t.IsQueued,
		"continue_on_failure": t.ContinueOnFailure,
		"created_at":          t.CreatedAt,
		"updated_at":          t.UpdatedAt,
	}
}

func trBackup(b *store.Backup) map[string]any {
	return map[string]any{
		"uuid":          b.UUID,
		"is_successful": b.IsSuccessful,
		"is_locked":     b.IsLocked,
		"name":          b.Name,
		"ignored_files": jsonArr(b.IgnoredFiles),
		"checksum":      b.Checksum,
		"bytes":         b.Bytes,
		"created_at":    b.CreatedAt,
		"completed_at":  b.CompletedAt,
	}
}

func (a *API) trClientDatabase(d *store.ServerDatabase, includePassword bool) map[string]any {
	host, _ := a.Store.DatabaseHostByID(d.DatabaseHostID)
	attrs := map[string]any{
		"id":               strconv.FormatInt(d.ID, 10),
		"name":             d.Database,
		"username":         d.Username,
		"connections_from": d.Remote,
		"max_connections":  d.MaxConnections,
	}
	if host != nil {
		attrs["host"] = map[string]any{"address": host.Host, "port": host.Port}
	}
	if includePassword {
		attrs["relationships"] = map[string]any{
			"password": obj("database_password", map[string]any{"password": d.Password}),
		}
	}
	return attrs
}

func trServerDatabaseApp(d *store.ServerDatabase) map[string]any {
	return map[string]any{
		"id":              d.ID,
		"server":          d.ServerID,
		"host":            d.DatabaseHostID,
		"database":        d.Database,
		"username":        d.Username,
		"remote":          d.Remote,
		"max_connections": d.MaxConnections,
		"created_at":      d.CreatedAt,
		"updated_at":      d.UpdatedAt,
	}
}

func trDatabaseHost(h *store.DatabaseHost) map[string]any {
	return map[string]any{
		"id":            h.ID,
		"name":          h.Name,
		"host":          h.Host,
		"port":          h.Port,
		"username":      h.Username,
		"max_databases": h.MaxDatabases,
		"node":          h.NodeID,
		"created_at":    h.CreatedAt,
		"updated_at":    h.UpdatedAt,
	}
}

func trMount(m *store.Mount) map[string]any {
	return map[string]any{
		"id":             m.ID,
		"uuid":           m.UUID,
		"name":           m.Name,
		"description":    m.Description,
		"source":         m.Source,
		"target":         m.Target,
		"read_only":      m.ReadOnly,
		"user_mountable": m.UserMountable,
	}
}

func (a *API) trSubuser(sub *store.Subuser) map[string]any {
	u, err := a.Store.UserByID(sub.UserID)
	if err != nil {
		return nil
	}
	return map[string]any{
		"uuid":        u.UUID,
		"username":    u.Username,
		"email":       u.Email,
		"image":       "",
		"2fa_enabled": u.UseTOTP,
		"created_at":  sub.CreatedAt,
		"permissions": jsonArr(sub.Permissions),
	}
}

func trAPIKey(k *store.APIKey) map[string]any {
	return map[string]any{
		"identifier":   k.Identifier,
		"description":  k.Memo,
		"allowed_ips":  jsonArr(k.AllowedIPs),
		"last_used_at": k.LastUsedAt,
		"created_at":   k.CreatedAt,
	}
}

func trSSHKey(k *store.SSHKey) map[string]any {
	return map[string]any{
		"name":        k.Name,
		"fingerprint": k.Fingerprint,
		"public_key":  k.PublicKey,
		"created_at":  k.CreatedAt,
	}
}

func trActivity(l *store.ActivityLog) map[string]any {
	return map[string]any{
		"id":          l.ID,
		"batch":       l.Batch,
		"event":       l.Event,
		"is_api":      l.APIKeyID != nil,
		"ip":          l.IP,
		"description": l.Description,
		"properties":  jsonObj(l.Properties),
		"timestamp":   l.Timestamp,
	}
}
