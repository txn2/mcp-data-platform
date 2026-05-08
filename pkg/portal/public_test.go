package portal

import (
	"context"
	"crypto/tls"
	"fmt"
	htmlpkg "html"
	"net/http"
	"net/http/httptest"
	"regexp"
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

	// CSP header must be set on public view responses.
	csp := w.Header().Get("Content-Security-Policy")
	assert.NotEmpty(t, csp)

	body := w.Body.String()

	// Content viewer bundle infrastructure
	assert.Contains(t, body, `id="content-data"`)
	assert.Contains(t, body, `id="content-root"`)
	// Content JSON must include the content type and content
	assert.Contains(t, body, `"contentType"`)
	assert.Contains(t, body, `"content"`)

	// Default brand on right: logo + "MCP Data Platform"
	assert.Contains(t, body, "MCP Data Platform")
	assert.Contains(t, body, `class="brand-logo"`)
	assert.Contains(t, body, defaultLogoSVG)
	assert.Contains(t, body, `brand-platform`)

	// No implementor on left by default (CSS has the class name, but no HTML element uses it)
	assert.NotContains(t, body, `class="brand brand-implementor"`)

	// Dark mode toggle is present
	assert.Contains(t, body, `id="theme-toggle"`)
	assert.Contains(t, body, `icon-sun`)
	assert.Contains(t, body, `icon-moon`)

	// Privacy notice is shown (no expiration for this share)
	assert.Contains(t, body, htmlNoticeText)

	// No expiry notice when ExpiresAt is nil
	assert.NotContains(t, body, `id="expiry-notice"`)
}

func TestPublicViewVersionBadge(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false, NoticeText: defaultNoticeText}

	t.Run("shows version badge", func(t *testing.T) {
		asset := &Asset{
			ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/plain",
			Tags: []string{}, CreatedAt: now, UpdatedAt: now, CurrentVersion: 3,
		}
		h := NewHandler(Deps{
			AssetStore: &mockAssetStore{getAsset: asset},
			ShareStore: &mockShareStore{getByTokenRes: share},
			S3Client:   &mockS3Client{getData: []byte("content"), getCT: "text/plain"},
			S3Bucket:   "test",
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), ">v3</span>")
	})

	t.Run("no badge for version 0", func(t *testing.T) {
		asset := &Asset{
			ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/plain",
			Tags: []string{}, CreatedAt: now, UpdatedAt: now, CurrentVersion: 0,
		}
		h := NewHandler(Deps{
			AssetStore: &mockAssetStore{getAsset: asset},
			ShareStore: &mockShareStore{getByTokenRes: share},
			S3Client:   &mockS3Client{getData: []byte("content"), getCT: "text/plain"},
			S3Bucket:   "test",
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		// Version 0 is falsy in Go templates, so badge should not appear
		assert.NotContains(t, w.Body.String(), ">v0</span>")
	})
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

	// Dark class bridge for Tailwind
	assert.Contains(t, body, `classList.toggle("dark"`)
}

// --- publicCSP ---

func TestPublicCSPUnified(t *testing.T) {
	csp := publicCSP()
	assert.Contains(t, csp, "default-src 'none'")
	assert.Contains(t, csp, "frame-src blob:")
	assert.Contains(t, csp, "script-src")
	assert.Contains(t, csp, "'unsafe-eval'")
	assert.Contains(t, csp, "'unsafe-inline'")
	assert.Contains(t, csp, "https:")
	assert.Contains(t, csp, "style-src")
	assert.Contains(t, csp, "img-src")
	assert.Contains(t, csp, "font-src")
	assert.Contains(t, csp, "connect-src")
}

// --- Content viewer bundle injection ---

func TestPublicViewContentViewerBundle(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false, NoticeText: defaultNoticeText}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/markdown",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("# Hello"), getCT: "text/markdown"},
		S3Bucket:   "test",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Content data JSON is present with the right structure
	assert.Contains(t, body, `id="content-data"`)
	assert.Contains(t, body, `"contentType":"text/markdown"`)
	assert.Contains(t, body, `"content":"# Hello"`)

	// Content viewer root element
	assert.Contains(t, body, `id="content-root"`)

	// No server-rendered iframes with sandboxed scripts
	assert.NotContains(t, body, `sandbox="allow-scripts"`)
}

// --- Content types all use same rendering path ---

func TestPublicViewAllContentTypesUseSameTemplate(t *testing.T) {
	contentTypes := []string{
		"text/plain",
		"text/markdown",
		"image/svg+xml",
		"text/jsx",
		"text/html",
		"application/octet-stream",
	}

	for _, ct := range contentTypes {
		t.Run(ct, func(t *testing.T) {
			now := time.Now()
			share := &Share{ID: "s1", AssetID: "a1", Token: "tok1", Revoked: false}
			asset := &Asset{
				ID: "a1", OwnerID: "u1", Name: "Test", ContentType: ct,
				Tags: []string{}, CreatedAt: now, UpdatedAt: now,
			}

			h := NewHandler(Deps{
				AssetStore: &mockAssetStore{getAsset: asset},
				ShareStore: &mockShareStore{getByTokenRes: share},
				S3Client:   &mockS3Client{getData: []byte("test content"), getCT: ct},
				S3Bucket:   "test",
			}, nil)

			req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()

			// All content types use the same content viewer infrastructure
			assert.Contains(t, body, `id="content-data"`)
			assert.Contains(t, body, `id="content-root"`)
		})
	}
}

// --- defaultLogoSVG ---

