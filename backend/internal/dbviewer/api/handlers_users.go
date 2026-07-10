package api

import (
	"net/http"
	"strings"

	"roost/internal/dbviewer/db"
)

func (a *API) handleUsers(w http.ResponseWriter, r *http.Request) {
	users, err := db.Users(r.Context(), sess(r).DB)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (a *API) handleUserGrants(w http.ResponseWriter, r *http.Request) {
	grants, err := db.Grants(r.Context(), sess(r).DB, r.PathValue("user"), r.PathValue("host"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": grants})
}

type createUserReq struct {
	User       string `json:"user"`
	Host       string `json:"host"`
	Password   string `json:"password"`
	Privileges string `json:"privileges"`
	Scope      string `json:"scope"`
}

func (a *API) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if err := decode(r, &req); err != nil || strings.TrimSpace(req.User) == "" {
		writeErr(w, http.StatusBadRequest, "username required")
		return
	}
	if req.Host == "" {
		req.Host = "%"
	}
	if err := db.CreateUser(r.Context(), sess(r).DB, req.User, req.Host, req.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Privileges != "" {
		if err := db.GrantPrivileges(r.Context(), sess(r).DB, req.User, req.Host, req.Privileges, req.Scope); err != nil {
			writeErr(w, http.StatusBadRequest, "user created but grant failed: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) handleDropUser(w http.ResponseWriter, r *http.Request) {
	if err := db.DropUser(r.Context(), sess(r).DB, r.PathValue("user"), r.PathValue("host")); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
