package portal

import (
	"context"
	"encoding/json"
	"fmt"
	htmlpkg "html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// htmlNoticeText is the HTML-escaped default notice text for template assertions.
var htmlNoticeText = htmlpkg.EscapeString(defaultNoticeText)

// --- publicView ---

func TestPublicViewSuccess(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false, NoticeText: defaultNoticeText}
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

	// Default brand on right: logo + "MCP Data Platform"
	body := w.Body.String()
	assert.Contains(t, body, "MCP Data Platform")
	assert.Contains(t, body, `class="brand-logo"`)
	assert.Contains(t, body, defaultLogoSVG)
	assert.Contains(t, body, `brand-platform`)

	// No implementor on left by default
	assert.NotContains(t, body, `brand-implementor`)

	// Dark mode toggle is present
	assert.Contains(t, body, `id="theme-toggle"`)
	assert.Contains(t, body, `icon-sun`)
	assert.Contains(t, body, `icon-moon`)

	// Privacy notice is shown (no expiration for this share)
	assert.Contains(t, body, htmlNoticeText)

	// No expiry notice when ExpiresAt is nil
	assert.NotContains(t, body, `id="expiry-notice"`)
}

func TestPublicViewCustomBrand(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false, NoticeText: defaultNoticeText}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Report", ContentType: "text/plain",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	customLogo := `<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/></svg>`
	implLogo := `<svg viewBox="0 0 32 32"><rect width="32" height="32"/></svg>`
	h := NewHandler(Deps{
		AssetStore:         &mockAssetStore{getAsset: asset},
		ShareStore:         &mockShareStore{getByTokenRes: share},
		S3Client:           &mockS3Client{getData: []byte("data"), getCT: "text/plain"},
		S3Bucket:           "test",
		BrandName:          "Plexara",
		BrandLogoSVG:       customLogo,
		BrandURL:           "https://plexara.io",
		ImplementorName:    "ACME Corp",
		ImplementorLogoSVG: implLogo,
		ImplementorURL:     "https://acme.com",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Right side: custom platform brand
	assert.Contains(t, body, "Plexara")
	assert.Contains(t, body, customLogo)
	assert.NotContains(t, body, defaultLogoSVG)
	assert.Contains(t, body, `href="https://plexara.io"`)

	// Left side: implementor
	assert.Contains(t, body, "ACME Corp")
	assert.Contains(t, body, implLogo)
	assert.Contains(t, body, `href="https://acme.com"`)
	assert.Contains(t, body, `brand-implementor`)
}

func TestPublicViewImplementorOnly(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false, NoticeText: defaultNoticeText}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Report", ContentType: "text/plain",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore:      &mockAssetStore{getAsset: asset},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		S3Client:        &mockS3Client{getData: []byte("data"), getCT: "text/plain"},
		S3Bucket:        "test",
		ImplementorName: "ACME Corp",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Left side: implementor name shown (no logo, no link)
	assert.Contains(t, body, "ACME Corp")
	assert.Contains(t, body, `brand-implementor`)

	// Right side: default platform brand
	assert.Contains(t, body, "MCP Data Platform")
	assert.Contains(t, body, defaultLogoSVG)
	assert.Contains(t, body, `brand-platform`)
}

