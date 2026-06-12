package portal

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStakeholderCount(t *testing.T) {
	// owner(1) + distinct active grantees (g1, g2); duplicate and revoked excluded.
	shares := []Share{
		{SharedWithUserID: "g1"},
		{SharedWithUserID: "g2"},
		{SharedWithUserID: "g1"},
		{SharedWithUserID: "g3", Revoked: true},
		{SharedWithEmail: ""}, // no identity: skipped
	}
	assert.Equal(t, 3, stakeholderCount("owner1", "owner1@x", shares))
	assert.Equal(t, 1, stakeholderCount("owner1", "owner1@x", nil)) // owner only
	// An owner who self-shares is not double-counted (by id or by email).
	assert.Equal(t, 1, stakeholderCount("owner1", "owner1@x", []Share{{SharedWithUserID: "owner1"}}))
	assert.Equal(t, 1, stakeholderCount("uuid-owner", "owner@x", []Share{{SharedWithEmail: "Owner@X"}}))
}

func TestAssetSignoff(t *testing.T) {
	threads := &mockThreadStore{signoffCount: 2}
	assets := &mockAssetStore{getAsset: &Asset{ID: "asset_1", OwnerID: "u1"}}
	shares := &mockShareStore{listByAsset: []Share{{SharedWithUserID: "g1"}, {SharedWithUserID: "g2"}}}
	h := newThreadHandlerFull(threads, assets, shares, nil, nil, &User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/assets/asset_1/signoff", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var resp signoffSummary
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2, resp.SignedOff)
	assert.Equal(t, 3, resp.Stakeholders) // owner + g1 + g2
}

func TestAssetSignoffClampsSignedOff(t *testing.T) {
	// More distinct approvers than stakeholders (e.g. a collection-inherited
	// viewer approved) must not over-report: signed_off is clamped to M.
	threads := &mockThreadStore{signoffCount: 5}
	assets := &mockAssetStore{getAsset: &Asset{ID: "asset_1", OwnerID: "u1"}}
	h := newThreadHandlerFull(threads, assets, &mockShareStore{}, nil, nil, &User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/assets/asset_1/signoff", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var resp signoffSummary
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Stakeholders)
	assert.Equal(t, 1, resp.SignedOff) // clamped from 5 to M=1
}

func TestAssetSignoffNotFound(t *testing.T) {
	assets := &mockAssetStore{getErr: assert.AnError}
	h := newThreadHandlerFull(&mockThreadStore{}, assets, &mockShareStore{}, nil, nil, &User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/assets/nope/signoff", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAssetSignoffViewDenied(t *testing.T) {
	assets := &mockAssetStore{getAsset: &Asset{ID: "asset_1", OwnerID: "other"}}
	h := newThreadHandlerFull(&mockThreadStore{}, assets, &mockShareStore{}, nil, nil, &User{UserID: "u1", Email: "u1@example.com"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/assets/asset_1/signoff", nil)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAssetSignoffUnauthorized(t *testing.T) {
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, nil, nil, nil)
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/assets/asset_1/signoff", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCollectionSignoff(t *testing.T) {
	threads := &mockThreadStore{signoffCount: 1}
	colls := &mockCollectionStore{getResult: &Collection{ID: "col_1", OwnerID: "u1"}}
	h := newThreadHandlerFull(threads, &mockAssetStore{}, &mockShareStore{}, colls, nil, &User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/collections/col_1/signoff", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var resp signoffSummary
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.SignedOff)
	assert.Equal(t, 1, resp.Stakeholders) // owner only (no collection shares)
}

func TestAssetSignoffCountError(t *testing.T) {
	threads := &mockThreadStore{signoffErr: assert.AnError}
	assets := &mockAssetStore{getAsset: &Asset{ID: "asset_1", OwnerID: "u1"}}
	h := newThreadHandlerFull(threads, assets, &mockShareStore{}, nil, nil, &User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/assets/asset_1/signoff", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCollectionSignoffNotFound(t *testing.T) {
	colls := &mockCollectionStore{getErr: assert.AnError}
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, colls, nil, &User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/collections/nope/signoff", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCollectionSignoffAdmin(t *testing.T) {
	threads := &mockThreadStore{signoffCount: 0}
	colls := &mockCollectionStore{getResult: &Collection{ID: "col_1", OwnerID: "other"}}
	h := newThreadHandlerFull(threads, &mockAssetStore{}, &mockShareStore{}, colls, nil, &User{UserID: "admin1", Roles: []string{"admin"}})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/collections/col_1/signoff", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCollectionSignoffShareError(t *testing.T) {
	colls := &mockCollectionStore{getResult: &Collection{ID: "col_1", OwnerID: "u1"}}
	shares := &mockShareStore{listByCollE: assert.AnError}
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, shares, colls, nil, &User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/collections/col_1/signoff", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCollectionSignoffDenied(t *testing.T) {
	colls := &mockCollectionStore{getResult: &Collection{ID: "col_1", OwnerID: "other"}}
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, colls, nil, &User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/collections/col_1/signoff", nil)
	assert.Equal(t, http.StatusForbidden, w.Code)
}
