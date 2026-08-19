package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/contentviewer"
)

// The public share page is the surface most likely to be opened by someone
// outside the organization, on an unknown connection, who has never used the
// platform. It used to inline the entire renderer into every response, so an
// 850-byte markdown document arrived as a 4.9 MB page (#1355). These tests
// hold the page to what the asset it shows actually needs.

// scriptTagRE matches the page's script elements so the test can weigh what
// the document carries itself, separately from what it references.
var scriptTagRE = regexp.MustCompile(`(?s)<script\b[^>]*>(.*?)</script>`)

// maxInlineScriptBytes bounds the script the share page carries in its own
// body: the theme toggle, the expiry countdown, the modal handlers and the
// asset's own JSON payload. The renderer is referenced, not inlined, so this
// stays flat no matter which family is being shared. The old page put
// 4,782,230 characters here.
const maxInlineScriptBytes = 64 * 1024

// newWeightTestHandler serves one markdown share. Markdown is the case the
// ticket is written about — an 850-byte document that arrived as a 4.9 MB
// page — and it is the family that proves the split, since it must load its
// own renderer and none of the others.
func newWeightTestHandler(content []byte) *Handler {
	const contentType = "text/markdown"
	now := time.Now()
	return NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: &Asset{
			ID: "a1", OwnerID: "u1", Name: "Doc", ContentType: contentType,
			Tags: []string{}, CreatedAt: now, UpdatedAt: now,
		}},
		ShareStore: &mockShareStore{getByTokenRes: &Share{
			AccessMode: AccessModePublic, ID: "s1", AssetID: "a1", Token: "tok1",
		}},
		S3Client: &mockS3Client{getData: content, getCT: contentType},
		S3Bucket: "test",
	}, nil)
}

func fetchShare(t *testing.T, h *Handler) string {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	return w.Body.String()
}

// A share of a small markdown document must not carry a renderer in its body.
func TestPublicViewDoesNotInlineTheRenderer(t *testing.T) {
	body := fetchShare(t, newWeightTestHandler([]byte("# Title\n\nA short document.\n")))

	var inline int
	for _, m := range scriptTagRE.FindAllStringSubmatch(body, -1) {
		inline += len(m[1])
	}
	assert.Less(t, inline, maxInlineScriptBytes,
		"the share page is carrying %d bytes of inline script; the renderer must be referenced, not inlined", inline)
}

// The page must reference the viewer as a module from the asset route, so the
// browser fetches only the chunks the asset's family needs and caches them for
// the next share. Skipped in a checkout with no frontend build, where there is
// no bundle to reference.
func TestPublicViewReferencesViewerModule(t *testing.T) {
	if contentviewer.EntryURL() == "" {
		t.Skip("no content viewer bundle embedded (run: make frontend-build-content-viewer)")
	}
	body := fetchShare(t, newWeightTestHandler([]byte("# Title\n")))

	assert.Contains(t, body, `<script type="module" src="`+contentviewer.EntryURL()+`">`)
	assert.True(t, strings.HasPrefix(contentviewer.EntryURL(), contentviewer.AssetPathPrefix),
		"entry URL %q must sit under the asset prefix the handler serves", contentviewer.EntryURL())
}

// The referenced entry must actually be servable from the same handler: a page
// that names a chunk the server 404s renders nothing at all.
func TestViewerAssetRouteServesTheReferencedEntry(t *testing.T) {
	if contentviewer.EntryURL() == "" {
		t.Skip("no content viewer bundle embedded (run: make frontend-build-content-viewer)")
	}
	h := newWeightTestHandler([]byte("# Title\n"))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, contentviewer.EntryURL(), http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "javascript")
	assert.Contains(t, w.Header().Get("Cache-Control"), "immutable")
	assert.NotEmpty(t, w.Body.Bytes())
}

// The asset route is reached without a share token and without the access
// gate, since it serves the code that renders every share rather than any
// share's content.
func TestViewerAssetRouteNeedsNoShare(t *testing.T) {
	h := NewHandler(Deps{
		// No share store at all: nothing here may consult one.
		AssetStore: &mockAssetStore{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		contentviewer.AssetPathPrefix+"definitely-not-a-chunk.js", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// 404 for the unknown name, not a panic and not a share lookup.
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// A share token is hex, so it can never collide with the asset prefix. This
// guards the dispatch order in ServeHTTP.
func TestViewerAssetPrefixCannotBeAShareToken(t *testing.T) {
	prefix := strings.TrimSuffix(strings.TrimPrefix(contentviewer.AssetPathPrefix, "/portal/view/"), "/")
	require.NotEmpty(t, prefix)
	assert.False(t, isHex(prefix),
		"the viewer asset prefix %q is a valid share token shape and would shadow a real share", prefix)
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// A cold view of a markdown document with a diagram in it fetches the entry,
// the markdown viewer, and the diagram engine's own graph — around thirty
// chunks at once. The share-viewer rate limiter is sized for page loads
// (60/min, burst 10), so putting the chunk route behind it would answer most
// of that 429 and blank the page. The chunks carry no share and cost a map
// lookup, so they are served outside it.
func TestViewerAssetRouteIsNotRateLimited(t *testing.T) {
	if contentviewer.EntryURL() == "" {
		t.Skip("no content viewer bundle embedded (run: make frontend-build-content-viewer)")
	}
	h := newWeightTestHandler([]byte("# Title\n"))

	// Comfortably past the per-IP burst the share page is limited to.
	const requests = 40
	for i := range requests {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, contentviewer.EntryURL(), http.NoBody)
		req.RemoteAddr = "203.0.113.7:5555"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equalf(t, http.StatusOK, w.Code,
			"request %d of %d was refused; the viewer's chunks must not share the share-page rate limit", i+1, requests)
	}
}
