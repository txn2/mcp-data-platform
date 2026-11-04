package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serveIcon(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	// No cookie, no Authorization: the icon is read signed out (#2460).
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/favicon.ico", http.NoBody))
	return w
}

func TestFavicon_RedirectsToTheConfiguredLogo(t *testing.T) {
	w := serveIcon(t, faviconHandler("https://acme.example.com/mark.svg", []byte("<svg/>")))
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if got := w.Header().Get("Location"); got != "https://acme.example.com/mark.svg" {
		t.Errorf("Location = %q", got)
	}
}

func TestFavicon_ServesTheBundledMarkWithNoLogo(t *testing.T) {
	w := serveIcon(t, faviconHandler("", []byte("<svg/>")))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	if w.Header().Get("Cache-Control") == "" {
		t.Error("no Cache-Control: every page load would ask again")
	}
	if w.Body.String() != "<svg/>" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestFavicon_NotFoundWithNeither(t *testing.T) {
	if w := serveIcon(t, faviconHandler("", nil)); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestFavicon_ReadsTheEmbeddedMark(t *testing.T) {
	w := serveIcon(t, Favicon(""))
	if Available() {
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d with the portal built, want 200", w.Code)
		}
		return
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d with no portal build, want 404", w.Code)
	}
}

func TestMountSiteFiles(t *testing.T) {
	mux := http.NewServeMux()
	MountSiteFiles(mux, "", "https://acme.example.com/logo.svg")
	w := serveIcon(t, mux)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "https://acme.example.com/logo.svg" {
		t.Fatalf("the icon is not the first logo set: %d %q", w.Code, w.Header().Get("Location"))
	}
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/robots.txt", http.NoBody))
	if r.Code != http.StatusOK || r.Body.String() != "User-agent: *\nDisallow: /\n" {
		t.Fatalf("robots.txt = %d %q", r.Code, r.Body.String())
	}
}