func TestDefaultLogoSVG(t *testing.T) {
	assert.Contains(t, defaultLogoSVG, "<svg")
	assert.Contains(t, defaultLogoSVG, "</svg>")
	assert.Contains(t, defaultLogoSVG, "viewBox")
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

// --- mockCollectionStore for collection tests ---

type mockCollectionStore struct {
	getResult   *Collection
	getErr      error
	listResult  []Collection
	listTotal   int
	listErr     error
	insertErr   error
	updateErr   error
	deleteErr   error
	configErr   error
	thumbErr    error
	sectionsErr error
}

func (m *mockCollectionStore) Insert(_ context.Context, _ Collection) error { return m.insertErr }
func (m *mockCollectionStore) Get(_ context.Context, _ string) (*Collection, error) {
	return m.getResult, m.getErr
}

func (m *mockCollectionStore) List(_ context.Context, _ CollectionFilter) ([]Collection, int, error) {
	return m.listResult, m.listTotal, m.listErr
}
func (m *mockCollectionStore) Update(_ context.Context, _, _, _ string) error { return m.updateErr }
func (m *mockCollectionStore) UpdateConfig(_ context.Context, _ string, _ CollectionConfig) error {
	return m.configErr
}

func (m *mockCollectionStore) UpdateThumbnail(_ context.Context, _, _ string) error {
	return m.thumbErr
}
func (m *mockCollectionStore) SoftDelete(_ context.Context, _ string) error { return m.deleteErr }
func (m *mockCollectionStore) SetSections(_ context.Context, _ string, _ []CollectionSection) error {
	return m.sectionsErr
}

// mockMultiAssetStore extends mockAssetStore to support multiple assets in GetByIDs.
type mockMultiAssetStore struct {
	mockAssetStore
	assets map[string]*Asset
}

func (m *mockMultiAssetStore) GetByIDs(_ context.Context, ids []string) (map[string]*Asset, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	result := make(map[string]*Asset)
	for _, id := range ids {
		if a, ok := m.assets[id]; ok {
			result[id] = a
		}
	}
	return result, nil
}

// --- Collection public view tests ---

func TestPublicCollectionView(t *testing.T) {
	now := time.Now()
	share := &Share{
		ID: "s1", Token: "tok1", CollectionID: "c1",
		NoticeText: "Collection notice.",
	}

	coll := &Collection{
		ID:          "c1",
		Name:        "My Collection",
		Description: "A test collection",
		Config:      CollectionConfig{ThumbnailSize: "medium"},
		Sections: []CollectionSection{
			{
				ID: "sec1", Title: "Section 1", Description: "First section",
				Items: []CollectionItem{
					{ID: "item1", AssetID: "a1"},
				},
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	asset := &Asset{
		ID: "a1", Name: "Asset One", ContentType: "text/plain",
		Description: "Desc", ThumbnailS3Key: "thumb/a1.png",
	}

	h := NewHandler(Deps{
		AssetStore:      &mockMultiAssetStore{assets: map[string]*Asset{"a1": asset}},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")

	body := w.Body.String()
	assert.Contains(t, body, "My Collection")
	assert.Contains(t, body, "A test collection")
	assert.Contains(t, body, "Collection notice.")
	// CSP must include 'self' for collection viewer iframes
	csp := w.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "'self'")
}

// TestPublicCollectionViewHeaderBranding asserts the collection viewer
// header renders implementor branding (left), the collection name as an
// <h1> in the same header row, and platform branding (right) — using
// the same brand block layout as the asset viewer (note: the asset
// viewer wraps with <div class="header"> and the collection viewer
// wraps with <header>, so they are not pixel-identical, only
// structurally aligned). Regression guard for the bug where the
// collection viewer header omitted the title and used a less-robust
// implementor-branding conditional.
func TestPublicCollectionViewHeaderBranding(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", Token: "tok2", CollectionID: "c1"}
	coll := &Collection{
		ID:        "c1",
		Name:      "Q3 Revenue Report",
		Sections:  []CollectionSection{{ID: "sec1", Items: []CollectionItem{{ID: "i1", AssetID: "a1"}}}},
		CreatedAt: now, UpdatedAt: now,
	}
	asset := &Asset{ID: "a1", Name: "rows.csv", ContentType: "text/csv"}

	h := NewHandler(Deps{
		AssetStore:         &mockMultiAssetStore{assets: map[string]*Asset{"a1": asset}},
		ShareStore:         &mockShareStore{getByTokenRes: share},
		CollectionStore:    &mockCollectionStore{getResult: coll},
		S3Client:           &mockS3Client{},
		BrandName:          "ACME Data Platform",
		BrandURL:           "https://example.com/platform",
		ImplementorName:    "ACME Corp",
		ImplementorLogoSVG: `<svg id="impl-logo"></svg>`,
		ImplementorURL:     "https://example.com/about",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok2", http.NoBody)
	req.SetPathValue("token", "tok2")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Header includes the collection name as an <h1> alongside branding.
	assert.Contains(t, body, "<h1>Q3 Revenue Report</h1>",
		"collection name must appear as <h1> in the header (parity with asset viewer)")
	// Implementor branding renders with name + logo + link, asserted as the
	// fully-formed span (not bare substring) to prove the name lives in the
	// brand block rather than e.g. an error message or comment elsewhere.
	assert.Contains(t, body, `class="brand brand-implementor"`,
		"implementor brand block must use the asset-viewer class hierarchy")
	assert.Contains(t, body, `<span class="brand-name">ACME Corp</span>`,
		"implementor name must render inside the brand-name span")
	assert.Contains(t, body, `id="impl-logo"`, "implementor logo SVG should be inlined")
	assert.Contains(t, body, `href="https://example.com/about"`, "implementor URL should wrap the brand")
	// Hardened rel attribute: noopener+noreferrer (locks the policy so a
	// future revert to bare `noopener` is caught).
	assert.Contains(t, body, `rel="noopener noreferrer"`,
		"external brand links must carry rel=\"noopener noreferrer\"")
	// Platform branding renders on the right.
	assert.Contains(t, body, `class="brand brand-platform"`,
		"platform brand block must use the asset-viewer class hierarchy")
	assert.Contains(t, body, `<span class="brand-name">ACME Data Platform</span>`,
		"platform brand name must render inside the brand-name span")

	// DOM order: implementor (left) → h1 (center) → platform (right). Flexbox
	// layout follows DOM order, so a regression that misorders these blocks
	// is visible by source-position alone. Search anchors include the
	// `<div ` element prefix so they cannot match a CSS rule, comment, or
	// stray attribute string in the embedded <style> block.
	implIdx := strings.Index(body, `<div class="brand brand-implementor">`)
	h1Idx := strings.Index(body, "<h1>Q3 Revenue Report</h1>")
	platIdx := strings.Index(body, `<div class="brand brand-platform">`)
	require.NotEqual(t, -1, implIdx, "implementor block must be present")
	require.NotEqual(t, -1, h1Idx, "h1 must be present")
	require.NotEqual(t, -1, platIdx, "platform block must be present")
	assert.Less(t, implIdx, h1Idx, "implementor brand must precede the title in DOM order")
	assert.Less(t, h1Idx, platIdx, "title must precede the platform brand in DOM order")

	// Implementor anchor is fully closed: the {{if .ImplementorURL}}</a>{{end}}
	// pair must emit a closing tag *before* the header-center block, not
	// bleed the anchor into the title or beyond. require.NotEqual on the
	// index look-ups halts execution before any out-of-range slice.
	hdrCenterIdx := strings.Index(body, `<div class="header-center">`)
	require.NotEqual(t, -1, hdrCenterIdx, "header-center block must be present")
	require.Less(t, implIdx, hdrCenterIdx, "implementor block must precede header-center")
	implBlock := body[implIdx:hdrCenterIdx]
	assert.Contains(t, implBlock, "</a>",
		"implementor <a> must be closed before the header-center block")
	// Platform anchor must close before </body>. require to avoid panic.
	bodyEndIdx := strings.Index(body, "</body>")
	require.NotEqual(t, -1, bodyEndIdx, "body must close")
	require.Less(t, platIdx, bodyEndIdx, "platform block must precede </body>")
	platBlock := body[platIdx:bodyEndIdx]
	assert.Contains(t, platBlock, "</a>",
		"platform <a> must be closed before </body>")
	// And the open/close anchor counts must balance. Strip embedded
	// <script>/<style> blocks first because the bundled markdown
	// renderer's source contains literal `<a` tokens that aren't real
	// HTML anchors (e.g. regex patterns inside the JS bundle).
	balanced := stripScriptAndStyle(body)
	openAnchorRE := regexp.MustCompile(`<a[\s>]`)
	openCount := len(openAnchorRE.FindAllString(balanced, -1))
	closeCount := strings.Count(balanced, "</a>")
	assert.Equal(t, openCount, closeCount,
		"opening and closing <a> tag counts must match")
}

// stripScriptAndStyle removes <script>...</script> and <style>...</style>
// blocks from the body so anchor-balance assertions don't trip over
// `<a` tokens that appear as JavaScript source (e.g. regex patterns
// inside the bundled markdown renderer's marked.js source).
func stripScriptAndStyle(body string) string {
	for _, tag := range []string{"script", "style"} {
		open := "<" + tag
		closeTag := "</" + tag + ">"
		for {
			s := strings.Index(body, open)
			if s < 0 {
				break
			}
			e := strings.Index(body[s:], closeTag)
			if e < 0 {
				break
			}
			body = body[:s] + body[s+e+len(closeTag):]
		}
	}
	return body
}

// TestPublicCollectionViewLogoOnlyImplementor asserts implementor branding
// renders when only the logo is configured (no name). The previous template
// gated on ImplementorName alone and would have hidden the entire block.
// Collection has no Sections; the assertions only target the <header>
// block, which is unaffected by collection body content.
func TestPublicCollectionViewLogoOnlyImplementor(t *testing.T) {
	share := &Share{ID: "s1", Token: "tok3", CollectionID: "c1"}
	coll := &Collection{ID: "c1", Name: "Logo-Only Collection"}

	h := NewHandler(Deps{
		AssetStore:         &mockMultiAssetStore{assets: map[string]*Asset{}},
		ShareStore:         &mockShareStore{getByTokenRes: share},
		CollectionStore:    &mockCollectionStore{getResult: coll},
		S3Client:           &mockS3Client{},
		ImplementorLogoSVG: `<svg id="logo-only"></svg>`,
		// ImplementorName intentionally empty.
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok3", http.NoBody)
	req.SetPathValue("token", "tok3")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `class="brand brand-implementor"`,
		"implementor block must render even when only logo is configured")
	assert.Contains(t, body, `id="logo-only"`, "implementor logo SVG should be inlined")
	// No name span should render in the implementor block when name is empty
	// — guards against an accidentally-emitted empty <span class="brand-name">.
	// The platform brand also uses brand-name, so we constrain the search
	// to the implementor block by slicing on the platform marker.
	implSection := body
	if before, _, found := strings.Cut(body, `<div class="brand brand-platform">`); found {
		implSection = before
	}
	assert.NotContains(t, implSection, `<span class="brand-name">`,
		"empty ImplementorName must not produce a brand-name span")
	// Anchor balance must hold even in the URL-absent case (the implementor
	// anchor open/close conditional is governed by ImplementorURL, which is
	// empty here, so both should be absent — net zero on each side).
	balanced := stripScriptAndStyle(body)
	openAnchorRE := regexp.MustCompile(`<a[\s>]`)
	openCount := len(openAnchorRE.FindAllString(balanced, -1))
	closeCount := strings.Count(balanced, "</a>")
	assert.Equal(t, openCount, closeCount,
		"opening and closing <a> tag counts must match")
}

// TestPublicCollectionViewNameOnlyImplementor asserts implementor branding
// renders when only the name is configured (no logo). This was the legacy
// behavior under the old `{{if .ImplementorName}}` gate; the new
// `{{if or .ImplementorName .ImplementorLogoSVG}}` must preserve it.
// Regression guard against an accidental rewrite to AND.
func TestPublicCollectionViewNameOnlyImplementor(t *testing.T) {
	share := &Share{ID: "s1", Token: "tok4", CollectionID: "c1"}
	coll := &Collection{ID: "c1", Name: "Name-Only Collection"}

	h := NewHandler(Deps{
		AssetStore:      &mockMultiAssetStore{assets: map[string]*Asset{}},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{},
		ImplementorName: "Name Only Co.",
		// ImplementorLogoSVG intentionally empty.
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok4", http.NoBody)
	req.SetPathValue("token", "tok4")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `class="brand brand-implementor"`,
		"implementor block must render when only name is configured")
	assert.Contains(t, body, `<span class="brand-name">Name Only Co.</span>`,
		"implementor name must render inside the brand-name span")
	// Anchor balance even without a URL set.
	balanced := stripScriptAndStyle(body)
	openAnchorRE := regexp.MustCompile(`<a[\s>]`)
	openCount := len(openAnchorRE.FindAllString(balanced, -1))
	closeCount := strings.Count(balanced, "</a>")
	assert.Equal(t, openCount, closeCount,
		"opening and closing <a> tag counts must match")
}

func TestPublicCollectionViewNotFound(t *testing.T) {
	share := &Share{
		ID: "s1", Token: "tok1", CollectionID: "c1",
	}

	h := NewHandler(Deps{
		AssetStore:      &mockAssetStore{},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getErr: fmt.Errorf("not found")},
		S3Client:        &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublicCollectionViewDeleted(t *testing.T) {
	now := time.Now()
	share := &Share{
		ID: "s1", Token: "tok1", CollectionID: "c1",
	}
	coll := &Collection{
		ID: "c1", Name: "Gone", DeletedAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore:      &mockAssetStore{},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestPublicCollectionItemContent(t *testing.T) {
	now := time.Now()
	share := &Share{
		ID: "s1", Token: "tok1", CollectionID: "c1",
	}
	coll := &Collection{
		ID: "c1", Name: "Coll",
		Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}}},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	asset := &Asset{
		ID: "a1", Name: "Doc", ContentType: "text/plain",
		S3Bucket: "b1", S3Key: "assets/a1",
		CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore:      &mockAssetStore{getAsset: asset},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{getData: []byte("file content"), getCT: "text/plain"},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/content", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/plain", w.Header().Get("Content-Type"))
	assert.Equal(t, "file content", w.Body.String())
}

func TestPublicCollectionItemContentNotInCollection(t *testing.T) {
	now := time.Now()
	share := &Share{
		ID: "s1", Token: "tok1", CollectionID: "c1",
	}
	coll := &Collection{
		ID: "c1", Name: "Coll",
		Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}}},
		},
		CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore:      &mockAssetStore{getAsset: &Asset{ID: "a2"}},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{},
	}, nil)

	// Request asset a2, but collection only has a1
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a2/content", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a2")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPublicCollectionItemThumbnail(t *testing.T) {
	now := time.Now()
	share := &Share{
		ID: "s1", Token: "tok1", CollectionID: "c1",
	}
	coll := &Collection{
		ID: "c1", Name: "Coll",
		Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}}},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	asset := &Asset{
		ID: "a1", Name: "Photo", ContentType: "image/png",
		S3Bucket: "b1", S3Key: "assets/a1", ThumbnailS3Key: "thumb/a1.png",
		CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore:      &mockAssetStore{getAsset: asset},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{getData: []byte("pngdata"), getCT: "image/png"},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/thumbnail", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "public, max-age=3600", w.Header().Get("Cache-Control"))
	assert.Equal(t, "pngdata", w.Body.String())
}

func TestPublicCollectionItemView(t *testing.T) {
	now := time.Now()
	share := &Share{
		ID: "s1", Token: "tok1", CollectionID: "c1",
		NoticeText: "View notice.",
	}
	coll := &Collection{
		ID: "c1", Name: "Coll",
		Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}}},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	asset := &Asset{
		ID: "a1", Name: "My Asset", ContentType: "text/markdown",
		S3Bucket: "b1", S3Key: "assets/a1",
		Tags: []string{"tag1"}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore:      &mockAssetStore{getAsset: asset},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{getData: []byte("# Heading"), getCT: "text/markdown"},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/view", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	body := w.Body.String()
	assert.Contains(t, body, "My Asset")
	assert.Contains(t, body, `id="content-data"`)
	assert.Contains(t, body, "View notice.")
}

func TestPublicCollectionCSP(t *testing.T) {
	csp := publicCollectionCSP()
	assert.Contains(t, csp, "default-src 'none'")
	assert.Contains(t, csp, "frame-src 'self'")
	assert.Contains(t, csp, "script-src")
	assert.Contains(t, csp, "'unsafe-eval'")
	assert.Contains(t, csp, "'unsafe-inline'")
	assert.Contains(t, csp, "style-src")
	assert.Contains(t, csp, "img-src")
	assert.Contains(t, csp, "font-src")
	assert.Contains(t, csp, "connect-src")

	// Regular public CSP should NOT contain 'self' in frame-src
	regularCSP := publicCSP()
	assert.NotContains(t, regularCSP, "frame-src 'self'")
}

func TestCollectAssetIDs(t *testing.T) {
	t.Run("deduplication", func(t *testing.T) {
		coll := &Collection{
			Sections: []CollectionSection{
				{Items: []CollectionItem{
					{AssetID: "a1"}, {AssetID: "a2"}, {AssetID: "a1"},
				}},
				{Items: []CollectionItem{
					{AssetID: "a2"}, {AssetID: "a3"},
				}},
			},
		}
		ids := collectAssetIDs(coll)
		assert.Equal(t, []string{"a1", "a2", "a3"}, ids)
	})

	t.Run("empty collection", func(t *testing.T) {
		coll := &Collection{}
		ids := collectAssetIDs(coll)
		assert.Empty(t, ids)
	})

	t.Run("no items", func(t *testing.T) {
		coll := &Collection{
			Sections: []CollectionSection{
				{Title: "Empty Section", Items: nil},
			},
		}
		ids := collectAssetIDs(coll)
		assert.Empty(t, ids)
	})
}

func TestBuildPublicSections(t *testing.T) {
	assets := map[string]*Asset{
		"a1": {ID: "a1", Name: "Asset One", ContentType: "text/plain", Description: "Desc1", ThumbnailS3Key: "thumb/a1.png"},
		"a2": {ID: "a2", Name: "Asset Two", ContentType: "image/png", Description: "Desc2"},
	}

	coll := &Collection{
		Sections: []CollectionSection{
			{
				Title: "Section A", Description: "First",
				Items: []CollectionItem{
					{AssetID: "a1"}, {AssetID: "a2"},
				},
			},
			{
				Title: "Section B", Description: "Second",
				Items: []CollectionItem{
					{AssetID: "a3"}, // not in assets map — should be skipped
				},
			},
		},
	}

	sections := buildPublicSections(coll, assets)
	assert.Len(t, sections, 2)

	// Section A: both items resolved
	secA := sections[0]
	assert.Equal(t, "Section A", secA["title"])
	assert.Equal(t, "First", secA["description"])
	itemsA, ok := secA["items"].([]map[string]any)
	assert.True(t, ok)
	assert.Len(t, itemsA, 2)
	assert.Equal(t, "a1", itemsA[0]["assetId"])
	assert.Equal(t, "Asset One", itemsA[0]["name"])
	assert.Equal(t, true, itemsA[0]["hasThumbnail"])
	assert.Equal(t, "a2", itemsA[1]["assetId"])
	assert.Equal(t, false, itemsA[1]["hasThumbnail"])

	// Section B: item a3 not found, so items empty
	secB := sections[1]
	assert.Equal(t, "Section B", secB["title"])
	itemsB, ok := secB["items"].([]map[string]any)
	assert.True(t, ok)
	assert.Empty(t, itemsB)
}

func TestCollectionContainsAsset(t *testing.T) {
	coll := &Collection{
		Sections: []CollectionSection{
			{Items: []CollectionItem{
				{AssetID: "a1"}, {AssetID: "a2"},
			}},
			{Items: []CollectionItem{
				{AssetID: "a3"},
			}},
		},
	}

	assert.True(t, collectionContainsAsset(coll, "a1"))
	assert.True(t, collectionContainsAsset(coll, "a2"))
	assert.True(t, collectionContainsAsset(coll, "a3"))
	assert.False(t, collectionContainsAsset(coll, "a4"))
	assert.False(t, collectionContainsAsset(coll, ""))

	// Empty collection
	assert.False(t, collectionContainsAsset(&Collection{}, "a1"))
}

func TestFetchAssetMap(t *testing.T) {
	asset := &Asset{ID: "a1", Name: "Test"}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{},
	}, nil)

	result := h.fetchAssetMap(context.Background(), []string{"a1", "a99"})
	assert.Len(t, result, 1)
	assert.Equal(t, "Test", result["a1"].Name)

	// Error case: getErr causes empty map
	hErr := NewHandler(Deps{
		AssetStore: &mockAssetStore{getErr: fmt.Errorf("db fail")},
		ShareStore: &mockShareStore{},
	}, nil)
	resultErr := hErr.fetchAssetMap(context.Background(), []string{"a1"})
	assert.Empty(t, resultErr)
}

func TestValidateCollectionItemAccessInvalidToken(t *testing.T) {
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{},
		S3Client:   &mockS3Client{},
	}, nil)

	// Empty token
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view//items/a1/content", http.NoBody)
	req.SetPathValue("token", "")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()

	result := h.validateCollectionItemAccess(w, req)
	assert.Nil(t, result)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Empty assetId
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items//content", http.NoBody)
	req2.SetPathValue("token", "tok1")
	req2.SetPathValue("assetId", "")
	result2 := h.validateCollectionItemAccess(w2, req2)
	assert.Nil(t, result2)
	assert.Equal(t, http.StatusNotFound, w2.Code)
}

