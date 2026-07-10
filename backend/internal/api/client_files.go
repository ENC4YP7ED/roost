package api

import (
	"io"
	"net/http"
	"net/url"

	"roost/internal/wings"
)

// The file manager endpoints proxy wings' file API 1:1, so the browser only
// ever talks to the panel (except for signed download/upload URLs).

func (a *API) routesClientFiles(mux *http.ServeMux) {
	h := a.requireUser
	base := "/api/client/servers/{server}/files"

	proxy := func(method, wingsPath string, perm string, event string) http.HandlerFunc {
		return h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
			srv := serverFrom(r)
			node, err := a.Store.NodeByID(srv.NodeID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "Node for this server is missing.")
				return
			}
			target := "/api/servers/" + srv.UUID + wingsPath
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			res, err := wings.New(node).Raw(method, target, r.Body, r.Header.Get("Content-Type"))
			if err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
			defer res.Body.Close()
			if event != "" && res.StatusCode < 300 {
				a.activity(r, event, map[string]any{"directory": r.URL.Query().Get("directory")}, [2]any{"server", srv.ID})
			}
			for k, vs := range res.Header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(res.StatusCode)
			io.Copy(w, res.Body)
		}, perm))
	}

	mux.HandleFunc("GET "+base+"/list", proxy("GET", "/files/list-directory", "file.read", ""))
	mux.HandleFunc("GET "+base+"/contents", proxy("GET", "/files/contents", "file.read-content", ""))
	mux.HandleFunc("PUT "+base+"/rename", proxy("PUT", "/files/rename", "file.update", "server:file.rename"))
	mux.HandleFunc("POST "+base+"/copy", proxy("POST", "/files/copy", "file.create", "server:file.copy"))
	mux.HandleFunc("POST "+base+"/write", proxy("POST", "/files/write", "file.create", "server:file.write"))
	mux.HandleFunc("POST "+base+"/compress", proxy("POST", "/files/compress", "file.archive", "server:file.compress"))
	mux.HandleFunc("POST "+base+"/decompress", proxy("POST", "/files/decompress", "file.archive", "server:file.decompress"))
	mux.HandleFunc("POST "+base+"/delete", proxy("POST", "/files/delete", "file.delete", "server:file.delete"))
	mux.HandleFunc("POST "+base+"/create-folder", proxy("POST", "/files/create-directory", "file.create", "server:file.create-directory"))
	mux.HandleFunc("POST "+base+"/chmod", proxy("POST", "/files/chmod", "file.update", "server:file.chmod"))
	mux.HandleFunc("POST "+base+"/pull", proxy("POST", "/files/pull", "file.create", "server:file.pull"))

	// Signed direct-to-wings URLs.
	mux.HandleFunc("GET "+base+"/download", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		u, srv := userFrom(r), serverFrom(r)
		node, _ := a.Store.NodeByID(srv.NodeID)
		file, err := url.QueryUnescape(r.URL.Query().Get("file"))
		if err != nil || file == "" {
			writeError(w, http.StatusUnprocessableEntity, "A file path must be provided.")
			return
		}
		link, err := wings.FileDownloadURL(a.PanelURL(), node, srv, u, file)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.activity(r, "server:file.download", map[string]any{"file": file}, [2]any{"server", srv.ID})
		writeItem(w, http.StatusOK, "signed_url", map[string]any{"url": link})
	}, "file.read-content")))

	mux.HandleFunc("GET "+base+"/upload", h(a.withServer(func(w http.ResponseWriter, r *http.Request) {
		u, srv := userFrom(r), serverFrom(r)
		node, _ := a.Store.NodeByID(srv.NodeID)
		link, err := wings.UploadURL(a.PanelURL(), node, srv, u)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeItem(w, http.StatusOK, "signed_url", map[string]any{"url": link})
	}, "file.create")))
}
