package contentviewer

import (
	"bytes"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// immutableCacheControl is the caching policy for a viewer chunk. Every
// filename carries a content hash, so a chunk's bytes can never change under a
// name a client already holds: the second share someone opens costs no
// JavaScript at all.
const immutableCacheControl = "public, max-age=31536000, immutable"

// Handler serves the viewer's chunks under AssetPathPrefix.
//
// The share page is reachable without signing in, so this is too — it is the
// code that renders the page, and gating it would leave every public share
// blank. Nothing here is share-specific: the same bytes are served whatever
// token was opened, and no share is looked up, so the caller mounts it outside
// the share access gate.
func Handler() http.Handler {
	return handlerFor(distFS)
}

// handlerFor serves the flat chunk directory inside fsys. An unbuilt tree
// needs no special case: it holds nothing servableName admits, so every
// request 404s and the share page falls back to its metadata and download
// link, which is the degradation an un-built bundle produced before.
func handlerFor(fsys fs.FS) http.Handler {
	assets, err := fs.Sub(fsys, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, AssetPathPrefix)
		if !servableName(name) {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentTypeFor(name))
		w.Header().Set("Cache-Control", immutableCacheControl)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// A zero modtime leaves Last-Modified off: the filename is the
		// version, so there is nothing a date would tell a client that the
		// immutable policy above has not already settled.
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}

// servableName reports whether name addresses a file in the flat chunk
// directory. Anything with a path separator, a leading dot, or an extension
// the bundle does not contain is refused rather than read: the directory holds
// only the emitted chunks and their assets, and vite's manifest under .vite/
// describes the graph rather than being part of it.
func servableName(name string) bool {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return false
	}
	switch {
	case strings.HasSuffix(name, ".js"), strings.HasSuffix(name, ".css"),
		strings.HasSuffix(name, ".map"), strings.HasSuffix(name, ".svg"),
		strings.HasSuffix(name, ".woff2"):
		return true
	default:
		return false
	}
}

// contentTypeFor returns the media type for a chunk filename. The set is
// closed to what servableName admits, so there is no sniffing to fall back on.
func contentTypeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".map"):
		return "application/json; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	default:
		return "font/woff2"
	}
}