func TestValidateCollectionItemAccessExpiredShare(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	share := &Share{
		ID: "s1", Token: "tok1", CollectionID: "c1",
		ExpiresAt: &past,
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/content", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()

	result := h.validateCollectionItemAccess(w, req)
	assert.Nil(t, result)
	assert.Equal(t, http.StatusGone, w.Code)
}

func TestValidateCollectionItemAccessCollectionNotFound(t *testing.T) {
	share := &Share{ID: "s1", CollectionID: "coll-missing", Token: "tok1"}

	h := NewHandler(Deps{
		ShareStore: &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{
			getErr: fmt.Errorf("not found"),
		},
		RateLimit: RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/content", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()

	result := h.validateCollectionItemAccess(w, req)
	assert.Nil(t, result)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestValidateCollectionItemAccessNoCollectionStore(t *testing.T) {
	share := &Share{ID: "s1", CollectionID: "coll1", Token: "tok1"}

	h := NewHandler(Deps{
		ShareStore: &mockShareStore{getByTokenRes: share},
		// CollectionStore deliberately nil
		RateLimit: RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/content", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()

	result := h.validateCollectionItemAccess(w, req)
	assert.Nil(t, result)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestPublicCollectionItemViewSuccess(t *testing.T) {
	now := time.Now()
	coll := &Collection{
		ID: "coll1", Name: "Test", OwnerID: "u1",
		Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}}},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	share := &Share{ID: "s1", CollectionID: "coll1", Token: "tok1", NoticeText: defaultNoticeText}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
		S3Bucket: "b", S3Key: "k", Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		AssetStore:      &mockAssetStore{getAsset: asset},
		S3Client:        &mockS3Client{getData: []byte("<h1>hi</h1>"), getCT: "text/html"},
		S3Bucket:        "b",
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/view", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()
	h.publicMux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "text/html")
}

func TestPublicCollectionItemThumbnailNoS3(t *testing.T) {
	now := time.Now()
	coll := &Collection{
		ID: "coll1", Name: "Test", OwnerID: "u1",
		Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}}},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	share := &Share{ID: "s1", CollectionID: "coll1", Token: "tok1"}

	h := NewHandler(Deps{
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		// S3Client deliberately nil
		RateLimit: RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/thumbnail", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()
	h.publicMux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublicCollectionItemThumbnailNoThumbKey(t *testing.T) {
	now := time.Now()
	coll := &Collection{
		ID: "coll1", Name: "Test", OwnerID: "u1",
		Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}}},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	share := &Share{ID: "s1", CollectionID: "coll1", Token: "tok1"}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
		ThumbnailS3Key: "", // no thumbnail
		Tags:           []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		AssetStore:      &mockAssetStore{getAsset: asset},
		S3Client:        &mockS3Client{},
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/thumbnail", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()
	h.publicMux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublicCollectionItemThumbnailS3Error(t *testing.T) {
	now := time.Now()
	coll := &Collection{
		ID: "coll1", Name: "Test", OwnerID: "u1",
		Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}}},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	share := &Share{ID: "s1", CollectionID: "coll1", Token: "tok1"}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
		ThumbnailS3Key: "portal/thumb.png", S3Bucket: "b",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		AssetStore:      &mockAssetStore{getAsset: asset},
		S3Client:        &mockS3Client{getErr: fmt.Errorf("s3 down")},
		S3Bucket:        "b",
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/thumbnail", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()
	h.publicMux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublicCollectionViewNoCollectionStore(t *testing.T) {
	share := &Share{ID: "s1", CollectionID: "coll1", Token: "tok1"}

	h := NewHandler(Deps{
		ShareStore: &mockShareStore{getByTokenRes: share},
		// CollectionStore deliberately nil
		RateLimit: RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestPublicCollectionItemContentFetchError(t *testing.T) {
	now := time.Now()
	coll := &Collection{
		ID: "coll1", Name: "Test", OwnerID: "u1",
		Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}}},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	share := &Share{ID: "s1", CollectionID: "coll1", Token: "tok1"}

	h := NewHandler(Deps{
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		AssetStore:      &mockAssetStore{getErr: fmt.Errorf("not found")},
		S3Client:        &mockS3Client{},
		S3Bucket:        "b",
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/content", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()
	h.publicMux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublicCollectionItemViewFetchError(t *testing.T) {
	now := time.Now()
	coll := &Collection{
		ID: "coll1", Name: "Test", OwnerID: "u1",
		Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}}},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	share := &Share{ID: "s1", CollectionID: "coll1", Token: "tok1"}

	h := NewHandler(Deps{
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		AssetStore:      &mockAssetStore{getErr: fmt.Errorf("not found")},
		S3Client:        &mockS3Client{},
		S3Bucket:        "b",
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/items/a1/view", http.NoBody)
	req.SetPathValue("token", "tok1")
	req.SetPathValue("assetId", "a1")
	w := httptest.NewRecorder()
	h.publicMux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublicAssetContentDownload(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "export.csv", ContentType: "text/csv",
		SizeBytes: 5000, Tags: []string{}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("a,b\n1,2"), getCT: "text/csv"},
		S3Bucket:   "test",
		RateLimit:  RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/content", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.publicMux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/csv")
	assert.Contains(t, w.Header().Get("Content-Disposition"), "export.csv")
	assert.Equal(t, "a,b\n1,2", w.Body.String())
}

func TestPublicAssetContentTokenNotFound(t *testing.T) {
	h := NewHandler(Deps{
		ShareStore: &mockShareStore{getByTokenErr: fmt.Errorf("not found")},
		S3Client:   &mockS3Client{},
		RateLimit:  RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/bad/content", http.NoBody)
	req.SetPathValue("token", "bad")
	w := httptest.NewRecorder()
	h.publicMux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublicViewLargeAssetShowsTooLarge(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "big-export.csv", ContentType: "text/csv",
		SizeBytes: 10 * 1024 * 1024, // 10 MB — exceeds 2 MB threshold
		Tags:      []string{}, CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("should not be fetched"), getCT: "text/csv"},
		S3Bucket:   "test",
		RateLimit:  RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.publicMux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `"tooLarge":true`)
	assert.Contains(t, body, `"downloadURL"`)
}

func TestPublicAssetContentLargeStillDownloads(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "big.csv", ContentType: "text/csv",
		SizeBytes: 10 * 1024 * 1024, // 10 MB
		Tags:      []string{}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("big content"), getCT: "text/csv"},
		S3Bucket:   "test",
		RateLimit:  RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	// The content download endpoint must serve full content even for large assets
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/content", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.publicMux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "big content", w.Body.String())
}

// --- OG / link-preview metadata ---

// TestResolvePublicBaseURL covers each branch of the base-URL resolver:
// configured value wins, request-derived fallback works, X-Forwarded-Proto
// is honored only when not directly TLS, multi-proxy chains are collapsed
// to the originating client scheme, and missing Host yields empty (so
// callers omit OG tags rather than emit relative URLs that crawlers reject).
func TestResolvePublicBaseURL(t *testing.T) {
	t.Run("configured value wins and trailing slash is trimmed", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
		req.Host = "should-be-ignored.example.com"
		got := resolvePublicBaseURL(req, "https://share.example.com/")
		assert.Equal(t, "https://share.example.com", got)
	})
	t.Run("falls back to request scheme+host when config empty", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "http://example.com/x", http.NoBody)
		req.Host = "example.com"
		got := resolvePublicBaseURL(req, "")
		assert.Equal(t, "http://example.com", got)
	})
	t.Run("X-Forwarded-Proto upgrades scheme behind a proxy", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "http://example.com/x", http.NoBody)
		req.Host = "example.com"
		req.Header.Set("X-Forwarded-Proto", "https")
		got := resolvePublicBaseURL(req, "")
		assert.Equal(t, "https://example.com", got)
	})
	t.Run("X-Forwarded-Proto with comma-chain takes first token", func(t *testing.T) {
		// Multi-proxy chains can produce values like "https, http".
		// The leftmost value is the originating client's scheme — taking
		// it whole would yield "https, http://example.com/...", which is
		// malformed and would be rejected by every social-media crawler.
		req := httptest.NewRequestWithContext(context.Background(), "GET", "http://example.com/x", http.NoBody)
		req.Host = "example.com"
		req.Header.Set("X-Forwarded-Proto", "https, http")
		got := resolvePublicBaseURL(req, "")
		assert.Equal(t, "https://example.com", got)
	})
	t.Run("X-Forwarded-Proto with bogus value falls back to default", func(t *testing.T) {
		// Only http/https are accepted; anything else must fall back to
		// the default scheme to prevent a misbehaving proxy from emitting
		// non-HTTP og:url URLs (e.g. "javascript://..." or "ftp://...").
		req := httptest.NewRequestWithContext(context.Background(), "GET", "http://example.com/x", http.NoBody)
		req.Host = "example.com"
		req.Header.Set("X-Forwarded-Proto", "javascript")
		got := resolvePublicBaseURL(req, "")
		assert.Equal(t, "http://example.com", got)
	})
	t.Run("X-Forwarded-Proto cannot downgrade direct TLS scheme", func(t *testing.T) {
		// When the server is the TLS terminator itself (r.TLS != nil),
		// an attacker-controlled X-Forwarded-Proto must not be allowed to
		// override the real scheme. Without this guard, a request
		// arriving on https:// could emit http:// og:url tags, a
		// gratuitous client-trust regression.
		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
		req.Host = "example.com"
		req.TLS = &tls.ConnectionState{}
		req.Header.Set("X-Forwarded-Proto", "http")
		got := resolvePublicBaseURL(req, "")
		assert.Equal(t, "https://example.com", got)
	})
	t.Run("empty when no config and no Host", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
		req.Host = ""
		got := resolvePublicBaseURL(req, "")
		assert.Equal(t, "", got)
	})
}

