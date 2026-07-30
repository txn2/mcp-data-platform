package feedbackapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

func TestSMEWorklist(t *testing.T) {
	store := &mockThreadStore{listResult: []threads.ThreadWithMeta{
		{Thread: threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", ValidationState: threads.ValidationStatePending}},
	}, listTotal: 1}
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, nil, nil, &access.User{UserID: "sme1", Email: "sme1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/sme", nil)
	require.Equal(t, http.StatusOK, w.Code)
	// The filter scopes to threads the caller authored (by id or email) that are
	// awaiting validation, matching how respond-permission resolves the author.
	assert.Equal(t, "sme1", store.lastListFilter.AuthorID)
	assert.Equal(t, "sme1@example.com", store.lastListFilter.AuthorEmail)
	assert.Equal(t, threads.ValidationStatePending, store.lastListFilter.ValidationState)

	var resp pagedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Total)
}

func TestSMEWorklistUnauthorized(t *testing.T) {
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, nil, nil, nil)
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/sme", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPractitionerWorklist(t *testing.T) {
	store := &mockThreadStore{listResult: []threads.ThreadWithMeta{
		{Thread: threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_owned", RequiresResolution: true}},
	}, listTotal: 1}
	assets := &mockAssetStore{listRes: []portaldomain.Asset{{ID: "asset_owned", OwnerID: "u1"}}}
	shares := &mockShareStore{sharedWithRes: []portaldomain.SharedAsset{
		{Asset: portaldomain.Asset{ID: "asset_edit"}, Permission: portaldomain.PermissionEditor},
		{Asset: portaldomain.Asset{ID: "asset_view"}, Permission: portaldomain.PermissionViewer}, // must be excluded
	}}
	colls := &mockCollectionStore{listResult: []portaldomain.Collection{{ID: "col_owned", OwnerID: "u1"}}}
	h := newThreadHandlerFull(store, assets, shares, colls, nil, &access.User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/practitioner", nil)
	require.Equal(t, http.StatusOK, w.Code)

	f := store.lastListFilter
	// Owned + editor-shared assets are included; viewer-shared is not.
	assert.ElementsMatch(t, []string{"asset_owned", "asset_edit"}, f.TargetAssetIDs)
	assert.Equal(t, []string{"col_owned"}, f.TargetCollectionIDs)
	assert.Equal(t, threads.ThreadStatusOpen, f.Status)
	require.NotNil(t, f.RequiresResolution)
	assert.True(t, *f.RequiresResolution)
}

func TestSMEWorklistStoreError(t *testing.T) {
	store := &mockThreadStore{listErr: assert.AnError}
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, nil, nil, &access.User{UserID: "sme1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/sme", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPractitionerWorklistTargetError(t *testing.T) {
	assets := &mockAssetStore{listErr: assert.AnError}
	h := newThreadHandlerFull(&mockThreadStore{}, assets, &mockShareStore{}, &mockCollectionStore{}, nil, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/practitioner", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPractitionerWorklistUnauthorized(t *testing.T) {
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, &mockCollectionStore{}, nil, nil)
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/practitioner", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPractitionerWorklistNoArtifacts(t *testing.T) {
	store := &mockThreadStore{}
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, &mockCollectionStore{}, nil, &access.User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/practitioner", nil)
	require.Equal(t, http.StatusOK, w.Code)
	// With no owned/editable artifacts the store is NOT queried unscoped.
	assert.Empty(t, store.lastListFilter.TargetAssetIDs)
	assert.Empty(t, store.lastListFilter.TargetCollectionIDs)

	var resp pagedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Total)
}
