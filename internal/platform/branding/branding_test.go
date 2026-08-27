package branding

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// pngBytes is a minimal but structurally valid PNG: the signature plus an IHDR
// chunk, which is what http.DetectContentType keys off.
var pngBytes = append(
	[]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
	[]byte{0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R'}...,
)

var gifBytes = []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00,")

// imageServer serves one body under one declared Content-Type.
func imageServer(t *testing.T, contentType string, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	}))
}

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
		// The app config carries the SVG inline; the platform's own pages link
		// the same logo by URL instead (TestBrandMarkup).
		if want := `<img src="` + srv.URL + `/logo.svg" alt="Test">`; h.BrandLogoHTML() != want {
			t.Errorf("BrandLogoHTML() = %q, want %q", h.BrandLogoHTML(), want)
		}
	})

	// A raster logo reaches the apps as a data: URI under logo_url, which is
	// the key their <img> element already renders from. It is inlined rather
	// than linked for the same reason the SVG is: a sandboxed app iframe loads
	// no external image (#1500).
	t.Run("fetches a PNG and injects it as a data URI", func(t *testing.T) {
		srv := imageServer(t, "image/png", pngBytes)
		defer srv.Close()

		h := New(Config{PortalLogo: srv.URL + "/logo.png", BrandName: "ACME"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{}))
		wantURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
		if m["logo_url"] != wantURI {
			t.Errorf("logo_url = %v, want the PNG as a data URI", m["logo_url"])
		}
		if m["logo_svg"] != nil {
			t.Errorf("logo_svg = %v, want nil for a raster logo", m["logo_svg"])
		}
		if want := `<img src="` + srv.URL + `/logo.png" alt="ACME">`; h.BrandLogoHTML() != want {
			t.Errorf("BrandLogoHTML() = %q, want the page to link the URL, not the data URI: %q",
				h.BrandLogoHTML(), want)
		}
	})

	// A bucket that labels every object application/octet-stream is the case
	// that made this a bug report: the header alone cannot accept the logo, so
	// the decision is taken over the bytes by pkg/contenttype.
	t.Run("accepts a PNG a server declared as octet-stream", func(t *testing.T) {
		srv := imageServer(t, "application/octet-stream", pngBytes)
		defer srv.Close()

		h := New(Config{PortalLogo: srv.URL + "/logo.png"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{}))
		if uri, _ := m["logo_url"].(string); !strings.HasPrefix(uri, "data:image/png;base64,") {
			t.Errorf("logo_url = %v, want a PNG data URI", m["logo_url"])
		}
	})

	t.Run("falls back to logo_url when the content is not an image", func(t *testing.T) {
		srv := imageServer(t, "application/json", []byte(`{"not":"an image"}`))
		defer srv.Close()

		h := New(Config{PortalLogo: srv.URL + "/logo.json"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{}))
		if m["logo_url"] != srv.URL+"/logo.json" {
			t.Errorf("logo_url = %v, want the configured URL", m["logo_url"])
		}
		if m["logo_svg"] != nil {
			t.Errorf("logo_svg = %v, want nil", m["logo_svg"])
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

	// Both built-in apps are injected from the same Handle, and one
	// misconfiguration is one warning, so the fetch happens once.
	t.Run("fetches once across both app configs", func(t *testing.T) {
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits++
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(svgContent))
		}))
		defer srv.Close()

		h := New(Config{PortalLogo: srv.URL + "/logo.svg"})
		first := mustMap(t, h.InjectPortalLogo(map[string]any{}))
		second := mustMap(t, h.InjectPortalLogo(map[string]any{}))
		if first["logo_svg"] != svgContent || second["logo_svg"] != svgContent {
			t.Errorf("both app configs must carry the logo: %v, %v", first["logo_svg"], second["logo_svg"])
		}
		if hits != 1 {
			t.Errorf("server saw %d requests, want 1", hits)
		}
	})

	// A nil map inside a non-nil any is what the caller hands in when the
	// operator declares no platform-info app config: writing the brand into it
	// panicked.
	t.Run("takes a typed nil map", func(t *testing.T) {
		var typedNil map[string]any
		h := New(Config{BrandName: "ACME", BrandURL: "https://acme.example.com"})
		m := mustMap(t, h.InjectPortalLogo(any(typedNil)))
		if m["brand_name"] != "ACME" {
			t.Errorf("brand_name = %v, want %q", m["brand_name"], "ACME")
		}
	})

	// A path this server serves is a logo the pages render and an app on
	// another origin cannot: nothing is fetched, and nothing unusable is
	// written into the app config.
	t.Run("writes no logo the app could not load", func(t *testing.T) {
		h := New(Config{PortalLogo: "/assets/logo.png"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{}))
		if _, ok := m["logo_url"]; ok {
			t.Errorf("logo_url = %v, want absent for a same-origin path", m["logo_url"])
		}
		if _, ok := m["logo_svg"]; ok {
			t.Errorf("logo_svg = %v, want absent for a same-origin path", m["logo_svg"])
		}
		if got := h.BrandLogoHTML(); got != `<img src="/assets/logo.png" alt="">` {
			t.Errorf("BrandLogoHTML() = %q, want the page to link the path", got)
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

// TestBrandMarkup covers what the platform's own HTML pages render: an <img>
// element at the configured URL, resolved from config with no network call at
// all. The pages are served from the platform's origin under a policy that
// admits the image host, so there is nothing to inline (#1500).
func TestBrandMarkup(t *testing.T) {
	t.Run("links the portal logo", func(t *testing.T) {
		h := New(Config{PortalLogo: "https://cdn.example.com/logo.png", BrandName: "ACME"})
		want := `<img src="https://cdn.example.com/logo.png" alt="ACME">`
		if got := h.BrandLogoHTML(); got != want {
			t.Errorf("BrandLogoHTML() = %q, want %q", got, want)
		}
	})

	t.Run("links the implementor logo", func(t *testing.T) {
		h := New(Config{ImplementorLogo: "https://cdn.example.com/badge.png", ImplementorName: "ACME Corp"})
		want := `<img src="https://cdn.example.com/badge.png" alt="ACME Corp">`
		if got := h.ImplementorLogoHTML(); got != want {
			t.Errorf("ImplementorLogoHTML() = %q, want %q", got, want)
		}
	})

	// The reported deployment names a logo and no implementor name; the image
	// is then the whole block and carries no invented alt text.
	t.Run("renders an empty alt when nothing names the logo", func(t *testing.T) {
		h := New(Config{ImplementorLogo: "https://cdn.example.com/badge.png"})
		want := `<img src="https://cdn.example.com/badge.png" alt="">`
		if got := h.ImplementorLogoHTML(); got != want {
			t.Errorf("ImplementorLogoHTML() = %q, want %q", got, want)
		}
	})

	// The slot inserts the result unescaped, so neither operator value may be
	// able to close an attribute.
	t.Run("escapes the URL and the alt text", func(t *testing.T) {
		h := New(Config{
			PortalLogo: `https://cdn.example.com/a.png" onerror="x`,
			BrandName:  `ACME" onload="y`,
		})
		got := h.BrandLogoHTML()
		if strings.Contains(got, `onerror="x"`) || strings.Contains(got, `onload="y"`) {
			t.Errorf("BrandLogoHTML() = %q, want the quotes escaped", got)
		}
		if strings.Count(got, "&#34;") != 4 {
			t.Errorf("BrandLogoHTML() = %q, want every quote escaped", got)
		}
	})

	t.Run("an app config's inline SVG renders as itself", func(t *testing.T) {
		h := New(Config{PortalLogo: "https://cdn.example.com/logo.png"})
		_ = h.InjectPortalLogo(map[string]any{"logo_svg": "<svg id=\"operator\"/>"})
		if got := h.BrandLogoHTML(); got != `<svg id="operator"/>` {
			t.Errorf("BrandLogoHTML() = %q, want the operator's inline SVG", got)
		}
	})

	t.Run("returns empty with nothing configured", func(t *testing.T) {
		h := New(Config{})
		if got := h.BrandLogoHTML(); got != "" {
			t.Errorf("BrandLogoHTML() = %q, want empty", got)
		}
		if got := h.ImplementorLogoHTML(); got != "" {
			t.Errorf("ImplementorLogoHTML() = %q, want empty", got)
		}
	})

	t.Run("nil handle returns empty", func(t *testing.T) {
		var h *Handle
		if got := h.BrandLogoHTML(); got != "" {
			t.Errorf("nil BrandLogoHTML() = %q, want empty", got)
		}
		if got := h.ImplementorLogoHTML(); got != "" {
			t.Errorf("nil ImplementorLogoHTML() = %q, want empty", got)
		}
	})
}

// A page that renders a linked logo has to admit its origin, and a deployment
// that configures none must not widen its policy at all.
func TestImageSources(t *testing.T) {
	tests := []struct {
		name string
		urls []string
		want []string
	}{
		{"none configured", nil, nil},
		{"empty strings", []string{"", ""}, nil},
		{
			"both logos on one host",
			[]string{"https://cdn.example.com/a.png", "https://cdn.example.com/b.png"},
			[]string{"https://cdn.example.com"},
		},
		{
			"two hosts",
			[]string{"https://cdn.example.com/a.png", "http://img.example.net:8080/b.png"},
			[]string{"https://cdn.example.com", "http://img.example.net:8080"},
		},
		{
			// A path the platform serves itself needs 'self': neither policy
			// lists it, so a same-origin logo would be blocked without it.
			"a same-origin path names self",
			[]string{"/assets/logo.png", "assets/badge.png"},
			[]string{"'self'"},
		},
		{
			// A scheme-relative URL loads over the page's own scheme; the bare
			// host is what a CSP source list names for it.
			"a scheme-relative URL names its host",
			[]string{"//cdn.example.com/logo.png"},
			[]string{"cdn.example.com"},
		},
		{
			"a setting that names no loadable image contributes nothing",
			[]string{"ftp://files.example.com/a.png", "https://%zz/bad", "data:image/png;base64,AAAA"},
			nil,
		},
		{
			"a remote logo beside a local one names both",
			[]string{"https://cdn.example.com/a.png", "/assets/badge.png"},
			[]string{"https://cdn.example.com", "'self'"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ImageSources(tt.urls...); !slices.Equal(got, tt.want) {
				t.Errorf("ImageSources(%v) = %v, want %v", tt.urls, got, tt.want)
			}
		})
	}
}

func TestResolveLogo(t *testing.T) {
	svgContent := `<svg viewBox="0 0 40 40"><circle cx="20" cy="20" r="10"/></svg>`

	t.Run("returns SVG content as its own markup", func(t *testing.T) {
		srv := imageServer(t, "image/svg+xml", []byte(svgContent))
		defer srv.Close()

		got, err := resolveLogo(srv.URL + "/logo.svg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.svg != svgContent {
			t.Errorf("svg = %q, want %q", got.svg, svgContent)
		}
		if got.dataURI != "" {
			t.Errorf("dataURI = %q, want empty for a vector logo", got.dataURI)
		}
	})

	t.Run("returns a raster image as a data URI", func(t *testing.T) {
		srv := imageServer(t, "image/gif", gifBytes)
		defer srv.Close()

		got, err := resolveLogo(srv.URL + "/logo.gif")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := "data:image/gif;base64," + base64.StdEncoding.EncodeToString(gifBytes); got.dataURI != want {
			t.Errorf("dataURI = %q, want %q", got.dataURI, want)
		}
		if got.svg != "" {
			t.Errorf("svg = %q, want empty for a raster logo", got.svg)
		}
	})

	// pkg/contenttype may not name SVG from content, so a vector logo a bucket
	// labels generically arrives as XML or plain text; the root element is what
	// identifies it (#1500).
	t.Run("accepts an SVG a server declared generically", func(t *testing.T) {
		bodies := map[string]string{
			"bare root":       svgContent,
			"xml declaration": `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + svgContent,
			"doctype":         `<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">` + svgContent,
			// The declarations inside an internal subset carry '>' of their
			// own, so the doctype ends at the closing bracket.
			"doctype with an internal subset": `<!DOCTYPE svg [<!ENTITY ns "http://example.com"> <!ENTITY co "ACME">]>` + svgContent,
			"leading comment":                 "<!-- Generator: Acme Designer -->\n" + svgContent,
			"uppercase root":                  `<SVG viewBox="0 0 40 40"></SVG>`,
		}
		for name, body := range bodies {
			t.Run(name, func(t *testing.T) {
				for _, declared := range []string{"application/octet-stream", "text/plain", ""} {
					srv := imageServer(t, declared, []byte(body))
					got, err := resolveLogo(srv.URL + "/logo.svg")
					srv.Close()
					if err != nil {
						t.Fatalf("declared %q: unexpected error: %v", declared, err)
					}
					if got.svg != body {
						t.Errorf("declared %q: svg = %q, want %q", declared, got.svg, body)
					}
				}
			})
		}
	})

	// Only the first element decides what the document is: a page that happens
	// to carry an inline <svg> is not a logo.
	t.Run("rejects a document whose root element is not svg", func(t *testing.T) {
		bodies := map[string]string{
			"html page with an inline svg": `<!DOCTYPE html><html><body><svg viewBox="0 0 1 1"></svg></body></html>`,
			"unterminated comment":         "<!-- " + svgContent,
			"unterminated declaration":     `<?xml version="1.0"` + svgContent,
			"json":                         `{"svg":"<svg/>"}`,
			// A root element past the sniff window is not found, and the
			// document is not accepted on the strength of a long prolog.
			"root element past the sniff window": "<!-- " + strings.Repeat("x", svgSniffLen) + " -->" + svgContent,
			// A doctype that opens a subset it never closes ends the scan
			// rather than running past the end of the document.
			"unterminated internal subset": `<!DOCTYPE svg [<!ENTITY co "ACME"`,
		}
		for name, body := range bodies {
			t.Run(name, func(t *testing.T) {
				srv := imageServer(t, "application/octet-stream", []byte(body))
				defer srv.Close()
				if _, err := resolveLogo(srv.URL + "/logo.bin"); err == nil {
					t.Fatal("expected an error for a document that is not an SVG")
				}
			})
		}
	})

	t.Run("rejects content that is not an image", func(t *testing.T) {
		srv := imageServer(t, "text/html", []byte("<html><body>not a logo</body></html>"))
		defer srv.Close()

		if _, err := resolveLogo(srv.URL + "/logo.html"); err == nil {
			t.Fatal("expected an error for non-image content")
		}
	})

	// An oversized logo is refused rather than truncated: half a PNG renders as
	// a broken image, which is harder to diagnose than no logo plus a warning.
	t.Run("rejects content larger than the cap", func(t *testing.T) {
		body := append(append([]byte{}, pngBytes...), make([]byte, logoMaxBytes)...)
		srv := imageServer(t, "image/png", body)
		defer srv.Close()

		_, err := resolveLogo(srv.URL + "/huge.png")
		if err == nil {
			t.Fatal("expected an error for an oversized logo")
		}
		if !strings.Contains(err.Error(), "larger than") {
			t.Errorf("err = %v, want it to name the size limit", err)
		}
	})

	// One byte under the cap is still a logo: the refusal must not fire on the
	// boundary it is allowed to serve.
	t.Run("accepts content at the cap", func(t *testing.T) {
		body := append(append([]byte{}, pngBytes...), make([]byte, logoMaxBytes-len(pngBytes))...)
		srv := imageServer(t, "image/png", body)
		defer srv.Close()

		if _, err := resolveLogo(srv.URL + "/big.png"); err != nil {
			t.Fatalf("unexpected error at exactly the cap: %v", err)
		}
	})

	t.Run("rejects non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		if _, err := resolveLogo(srv.URL + "/missing.svg"); err == nil {
			t.Fatal("expected error for 404")
		}
	})

	t.Run("rejects non-HTTP scheme", func(t *testing.T) {
		if _, err := resolveLogo("ftp://example.com/logo.svg"); err == nil {
			t.Fatal("expected error for non-HTTP scheme")
		}
	})

	// A response that ends early is a read failure, not a small logo.
	t.Run("rejects a body that ends before it is complete", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", "4096")
			_, _ = w.Write(pngBytes)
		}))
		defer srv.Close()

		if _, err := resolveLogo(srv.URL + "/short.png"); err == nil {
			t.Fatal("expected an error for a truncated body")
		}
	})

	t.Run("handles SVG with charset in content type", func(t *testing.T) {
		srv := imageServer(t, "image/svg+xml; charset=utf-8", []byte(svgContent))
		defer srv.Close()

		got, err := resolveLogo(srv.URL + "/logo.svg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.svg != svgContent {
			t.Errorf("svg = %q, want %q", got.svg, svgContent)
		}
	})
}

