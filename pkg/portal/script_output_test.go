package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptOutput is what a managed script's portal output looks like on disk: the
// script principal as its owner id, and the address of the person who owns the
// script as owner_email (#1551).
func scriptOutput() *Asset {
	return &Asset{
		ID: "a-script", OwnerID: "script:weekly-revenue", OwnerEmail: "Alice@Example.com",
		Name: "Weekly revenue", ContentType: "text/csv", CurrentVersion: 1,
	}
}

func scriptOwner() *User { return &User{UserID: "u-alice", Email: "alice@example.com"} }

func stranger() *User { return &User{UserID: "u-bob", Email: "bob@example.com"} }

// The owner's Assets page asks for their own assets, and the scope it sends
// carries both identifiers, so the store can match the address a run stamped.
func TestListAssetsScopesOnBothIdentifiers(t *testing.T) {
	store := &mockAssetStore{listRes: []Asset{*scriptOutput()}, listTotal: 1}
	h := NewHandler(Deps{AssetStore: store, ShareStore: &mockShareStore{}},
		testAuthMiddleware(scriptOwner()))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, store.lastFilter)
	assert.Equal(t, "u-alice", store.lastFilter.Owner.UserID)
	assert.Equal(t, "alice@example.com", store.lastFilter.Owner.Arms().Email)
}

// Opening a run's output reports the person it was produced for as its owner,
// which is what turns on the rename, share and delete affordances in the portal.
func TestGetAssetReportsTheScriptOwnerAsOwner(t *testing.T) {
	store := &mockAssetStore{getAsset: scriptOutput()}
	h := NewHandler(Deps{AssetStore: store, ShareStore: &mockShareStore{}},
		testAuthMiddleware(scriptOwner()))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/assets/a-script", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		IsOwner bool `json:"is_owner"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.IsOwner)
}

// A second authenticated person who is neither the owner nor a share recipient
// is refused, and the refusal does not say a script produced it.
func TestGetAssetRefusesAnotherPersonsScriptOutput(t *testing.T) {
	store := &mockAssetStore{getAsset: scriptOutput()}
	h := NewHandler(Deps{AssetStore: store, ShareStore: &mockShareStore{}},
		testAuthMiddleware(stranger()))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/assets/a-script", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NotContains(t, w.Body.String(), "script")
}

// Renaming it needs no administrator.
func TestUpdateAssetByTheScriptOwner(t *testing.T) {
	store := &mockAssetStore{getAsset: scriptOutput()}
	h := NewHandler(Deps{AssetStore: store, ShareStore: &mockShareStore{}},
		testAuthMiddleware(scriptOwner()))

	body := `{"name":"Weekly revenue (Q3)"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/portal/assets/a-script", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, store.lastUpdate)
	require.NotNil(t, store.lastUpdate.Name)
	assert.Equal(t, "Weekly revenue (Q3)", *store.lastUpdate.Name)
}

// An authenticated caller carrying neither identifier owns nothing. The empty
// owner scope means every owner at the store, which is the administrator's
// listing and not these routes', so both answer with an empty page rather than
// passing it down.
func TestOwnerScopedListingsRefuseAnUnidentifiedCaller(t *testing.T) {
	for _, path := range []string{
		"/api/v1/portal/assets",
		"/api/v1/portal/thumbnails/pending",
	} {
		t.Run(path, func(t *testing.T) {
			store := &mockAssetStore{listRes: []Asset{*scriptOutput()}, listTotal: 1}
			h := NewHandler(Deps{AssetStore: store, ShareStore: &mockShareStore{}},
				testAuthMiddleware(&User{}))

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			var resp struct {
				Data  []Asset `json:"data"`
				Total int     `json:"total"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Empty(t, resp.Data)
			assert.Zero(t, resp.Total)
			assert.Nil(t, store.lastFilter, "the store must not be asked for an unscoped page")
		})
	}
}