// TestPublicAssetOGImage covers the og:image URL selection for single-asset
// shares: image content types win, then thumbnail key, else empty.
func TestPublicAssetOGImage(t *testing.T) {
	const base = "https://share.example.com"
	t.Run("image content type uses asset content URL", func(t *testing.T) {
		got := publicAssetOGImage(&Asset{ContentType: "image/png"}, "tok", base)
		assert.Equal(t, base+"/portal/view/tok/content", got)
	})
	t.Run("non-image with thumbnail uses thumbnail URL", func(t *testing.T) {
		got := publicAssetOGImage(&Asset{ContentType: "text/csv", ThumbnailS3Key: "k"}, "tok", base)
		assert.Equal(t, base+"/portal/view/tok/thumbnail", got)
	})
	t.Run("non-image without thumbnail returns empty", func(t *testing.T) {
		got := publicAssetOGImage(&Asset{ContentType: "text/csv"}, "tok", base)
		assert.Equal(t, "", got)
	})
	t.Run("empty baseURL disables emission entirely", func(t *testing.T) {
		got := publicAssetOGImage(&Asset{ContentType: "image/png"}, "tok", "")
		assert.Equal(t, "", got)
	})
	t.Run("nil asset returns empty without panicking", func(t *testing.T) {
		got := publicAssetOGImage(nil, "tok", base)
		assert.Equal(t, "", got)
	})
}

