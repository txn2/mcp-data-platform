package ui

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/andybalholm/brotli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testFS builds a synthetic in-memory filesystem for testing the SPA handler
// independent of whether the real frontend was built.
func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":       {Data: []byte("<html>SPA</html>")},
		"assets/app.js":    {Data: []byte("console.log('app')")},
		"assets/style.css": {Data: []byte("body{}")},
	}
}

func TestAvailable(t *testing.T) {
	// Available() returns whether built frontend assets are embedded.
	// Both states are valid depending on whether the frontend was built.
	if Available() {
		t.Log("dist has built frontend assets (local build present)")
	} else {
		t.Log("dist has .gitkeep only (clean checkout / CI)")
	}
}

func TestHandler_ReturnsHandler(t *testing.T) {
	h := Handler()
	assert.NotNil(t, h)
}

func TestSPAHandler_Root_ServesIndexHTML(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), "<html>SPA</html>")
}

func TestSPAHandler_SPAFallback_ServesIndexHTML(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/dashboard", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), "<html>SPA</html>")
}

func TestSPAHandler_StaticAsset_ServedByFileServer(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/assets/app.js", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "console.log")
}

func TestSPAHandler_CSSAsset(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/assets/style.css", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "body{}")
}

func TestSPAHandler_NoIndexHTML_Returns404(t *testing.T) {
	emptyFS := fstest.MapFS{}
	h := newSPAHandler(emptyFS)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSPAHandler_NoRedirectLoop(t *testing.T) {
	h := newSPAHandler(testFS())

	// The exact bug: /index.html must NOT produce a 301
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/index.html", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// /index.html is a real file, so FileServer serves it (200), not redirect
	require.Equal(t, http.StatusOK, rec.Code, "index.html must not redirect")
}

func TestSPAHandler_NestedUnknownRoute_Fallback(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/settings/profile", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "<html>SPA</html>")
}

// A hashed build file the embedded build does not have answers 404, never
// index.html: during a rolling update a page from one build asks a replica on
// the other build for its files, and HTML under a stylesheet's or a chunk's
// URL breaks the page and can be cached as that file (#2139).
func TestSPAHandler_MissingAsset_Returns404(t *testing.T) {
	h := newSPAHandler(testFS())

	for _, path := range []string{"/assets/index-DOESNOTEXIST.css", "/assets/AutomationsPage-DOESNOTEXIST.js", "/assets/nested/missing.js"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code, path)
		assert.NotContains(t, rec.Body.String(), "<html>SPA</html>", path)
	}
}

// index.html names the current build's hashed files, so it is never served
// from a cache: a reload always picks up the file names this build has (#2139).
func TestSPAHandler_IndexHTML_NoCache(t *testing.T) {
	h := newSPAHandler(testFS())

	for _, path := range []string{"/", "/automations/abc/runs/def", "/assets-overview"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, path)
		assert.Contains(t, rec.Body.String(), "<html>SPA</html>", path)
		assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"), path)
	}
}

// The asset viewer route shares Vite's build directory prefix: a share link
// redirects to /portal/assets/{id}?share=..., and that page is the SPA, not a
// missing build file (#2205).
func TestSPAHandler_AssetViewerRoute_ServesIndexHTML(t *testing.T) {
	h := newSPAHandler(testFS())

	for _, path := range []string{
		"/assets/490c49f53b801512ef219da0be06dac6?share=abc",
		"/assets/2fbb2fbd-0184-4478-a003-fedb9001f31d",
		"/assets/a1/provenance",
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, path)
		assert.Contains(t, rec.Body.String(), "<html>SPA</html>", path)
	}
}

// compressFS is a build whose chunk is large enough to compress, beside a
// small file, a binary one and a file outside assets/.
func compressFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<html>SPA</html>")},
		"assets/elk-abc123.js": {Data: []byte(strings.Repeat("function layout(node){return node.children.map(layout)}\n", 400))},
		"assets/tiny-abc.css":  {Data: []byte("body{}")},
		"assets/logo-abc.png":  {Data: bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47}, 600)},
		"favicon.svg":          {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg">` + strings.Repeat(`<path d="M0 0h1v1z"/>`, 100) + `</svg>`)},
	}
}

