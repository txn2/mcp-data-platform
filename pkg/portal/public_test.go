package portal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- publicView ---

func TestPublicViewSuccess(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/plain",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("Hello World"), getCT: "text/plain"},
		S3Bucket:   "test",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, w.Body.String(), "Test") // asset name rendered

	// CSP header must be set on public view responses (plain text uses default CSP).
	csp := w.Header().Get("Content-Security-Policy")
	assert.NotEmpty(t, csp)
}

func TestPublicViewTokenNotFound(t *testing.T) {
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{getByTokenErr: fmt.Errorf("not found")},
		S3Client:   &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/badtoken", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublicViewRevoked(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: true}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestPublicViewExpired(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", ExpiresAt: &past}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestPublicViewAssetNotFound(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getErr: fmt.Errorf("not found")},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublicViewAssetDeleted(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}
	asset := &Asset{ID: "a1", DeletedAt: &now}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestPublicViewNilS3Client(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}
	asset := &Asset{ID: "a1"}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   nil, // no S3 configured
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestPublicViewS3Error(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}
	asset := &Asset{ID: "a1"}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getErr: fmt.Errorf("s3 fail")},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPublicViewEmptyToken(t *testing.T) {
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{},
		S3Client:   &mockS3Client{},
	}, nil)

	// No token in path — we need to hit /portal/view/ which doesn't match the route
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Empty token should be caught — either 404 or mux mismatch
	assert.NotEqual(t, http.StatusOK, w.Code)
}

// --- renderContent ---

func TestRenderContentMarkdown(t *testing.T) {
	result, err := renderContent("text/markdown", []byte("**bold**"))
	require.NoError(t, err)
	assert.Contains(t, result, "<strong>bold</strong>")
}

func TestRenderContentMarkdownSuffix(t *testing.T) {
	result, err := renderContent("text/x-markdown.md", []byte("# Title"))
	require.NoError(t, err)
	assert.Contains(t, result, "Title")
}

func TestRenderContentSVG(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100"><circle cx="50" cy="50" r="40"/></svg>`
	result, err := renderContent("image/svg+xml", []byte(svg))
	require.NoError(t, err)
	assert.Contains(t, result, "<svg")
	assert.Contains(t, result, "<circle")
}

func TestRenderContentSVGSanitizesScript(t *testing.T) {
	svg := `<svg><script>alert('xss')</script></svg>`
	result, err := renderContent("image/svg+xml", []byte(svg))
	require.NoError(t, err)
	assert.NotContains(t, result, "<script>")
}

func TestRenderContentHTML(t *testing.T) {
	html := `<div>Hello</div>`
	result, err := renderContent("text/html", []byte(html))
	require.NoError(t, err)
	assert.Contains(t, result, "iframe")
	assert.Contains(t, result, `sandbox="allow-scripts"`)
}

func TestRenderContentJSX(t *testing.T) {
	jsx := `export default function App() { return <div>Hello</div> }`
	result, err := renderContent("text/jsx", []byte(jsx))
	require.NoError(t, err)
	assert.Contains(t, result, "iframe")
	assert.Contains(t, result, "importmap")
	assert.Contains(t, result, "esm.sh")
	assert.Contains(t, result, "sucrase")
}

func TestRenderContentPlainText(t *testing.T) {
	result, err := renderContent("text/plain", []byte("<script>xss</script>"))
	require.NoError(t, err)
	assert.Contains(t, result, "<pre>")
	assert.Contains(t, result, "&lt;script&gt;") // escaped
	assert.NotContains(t, result, "<script>")    // not raw
}

func TestRenderContentUnknown(t *testing.T) {
	result, err := renderContent("application/octet-stream", []byte("binary data"))
	require.NoError(t, err)
	assert.Contains(t, result, "<pre>")
}

// --- renderMarkdown ---