// TestPublicCollectionOGImage covers og:image selection for collection
// shares: collection's own thumbnail wins, else first item with thumbnail,
// else empty.
func TestPublicCollectionOGImage(t *testing.T) {
	const base = "https://share.example.com"
	t.Run("collection thumbnail wins", func(t *testing.T) {
		coll := &Collection{ThumbnailS3Key: "k"}
		got := publicCollectionOGImage(coll, nil, "tok", base)
		assert.Equal(t, base+"/portal/view/tok/collection-thumbnail", got)
	})
	t.Run("falls back to first item with thumbnail", func(t *testing.T) {
		coll := &Collection{Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}, {AssetID: "a2"}}},
		}}
		assets := map[string]*Asset{
			"a1": {ID: "a1"},                          // no thumbnail
			"a2": {ID: "a2", ThumbnailS3Key: "thumb"}, // has thumbnail
		}
		got := publicCollectionOGImage(coll, assets, "tok", base)
		assert.Equal(t, base+"/portal/view/tok/items/a2/thumbnail", got)
	})
	t.Run("returns empty when no thumbnails available", func(t *testing.T) {
		coll := &Collection{Sections: []CollectionSection{
			{Items: []CollectionItem{{AssetID: "a1"}}},
		}}
		assets := map[string]*Asset{"a1": {ID: "a1"}}
		got := publicCollectionOGImage(coll, assets, "tok", base)
		assert.Equal(t, "", got)
	})
	t.Run("empty baseURL disables emission entirely", func(t *testing.T) {
		coll := &Collection{ThumbnailS3Key: "k"}
		got := publicCollectionOGImage(coll, nil, "tok", "")
		assert.Equal(t, "", got)
	})
}

