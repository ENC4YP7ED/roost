package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"roost/internal/auth"
	"roost/internal/store"
)

func newUUID() string { return auth.UUID() }

// serverEnvironment resolves the environment map wings passes into the
// container: egg variable defaults overridden by per-server values, plus the
// standard P_/SERVER_ variables Pterodactyl always injects.
func (a *API) serverEnvironment(s *store.Server) (map[string]string, error) {
	env := map[string]string{}
	vars, err := a.Store.EggVariables(s.EggID)
	if err != nil {
		return nil, err
	}
	values, err := a.Store.ServerVariableValues(s.ID)
	if err != nil {
		return nil, err
	}
	for _, v := range vars {
		if val, ok := values[v.ID]; ok {
			env[v.EnvVariable] = val
		} else {
			env[v.EnvVariable] = v.DefaultValue
		}
	}
	env["P_SERVER_UUID"] = s.UUID
	env["P_SERVER_ALLOCATION_LIMIT"] = fmt.Sprintf("%d", s.AllocationLimit)
	env["STARTUP"] = s.Startup
	env["SERVER_MEMORY"] = fmt.Sprintf("%d", s.Memory)
	if s.AllocationID != nil {
		if al, err := a.Store.AllocationByID(*s.AllocationID); err == nil {
			env["SERVER_IP"] = al.IP
			env["SERVER_PORT"] = fmt.Sprintf("%d", al.Port)
		}
	}
	if node, err := a.Store.NodeByID(s.NodeID); err == nil {
		if loc, err := a.Store.LocationByID(node.LocationID); err == nil {
			env["P_SERVER_LOCATION"] = loc.Short
		}
	}
	return env, nil
}

// renderStartup substitutes {{VAR}} placeholders in the startup command for
// display in the client UI.
func (a *API) renderStartup(s *store.Server) string {
	env, err := a.serverEnvironment(s)
	if err != nil {
		return s.Startup
	}
	out := s.Startup
	for k, v := range env {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
		out = strings.ReplaceAll(out, "${"+k+"}", v)
	}
	return out
}

// wingsServerConfiguration builds the configuration document wings fetches
// from GET /api/remote/servers/{uuid} — the same structure Pterodactyl's
// ServerConfigurationStructureService emits.
func (a *API) wingsServerConfiguration(s *store.Server) (map[string]any, error) {
	env, err := a.serverEnvironment(s)
	if err != nil {
		return nil, err
	}
	egg, err := a.Store.EggByID(s.EggID)
	if err != nil {
		return nil, err
	}

	allocations, _ := a.Store.AllocationsForServer(s.ID)
	mappings := map[string][]int{}
	var defaultAlloc map[string]any
	for _, al := range allocations {
		mappings[al.IP] = append(mappings[al.IP], al.Port)
		if s.AllocationID != nil && *s.AllocationID == al.ID {
			defaultAlloc = map[string]any{"ip": al.IP, "port": al.Port}
		}
	}
	if defaultAlloc == nil {
		defaultAlloc = map[string]any{"ip": "127.0.0.1", "port": 0}
	}

	mounts, _ := a.Store.MountsForServer(s.ID)
	mountList := []map[string]any{}
	for _, m := range mounts {
		mountList = append(mountList, map[string]any{
			"source": m.Source, "target": m.Target, "read_only": m.ReadOnly,
		})
	}

	envAny := map[string]any{}
	for k, v := range env {
		envAny[k] = v
	}

	return map[string]any{
		"uuid":             s.UUID,
		"meta":             map[string]any{"name": s.Name, "description": s.Description},
		"suspended":        s.Status != nil && *s.Status == "suspended",
		"environment":      envAny,
		"invocation":       s.Startup,
		"skip_egg_scripts": s.SkipScripts,
		"build": map[string]any{
			"memory_limit": s.Memory,
			"swap":         s.Swap,
			"io_weight":    s.IO,
			"cpu_limit":    s.CPU,
			"threads":      s.Threads,
			"disk_space":   s.Disk,
			"oom_disabled": s.OOMDisabled,
		},
		"container": map[string]any{
			"image":            s.Image,
			"requires_rebuild": false,
		},
		"allocations": map[string]any{
			"force_outgoing_ip": false,
			"default":           defaultAlloc,
			"mappings":          mappings,
		},
		"mounts": mountList,
		"egg": map[string]any{
			"id":            egg.UUID,
			"file_denylist": jsonArr(egg.FileDenylist),
		},
	}, nil
}

