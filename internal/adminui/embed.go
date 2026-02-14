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
	return newSPAHandler(sub)
}

// newSPAHandler builds an SPA handler from the given filesystem. Exported via
// Handler() for production use; called directly in tests with a synthetic FS.
func newSPAHandler(root fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(root))

	// Pre-read index.html so the SPA fallback can serve it directly.
	// Routing through http.FileServer with path="/index.html" causes a
	// 301 redirect loop: FileServer treats index.html as a directory
	// index and redirects to "./" which resolves back to the same URL.
	indexHTML, _ := fs.ReadFile(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path: strip leading slash for fs lookup
		path := strings.TrimPrefix(r.URL.Path, "/")

		// If the path points to an actual file (other than index.html),
		// serve it via FileServer for proper Content-Type and caching.
		// index.html is excluded because FileServer redirects it to "./"
		// which causes a redirect loop when mounted under a prefix.
		if path != "" && path != "index.html" {
			if f, err := root.Open(path); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// SPA fallback: serve index.html directly for unmatched routes.
		if indexHTML == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
}
