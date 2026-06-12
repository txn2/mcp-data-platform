package portal

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func authoredThread() *mockThreadStore {
	return &mockThreadStore{getResult: &Thread{
		ID: "thr_1", TargetType: targetTypeAsset, AssetID: "asset_1",
		AuthorID: "sme1", AuthorEmail: "sme1@example.com", Status: ThreadStatusResolved,
	}}
}

func TestRespondValidationAuthorValidates(t *testing.T) {
	threads := authoredThread()
	h := newThreadHandlerFull(threads, &mockAssetStore{}, &mockShareStore{}, nil, nil, &User{UserID: "sme1", Email: "sme1@example.com"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "validated"})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "validated", threads.respondedResult)
}

func TestRespondValidationAuthorDisputes(t *testing.T) {
	threads := authoredThread()
	h := newThreadHandlerFull(threads, &mockAssetStore{}, &mockShareStore{}, nil, nil, &User{UserID: "sme1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "disputed", Reason: "still wrong"})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "disputed", threads.respondedResult)
}

func TestRespondValidationNonAuthorDenied(t *testing.T) {
	h := newThreadHandlerFull(authoredThread(), &mockAssetStore{}, &mockShareStore{}, nil, nil, &User{UserID: "other"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "validated"})
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestRespondValidationBadResult(t *testing.T) {
	h := newThreadHandlerFull(authoredThread(), &mockAssetStore{}, &mockShareStore{}, nil, nil, &User{UserID: "sme1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "maybe"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRespondValidationNotFound(t *testing.T) {
	threads := &mockThreadStore{getErr: assert.AnError}
	h := newThreadHandlerFull(threads, &mockAssetStore{}, &mockShareStore{}, nil, nil, &User{UserID: "sme1"})
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
	threads := authoredThread()
	threads.respondErr = assert.AnError
	h := newThreadHandlerFull(threads, &mockAssetStore{}, &mockShareStore{}, nil, nil, &User{UserID: "sme1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "validated"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRespondValidationAdmin(t *testing.T) {
	threads := authoredThread()
	h := newThreadHandlerFull(threads, &mockAssetStore{}, &mockShareStore{}, nil, nil, &User{UserID: "admin1", Roles: []string{"admin"}})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/validation",
		respondValidationRequest{Result: "validated"})
	assert.Equal(t, http.StatusOK, w.Code)
}
