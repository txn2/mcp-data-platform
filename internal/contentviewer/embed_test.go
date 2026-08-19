package contentviewer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbedInitDefaultsEmpty(t *testing.T) {
	// In a clean checkout (or after embed-clean) the dist/ directory contains
	// only .gitkeep, so init() should leave the stylesheet and the entry name
	// empty. When the frontend has been built, both are set; both are valid.
	if CSS == "" && entryFile == "" {
		if EntryURL() != "" {
			t.Errorf("EntryURL() = %q with no bundle, want empty", EntryURL())
		}
		return
	}
	if CSS != "" && len(CSS) < 10 {
		t.Errorf("CSS is present but suspiciously short (%d bytes)", len(CSS))
	}
	if entryFile != "" && !strings.HasSuffix(EntryURL(), entryFile) {
		t.Errorf("EntryURL() = %q, want it to end in %q", EntryURL(), entryFile)
	}
}

func TestDistFSReadable(t *testing.T) {
	entries, err := distFS.ReadDir("dist")
	if err != nil {
		t.Fatalf("failed to read embedded dist directory: %v", err)
	}
	if len(entries) == 0 {
		t.Error("embedded dist directory is empty — expected at least .gitkeep")
	}
}

// builtFS is a stand-in for a completed frontend build: a hashed entry chunk,
// a chunk it imports, the stylesheet and vite's manifest.
func builtFS() fstest.MapFS {
	return fstest.MapFS{
		"dist/content-viewer-entry-AAAA1111.js": {Data: []byte("export const e = 1;")},
		"dist/MarkdownRenderer-BBBB2222.js":     {Data: []byte("export const m = 2;")},
		"dist/content-viewer.css":               {Data: []byte(".root { color: red; }")},
		"dist/.vite/manifest.json": {Data: []byte(`{
			"_MarkdownRenderer-BBBB2222.js": {"file": "MarkdownRenderer-BBBB2222.js", "name": "MarkdownRenderer"},
			"src/content-viewer-entry.tsx": {"file": "content-viewer-entry-AAAA1111.js", "isEntry": true, "name": "content-viewer-entry"}
		}`)},
	}
}

func TestLoadBundleWithFiles(t *testing.T) {
	css, entry := loadBundle(builtFS())

	if css != ".root { color: red; }" {
		t.Errorf("css = %q, want the stylesheet", css)
	}
	if entry != "content-viewer-entry-AAAA1111.js" {
		t.Errorf("entry = %q, want the hashed entry chunk", entry)
	}
}

func TestLoadBundleEmpty(t *testing.T) {
	css, entry := loadBundle(fstest.MapFS{"dist/.gitkeep": {Data: []byte{}}})

	if css != "" {
		t.Errorf("css = %q, want empty", css)
	}
	if entry != "" {
		t.Errorf("entry = %q, want empty", entry)
	}
}

// A stylesheet without a manifest is a half-built tree. The page must not
// advertise an entry it cannot serve, so the entry stays empty while the
// stylesheet still loads.
func TestLoadBundleCSSWithoutManifest(t *testing.T) {
	css, entry := loadBundle(fstest.MapFS{
		"dist/content-viewer.css": {Data: []byte(".root {}")},
	})

	if css != ".root {}" {
		t.Errorf("css = %q, want the stylesheet", css)
	}
	if entry != "" {
		t.Errorf("entry = %q, want empty without a manifest", entry)
	}
}

// A manifest naming a chunk the build did not emit would point every viewer at
// a 404. The name is only accepted once the file is confirmed present.
func TestLoadBundleManifestNamesMissingChunk(t *testing.T) {
	_, entry := loadBundle(fstest.MapFS{
		"dist/.vite/manifest.json": {Data: []byte(
			`{"src/content-viewer-entry.tsx": {"file": "gone-DEAD0000.js", "isEntry": true}}`)},
	})

	if entry != "" {
		t.Errorf("entry = %q, want empty when the chunk is absent", entry)
	}
}

func TestLoadBundleManifestUnparsable(t *testing.T) {
	_, entry := loadBundle(fstest.MapFS{
		"dist/.vite/manifest.json": {Data: []byte("not json")},
	})

	if entry != "" {
		t.Errorf("entry = %q, want empty for an unparsable manifest", entry)
	}
}

