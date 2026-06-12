package portal

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSMEWorklist(t *testing.T) {
	threads := &mockThreadStore{listResult: []ThreadWithMeta{
		{Thread: Thread{ID: "thr_1", TargetType: targetTypeAsset, AssetID: "asset_1", ValidationState: ValidationStatePending}},
	}, listTotal: 1}
	h := newThreadHandlerFull(threads, &mockAssetStore{}, &mockShareStore{}, nil, nil, &User{UserID: "sme1", Email: "sme1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/sme", nil)
	require.Equal(t, http.StatusOK, w.Code)
	// The filter scopes to threads the caller authored (by id or email) that are
	// awaiting validation, matching how respond-permission resolves the author.
	assert.Equal(t, "sme1", threads.lastListFilter.AuthorID)
	assert.Equal(t, "sme1@example.com", threads.lastListFilter.AuthorEmail)
	assert.Equal(t, ValidationStatePending, threads.lastListFilter.ValidationState)

	var resp paginatedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Total)
}

func TestSMEWorklistUnauthorized(t *testing.T) {
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, nil, nil, nil)
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/sme", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPractitionerWorklist(t *testing.T) {
	threads := &mockThreadStore{listResult: []ThreadWithMeta{
		{Thread: Thread{ID: "thr_1", TargetType: targetTypeAsset, AssetID: "asset_owned", RequiresResolution: true}},
	}, listTotal: 1}
	assets := &mockAssetStore{listRes: []Asset{{ID: "asset_owned", OwnerID: "u1"}}}
	shares := &mockShareStore{sharedWithRes: []SharedAsset{
		{Asset: Asset{ID: "asset_edit"}, Permission: PermissionEditor},
		{Asset: Asset{ID: "asset_view"}, Permission: PermissionViewer}, // must be excluded
	}}
	colls := &mockCollectionStore{listResult: []Collection{{ID: "col_owned", OwnerID: "u1"}}}
	h := newThreadHandlerFull(threads, assets, shares, colls, nil, &User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/practitioner", nil)
	require.Equal(t, http.StatusOK, w.Code)

	f := threads.lastListFilter
	// Owned + editor-shared assets are included; viewer-shared is not.
	assert.ElementsMatch(t, []string{"asset_owned", "asset_edit"}, f.TargetAssetIDs)
	assert.Equal(t, []string{"col_owned"}, f.TargetCollectionIDs)
	assert.Equal(t, ThreadStatusOpen, f.Status)
	require.NotNil(t, f.RequiresResolution)
	assert.True(t, *f.RequiresResolution)
}

func TestSMEWorklistStoreError(t *testing.T) {
	threads := &mockThreadStore{listErr: assert.AnError}
	h := newThreadHandlerFull(threads, &mockAssetStore{}, &mockShareStore{}, nil, nil, &User{UserID: "sme1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/sme", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPractitionerWorklistTargetError(t *testing.T) {
	assets := &mockAssetStore{listErr: assert.AnError}
	h := newThreadHandlerFull(&mockThreadStore{}, assets, &mockShareStore{}, &mockCollectionStore{}, nil, &User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/practitioner", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPractitionerWorklistUnauthorized(t *testing.T) {
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, &mockCollectionStore{}, nil, nil)
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/practitioner", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPractitionerWorklistNoArtifacts(t *testing.T) {
	threads := &mockThreadStore{}
	h := newThreadHandlerFull(threads, &mockAssetStore{}, &mockShareStore{}, &mockCollectionStore{}, nil, &User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/worklist/practitioner", nil)
	require.Equal(t, http.StatusOK, w.Code)
	// With no owned/editable artifacts the store is NOT queried unscoped.
	assert.Empty(t, threads.lastListFilter.TargetAssetIDs)
	assert.Empty(t, threads.lastListFilter.TargetCollectionIDs)

	var resp paginatedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Total)
}
