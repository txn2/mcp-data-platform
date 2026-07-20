package branding

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", v)
	}
	return m
}

func TestInjectPortalLogo(t *testing.T) {
	svgContent := `<svg viewBox="0 0 40 40"><circle cx="20" cy="20" r="10"/></svg>`

	t.Run("fetches SVG and injects as logo_svg", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(svgContent))
		}))
		defer srv.Close()

		h := New(Config{PortalLogo: srv.URL + "/logo.svg"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{"brand_name": "Test"}))
		if m["logo_svg"] != svgContent {
			t.Errorf("logo_svg = %v, want %q", m["logo_svg"], svgContent)
		}
		if m["logo_url"] != nil {
			t.Error("logo_url should be nil when SVG was fetched")
		}
		if h.BrandLogoSVG() != svgContent {
			t.Errorf("BrandLogoSVG() = %q, want %q", h.BrandLogoSVG(), svgContent)
		}
	})

	t.Run("falls back to logo_url on non-SVG content type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("not-svg"))
		}))
		defer srv.Close()

		h := New(Config{PortalLogo: srv.URL + "/logo.png"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{"brand_name": "Test"}))
		if m["logo_url"] != srv.URL+"/logo.png" {
			t.Errorf("logo_url = %v, want %q", m["logo_url"], srv.URL+"/logo.png")
		}
		if m["logo_svg"] != nil {
			t.Error("logo_svg should be nil for non-SVG")
		}
	})

	t.Run("falls back to logo_url on fetch error", func(t *testing.T) {
		h := New(Config{PortalLogo: "http://127.0.0.1:1/unreachable.svg"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{"brand_name": "Test"}))
		if m["logo_url"] != "http://127.0.0.1:1/unreachable.svg" {
			t.Errorf("logo_url = %v, want unreachable URL", m["logo_url"])
		}
	})

	t.Run("does not overwrite explicit logo_svg", func(t *testing.T) {
		h := New(Config{PortalLogo: "https://example.com/logo.svg"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{"logo_svg": "<svg>custom</svg>"}))
		if m["logo_svg"] != "<svg>custom</svg>" {
			t.Errorf("logo_svg was overwritten: %v", m["logo_svg"])
		}
	})

	t.Run("does not overwrite explicit logo_url", func(t *testing.T) {
		h := New(Config{PortalLogo: "https://example.com/logo.svg"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{"logo_url": "https://other.com/logo.png"}))
		if m["logo_url"] != "https://other.com/logo.png" {
			t.Errorf("logo_url = %v, want %q", m["logo_url"], "https://other.com/logo.png")
		}
	})

	t.Run("no-op when portal logo is empty", func(t *testing.T) {
		h := New(Config{})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{"brand_name": "Test"}))
		if m["logo_url"] != nil {
			t.Errorf("logo_url should be nil when portal logo is empty, got %v", m["logo_url"])
		}
	})

	t.Run("creates map when config is nil", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(svgContent))
		}))
		defer srv.Close()

		h := New(Config{PortalLogo: srv.URL + "/logo.svg"})
		m := mustMap(t, h.InjectPortalLogo(nil))
		if m["logo_svg"] != svgContent {
			t.Errorf("logo_svg = %v, want %q", m["logo_svg"], svgContent)
		}
	})

	t.Run("nil handle returns the map unchanged", func(t *testing.T) {
		var h *Handle
		m := mustMap(t, h.InjectPortalLogo(map[string]any{"brand_url": "https://x"}))
		if m["brand_url"] != "https://x" {
			t.Errorf("nil handle should pass the map through: %v", m)
		}
	})
}

func TestFetchLogoSVG(t *testing.T) {
	svgContent := `<svg viewBox="0 0 40 40"><circle cx="20" cy="20" r="10"/></svg>`

	t.Run("returns SVG content", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(svgContent))
		}))
		defer srv.Close()

		got, err := fetchLogoSVG(srv.URL + "/logo.svg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != svgContent {
			t.Errorf("got %q, want %q", got, svgContent)
		}
	})

	t.Run("rejects non-SVG content type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("PNG"))
		}))
		defer srv.Close()

		if _, err := fetchLogoSVG(srv.URL + "/logo.png"); err == nil {
			t.Fatal("expected error for non-SVG content type")
		}
	})

	t.Run("rejects non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		if _, err := fetchLogoSVG(srv.URL + "/missing.svg"); err == nil {
			t.Fatal("expected error for 404")
		}
	})

	t.Run("rejects non-HTTP scheme", func(t *testing.T) {
		if _, err := fetchLogoSVG("ftp://example.com/logo.svg"); err == nil {
			t.Fatal("expected error for non-HTTP scheme")
		}
	})

	t.Run("handles SVG with charset in content type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
			_, _ = w.Write([]byte(svgContent))
		}))
		defer srv.Close()

		got, err := fetchLogoSVG(srv.URL + "/logo.svg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != svgContent {
			t.Errorf("got %q, want %q", got, svgContent)
		}
	})
}

