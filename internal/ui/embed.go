// Package ui embeds and serves the unified portal SPA frontend.
package ui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	pathpkg "path"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/contentenc"
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
	files := &fileCache{root: root}

	// Pre-read index.html so the SPA fallback can serve it directly.
	// Routing through http.FileServer with path="/index.html" causes a
	// 301 redirect loop: FileServer treats index.html as a directory
	// index and redirects to "./" which resolves back to the same URL.
	indexHTML, _ := fs.ReadFile(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path: strip leading slash for fs lookup
		path := strings.TrimPrefix(r.URL.Path, "/")

		// A file the build holds (other than index.html, which the fallback
		// below serves) is sent with its validator, its caching and, where
		// the request allows, compressed (#2598).
		if path != "" && path != "index.html" {
			if f := files.get(path); f != nil {
				f.serve(w, r, isBuildFile(path))
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

// Caching of what the portal serves (#2598).
const (
	// cacheImmutable is a hashed build file's: its name changes with its
	// content, so a browser keeps it for a year and never asks again.
	cacheImmutable = "public, max-age=31536000, immutable"
	// cacheRevalidate is every other file's: kept, but checked against its
	// ETag before each use, so a new build's favicon shows on the next load.
	cacheRevalidate = "no-cache"

	etagHexChars = 32
)

// fileCache holds each file the portal serves, read from the build once: an
// embedded build never changes while the process runs, so its validator and
// its compressed forms are worked out on first request and kept.
type fileCache struct {
	root  fs.FS
	files sync.Map // path -> *staticFile
}

// get is the file at path, nil when the build holds no regular file there.
func (c *fileCache) get(path string) *staticFile {
	if v, ok := c.files.Load(path); ok {
		f, _ := v.(*staticFile)
		return f
	}
	info, err := fs.Stat(c.root, path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	body, err := fs.ReadFile(c.root, path)
	if err != nil {
		return nil
	}
	sum := sha256.Sum256(body)
	f := &staticFile{
		name:        pathpkg.Base(path),
		body:        body,
		tag:         hex.EncodeToString(sum[:])[:etagHexChars],
		contentType: contentTypeOf(path, body),
	}
	actual, _ := c.files.LoadOrStore(path, f)
	held, _ := actual.(*staticFile)
	return held
}

// contentTypeOf is a file's media type by its extension, sniffed when the
// extension names none, as http.FileServer chose it.
func contentTypeOf(path string, body []byte) string {
	if ct := mime.TypeByExtension(pathpkg.Ext(path)); ct != "" {
		return ct
	}
	return http.DetectContentType(body)
}

// staticFile is one file and its compressed forms, built at most once.
type staticFile struct {
	name        string
	body        []byte
	tag         string
	contentType string

	once   sync.Once
	br, gz []byte
}

// forms builds the brotli and gzip forms of a compressible file, keeping each
// only when it saves enough to be worth sending.
func (f *staticFile) forms() (br, gz []byte) {
	f.once.Do(func() {
		if !contentenc.Compressible(f.contentType) || len(f.body) < contentenc.MinBytes {
			return
		}
		if b, err := contentenc.Brotli(f.body); err == nil && contentenc.Worthwhile(len(f.body), len(b)) {
			f.br = b
		}
		if g, err := contentenc.Gzip(f.body); err == nil && contentenc.Worthwhile(len(f.body), len(g)) {
			f.gz = g
		}
	})
	return f.br, f.gz
}

// serve sends the file, or the 304 or range http.ServeContent answers from
// its ETag, compressed as the request's Accept-Encoding allows. A hashed build
// file is cached for good; any other file is revalidated on each use.
func (f *staticFile) serve(w http.ResponseWriter, r *http.Request, hashed bool) {
	h := w.Header()
	h.Set("Content-Type", f.contentType)
	if hashed {
		h.Set("Cache-Control", cacheImmutable)
	} else {
		h.Set("Cache-Control", cacheRevalidate)
	}
	body, tag := f.body, f.tag
	br, gz := f.forms()
	if br != nil || gz != nil {
		h.Add("Vary", "Accept-Encoding")
		acceptBR, acceptGZ := contentenc.Accepted(r.Header.Get("Accept-Encoding"))
		switch {
		case acceptBR && br != nil:
			body, tag = br, f.tag+"-br"
			h.Set("Content-Encoding", "br")
		case acceptGZ && gz != nil:
			body, tag = gz, f.tag+"-gz"
			h.Set("Content-Encoding", "gzip")
		}
	}
	// Each representation has its own validator, so a cache never answers a
	// request for one encoding with the bytes of another.
	h.Set("ETag", `"`+tag+`"`)
	http.ServeContent(w, r, f.name, time.Time{}, bytes.NewReader(body))
}