func TestRenderMarkdown(t *testing.T) {
	result, err := renderMarkdown([]byte("# Hello\n\n*world*"))
	require.NoError(t, err)
	assert.Contains(t, result, "Hello")
	assert.Contains(t, result, "<em>world</em>")
}

func TestRenderMarkdownSanitizes(t *testing.T) {
	result, err := renderMarkdown([]byte(`<script>alert('xss')</script>`))
	require.NoError(t, err)
	assert.NotContains(t, result, "<script>")
}

// --- sanitizeSVG ---

func TestSanitizeSVG(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100"><rect x="0" y="0" width="100" height="100" fill="red"/></svg>`
	result := sanitizeSVG([]byte(svg))
	assert.Contains(t, result, "<svg")
	assert.Contains(t, result, "<rect")
}

func TestSanitizeSVGRemovesScript(t *testing.T) {
	svg := `<svg><script>alert(1)</script><circle r="10"/></svg>`
	result := sanitizeSVG([]byte(svg))
	assert.NotContains(t, result, "<script")
	assert.Contains(t, result, "<circle")
}

func TestSanitizeSVGStripsStyleAttr(t *testing.T) {
	svg := `<svg><rect style="background:url(javascript:alert(1))" width="10" height="10"/></svg>`
	result := sanitizeSVG([]byte(svg))
	assert.NotContains(t, result, "style=")
	assert.Contains(t, result, "<rect")
}

// --- publicCSP ---

func TestPublicCSP(t *testing.T) {
	csp := publicCSP("text/jsx")
	assert.Contains(t, csp, "frame-src blob:")
	assert.Contains(t, csp, "script-src 'unsafe-eval' 'unsafe-inline' blob: https://esm.sh")
	assert.Contains(t, csp, "connect-src https://esm.sh")
	assert.Contains(t, csp, "font-src data: https://fonts.gstatic.com")
	assert.Contains(t, csp, "style-src 'unsafe-inline' https://fonts.googleapis.com")
	assert.Contains(t, csp, "img-src data: blob:")

	csp2 := publicCSP("text/html")
	assert.NotContains(t, csp2, "frame-src")
	assert.Contains(t, csp2, "default-src 'none'")
}

// --- jsxIframe ---

func TestJsxIframe(t *testing.T) {
	result := jsxIframe([]byte(`export default function App() { return <h1>Hi</h1> }`))
	assert.Contains(t, result, `sandbox="allow-scripts"`)
	assert.Contains(t, result, "importmap")
	assert.Contains(t, result, "esm.sh/sucrase")
	assert.Contains(t, result, "esm.sh/react@19")
	// blob: must be in script-src so the dynamic import of the transformed-code blob works.
	// The CSP is inside a meta tag within the srcdoc attribute, so quotes are HTML-escaped.
	assert.Contains(t, result, "script-src &#39;unsafe-eval&#39; &#39;unsafe-inline&#39; blob: https://esm.sh")
}

func TestJsxIframeSpecialChars(t *testing.T) {
	result := jsxIframe([]byte(`function App() { return <div title="hello &amp; world">test</div> }`))
	assert.Contains(t, result, `sandbox="allow-scripts"`)
	// Content is JSON-encoded then HTML-escaped, so it's safely embedded.
	assert.NotContains(t, result, `<div title=`)
}

// --- sandboxedIframe ---

func TestSandboxedIframe(t *testing.T) {
	result := sandboxedIframe([]byte("<div>test</div>"))
	assert.Contains(t, result, `sandbox="allow-scripts"`)
	assert.Contains(t, result, "srcdoc=")
	// Original HTML should be escaped in the attribute
	assert.NotContains(t, result, `<div>test</div>`) // should be escaped
}

func TestSandboxedIframeSpecialChars(t *testing.T) {
	result := sandboxedIframe([]byte(`<img onerror="alert(1)" src=x>`))
	assert.Contains(t, result, `sandbox="allow-scripts"`)
	// Double quotes in content should be escaped
	assert.NotContains(t, result, `onerror="alert(1)"`)
}