func TestPublicViewBrandLinks(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false, NoticeText: defaultNoticeText}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Report", ContentType: "text/plain",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	t.Run("with URLs", func(t *testing.T) {
		h := NewHandler(Deps{
			AssetStore:      &mockAssetStore{getAsset: asset},
			ShareStore:      &mockShareStore{getByTokenRes: share},
			S3Client:        &mockS3Client{getData: []byte("data"), getCT: "text/plain"},
			S3Bucket:        "test",
			BrandURL:        "https://platform.io",
			ImplementorName: "Impl",
			ImplementorURL:  "https://impl.com",
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		body := w.Body.String()
		assert.Contains(t, body, `href="https://platform.io"`)
		assert.Contains(t, body, `href="https://impl.com"`)
	})

	t.Run("without URLs", func(t *testing.T) {
		h := NewHandler(Deps{
			AssetStore:      &mockAssetStore{getAsset: asset},
			ShareStore:      &mockShareStore{getByTokenRes: share},
			S3Client:        &mockS3Client{getData: []byte("data"), getCT: "text/plain"},
			S3Bucket:        "test",
			ImplementorName: "Impl",
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		body := w.Body.String()
		// No <a> links should be present for brands without URLs
		assert.NotContains(t, body, `href="https://platform.io"`)
		assert.NotContains(t, body, `href="https://impl.com"`)
		// But brand names should still render
		assert.Contains(t, body, "MCP Data Platform")
		assert.Contains(t, body, "Impl")
	})
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

func TestPublicViewWithExpiration(t *testing.T) {
	now := time.Now()
	future := now.Add(6 * time.Hour)
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false, ExpiresAt: &future, NoticeText: defaultNoticeText}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Report", ContentType: "text/plain",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("data"), getCT: "text/plain"},
		S3Bucket:   "test",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Expiry notice is present (not hidden)
	assert.Contains(t, body, `id="expiry-notice"`)
	assert.NotContains(t, body, `style="display:none"`)
	// Privacy notice always present
	assert.Contains(t, body, htmlNoticeText)
	// ISO timestamp passed to JS
	assert.Contains(t, body, future.UTC().Format(time.RFC3339))
}

func TestPublicViewHideExpiration(t *testing.T) {
	now := time.Now()
	future := now.Add(3 * 24 * time.Hour)
	share := &Share{
		ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false,
		ExpiresAt: &future, HideExpiration: true, NoticeText: defaultNoticeText,
	}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Secret", ContentType: "text/plain",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("data"), getCT: "text/plain"},
		S3Bucket:   "test",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Expiry notice element exists but is hidden
	assert.Contains(t, body, `id="expiry-notice"`)
	assert.Contains(t, body, `style="display:none"`)
	// Privacy notice still shown
	assert.Contains(t, body, htmlNoticeText)
	// Separator dot should not be visible (it's outside the hidden span, but only shown when not hidden)
	assert.NotContains(t, body, `class="notice-sep"`)
}

func TestPublicViewDarkModeToggle(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false, NoticeText: defaultNoticeText}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/plain",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("Hello"), getCT: "text/plain"},
		S3Bucket:   "test",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Theme infrastructure
	assert.Contains(t, body, `data-theme="light"`)
	assert.Contains(t, body, `id="theme-toggle"`)
	assert.Contains(t, body, `mdp-theme`) // localStorage key
	assert.Contains(t, body, `prefers-color-scheme`)

	// CSS custom properties
	assert.Contains(t, body, `--bg:`)
	assert.Contains(t, body, `--bg-surface:`)
	assert.Contains(t, body, `--text:`)
	assert.Contains(t, body, `[data-theme="dark"]`)
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
	// JSX content: allows external resources for esm.sh module imports.
	csp := publicCSP("text/jsx")
	assert.Contains(t, csp, "frame-src blob:")
	assert.Contains(t, csp, "script-src")
	assert.Contains(t, csp, "'unsafe-eval'")
	assert.Contains(t, csp, "'unsafe-inline'")
	assert.Contains(t, csp, "https:")
	assert.Contains(t, csp, "connect-src")
	assert.Contains(t, csp, "img-src")

	// HTML content: allows external CDN scripts/styles because blob: iframes
	// inherit the parent's CSP in modern browsers. Security isolation comes
	// from the iframe sandbox attribute, not CSP.
	csp2 := publicCSP("text/html")
	assert.Contains(t, csp2, "frame-src blob:")
	assert.Contains(t, csp2, "default-src 'none'")
	assert.Contains(t, csp2, "script-src")
	assert.Contains(t, csp2, "'unsafe-inline'")
	assert.Contains(t, csp2, "https:") // must allow external CDN scripts (Chart.js, D3, etc.)
	assert.Contains(t, csp2, "style-src")
	assert.Contains(t, csp2, "img-src")
	assert.Contains(t, csp2, "font-src")
	assert.Contains(t, csp2, "connect-src") // HTML may fetch data via XHR/fetch
}

// --- jsxIframe ---

func TestJsxIframe(t *testing.T) {
	result := jsxIframe([]byte(`export default function App() { return <h1>Hi</h1> }`))
	assert.Contains(t, result, `sandbox="allow-scripts"`)
	assert.Contains(t, result, `content-data`)
	assert.Contains(t, result, "createObjectURL")
	// The inner HTML (importmap, esm.sh, sucrase) is JSON-encoded inside content-data.
	assert.Contains(t, result, "importmap")
	assert.Contains(t, result, "esm.sh")
	assert.Contains(t, result, "sucrase")
	// No srcdoc — uses blob: URL instead.
	assert.NotContains(t, result, "srcdoc")
}

