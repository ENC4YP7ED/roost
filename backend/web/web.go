// Package web serves the built SPA. Release builds embed frontend/dist into
// the binary; during development -static can point at the dist directory on
// disk instead.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// Handler serves static assets with an index.html fallback for SPA routes.
func Handler(staticDir string) http.Handler {
	var root fs.FS
	if staticDir != "" {
		if _, err := os.Stat(staticDir); err == nil {
			root = os.DirFS(staticDir)
		}
	}
	if root == nil {
		sub, err := fs.Sub(embedded, "dist")
		if err == nil {
			root = sub
		} else {
			root = embedded
		}
	}
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(root, p); err != nil {
			// SPA fallback: unknown paths render the app shell.
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

//go:embed all:dbviewer
var dbviewerFS embed.FS

// DBViewerHandler serves the built-in database viewer SPA mounted at
// /dbviewer/. Like Handler it falls back to index.html for client routes.
func DBViewerHandler(staticDir string) http.Handler {
	var root fs.FS
	if staticDir != "" {
		if _, err := os.Stat(staticDir); err == nil {
			root = os.DirFS(staticDir)
		}
	}
	if root == nil {
		sub, err := fs.Sub(dbviewerFS, "dbviewer")
		if err == nil {
			root = sub
		} else {
			root = dbviewerFS
		}
	}
	fileServer := http.StripPrefix("/dbviewer/", http.FileServer(http.FS(root)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/dbviewer/")
		if p == "" || p == "dbviewer" {
			p = "index.html"
		}
		if _, err := fs.Stat(root, p); err != nil {
			r.URL.Path = "/dbviewer/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
