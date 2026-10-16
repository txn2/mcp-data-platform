//go:build integration

package acceptance

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Issue #2139: the portal answered every path it had no file for with
// index.html and a 200, including a hashed build file under /portal/assets/.
// During a rolling update a page from one build asks a replica on the other
// for its files, and HTML under a stylesheet's or a chunk's URL broke the page
// and could be cached as that file. These criteria fetch the portal the way a
// browser does, without a session: the build files and index.html are public.
//
// Wire forms: plain HTTP GET requests with no body and no credentials.

func get2139(t *testing.T, path string) (int, http.Header, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL()+path, http.NoBody)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("GET %s: reading the body: %v", path, err)
	}
	return res.StatusCode, res.Header, string(raw)
}

func TestIssue2139_AMissingBuildFileIsNotFound(t *testing.T) {
	for _, path := range []string{"/portal/assets/index-DOESNOTEXIST.css", "/portal/assets/AutomationsPage-DOESNOTEXIST.js"} {
		status, header, body := get2139(t, path)
		if status != http.StatusNotFound {
			t.Errorf("GET %s answered %d %s, want 404", path, status, header.Get("Content-Type"))
		}
		if strings.Contains(body, "<!doctype html>") || strings.Contains(body, "<!DOCTYPE html>") {
			t.Errorf("GET %s answered with the portal's index.html", path)
		}
	}

	// A build file the running build has is still served as itself.
	_, _, index := get2139(t, "/portal/")
	asset := regexp.MustCompile(`/portal/assets/[A-Za-z0-9_.-]+\.(?:js|css)`).FindString(index)
	if asset == "" {
		t.Fatalf("the portal's index.html names no build file under /portal/assets/: %.300s", index)
	}
	status, header, _ := get2139(t, asset)
	if status != http.StatusOK || strings.Contains(header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET %s answered %d %s, want the build file", asset, status, header.Get("Content-Type"))
	}
}

func TestIssue2139_AClientRouteGetsIndexHTMLRevalidatedOnEveryLoad(t *testing.T) {
	for _, path := range []string{"/portal/", "/portal/automations/scr_x/runs/run_y", "/portal/knowledge/catalog"} {
		status, header, body := get2139(t, path)
		if status != http.StatusOK || !strings.Contains(header.Get("Content-Type"), "text/html") {
			t.Errorf("GET %s answered %d %s, want index.html", path, status, header.Get("Content-Type"))
		}
		if !strings.Contains(body, "/portal/assets/") {
			t.Errorf("GET %s is not the portal's index.html: %.200s", path, body)
		}
		if got := header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache", path, got)
		}
	}
}
