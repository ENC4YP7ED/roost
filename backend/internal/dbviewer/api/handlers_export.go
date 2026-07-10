package api

import (
	"fmt"
	"net/http"

	"roost/internal/dbviewer/db"
)

func (a *API) handleExportTable(w http.ResponseWriter, r *http.Request) {
	schema := r.PathValue("db")
	table := r.PathValue("table")
	format := db.ExportFormat(r.URL.Query().Get("format"))
	if format == "" {
		format = db.FormatSQL
	}

	// Open first so a bad table/permission errors before we commit to a 200.
	src, err := db.OpenExportTable(r.Context(), sess(r).DB, schema, table, format)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer src.Close()

	downloadHeaders(w, db.MimeFor(format), fmt.Sprintf("%s.%s.%s", schema, table, format))
	_ = src.Stream(w) // streamed; mid-stream errors can't change the status
}

func (a *API) handleExportDatabase(w http.ResponseWriter, r *http.Request) {
	schema := r.PathValue("db")
	// Probe the schema before committing to a 200.
	if _, err := db.Tables(r.Context(), sess(r).DB, schema); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	downloadHeaders(w, "application/sql", schema+".sql")
	_ = db.ExportDatabaseTo(r.Context(), sess(r).DB, schema, w)
}

func downloadHeaders(w http.ResponseWriter, contentType, filename string) {
	w.Header().Set("Content-Type", contentType+"; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Accel-Buffering", "no")
}

// ---- import ----------------------------------------------------------------

// handleImport streams the raw SQL request body straight into the executor, so
// an import of any size is never buffered in memory. The target database comes
// from the ?database= query param.
func (a *API) handleImport(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	res, err := db.RunScriptStream(r.Context(), sess(r).DB, r.URL.Query().Get("database"), r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res})
}
