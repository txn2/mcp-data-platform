package feedbackapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

func authoredThread() *mockThreadStore {
	return &mockThreadStore{getResult: &threads.Thread{
		ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1",
		AuthorID: "sme1", AuthorEmail: "sme1@example.com", Status: threads.ThreadStatusResolved,
	}}
}

func TestRespondValidationAuthorValidates(t *testing.T) {
	store := authoredThread()
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, nil, nil, &access.User{UserID: "sme1", Email: "sme1@example.com"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "validated"})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "validated", store.respondedResult)
}

func TestRespondValidationAuthorDisputes(t *testing.T) {
	store := authoredThread()
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, nil, nil, &access.User{UserID: "sme1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "disputed", Reason: "still wrong"})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "disputed", store.respondedResult)
}

func TestRespondValidationNonAuthorDenied(t *testing.T) {
	h := newThreadHandlerFull(authoredThread(), &mockAssetStore{}, &mockShareStore{}, nil, nil, &access.User{UserID: "other"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "validated"})
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestRespondValidationBadResult(t *testing.T) {
	h := newThreadHandlerFull(authoredThread(), &mockAssetStore{}, &mockShareStore{}, nil, nil, &access.User{UserID: "sme1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "maybe"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRespondValidationNotFound(t *testing.T) {
	store := &mockThreadStore{getErr: assert.AnError}
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, nil, nil, &access.User{UserID: "sme1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/nope/validation",
		respondValidationRequest{Result: "validated"})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRespondValidationUnauthorized(t *testing.T) {
	h := newThreadHandlerFull(authoredThread(), &mockAssetStore{}, &mockShareStore{}, nil, nil, nil)
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "validated"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRespondValidationStoreError(t *testing.T) {
	store := authoredThread()
	store.respondErr = assert.AnError
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, nil, nil, &access.User{UserID: "sme1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "validated"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRespondValidationAdmin(t *testing.T) {
	store := authoredThread()
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, nil, nil, &access.User{UserID: "admin1", Roles: []string{"admin"}})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "validated"})
	assert.Equal(t, http.StatusOK, w.Code)
}