// wingsProcessConfiguration resolves the egg's config_* JSON into the
// process_configuration document, substituting {{server.build.*}} and
// {{env.*}} placeholders the way Pterodactyl does.
func (a *API) wingsProcessConfiguration(s *store.Server) (map[string]any, error) {
	egg, err := a.Store.EggByID(s.EggID)
	if err != nil {
		return nil, err
	}
	env, _ := a.serverEnvironment(s)

	replacer := func(raw string) string {
		out := raw
		out = strings.ReplaceAll(out, "{{server.build.default.port}}", env["SERVER_PORT"])
		out = strings.ReplaceAll(out, "{{server.build.default.ip}}", env["SERVER_IP"])
		out = strings.ReplaceAll(out, "{{server.build.memory}}", env["SERVER_MEMORY"])
		out = strings.ReplaceAll(out, "{{server.uuid}}", s.UUID)
		for k, v := range env {
			out = strings.ReplaceAll(out, "{{server.build.env."+k+"}}", v)
			out = strings.ReplaceAll(out, "{{env."+k+"}}", v)
		}
		return out
	}

	// config_files: {"path": {"parser": "...", "find": {key: value}}}
	configs := []map[string]any{}
	var files map[string]struct {
		Parser string         `json:"parser"`
		Find   map[string]any `json:"find"`
	}
	if err := json.Unmarshal([]byte(egg.ConfigFiles), &files); err == nil {
		for path, f := range files {
			replace := []map[string]any{}
			for k, v := range f.Find {
				switch val := v.(type) {
				case string:
					replace = append(replace, map[string]any{"match": k, "replace_with": replacer(val)})
				default:
					raw, _ := json.Marshal(val)
					replace = append(replace, map[string]any{"match": k, "replace_with": replacer(string(raw))})
				}
			}
			configs = append(configs, map[string]any{
				"parser":  f.Parser,
				"file":    path,
				"replace": replace,
			})
		}
	}

	// config_startup: {"done": "..."|[...], "userInteraction": [...]}
	startupCfg := jsonObj(egg.ConfigStartup)
	done := []string{}
	switch v := startupCfg["done"].(type) {
	case string:
		done = append(done, v)
	case []any:
		for _, d := range v {
			if s, ok := d.(string); ok {
				done = append(done, s)
			}
		}
	}

	stop := map[string]any{"type": "command", "value": egg.ConfigStop}
	if strings.HasPrefix(egg.ConfigStop, "^") {
		// "^C" style: send a signal instead of a command.
		stop = map[string]any{"type": "signal", "value": strings.TrimPrefix(egg.ConfigStop, "^")}
	}

	return map[string]any{
		"startup": map[string]any{
			"done":             done,
			"user_interaction": []string{},
			"strip_ansi":       false,
		},
		"stop":    stop,
		"configs": configs,
	}, nil
}

// wingsNodeConfiguration renders the wings config.yml content (as JSON) for
// GET /api/application/nodes/{id}/configuration.
func (a *API) wingsNodeConfiguration(n *store.Node) map[string]any {
	return map[string]any{
		"debug":    false,
		"uuid":     n.UUID,
		"token_id": n.DaemonTokenID,
		"token":    n.DaemonToken,
		"api": map[string]any{
			"host": "0.0.0.0",
			"port": n.DaemonListen,
			"ssl": map[string]any{
				"enabled": !n.BehindProxy && n.Scheme == "https",
				"cert":    "/etc/letsencrypt/live/" + n.FQDN + "/fullchain.pem",
				"key":     "/etc/letsencrypt/live/" + n.FQDN + "/privkey.pem",
			},
			"upload_limit": n.UploadSize,
		},
		"system": map[string]any{
			"data": n.DaemonBase,
			"sftp": map[string]any{"bind_port": n.DaemonSFTP},
		},
		"allowed_mounts": []string{},
		"remote":         a.PanelURL(),
	}
}
