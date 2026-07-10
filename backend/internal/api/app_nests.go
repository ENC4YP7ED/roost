package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"roost/internal/auth"
	"roost/internal/store"
)

func (a *API) routesApplicationNests(mux *http.ServeMux) {
	h := a.requireAdmin

	mux.HandleFunc("GET /api/application/nests", h(func(w http.ResponseWriter, r *http.Request) {
		nests, _ := a.Store.Nests()
		rows := make([]map[string]any, 0, len(nests))
		for _, n := range nests {
			at := trNest(n)
			eggs, _ := a.Store.EggsForNest(n.ID)
			at["eggs_count"] = len(eggs)
			rows = append(rows, at)
		}
		writeList(w, r, "nest", rows)
	}))

	mux.HandleFunc("GET /api/application/nests/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		n, err := a.Store.NestByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Nest not found.")
			return
		}
		writeItem(w, http.StatusOK, "nest", trNest(n))
	}))

	mux.HandleFunc("POST /api/application/nests", h(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Name, Description string }
		if err := decode(r, &body); err != nil || body.Name == "" {
			writeError(w, http.StatusUnprocessableEntity, "A name must be provided.")
			return
		}
		n := &store.Nest{UUID: auth.UUID(), Author: userFrom(r).Email, Name: body.Name, Description: body.Description}
		if err := a.Store.CreateNest(n); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeItem(w, http.StatusCreated, "nest", trNest(n))
	}))

	mux.HandleFunc("PATCH /api/application/nests/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		n, err := a.Store.NestByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Nest not found.")
			return
		}
		var body struct{ Name, Description *string }
		decode(r, &body)
		if body.Name != nil {
			n.Name = *body.Name
		}
		if body.Description != nil {
			n.Description = *body.Description
		}
		a.Store.UpdateNest(n)
		writeItem(w, http.StatusOK, "nest", trNest(n))
	}))

	mux.HandleFunc("DELETE /api/application/nests/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.DeleteNest(parseID(r, "id")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeNoContent(w)
	}))

	// ---- eggs ----
	mux.HandleFunc("GET /api/application/nests/{id}/eggs", h(func(w http.ResponseWriter, r *http.Request) {
		eggs, _ := a.Store.EggsForNest(parseID(r, "id"))
		rows := make([]map[string]any, 0, len(eggs))
		for _, e := range eggs {
			at := trEgg(e)
			if vars, err := a.Store.EggVariables(e.ID); err == nil {
				varItems := []item{}
				for _, v := range vars {
					varItems = append(varItems, obj("egg_variable", trEggVariable(v)))
				}
				at["relationships"] = map[string]any{"variables": map[string]any{"object": "list", "data": varItems}}
			}
			rows = append(rows, at)
		}
		writeList(w, r, "egg", rows)
	}))

	mux.HandleFunc("GET /api/application/nests/{id}/eggs/{egg}", h(func(w http.ResponseWriter, r *http.Request) {
		e, err := a.Store.EggByID(parseID(r, "egg"))
		if err != nil || e.NestID != parseID(r, "id") {
			writeError(w, http.StatusNotFound, "Egg not found.")
			return
		}
		at := trEgg(e)
		vars, _ := a.Store.EggVariables(e.ID)
		varItems := []item{}
		for _, v := range vars {
			varItems = append(varItems, obj("egg_variable", trEggVariable(v)))
		}
		at["relationships"] = map[string]any{"variables": map[string]any{"object": "list", "data": varItems}}
		writeItem(w, http.StatusOK, "egg", at)
	}))

	// Export in PTDL_v2 format (compatible with Pterodactyl's egg share).
	mux.HandleFunc("GET /api/application/nests/{id}/eggs/{egg}/export", h(func(w http.ResponseWriter, r *http.Request) {
		e, err := a.Store.EggByID(parseID(r, "egg"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Egg not found.")
			return
		}
		vars, _ := a.Store.EggVariables(e.ID)
		varList := []map[string]any{}
		for _, v := range vars {
			varList = append(varList, map[string]any{
				"name": v.Name, "description": v.Description,
				"env_variable": v.EnvVariable, "default_value": v.DefaultValue,
				"user_viewable": v.UserViewable, "user_editable": v.UserEditable,
				"rules": v.Rules, "field_type": "text",
			})
		}
		doc := map[string]any{
			"_comment":      "EGG EXPORTED FROM PTEROGO",
			"meta":          map[string]any{"version": "PTDL_v2", "update_url": e.UpdateURL},
			"exported_at":   time.Now().Format(time.RFC3339),
			"name":          e.Name,
			"author":        e.Author,
			"description":   e.Description,
			"features":      jsonArr(e.Features),
			"docker_images": jsonObj(e.DockerImages),
			"file_denylist": jsonArr(e.FileDenylist),
			"startup":       e.Startup,
			"config": map[string]any{
				"files":   e.ConfigFiles,
				"startup": e.ConfigStartup,
				"logs":    e.ConfigLogs,
				"stop":    e.ConfigStop,
			},
			"scripts": map[string]any{
				"installation": map[string]any{
					"script":     e.ScriptInstall,
					"container":  e.ScriptContainer,
					"entrypoint": e.ScriptEntry,
				},
			},
			"variables": varList,
		}
		w.Header().Set("Content-Disposition", `attachment; filename="egg-`+e.Name+`.json"`)
		writeJSON(w, http.StatusOK, doc)
	}))

	// Import a PTDL_v1/v2 egg JSON into a nest.
	mux.HandleFunc("POST /api/application/nests/{id}/eggs/import", h(func(w http.ResponseWriter, r *http.Request) {
		nest, err := a.Store.NestByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Nest not found.")
			return
		}
		var doc EggDocument
		if err := decode(r, &doc); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "The uploaded file is not a valid egg export.")
			return
		}
		egg, err := a.ImportEgg(nest.ID, &doc)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeItem(w, http.StatusCreated, "egg", trEgg(egg))
	}))

	mux.HandleFunc("DELETE /api/application/nests/{id}/eggs/{egg}", h(func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.DeleteEgg(parseID(r, "egg")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeNoContent(w)
	}))

	// ---- egg variables ----
	mux.HandleFunc("POST /api/application/nests/{id}/eggs/{egg}/variables", h(func(w http.ResponseWriter, r *http.Request) {
		a.upsertEggVariable(w, r, nil)
	}))
	mux.HandleFunc("PATCH /api/application/nests/{id}/eggs/{egg}/variables/{variable}", h(func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.EggVariableByID(parseID(r, "variable"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Variable not found.")
			return
		}
		a.upsertEggVariable(w, r, v)
	}))
	mux.HandleFunc("DELETE /api/application/nests/{id}/eggs/{egg}/variables/{variable}", h(func(w http.ResponseWriter, r *http.Request) {
		a.Store.DeleteEggVariable(parseID(r, "variable"))
		writeNoContent(w)
	}))

	// ---- egg update (admin editor) ----
	mux.HandleFunc("PATCH /api/application/nests/{id}/eggs/{egg}", h(func(w http.ResponseWriter, r *http.Request) {
		e, err := a.Store.EggByID(parseID(r, "egg"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Egg not found.")
			return
		}
		var body struct {
			Name            *string        `json:"name"`
			Description     *string        `json:"description"`
			DockerImages    map[string]any `json:"docker_images"`
			Startup         *string        `json:"startup"`
			ConfigFiles     *string        `json:"config_files"`
			ConfigStartup   *string        `json:"config_startup"`
			ConfigLogs      *string        `json:"config_logs"`
			ConfigStop      *string        `json:"config_stop"`
			ScriptInstall   *string        `json:"script_install"`
			ScriptEntry     *string        `json:"script_entry"`
			ScriptContainer *string        `json:"script_container"`
		}
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
			return
		}
		if body.Name != nil {
			e.Name = *body.Name
		}
		if body.Description != nil {
			e.Description = *body.Description
		}
		if body.DockerImages != nil {
			raw, _ := json.Marshal(body.DockerImages)
			e.DockerImages = string(raw)
		}
		if body.Startup != nil {
			e.Startup = *body.Startup
		}
		if body.ConfigFiles != nil {
			e.ConfigFiles = *body.ConfigFiles
		}
		if body.ConfigStartup != nil {
			e.ConfigStartup = *body.ConfigStartup
		}
		if body.ConfigLogs != nil {
			e.ConfigLogs = *body.ConfigLogs
		}
		if body.ConfigStop != nil {
			e.ConfigStop = *body.ConfigStop
		}
		if body.ScriptInstall != nil {
			e.ScriptInstall = *body.ScriptInstall
		}
		if body.ScriptEntry != nil {
			e.ScriptEntry = *body.ScriptEntry
		}
		if body.ScriptContainer != nil {
			e.ScriptContainer = *body.ScriptContainer
		}
		a.Store.UpdateEgg(e)
		writeItem(w, http.StatusOK, "egg", trEgg(e))
	}))
}