// TestLogoFilename covers the name detection resolves a wrong-but-specific
// declaration against.
func TestLogoFilename(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://cdn.example.com/brand/badge.png", "badge.png"},
		{"https://cdn.example.com/badge.png?v=2", "badge.png"},
		{"https://cdn.example.com/", ""},
		{"https://cdn.example.com", ""},
		// An unparsable URL names no file; the fetch below refuses it anyway.
		{"https://cdn.example.com/\x7f.png", ""},
	}
	for _, tt := range tests {
		if got := logoFilename(tt.url); got != tt.want {
			t.Errorf("logoFilename(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

// A server that declares a specific type the extension and the bytes both
// contradict loses to those two, which is what makes a logo usable when the
// machine serving it labels PNGs as spreadsheets.
func TestResolveLogoDeclarationContradictedByNameAndContent(t *testing.T) {
	srv := imageServer(t, "application/vnd.ms-excel", pngBytes)
	defer srv.Close()

	got, err := resolveLogo(srv.URL + "/badge.png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got.dataURI, "data:image/png;base64,") {
		t.Errorf("dataURI = %q, want a PNG data URI", got.dataURI)
	}
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

	t.Run("returns the configured portal brand URL", func(t *testing.T) {
		h := New(Config{BrandURL: "https://portal.example.com"})
		if got := h.BrandURL(); got != "https://portal.example.com" {
			t.Errorf("BrandURL() = %q, want %q", got, "https://portal.example.com")
		}
	})

	// portal.brand_url is the operator's first-class brand home; the app
	// config only backfills it, so a platform-info app that names a different
	// brand_url must not silently replace what the portal block configured.
	t.Run("configured brand URL survives InjectPortalLogo", func(t *testing.T) {
		h := New(Config{BrandURL: "https://portal.example.com"})
		_ = h.InjectPortalLogo(map[string]any{"brand_url": "https://app.example.com"})
		if got := h.BrandURL(); got != "https://portal.example.com" {
			t.Errorf("BrandURL() = %q, want %q", got, "https://portal.example.com")
		}
	})
}

// The portal block is documented as needing no MCP Apps configuration, so a
// deployment that sets only portal.brand_* must still render a branded app:
// the brand has to travel INTO the app config, not just out of it.
func TestInjectPortalLogoWritesBrandIntoAppConfig(t *testing.T) {
	t.Run("writes brand into an app config that names none", func(t *testing.T) {
		h := New(Config{BrandName: "ACME", BrandURL: "https://acme.example.com"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{}))
		if m["brand_name"] != "ACME" {
			t.Errorf("brand_name = %v, want %q", m["brand_name"], "ACME")
		}
		if m["brand_url"] != "https://acme.example.com" {
			t.Errorf("brand_url = %v, want %q", m["brand_url"], "https://acme.example.com")
		}
	})

	t.Run("writes brand into a nil app config", func(t *testing.T) {
		h := New(Config{BrandName: "ACME"})
		m := mustMap(t, h.InjectPortalLogo(nil))
		if m["brand_name"] != "ACME" {
			t.Errorf("brand_name = %v, want %q", m["brand_name"], "ACME")
		}
	})

	// An app config that names its own brand is the operator being specific
	// about that app; the portal-wide brand must not overwrite it.
	t.Run("leaves an app config that names its own brand", func(t *testing.T) {
		h := New(Config{BrandName: "ACME", BrandURL: "https://acme.example.com"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{
			"brand_name": "ACME Labs",
			"brand_url":  "https://labs.acme.example.com",
		}))
		if m["brand_name"] != "ACME Labs" {
			t.Errorf("brand_name = %v, want %q", m["brand_name"], "ACME Labs")
		}
		if m["brand_url"] != "https://labs.acme.example.com" {
			t.Errorf("brand_url = %v, want %q", m["brand_url"], "https://labs.acme.example.com")
		}
	})

	t.Run("writes nothing when no brand is configured", func(t *testing.T) {
		h := New(Config{})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{}))
		if _, ok := m["brand_name"]; ok {
			t.Errorf("brand_name present (%v), want absent", m["brand_name"])
		}
		if _, ok := m["brand_url"]; ok {
			t.Errorf("brand_url present (%v), want absent", m["brand_url"])
		}
	})

	// Both built-in apps are injected from the same Handle, and the prompt
	// browser may share platform-info's map: a second pass must be a no-op.
	t.Run("is idempotent across repeated injection", func(t *testing.T) {
		h := New(Config{BrandName: "ACME"})
		m := mustMap(t, h.InjectPortalLogo(map[string]any{}))
		m = mustMap(t, h.InjectPortalLogo(m))
		if m["brand_name"] != "ACME" {
			t.Errorf("brand_name = %v, want %q", m["brand_name"], "ACME")
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
		if got := h.BrandLogoHTML(); got != "<svg/>" {
			t.Errorf("BrandLogoHTML() = %q, want %q", got, "<svg/>")
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