// TestPublicAssetViewerEmitsOGMetadata asserts the asset viewer renders
// og:title/og:url/og:image/og:site_name and the Twitter card variants.
// Uses an explicit PublicBaseURL so the test doesn't depend on the
// httptest Host fallback path (covered separately by
// TestResolvePublicBaseURL).
func TestPublicAssetViewerEmitsOGMetadata(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}
	asset := &Asset{
		ID: "a1", Name: "Cover.png", ContentType: "image/png",
		Description: "Quarterly cover image",
		S3Bucket:    "b1", S3Key: "assets/a1.png",
		CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore:    &mockAssetStore{getAsset: asset},
		ShareStore:    &mockShareStore{getByTokenRes: share},
		S3Client:      &mockS3Client{getData: []byte("png"), getCT: "image/png"},
		PublicBaseURL: "https://share.example.com",
		BrandName:     "ACME Data Platform",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	assert.Contains(t, body, `<meta property="og:type" content="website">`)
	assert.Contains(t, body, `<meta property="og:title" content="Cover.png">`)
	assert.Contains(t, body, `<meta property="og:description" content="Quarterly cover image">`)
	assert.Contains(t, body, `<meta property="og:url" content="https://share.example.com/portal/view/tok1">`)
	assert.Contains(t, body, `<meta property="og:site_name" content="ACME Data Platform">`)
	// Image-typed asset → og:image points at /content (raw asset).
	assert.Contains(t, body, `<meta property="og:image" content="https://share.example.com/portal/view/tok1/content">`)
	// Twitter card upgrades to summary_large_image when og:image is present.
	assert.Contains(t, body, `<meta name="twitter:card" content="summary_large_image">`)
	assert.Contains(t, body, `<meta name="twitter:title" content="Cover.png">`)
	assert.Contains(t, body, `<meta name="twitter:image" content="https://share.example.com/portal/view/tok1/content">`)
}