func serveWith(t *testing.T, h http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestSPAHandler_BuildFilesAreCompressedAndCachedForGood pins #2598: a hashed
// build file is sent in the form Accept-Encoding allows, much smaller, with
// immutable caching, Vary and a validator per form; the bytes decode to the
// file.
func TestSPAHandler_BuildFilesAreCompressedAndCachedForGood(t *testing.T) {
	fsys := compressFS()
	original := fsys["assets/elk-abc123.js"].Data
	h := newSPAHandler(fsys)

	br := serveWith(t, h, "/assets/elk-abc123.js", map[string]string{"Accept-Encoding": "br, gzip"})
	require.Equal(t, http.StatusOK, br.Code)
	assert.Equal(t, "br", br.Header().Get("Content-Encoding"))
	assert.Equal(t, "public, max-age=31536000, immutable", br.Header().Get("Cache-Control"))
	assert.Equal(t, "Accept-Encoding", br.Header().Get("Vary"))
	assert.Contains(t, br.Header().Get("Content-Type"), "javascript")
	assert.Less(t, br.Body.Len(), len(original)/4, "brotli of repetitive script")
	decoded, err := io.ReadAll(brotli.NewReader(br.Body))
	require.NoError(t, err)
	assert.Equal(t, original, decoded)

	gz := serveWith(t, h, "/assets/elk-abc123.js", map[string]string{"Accept-Encoding": "gzip"})
	assert.Equal(t, "gzip", gz.Header().Get("Content-Encoding"))
	r, err := gzip.NewReader(gz.Body)
	require.NoError(t, err)
	decoded, err = io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)

	plain := serveWith(t, h, "/assets/elk-abc123.js", nil)
	assert.Empty(t, plain.Header().Get("Content-Encoding"), "no Accept-Encoding, no encoding")
	assert.Equal(t, original, plain.Body.Bytes())
	assert.Equal(t, "Accept-Encoding", plain.Header().Get("Vary"))

	refused := serveWith(t, h, "/assets/elk-abc123.js", map[string]string{"Accept-Encoding": "br;q=0"})
	assert.Empty(t, refused.Header().Get("Content-Encoding"), "q=0 refuses brotli")

	tags := map[string]bool{br.Header().Get("ETag"): true, gz.Header().Get("ETag"): true, plain.Header().Get("ETag"): true}
	assert.Len(t, tags, 3, "each form has its own validator: %v", tags)
}

// A request carrying the validator it was given is answered 304 with no body,
// for the form it asked for.
func TestSPAHandler_ARevalidationIsANotModified(t *testing.T) {
	h := newSPAHandler(compressFS())
	first := serveWith(t, h, "/favicon.svg", map[string]string{"Accept-Encoding": "gzip"})
	require.Equal(t, http.StatusOK, first.Code)
	tag := first.Header().Get("ETag")
	require.NotEmpty(t, tag)

	again := serveWith(t, h, "/favicon.svg", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": tag})
	assert.Equal(t, http.StatusNotModified, again.Code)
	assert.Zero(t, again.Body.Len())

	other := serveWith(t, h, "/favicon.svg", map[string]string{"If-None-Match": tag})
	assert.Equal(t, http.StatusOK, other.Code, "the gzip form's validator does not match the plain form")
}

// A file outside the hashed build directory keeps its name across builds, so
// it is revalidated rather than cached for good; it is still compressed.
func TestSPAHandler_AnUnhashedFileIsRevalidated(t *testing.T) {
	h := newSPAHandler(compressFS())
	rec := serveWith(t, h, "/favicon.svg", map[string]string{"Accept-Encoding": "br"})
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "br", rec.Header().Get("Content-Encoding"))
	assert.NotEmpty(t, rec.Header().Get("ETag"))
}

// A file too small to gain from compression, and one whose type does not
// compress, are sent as they are, with no Vary.
func TestSPAHandler_SmallAndBinaryFilesAreSentAsTheyAre(t *testing.T) {
	fsys := compressFS()
	h := newSPAHandler(fsys)
	for _, path := range []string{"/assets/tiny-abc.css", "/assets/logo-abc.png"} {
		rec := serveWith(t, h, path, map[string]string{"Accept-Encoding": "br, gzip"})
		require.Equal(t, http.StatusOK, rec.Code, path)
		assert.Empty(t, rec.Header().Get("Content-Encoding"), path)
		assert.Empty(t, rec.Header().Get("Vary"), path)
		assert.Equal(t, fsys[strings.TrimPrefix(path, "/")].Data, rec.Body.Bytes(), path)
		assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"), path)
	}
}

// A directory is not a file the build serves: /assets itself is a client
// route, answered with the shell.
func TestSPAHandler_ADirectoryIsNotServed(t *testing.T) {
	rec := serveWith(t, newSPAHandler(compressFS()), "/assets", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "<html>SPA</html>")
}

// A file whose extension names no type is sniffed, as http.FileServer did.
func TestContentTypeOf_SniffsAnUnknownExtension(t *testing.T) {
	assert.Contains(t, contentTypeOf("assets/blob.zzz", []byte("<!doctype html><p>x")), "text/html")
	assert.Contains(t, contentTypeOf("assets/app.css", nil), "text/css")
}
