//go:build integration

package httpserver

// Real-Postgres tests for the two composition-root mount functions in
// dbmounts.go. Their bodies only run once the platform has constructed its
// Postgres-backed portal/resource stores, which the unit suite cannot provide,
// so these lines are excluded from the coverage gates and exercised here
// instead. Run under `make test-realdb`.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// newRealDBPlatform builds a platform wired to a live Postgres with the portal
// and managed-resources subsystems enabled, so PortalAssetStore/ShareStore and
// ResourceStore are non-nil and the mount happy paths run.
func newRealDBPlatform(t *testing.T) *platform.Platform {
	t.Helper()
	_, dsn := testdb.NewWithDSN(t)
	p, err := platform.New(platform.WithConfig(&platform.Config{
		Server:   platform.ServerConfig{Name: "test"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Storage:  platform.StorageConfig{Provider: "noop"},
		Database: platform.DatabaseConfig{DSN: dsn},
		Portal:   platform.PortalConfig{Enabled: ptr(true)},
		Resources: platform.ResourcesConfig{
			Managed: platform.ManagedResourcesCfg{Enabled: ptr(true)},
		},
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func ptr[T any](v T) *T { return &v }

// TestMountPortalAPI_RealDB proves the portal REST routes are registered when
// the platform has live portal stores. An unauthenticated request must reach
// the handler (401), not miss the route (404).
func TestMountPortalAPI_RealDB(t *testing.T) {
	p := newRealDBPlatform(t)
	require.NotNil(t, p.PortalAssetStore(), "portal asset store must be wired from the real DB")
	require.NotNil(t, p.PortalShareStore(), "portal share store must be wired from the real DB")

	mux := http.NewServeMux()
	require.NoError(t, mountPortalAPI(mux, p, buildNotifications(p), true))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.NotEqual(t, http.StatusNotFound, w.Code, "portal route should be registered")
}

// TestMountScriptPortalAPI_RealDB proves the portal script read routes are
// registered when the deployment has a database to keep scripts in. They are
// mounted from mountPortalAPI, so this is the assembly the unit suite cannot
// reach: an unauthenticated request must be refused by the handler (401),
// never miss the route (404).
func TestMountScriptPortalAPI_RealDB(t *testing.T) {
	p := newRealDBPlatform(t)

	mux := http.NewServeMux()
	require.NoError(t, mountPortalAPI(mux, p, buildNotifications(p), true))

	// The pattern the mux matched is what proves these routes exist: every
	// /api/v1/portal/ path is answered by the portal subtree handler otherwise,
	// and an unauthenticated request is refused by the shared auth wrapper
	// either way, so a status code cannot tell the two apart.
	for path, want := range map[string]string{
		"/api/v1/portal/scripts":                     "GET /api/v1/portal/scripts",
		"/api/v1/portal/scripts/script_1":            "GET /api/v1/portal/scripts/{id}",
		"/api/v1/portal/scripts/script_1/versions":   "GET /api/v1/portal/scripts/{id}/versions",
		"/api/v1/portal/scripts/script_1/runs":       "GET /api/v1/portal/scripts/{id}/runs",
		"/api/v1/portal/scripts/script_1/runs/run_1": "GET /api/v1/portal/scripts/{id}/runs/{runID}",
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
		_, pattern := mux.Handler(req)
		require.Equal(t, want, pattern, "portal script route should be registered: %s", path)
	}
}

// TestMountResourcesAPI_RealDB proves the managed-resources REST routes are
// registered when the platform has a live resource store.
func TestMountResourcesAPI_RealDB(t *testing.T) {
	p := newRealDBPlatform(t)
	require.NotNil(t, p.ResourceStore(), "resource store must be wired from the real DB")

	mux := http.NewServeMux()
	mountResourcesAPI(mux, p)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.NotEqual(t, http.StatusNotFound, w.Code, "resources route should be registered")
}

// TestBuildNotifications_RealDB proves the notification substrate assembles
// against a live database: the handle exists, its stores round-trip real
// rows, and Start/Stop run cleanly.
func TestBuildNotifications_RealDB(t *testing.T) {
	p := newRealDBPlatform(t)

	h := buildNotifications(p)
	require.NotNil(t, h, "database-backed platform must yield a notification handle")
	require.NotNil(t, h.Enqueuer())
	require.NotNil(t, h.Prefs())
	require.NotNil(t, h.Settings())

	ctx := context.Background()
	prefs, err := h.Prefs().Get(ctx, "someone@example.com")
	require.NoError(t, err)
	require.Equal(t, "immediate", prefs.Mode, "absent row must yield the immediate default")

	h.Start(ctx)
	h.Stop()
}

// TestBuildReviewAlert_RealDB proves the scheduled review-queue check (#803)
// assembles against a live database: the checker exists, one Check runs
// cleanly against the real knowledge and settings tables, and Start/Stop are
// clean. An unconfigured deployment has no recipients, so the check reads the
// settings and stops there -- which is exactly the default-path assertion.
func TestBuildReviewAlert_RealDB(t *testing.T) {
	p := newRealDBPlatform(t)

	checker := buildReviewAlert(p, buildNotifications(p))
	require.NotNil(t, checker, "database-backed platform must yield a review-queue checker")
	require.NotNil(t, reviewAlertSettings(p, reviewalert.KnowledgeTarget()))

	ctx := context.Background()
	require.NoError(t, checker.Check(ctx))

	checker.Start(ctx)
	checker.Stop()
}