// The chunks are served from one flat directory. An entry naming a subpath is
// a build this loader no longer matches, and must not become a URL.
func TestLoadBundleManifestEntryWithPath(t *testing.T) {
	_, entry := loadBundle(fstest.MapFS{
		"dist/assets/entry-AAAA1111.js": {Data: []byte("export const e = 1;")},
		"dist/.vite/manifest.json": {Data: []byte(
			`{"src/content-viewer-entry.tsx": {"file": "assets/entry-AAAA1111.js", "isEntry": true}}`)},
	})

	if entry != "" {
		t.Errorf("entry = %q, want empty for a nested entry path", entry)
	}
}

func TestServableName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"content-viewer-entry-AAAA1111.js", true},
		{"content-viewer.css", true},
		{"chunk-AAAA1111.js.map", true},
		{"logo-AAAA1111.svg", true},
		{"inter-AAAA1111.woff2", true},
		// The manifest describes the graph; it is not part of what a browser
		// loads, and neither is anything else reached by traversal.
		{".vite/manifest.json", false},
		{".gitkeep", false},
		{"../../etc/passwd", false},
		{"sub/dir/chunk.js", false},
		{`sub\dir\chunk.js`, false},
		{"", false},
		{"README.md", false},
	}
	for _, tt := range tests {
		if got := servableName(tt.name); got != tt.want {
			t.Errorf("servableName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestContentTypeFor(t *testing.T) {
	tests := map[string]string{
		"a-1.js":    "text/javascript; charset=utf-8",
		"a-1.css":   "text/css; charset=utf-8",
		"a-1.map":   "application/json; charset=utf-8",
		"a-1.svg":   "image/svg+xml",
		"a-1.woff2": "font/woff2",
	}
	for name, want := range tests {
		if got := contentTypeFor(name); got != want {
			t.Errorf("contentTypeFor(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestHandlerServesChunk(t *testing.T) {
	h := handlerFor(builtFS())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, AssetPathPrefix+"MarkdownRenderer-BBBB2222.js", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "export const m = 2;" {
		t.Errorf("body = %q, want the chunk", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != immutableCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, immutableCacheControl)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestHandlerRefusesManifestAndTraversal(t *testing.T) {
	h := handlerFor(builtFS())

	for _, p := range []string{
		AssetPathPrefix + ".vite/manifest.json",
		AssetPathPrefix + "../embed.go",
		AssetPathPrefix,
		AssetPathPrefix + "missing-0000.js",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, p, http.NoBody))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", p, rec.Code)
		}
	}
}

// With no bundle built, the route answers rather than panicking: the share
// page still renders its metadata and download link.
func TestHandlerWithoutBundle(t *testing.T) {
	h := handlerFor(fstest.MapFS{"dist/.gitkeep": {Data: []byte{}}})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, AssetPathPrefix+"anything.js", http.NoBody))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestEntryURL(t *testing.T) {
	if got := viewerURLFor("content-viewer-entry-AAAA1111.js"); got != AssetPathPrefix+"content-viewer-entry-AAAA1111.js" {
		t.Errorf("viewerURLFor() = %q", got)
	}
	// No bundle: the page renders its metadata and download link and omits the
	// viewer script rather than pointing at a URL nothing answers.
	if got := viewerURLFor(""); got != "" {
		t.Errorf("viewerURLFor(\"\") = %q, want empty", got)
	}
}

// Handler is the wiring the portal mounts. It is one line over handlerFor, and
// this is what proves that line names the embedded tree.
func TestHandlerServesTheEmbeddedBundle(t *testing.T) {
	h := Handler()
	if entryFile == "" {
		// Clean checkout: nothing is embedded, so every name is a 404.
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, AssetPathPrefix+"anything.js", http.NoBody))
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 with no bundle", rec.Code)
		}
		return
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, EntryURL(), http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d serving %s, want 200", rec.Code, EntryURL())
	}
	if rec.Body.Len() == 0 {
		t.Error("entry chunk served empty")
	}
}
