// Package ui embeds and serves the unified portal SPA frontend.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	pathpkg "path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// buildAssetsDir is where Vite writes the hashed files a build's index.html
// names (its default build.assetsDir). The portal's own client routes share
// the prefix: the asset viewer is /portal/assets/{id} (#2205).
const buildAssetsDir = "assets/"

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

		// A build file this build does not have is a 404, never index.html:
		// a page from another build (a rolling update) asking for its hashed
		// files must fail the request, not get HTML under a stylesheet's or a
		// chunk's immutable URL (#2139).
		if indexHTML == nil || isBuildFile(path) {
			http.NotFound(w, r)
			return
		}
		// SPA fallback: serve index.html directly for unmatched routes. It
		// names the current build's hashed files, so it is revalidated on
		// every load.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexHTML)
	})
}

// isBuildFile reports whether path names a file Vite writes under
// buildAssetsDir rather than a client route sharing the prefix. Every file
// Vite writes there has an extension; an asset id never does (#2205).
func isBuildFile(path string) bool {
	return strings.HasPrefix(path, buildAssetsDir) && pathpkg.Ext(path) != ""
}
