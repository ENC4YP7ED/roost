package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// The Pterodactyl API wraps everything in typed envelopes:
//   {"object": "list", "data": [...], "meta": {...}}
//   {"object": "server", "attributes": {...}}
// and errors as {"errors": [{"code", "status", "detail"}]}.

type item struct {
	Object     string         `json:"object"`
	Attributes map[string]any `json:"attributes"`
}

func obj(object string, attributes map[string]any) item {
	return item{Object: object, Attributes: attributes}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeItem(w http.ResponseWriter, status int, object string, attributes map[string]any) {
	writeJSON(w, status, obj(object, attributes))
}

// writeList emulates Pterodactyl's paginated list envelope. The full result
// set is always available; pagination meta reflects ?page & ?per_page.
func writeList(w http.ResponseWriter, r *http.Request, object string, rows []map[string]any) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage < 1 {
		perPage = 50
	}
	if perPage > 500 {
		perPage = 500
	}
	total := len(rows)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	data := make([]item, 0, end-start)
	for _, row := range rows[start:end] {
		data = append(data, obj(object, row))
	}
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
		"meta": map[string]any{
			"pagination": map[string]any{
				"total":        total,
				"count":        end - start,
				"per_page":     perPage,
				"current_page": page,
				"total_pages":  totalPages,
				"links":        map[string]any{},
			},
		},
	})
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{
		"errors": []map[string]string{{
			"code":   http.StatusText(status),
			"status": strconv.Itoa(status),
			"detail": detail,
		}},
	})
}

func writeNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}
