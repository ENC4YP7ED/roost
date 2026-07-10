package api

import (
	"net/http"

	"roost/internal/dbviewer/db"
)

func (a *API) handleForeignKeys(w http.ResponseWriter, r *http.Request) {
	fks, err := db.ForeignKeys(r.Context(), sess(r).DB, r.PathValue("db"), r.PathValue("table"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"foreignKeys": fks})
}

type searchReq struct {
	Conditions []db.Condition `json:"conditions"`
	Limit      int            `json:"limit"`
	Offset     int            `json:"offset"`
	OrderBy    string         `json:"orderBy"`
	Dir        string         `json:"dir"`
}

func (a *API) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req searchReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Limit <= 0 || req.Limit > 1000 {
		req.Limit = 50
	}
	if req.Offset < 0 {
		req.Offset = 0
	}
	rs, total, err := db.Search(r.Context(), sess(r).DB,
		r.PathValue("db"), r.PathValue("table"), req.Conditions, req.Limit, req.Offset, req.OrderBy, req.Dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result": rs,
		"total":  total,
		"limit":  req.Limit,
		"offset": req.Offset,
	})
}
