package callapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
)

// fakeStore is a call catalog with canned answers, recording what the handlers
// asked of it.
type fakeStore struct {
	callrecord.Store
	records []callrecord.Record
	total   int
	missing bool
	listErr error

	gotFilter callrecord.Filter
	gotScope  callrecord.Scope
	promotion *callrecord.Promotion
	rejection *callrecord.Rejection
}

func (f *fakeStore) List(_ context.Context, filter callrecord.Filter) ([]callrecord.Record, error) {
	f.gotFilter = filter
	return f.records, f.listErr
}

func (f *fakeStore) Count(context.Context, callrecord.Filter) (int, error) {
	return f.total, nil
}

func (f *fakeStore) Get(_ context.Context, scope callrecord.Scope) (*callrecord.Record, error) {
	f.gotScope = scope
	if f.missing {
		return nil, callrecord.ErrNotFound
	}
	return &callrecord.Record{
		ID: scope.ID, EventID: "evt-1", Kind: callrecord.KindSQL,
		Outcome: callrecord.OutcomeSatisfied, Statement: "SELECT 1",
	}, nil
}

func (f *fakeStore) Promote(_ context.Context, _ string, p callrecord.Promotion) error {
	f.promotion = &p
	return nil
}

func (f *fakeStore) Reject(_ context.Context, _ string, r callrecord.Rejection) error {
	f.rejection = &r
	return nil
}

// queryWriter is a DataHub write path that always succeeds.
type queryWriter struct{}

func (queryWriter) CreateCuratedQuery(context.Context, []string, string, string, string) (string, error) {
	return "urn:li:query:abc", nil
}

const operator = "operator@example.com"

func config(store *fakeStore) Config {
	return Config{
		Calls:    store,
		Promoter: callrecord.NewPromoter(store, queryWriter{}, nil),
		Actor:    func(*http.Request) string { return operator },
	}
}

// serve runs one request against the routes.
func serve(t *testing.T, cfg Config, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, cfg)
	rec := httptest.NewRecorder()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	} else {
		req = httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	}
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRegisterNilStoreLeavesRoutesOff(t *testing.T) {
	rec := serve(t, Config{}, http.MethodGet, "/api/v1/admin/calls", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRegisterWithoutAPromoterLeavesTheActionsOff(t *testing.T) {
	cfg := Config{Calls: &fakeStore{}}
	assert.Equal(t, http.StatusOK, serve(t, cfg, http.MethodGet, "/api/v1/admin/calls", "").Code)
	assert.Equal(t, http.StatusNotFound,
		serve(t, cfg, http.MethodPost, "/api/v1/admin/calls/call-1/promote", "").Code)
}

func TestListIsUnscopedAndOffersTheUserFacet(t *testing.T) {
	store := &fakeStore{
		records: []callrecord.Record{{ID: "call-1", Outcome: callrecord.OutcomeSatisfied}},
		total:   1,
	}
	rec := serve(t, config(store), http.MethodGet,
		"/api/v1/admin/calls?user_id=someone&queue=promotable", "")
	require.Equal(t, http.StatusOK, rec.Code)

	// The operator surface is unrestricted by design, and the user facet is
	// the one parameter the two surfaces cannot share.
	assert.Equal(t, "someone", store.gotFilter.UserID)
	assert.True(t, store.gotFilter.PromotableOnly)

	var got callListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Data, 1)
}

func TestGetIsUnscoped(t *testing.T) {
	store := &fakeStore{}
	rec := serve(t, config(store), http.MethodGet, "/api/v1/admin/calls/call-1", "")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Empty(t, store.gotScope.UserID, "an operator reads any caller's record")
	assert.Equal(t, "call-1", store.gotScope.ID)
}

func TestGetMissingRecordIsNotFound(t *testing.T) {
	rec := serve(t, config(&fakeStore{missing: true}), http.MethodGet, "/api/v1/admin/calls/gone", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPromoteRecordsWhoActed(t *testing.T) {
	store := &fakeStore{}
	rec := serve(t, config(store), http.MethodPost, "/api/v1/admin/calls/call-1/promote", "")
	require.Equal(t, http.StatusOK, rec.Code)

	require.NotNil(t, store.promotion)
	assert.Equal(t, operator, store.promotion.Actor)
	assert.Equal(t, "urn:li:query:abc", store.promotion.URN)
}

func TestRejectRecordsTheNote(t *testing.T) {
	store := &fakeStore{}
	rec := serve(t, config(store), http.MethodPost,
		"/api/v1/admin/calls/call-1/reject", `{"note":"Superseded by the revenue view."}`)
	require.Equal(t, http.StatusOK, rec.Code)

	require.NotNil(t, store.rejection)
	assert.Equal(t, operator, store.rejection.Actor)
	assert.Equal(t, "Superseded by the revenue view.", store.rejection.Note)
}

func TestActionsWithoutAnActorStillWork(t *testing.T) {
	// A handler wired with no identity source records an empty actor rather
	// than refusing the decision.
	store := &fakeStore{}
	cfg := config(store)
	cfg.Actor = nil

	rec := serve(t, cfg, http.MethodPost, "/api/v1/admin/calls/call-1/promote", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, store.promotion.Actor)
}

func TestListFailureIsReported(t *testing.T) {
	store := &fakeStore{listErr: errors.New("catalog unavailable")}
	rec := serve(t, config(store), http.MethodGet, "/api/v1/admin/calls", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
}

func TestPromoteAMissingRecordIsNotFound(t *testing.T) {
	rec := serve(t, config(&fakeStore{missing: true}), http.MethodPost,
		"/api/v1/admin/calls/gone/promote", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
