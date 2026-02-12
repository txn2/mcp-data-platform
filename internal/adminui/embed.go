// Package adminui embeds and serves the admin SPA frontend.
package adminui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Available returns true when the embedded dist directory contains frontend
// assets (i.e., the frontend was built before the Go binary was compiled).
func Available() bool {
	entries, err := fs.ReadDir(distFS, "dist")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() != ".gitkeep" {
			return true
		}
	}
	return false
}

// Handler returns an http.Handler that serves the embedded SPA.
// It tries to serve the exact file first; if not found, it falls back to
// index.html for client-side routing.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Should never happen with a valid embed directive.
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path: strip leading slash for fs lookup
		path := strings.TrimPrefix(r.URL.Path, "/")

		// If the path points to an actual file, serve it directly
		if path != "" {
			if f, err := sub.Open(path); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// SPA fallback: serve index.html for unmatched routes
		// If index.html doesn't exist (empty dist), return 404
		f, err := sub.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_ = f.Close()
		r.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, r)
	})
}
