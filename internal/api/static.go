package api

import (
	"io/fs"
	"net/http"
	"strings"

	"ovd/ui"
)

// UIHandler serves the embedded Control Plane SPA. Existing static
// assets are served directly; unknown browser GET paths fall back to
// index.html for client-side routing. API and WebSocket paths never
// fall back — they return a plain 404.
func UIHandler() http.Handler {
	fsys, err := fs.Sub(ui.DistFS, "dist")
	if err != nil {
		// Cannot happen: the embed pattern guarantees the dist dir.
		panic(err)
	}
	return StaticHandler(fsys)
}

// StaticHandler serves static files from fsys with SPA fallback to
// index.html. It is split from UIHandler so tests can inject any fs.FS.
func StaticHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := r.URL.Path
		if path == "/api" || strings.HasPrefix(path, "/api/") ||
			path == "/ws" || strings.HasPrefix(path, "/ws/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(fsys, name); err != nil {
			// SPA fallback: serve index.html for client-side routes.
			http.ServeFileFS(w, r, fsys, "index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