// TestPublicAssetViewerOGFallsBackToSummaryWhenNoImage asserts a non-image,
// no-thumbnail asset still emits Twitter+OG basics but downgrades the
// Twitter card to "summary" and omits og:image.
func TestPublicAssetViewerOGFallsBackToSummaryWhenNoImage(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}
	asset := &Asset{
		ID: "a1", Name: "data.csv", ContentType: "text/csv",
		S3Bucket: "b1", S3Key: "assets/a1.csv",
		CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore:    &mockAssetStore{getAsset: asset},
		ShareStore:    &mockShareStore{getByTokenRes: share},
		S3Client:      &mockS3Client{getData: []byte("a,b\n1,2"), getCT: "text/csv"},
		PublicBaseURL: "https://share.example.com",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	assert.NotContains(t, body, `property="og:image"`,
		"og:image must be omitted when asset isn't an image and has no thumbnail")
	assert.Contains(t, body, `<meta name="twitter:card" content="summary">`,
		"twitter:card must downgrade to summary when no image is available")
}

// TestPublicCollectionViewerEmitsOGMetadata asserts collection share pages
// emit OG/Twitter meta tags pointing at the collection's own thumbnail
// endpoint when one is configured.
func TestPublicCollectionViewerEmitsOGMetadata(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", Token: "tok1", CollectionID: "c1"}
	coll := &Collection{
		ID: "c1", Name: "Q4 Review", Description: "Executive review pack",
		ThumbnailS3Key: "collections/c1/thumb.png",
		CreatedAt:      now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore:      &mockMultiAssetStore{assets: map[string]*Asset{}},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{},
		PublicBaseURL:   "https://share.example.com",
		BrandName:       "ACME Data Platform",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	assert.Contains(t, body, `<meta property="og:title" content="Q4 Review">`)
	assert.Contains(t, body, `<meta property="og:description" content="Executive review pack">`)
	assert.Contains(t, body, `<meta property="og:url" content="https://share.example.com/portal/view/tok1">`)
	assert.Contains(t, body, `<meta property="og:image" content="https://share.example.com/portal/view/tok1/collection-thumbnail">`)
	assert.Contains(t, body, `<meta name="twitter:card" content="summary_large_image">`)
}

// --- Public single-asset thumbnail endpoint ---

func TestPublicAssetThumbnail(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}
	asset := &Asset{
		ID: "a1", Name: "doc.pdf", ContentType: "application/pdf",
		S3Bucket: "b1", S3Key: "assets/a1.pdf", ThumbnailS3Key: "thumb/a1.png",
		CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: []byte("pngdata"), getCT: "image/png"},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/thumbnail", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "public, max-age=3600", w.Header().Get("Cache-Control"))
	assert.Equal(t, "pngdata", w.Body.String())
}