func TestJsxIframeSpecialChars(t *testing.T) {
	result := jsxIframe([]byte(`function App() { return <div title="hello &amp; world">test</div> }`))
	assert.Contains(t, result, `sandbox="allow-scripts"`)
	assert.Contains(t, result, `content-data`)
	assert.Contains(t, result, "createObjectURL")
	// Content is double-JSON-encoded (JSX in template, then template output in blob wrapper).
	assert.NotContains(t, result, `<div title=`)
}

// --- defaultLogoSVG ---

func TestDefaultLogoSVG(t *testing.T) {
	assert.Contains(t, defaultLogoSVG, "<svg")
	assert.Contains(t, defaultLogoSVG, "</svg>")
	assert.Contains(t, defaultLogoSVG, "viewBox")
}

// --- sandboxedIframe ---

func TestSandboxedIframe(t *testing.T) {
	result := sandboxedIframe([]byte("<div>test</div>"))
	assert.Contains(t, result, `sandbox="allow-scripts"`)
	assert.Contains(t, result, `content-data`)
	assert.Contains(t, result, "createObjectURL")
	// No srcdoc — uses blob: URL instead.
	assert.NotContains(t, result, "srcdoc")
}

func TestSandboxedIframeSpecialChars(t *testing.T) {
	result := sandboxedIframe([]byte(`<img onerror="alert(1)" src=x>`))
	assert.Contains(t, result, `sandbox="allow-scripts"`)
	assert.Contains(t, result, `content-data`)
	// Content is JSON-encoded, so raw HTML tags don't appear.
	assert.NotContains(t, result, `onerror="alert(1)"`)
}

// --- blobIframe ---

func TestBlobIframe(t *testing.T) {
	result := blobIframe("<h1>Hello</h1>", "width:100%;height:50vh;")
	assert.Contains(t, result, `id="content-data"`)
	assert.Contains(t, result, `id="content-frame"`)
	assert.Contains(t, result, "createObjectURL")
	assert.Contains(t, result, `sandbox="allow-scripts"`)
	assert.NotContains(t, result, "srcdoc")
}

func TestBlobIframeScriptBreakout(t *testing.T) {
	// </script> in content must be safely encoded via JSON.
	result := blobIframe(`<script>alert("xss")</script>`, "width:100%;")
	// json.Marshal encodes < as \u003c, so </script> cannot break out.
	assert.NotContains(t, result, `<script>alert`)
	assert.Contains(t, result, `\u003c`)
}

func TestBlobIframeRoundTrip(t *testing.T) {
	original := `<div class="test">Hello & "world"</div>`
	result := blobIframe(original, "width:100%;")
	// Extract JSON from between content-data tags.
	start := strings.Index(result, `id="content-data">`) + len(`id="content-data">`)
	end := strings.Index(result[start:], `</script>`)
	jsonStr := result[start : start+end]
	var decoded string
	err := json.Unmarshal([]byte(jsonStr), &decoded)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
}

// --- Notice text tests ---

func TestPublicViewCustomNoticeText(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false, NoticeText: "Internal use only."}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Report", ContentType: "text/plain",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("data"), getCT: "text/plain"},
		S3Bucket:   "test",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Internal use only.")
	assert.NotContains(t, body, htmlNoticeText)
	assert.Contains(t, body, `class="notice"`)
}

func TestPublicViewEmptyNoticeText(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false, NoticeText: ""}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Report", ContentType: "text/plain",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("data"), getCT: "text/plain"},
		S3Bucket:   "test",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	// No notice div when both notice text and expiration are empty
	assert.NotContains(t, body, `class="notice"`)
	assert.NotContains(t, body, htmlNoticeText)
}

func TestPublicViewEmptyNoticeWithExpiration(t *testing.T) {
	now := time.Now()
	future := now.Add(6 * time.Hour)
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false, ExpiresAt: &future, NoticeText: ""}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Report", ContentType: "text/plain",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("data"), getCT: "text/plain"},
		S3Bucket:   "test",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	// Notice div shown for expiration even with empty notice text
	assert.Contains(t, body, `class="notice"`)
	assert.Contains(t, body, `id="expiry-notice"`)
	// No separator or notice text
	assert.NotContains(t, body, `class="notice-sep"`)
	assert.NotContains(t, body, htmlNoticeText)
}