func (a *API) upsertEggVariable(w http.ResponseWriter, r *http.Request, v *store.EggVariable) {
	eggID := parseID(r, "egg")
	var body struct {
		Name         *string `json:"name"`
		Description  *string `json:"description"`
		EnvVariable  *string `json:"env_variable"`
		DefaultValue *string `json:"default_value"`
		UserViewable *bool   `json:"user_viewable"`
		UserEditable *bool   `json:"user_editable"`
		Rules        *string `json:"rules"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	isNew := v == nil
	if isNew {
		if body.Name == nil || body.EnvVariable == nil {
			writeError(w, http.StatusUnprocessableEntity, "A name and env_variable must be provided.")
			return
		}
		v = &store.EggVariable{EggID: eggID, UserViewable: true, UserEditable: true}
	}
	if body.Name != nil {
		v.Name = *body.Name
	}
	if body.Description != nil {
		v.Description = *body.Description
	}
	if body.EnvVariable != nil {
		v.EnvVariable = *body.EnvVariable
	}
	if body.DefaultValue != nil {
		v.DefaultValue = *body.DefaultValue
	}
	if body.UserViewable != nil {
		v.UserViewable = *body.UserViewable
	}
	if body.UserEditable != nil {
		v.UserEditable = *body.UserEditable
	}
	if body.Rules != nil {
		v.Rules = *body.Rules
	}
	var err error
	if isNew {
		err = a.Store.CreateEggVariable(v)
	} else {
		err = a.Store.UpdateEggVariable(v)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeItem(w, http.StatusOK, "egg_variable", trEggVariable(v))
}

// EggDocument models the PTDL_v1/v2 egg share format.
type EggDocument struct {
	Meta struct {
		Version   string  `json:"version"`
		UpdateURL *string `json:"update_url"`
	} `json:"meta"`
	Name         string          `json:"name"`
	Author       string          `json:"author"`
	Description  string          `json:"description"`
	Features     json.RawMessage `json:"features"`
	DockerImages json.RawMessage `json:"docker_images"`
	Image        string          `json:"image"`  // PTDL_v1
	Images       []string        `json:"images"` // PTDL_v1
	FileDenylist json.RawMessage `json:"file_denylist"`
	Startup      string          `json:"startup"`
	Config       struct {
		Files   json.RawMessage `json:"files"`
		Startup json.RawMessage `json:"startup"`
		Logs    json.RawMessage `json:"logs"`
		Stop    string          `json:"stop"`
	} `json:"config"`
	Scripts struct {
		Installation struct {
			Script     string `json:"script"`
			Container  string `json:"container"`
			Entrypoint string `json:"entrypoint"`
		} `json:"installation"`
	} `json:"scripts"`
	Variables []struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		EnvVariable  string `json:"env_variable"`
		DefaultValue string `json:"default_value"`
		UserViewable any    `json:"user_viewable"`
		UserEditable any    `json:"user_editable"`
		Rules        string `json:"rules"`
	} `json:"variables"`
}

// ImportEgg creates an egg + variables from a PTDL export document.
func (a *API) ImportEgg(nestID int64, doc *EggDocument) (*store.Egg, error) {
	images := "{}"
	if len(doc.DockerImages) > 0 {
		images = string(doc.DockerImages)
	} else if doc.Image != "" {
		raw, _ := json.Marshal(map[string]string{doc.Image: doc.Image})
		images = string(raw)
	} else if len(doc.Images) > 0 {
		m := map[string]string{}
		for _, img := range doc.Images {
			m[img] = img
		}
		raw, _ := json.Marshal(m)
		images = string(raw)
	}
	features := "[]"
	if len(doc.Features) > 0 && string(doc.Features) != "null" {
		features = string(doc.Features)
	}
	denylist := "[]"
	if len(doc.FileDenylist) > 0 && string(doc.FileDenylist) != "null" {
		denylist = string(doc.FileDenylist)
	}
	container := doc.Scripts.Installation.Container
	if container == "" {
		container = "alpine:3.4"
	}
	entry := doc.Scripts.Installation.Entrypoint
	if entry == "" {
		entry = "ash"
	}
	egg := &store.Egg{
		UUID:   auth.UUID(),
		NestID: nestID,
		Author: doc.Author, Name: doc.Name, Description: doc.Description,
		Features: features, DockerImages: images, FileDenylist: denylist,
		UpdateURL:   doc.Meta.UpdateURL,
		ConfigFiles: rawJSONString(doc.Config.Files), ConfigStartup: rawJSONString(doc.Config.Startup),
		ConfigLogs: rawJSONString(doc.Config.Logs), ConfigStop: doc.Config.Stop,
		Startup:         doc.Startup,
		ScriptContainer: container, ScriptEntry: entry, ScriptPrivileged: true,
		ScriptInstall: doc.Scripts.Installation.Script,
	}
	if err := a.Store.CreateEgg(egg); err != nil {
		return nil, err
	}
	for _, v := range doc.Variables {
		a.Store.CreateEggVariable(&store.EggVariable{
			EggID: egg.ID, Name: v.Name, Description: v.Description,
			EnvVariable: v.EnvVariable, DefaultValue: v.DefaultValue,
			UserViewable: truthy(v.UserViewable), UserEditable: truthy(v.UserEditable),
			Rules: v.Rules,
		})
	}
	return egg, nil
}

// rawJSONString normalises egg config fields, which exports encode either as
// a JSON string containing JSON, or as a plain JSON object.
func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "{}"
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return "{}"
		}
		return s
	}
	return string(raw)
}

// truthy handles the mixed bool/int encoding in old egg exports.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x == "1" || x == "true"
	}
	return false
}
