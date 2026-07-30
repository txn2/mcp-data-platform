package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/pkcestore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// oauthShimKind is a minimal OAuthKindHandler; the shim test only needs the
// kinds map to be non-empty, which is what selects the unified surface.
type oauthShimKind struct{}

func (oauthShimKind) ParseOAuthConfig(map[string]any) (connoauth.Config, error) {
	return connoauth.Config{}, nil
}
func (oauthShimKind) AfterConnect(context.Context, string, map[string]any) error { return nil }

func oauthShimHandler(t *testing.T, kinds OAuthKindHandlers, tokens connoauth.Store) *Handler {
	t.Helper()
	pkce := pkcestore.NewMemoryStore()
	t.Cleanup(func() { _ = pkce.Close() })
	return NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{getResult: &platform.ConnectionInstance{Kind: "mcp", Name: "alpha"}},
		ConfigStore:     &mockConfigStore{mode: "database"},
		PKCEStore:       pkce,
		ConnOAuthStore:  tokens,
		OAuthKinds:      kinds,
	}, nil)
}

func routeExists(h *Handler, method, path string) bool {
	req := httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code != http.StatusNotFound
}

// TestConnectionOAuthRoutesMountedThroughAdminHandler proves
// registerConnectionOAuthRoutes reaches the connoauthapi seam and that the
// seam's unified-vs-legacy branch still sees the parent's dependencies. The
// seam's own tests build Config directly, so only this can catch the parent
// passing the wrong stores into that branch.
func TestConnectionOAuthRoutesMountedThroughAdminHandler(t *testing.T) {
	t.Parallel()
	h := oauthShimHandler(t, OAuthKindHandlers{"mcp": oauthShimKind{}}, connoauth.NewMemoryStore())

	if !routeExists(h, http.MethodGet, "/api/v1/admin/connections/oauth-health") {
		t.Error("unified surface should be mounted when tokens and kinds are wired")
	}
	// The IdP redirect must reach the public mux: behind admin auth it would
	// answer 401 to the browser coming back from the identity provider.
	if !routeExists(h, http.MethodGet, "/api/v1/admin/oauth/callback") {
		t.Error("the OAuth callback should be reachable on the public mux")
	}
}

// TestConnectionOAuthFallsBackToLegacyRoutes proves the parent's dependencies
// still select the legacy per-kind surface when the shared token store or the
// kind registry is absent, which is the mid-rollout deployment shape.
func TestConnectionOAuthFallsBackToLegacyRoutes(t *testing.T) {
	t.Parallel()
	h := oauthShimHandler(t, nil, nil)

	if routeExists(h, http.MethodGet, "/api/v1/admin/connections/oauth-health") {
		t.Error("unified surface must not mount without a shared token store and kinds")
	}
	if !routeExists(h, http.MethodPost, "/api/v1/admin/gateway/connections/alpha/oauth-start") {
		t.Error("legacy MCP gateway oauth-start should mount as the fallback")
	}
	if !routeExists(h, http.MethodPost, "/api/v1/admin/api-gateway/connections/alpha/oauth-start") {
		t.Error("legacy API gateway oauth-start should mount as the fallback")
	}
}

// TestConnectionOAuthRoutesAbsentInFileMode proves the parent forwards its
// file-config mode, so no credential-mutating route mounts.
func TestConnectionOAuthRoutesAbsentInFileMode(t *testing.T) {
	t.Parallel()
	pkce := pkcestore.NewMemoryStore()
	t.Cleanup(func() { _ = pkce.Close() })
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ConfigStore:     &mockConfigStore{mode: "file"},
		PKCEStore:       pkce,
		ConnOAuthStore:  connoauth.NewMemoryStore(),
		OAuthKinds:      OAuthKindHandlers{"mcp": oauthShimKind{}},
	}, nil)
	if routeExists(h, http.MethodGet, "/api/v1/admin/connections/oauth-health") {
		t.Error("file config mode must not expose the connection OAuth routes")
	}
}
