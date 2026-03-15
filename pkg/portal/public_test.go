package portal

import (
	"context"
	"fmt"
	htmlpkg "html"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

	// No server-rendered content (no <pre>, <iframe>, <strong> etc.)
	assert.NotContains(t, body, "<strong>")
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
