package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// screenshotManifests are the portal's route manifest files, which hold every
// page the portal has as a concrete path, ids filled in.
const screenshotManifests = "../../ui/e2e/screenshots/route-*.ts"

var manifestPath = regexp.MustCompile(`path:\s*"(/portal/[^"]*)"`)

// manifestRoutes returns each manifest path as the SPA handler sees it: the
// /portal prefix, query and fragment removed.
func manifestRoutes(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(screenshotManifests)
	require.NoError(t, err)
	require.NotEmpty(t, files, "no route manifest under %s", screenshotManifests)

	seen := map[string]bool{}
	var routes []string
	for _, f := range files {
		src, err := os.ReadFile(f) //nolint:gosec // G304: a fixed glob in this repository
		require.NoError(t, err)
		for _, m := range manifestPath.FindAllStringSubmatch(string(src), -1) {
			route := strings.TrimPrefix(m[1], "/portal")
			route, _, _ = strings.Cut(route, "#")
			route, _, _ = strings.Cut(route, "?")
			if route == "" {
				route = "/"
			}
			if !seen[route] {
				seen[route] = true
				routes = append(routes, route)
			}
		}
	}
	return routes
}

// Every page the portal has is served index.html by the Go handler, whatever
// prefix it shares with the build's files. Playwright's interactive suite runs
// on the Vite dev server and never reaches this handler, which is how the
// asset viewer answered 404 in v1.147.0 with every suite green (#2205, #2206).
func TestSPAHandler_EveryPortalRouteServesIndexHTML(t *testing.T) {
	h := newSPAHandler(testFS())
	routes := manifestRoutes(t)
	require.Contains(t, routes, "/assets/ast-001", "the manifest no longer lists the asset viewer; this test would stop covering #2205")

	for _, route := range routes {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, route, http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, route)
		assert.Contains(t, rec.Body.String(), "<html>SPA</html>", route)
	}
}

// buildAssetsDir mirrors Vite's build.assetsDir. The portal's config leaves it
// at Vite's default; a config that moves it must move the constant with it.
func TestBuildAssetsDir_MatchesViteConfig(t *testing.T) {
	src, err := os.ReadFile("../../ui/vite.config.ts")
	require.NoError(t, err)
	m := regexp.MustCompile(`assetsDir:\s*["']([^"']+)["']`).FindStringSubmatch(string(src))
	want := "assets"
	if m != nil {
		want = strings.Trim(m[1], "/")
	}
	assert.Equal(t, want+"/", buildAssetsDir)
}
