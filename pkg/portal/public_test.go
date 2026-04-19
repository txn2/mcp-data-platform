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