func TestFetchEmailLogoPNG(t *testing.T) {
	pngContent := []byte("\x89PNG\r\n\x1a\nfake-raster-bytes")

	t.Run("returns PNG bytes", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngContent)
		}))
		defer srv.Close()

		got, err := FetchEmailLogoPNG(srv.URL + "/logo.png")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(got, pngContent) {
			t.Errorf("got %q, want %q", got, pngContent)
		}
	})

	// The portal's own brand asset is an SVG URL. Accepting it here would ship
	// a part no mail client renders, so the content-type check is what keeps an
	// operator from pointing this at the logo they already have.
	t.Run("rejects SVG content type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<svg viewBox="0 0 40 40"></svg>`))
		}))
		defer srv.Close()

		if _, err := FetchEmailLogoPNG(srv.URL + "/logo.svg"); err == nil {
			t.Fatal("expected error for SVG content type")
		}
	})

	t.Run("rejects non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		if _, err := FetchEmailLogoPNG(srv.URL + "/missing.png"); err == nil {
			t.Fatal("expected error for 404")
		}
	})

	t.Run("rejects non-HTTP scheme", func(t *testing.T) {
		if _, err := FetchEmailLogoPNG("/etc/logo.png"); err == nil {
			t.Fatal("expected error for filesystem path")
		}
	})
}

func TestBrandURL(t *testing.T) {
	t.Run("returns empty when not set", func(t *testing.T) {
		if got := New(Config{}).BrandURL(); got != "" {
			t.Errorf("BrandURL() = %q, want empty", got)
		}
	})

	t.Run("returns cached value from InjectPortalLogo", func(t *testing.T) {
		h := New(Config{})
		_ = h.InjectPortalLogo(map[string]any{"brand_url": "https://example.com"})
		if got := h.BrandURL(); got != "https://example.com" {
			t.Errorf("BrandURL() = %q, want %q", got, "https://example.com")
		}
	})

	t.Run("nil handle returns empty", func(t *testing.T) {
		var h *Handle
		if got := h.BrandURL(); got != "" {
			t.Errorf("nil BrandURL() = %q, want empty", got)
		}
	})
}

func TestInjectPortalLogoCachesBrandURL(t *testing.T) {
	t.Run("caches brand_url from config", func(t *testing.T) {
		h := New(Config{})
		_ = h.InjectPortalLogo(map[string]any{"brand_url": "https://platform.io"})
		if got := h.BrandURL(); got != "https://platform.io" {
			t.Errorf("BrandURL() = %q, want %q", got, "https://platform.io")
		}
	})

	t.Run("caches brand_url and logo_svg even without portal logo", func(t *testing.T) {
		h := New(Config{}) // no PortalLogo
		_ = h.InjectPortalLogo(map[string]any{"brand_url": "https://noportallogo.io", "logo_svg": "<svg/>"})
		if got := h.BrandURL(); got != "https://noportallogo.io" {
			t.Errorf("BrandURL() = %q, want %q", got, "https://noportallogo.io")
		}
		if got := h.BrandLogoSVG(); got != "<svg/>" {
			t.Errorf("BrandLogoSVG() = %q, want %q", got, "<svg/>")
		}
	})

	t.Run("does not set brand_url when absent", func(t *testing.T) {
		h := New(Config{})
		_ = h.InjectPortalLogo(map[string]any{"brand_name": "Test"})
		if got := h.BrandURL(); got != "" {
			t.Errorf("BrandURL() = %q, want empty", got)
		}
	})
}

func TestResolveImplementorLogo(t *testing.T) {
	svgContent := `<svg viewBox="0 0 32 32"><rect width="32" height="32"/></svg>`

	t.Run("fetches and caches SVG", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(svgContent))
		}))
		defer srv.Close()

		h := New(Config{ImplementorLogo: srv.URL + "/impl.svg"})
		if got := h.ResolveImplementorLogo(); got != svgContent {
			t.Errorf("ResolveImplementorLogo() = %q, want %q", got, svgContent)
		}

		// Second call must return the cached value even after the server stops.
		srv.Close()
		if got := h.ResolveImplementorLogo(); got != svgContent {
			t.Errorf("cached ResolveImplementorLogo() = %q, want %q", got, svgContent)
		}
	})

	t.Run("returns empty when logo URL is empty", func(t *testing.T) {
		if got := New(Config{}).ResolveImplementorLogo(); got != "" {
			t.Errorf("ResolveImplementorLogo() = %q, want empty", got)
		}
	})

	t.Run("returns empty on fetch failure", func(t *testing.T) {
		h := New(Config{ImplementorLogo: "http://127.0.0.1:1/unreachable.svg"})
		if got := h.ResolveImplementorLogo(); got != "" {
			t.Errorf("ResolveImplementorLogo() = %q, want empty on fetch failure", got)
		}
	})

	t.Run("nil handle returns empty", func(t *testing.T) {
		var h *Handle
		if got := h.ResolveImplementorLogo(); got != "" {
			t.Errorf("nil ResolveImplementorLogo() = %q, want empty", got)
		}
	})
}
