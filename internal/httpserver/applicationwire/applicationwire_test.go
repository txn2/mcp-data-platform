package applicationwire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application/appwire"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

func TestMount_WithoutAPlatform(t *testing.T) {
	assert.NotPanics(t, func() { Mount(http.NewServeMux(), nil, nil, nil, nil) })
}

func TestActor(t *testing.T) {
	actor := Actor(func(roles []string) bool { return len(roles) > 0 && roles[0] == "admin" })
	r := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	assert.Nil(t, actor(r), "signed out")
	r = r.WithContext(portal.ContextWithUser(r.Context(), &portal.User{Email: "a@b", Roles: []string{"admin"}}))
	got := actor(r)
	require.NotNil(t, got)
	assert.True(t, got.Admin)
	assert.Equal(t, "a@b", got.Email)
	assert.Equal(t, []string{"admin"}, got.Roles, "an import made through the portal runs under the caller's own roles")
}

func TestRegister_MountsTheRoutes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mux := http.NewServeMux()
	register(mux, func(h http.Handler) http.Handler { return h }, appwire.Deps{}, routes{})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/applications", http.NoBody))
	assert.Equal(t, http.StatusNotFound, w.Code, "no database, no routes")

	register(mux, func(h http.Handler) http.Handler { return h }, appwire.Deps{DB: db}, routes{isAdmin: func([]string) bool { return false }})
	mock.ExpectQuery("FROM applications ORDER BY name").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	r := httptest.NewRequestWithContext(portal.ContextWithUser(context.Background(), &portal.User{Email: "a@b"}),
		"GET", "/api/v1/portal/applications", http.NoBody)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	assert.NotEqual(t, http.StatusNotFound, w.Code, "the listing route is mounted")
}

// TestAssetViewer reads the portal caller off the context and answers by the
// portal's own view rule: the owner opens an asset, a stranger does not, and
// a signed-out request or a missing asset opens nothing (#2527).
type shareStore struct {
	portaldomain.ShareStore
	direct []portaldomain.Share
}

func (s shareStore) ListByAsset(context.Context, string) ([]portaldomain.Share, error) {
	return s.direct, nil
}

func (shareStore) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

func TestAssetViewer(t *testing.T) {
	view := assetViewer(nil)
	asset := &portaldomain.Asset{ID: "as1", OwnerEmail: "jane@example.com", OwnerID: "u-jane"}
	ctx := context.Background()
	assert.False(t, view(ctx, asset), "signed out")
	jane := portal.ContextWithUser(ctx, &portal.User{UserID: "u-jane", Email: "jane@example.com"})
	assert.True(t, view(jane, asset), "the owner opens it")
	assert.False(t, view(jane, nil))
	joe := portal.ContextWithUser(ctx, &portal.User{UserID: "u-joe", Email: "joe@example.com"})
	assert.False(t, view(joe, asset), "a person it is not shared with does not")

	shared := assetViewer(shareStore{direct: []portaldomain.Share{{
		AssetID: "as1", SharedWithEmail: "joe@example.com", Permission: portaldomain.PermissionViewer,
	}}})
	assert.True(t, shared(joe, asset), "a person it is shared with opens it")
	assert.False(t, assetViewer(shareStore{})(joe, asset))
}
