package api

import (
	"net/http"

	"roost/internal/dbviewer/db"
)

type insertReq struct {
	Values map[string]*string `json:"values"`
}

type updateReq struct {
	Values map[string]*string `json:"values"`
	Where  map[string]*string `json:"where"`
}

type deleteReq struct {
	Where map[string]*string `json:"where"`
}

func (a *API) handleInsertRow(w http.ResponseWriter, r *http.Request) {
	var req insertReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, err := db.InsertRow(r.Context(), sess(r).DB, r.PathValue("db"), r.PathValue("table"), req.Values)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "insertId": id})
}

func (a *API) handleUpdateRow(w http.ResponseWriter, r *http.Request) {
	var req updateReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	n, err := db.UpdateRow(r.Context(), sess(r).DB, r.PathValue("db"), r.PathValue("table"), req.Values, req.Where)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "affected": n})
}

func (a *API) handleDeleteRow(w http.ResponseWriter, r *http.Request) {
	var req deleteReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	n, err := db.DeleteRow(r.Context(), sess(r).DB, r.PathValue("db"), r.PathValue("table"), req.Where)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "affected": n})
}