func TestPublicAssetThumbnailNotFoundWhenNoThumbKey(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}
	asset := &Asset{ID: "a1", Name: "doc.pdf", ContentType: "application/pdf"} // no ThumbnailS3Key

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/thumbnail", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublicAssetThumbnailRejectsCollectionShare(t *testing.T) {
	// A collection-share token must not resolve via the single-asset
	// thumbnail endpoint — share.AssetID is empty, so we 404.
	share := &Share{ID: "s1", Token: "tok1", CollectionID: "c1"}

	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/thumbnail", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- Public collection thumbnail endpoint ---

func TestPublicCollectionThumbnail(t *testing.T) {
	now := time.Now()
	share := &Share{ID: "s1", Token: "tok1", CollectionID: "c1"}
	coll := &Collection{
		ID: "c1", Name: "Q4", ThumbnailS3Key: "collections/c1/thumb.png",
		CreatedAt: now, UpdatedAt: now,
	}

	h := NewHandler(Deps{
		AssetStore:      &mockAssetStore{},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{getData: []byte("png"), getCT: "image/png"},
		S3Bucket:        "b1",
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/collection-thumbnail", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "public, max-age=3600", w.Header().Get("Cache-Control"))
	assert.Equal(t, "png", w.Body.String())
}

func TestPublicCollectionThumbnailNotFoundWhenNoKey(t *testing.T) {
	share := &Share{ID: "s1", Token: "tok1", CollectionID: "c1"}
	coll := &Collection{ID: "c1", Name: "Q4"} // no ThumbnailS3Key

	h := NewHandler(Deps{
		AssetStore:      &mockAssetStore{},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/collection-thumbnail", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublicCollectionThumbnailRejectsAssetShare(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1", Token: "tok1"}

	h := NewHandler(Deps{
		AssetStore:      &mockAssetStore{},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{},
		S3Client:        &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/collection-thumbnail", http.NoBody)
	req.SetPathValue("token", "tok1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
