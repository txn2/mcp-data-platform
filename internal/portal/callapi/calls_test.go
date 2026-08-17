package callapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
)

const callerID = "user-a"

// fakeStore is a call catalog with canned answers, recording the filter and
// scope the handlers derived from the request. It does NOT enforce the scope:
// what these tests assert is that the caller reaches the store that can, since
// a handler that dropped it would otherwise pass against a fake that filtered
// on its behalf.
type fakeStore struct {
	callrecord.Store
	records []callrecord.Record
	total   int
	missing bool

	listErr error
	getErr  error

	gotFilter callrecord.Filter
	gotScope  callrecord.Scope
}

func (f *fakeStore) List(_ context.Context, filter callrecord.Filter) ([]callrecord.Record, error) {
	f.gotFilter = filter
	return f.records, f.listErr
}

func (f *fakeStore) Count(context.Context, callrecord.Filter) (int, error) {
	return f.total, nil
}

// Get models the catalog's not-found contract: an id the scope admits nothing
// for is ErrNotFound, never a nil record with a nil error.
func (f *fakeStore) Get(_ context.Context, scope callrecord.Scope) (*callrecord.Record, error) {
	f.gotScope = scope
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.missing {
		return nil, callrecord.ErrNotFound
	}
	return &callrecord.Record{
		ID: scope.ID, EventID: "evt-1", Kind: callrecord.KindSQL,
		Outcome: callrecord.OutcomeSatisfied, Statement: "SELECT 1",
		Targets: []string{"urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)"},
	}, nil
}

func (*fakeStore) Promote(context.Context, string, callrecord.Promotion) error { return nil }

func (*fakeStore) Reject(context.Context, string, callrecord.Rejection) error { return nil }

// queryWriter is a DataHub write path that always succeeds.
type queryWriter struct {
	targets []string
}

func (q *queryWriter) CreateCuratedQuery(_ context.Context, datasetURNs []string, _, _, _ string) (string, error) {
	q.targets = datasetURNs
	return "urn:li:query:abc", nil
}

// serve runs one request against the routes as the given caller. An empty
// caller sends the request unauthenticated.
func serve(t *testing.T, cfg Config, method, target, userID string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, cfg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	if userID != "" {
		req = req.WithContext(access.ContextWithUser(req.Context(), &access.User{
			UserID: userID,
			Email:  userID + "@example.com",
		}))
	}
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRegisterNilStoreLeavesRoutesOff(t *testing.T) {
	rec := serve(t, Config{}, http.MethodGet, "/api/v1/portal/calls", callerID)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRegisterWithoutAPromoterLeavesTheActionsOff(t *testing.T) {
	// A deployment with nowhere to promote to still serves the catalog: a
	// record is worth reading whether or not it can be published.
	cfg := Config{Calls: &fakeStore{}}
	assert.Equal(t, http.StatusOK, serve(t, cfg, http.MethodGet, "/api/v1/portal/calls", callerID).Code)
	assert.Equal(t, http.StatusNotFound,
		serve(t, cfg, http.MethodPost, "/api/v1/portal/calls/call-1/promote", callerID).Code)
}

func TestCallsRequireAuthentication(t *testing.T) {
	for _, target := range []string{
		"/api/v1/portal/calls",
		"/api/v1/portal/calls/call-1",
	} {
		t.Run(target, func(t *testing.T) {
			store := &fakeStore{}
			rec := serve(t, Config{Calls: store}, http.MethodGet, target, "")
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
			assert.Empty(t, store.gotFilter.UserID, "the store is never reached")
			assert.Empty(t, store.gotScope.ID)
		})
	}
}

func TestListScopesToTheCaller(t *testing.T) {
	store := &fakeStore{
		records: []callrecord.Record{{ID: "call-1", Outcome: callrecord.OutcomeSatisfied}},
		total:   1,
	}
	rec := serve(t, Config{Calls: store}, http.MethodGet,
		"/api/v1/portal/calls?kind=sql&user_id=someone-else", callerID)
	require.Equal(t, http.StatusOK, rec.Code)

	// The caller is assigned after the query string is read, so a
	// hand-written user_id cannot widen the listing.
	assert.Equal(t, callerID, store.gotFilter.UserID)
	assert.Equal(t, callrecord.KindSQL, store.gotFilter.Kind)

	var got callListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Data, 1)
	assert.Equal(t, 1, got.Total)
	assert.Equal(t, callrecord.DefaultPerPage, got.PerPage)
}

func TestGetScopesToTheCaller(t *testing.T) {
	store := &fakeStore{}
	rec := serve(t, Config{Calls: store}, http.MethodGet, "/api/v1/portal/calls/call-1", callerID)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, callerID, store.gotScope.UserID)
	assert.Equal(t, "call-1", store.gotScope.ID)
}

func TestGetAnotherCallersRecordIsNotFound(t *testing.T) {
	store := &fakeStore{missing: true}
	rec := serve(t, Config{Calls: store}, http.MethodGet, "/api/v1/portal/calls/call-9", callerID)

	// Not-found rather than refused: the answer for someone else's record is
	// the answer for an id that was never used.
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not found")
}

func TestPromoteScopesToTheCallerAndReturnsTheRecord(t *testing.T) {
	store := &fakeStore{}
	writer := &queryWriter{}
	cfg := Config{Calls: store, Promoter: callrecord.NewPromoter(store, writer, nil)}

	rec := serve(t, cfg, http.MethodPost, "/api/v1/portal/calls/call-1/promote", callerID)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, callerID, store.gotScope.UserID, "an owner promotes only their own record")
	assert.Len(t, writer.targets, 1, "the record's targets reach the catalog write")
}

func TestPromoteARecordThatAnsweredNothingIsAConflict(t *testing.T) {
	store := &notPromotableStore{}
	cfg := Config{Calls: store, Promoter: callrecord.NewPromoter(store, &queryWriter{}, nil)}

	rec := serve(t, cfg, http.MethodPost, "/api/v1/portal/calls/call-1/promote", callerID)
	// Nothing about the request was wrong; the record is in the wrong state.
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestPromoteWithNowhereToPromoteToIsAConflict(t *testing.T) {
	store := &fakeStore{}
	cfg := Config{Calls: store, Promoter: callrecord.NewPromoter(store, nil, nil)}

	rec := serve(t, cfg, http.MethodPost, "/api/v1/portal/calls/call-1/promote", callerID)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "no promotion target")
}

func TestRejectAcceptsANoteAndSurvivesWithoutOne(t *testing.T) {
	store := &fakeStore{}
	cfg := Config{Calls: store, Promoter: callrecord.NewPromoter(store, &queryWriter{}, nil)}

	mux := http.NewServeMux()
	Register(mux, cfg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/portal/calls/call-1/reject", strings.NewReader(`{"note":"Superseded."}`))
	req = req.WithContext(access.ContextWithUser(req.Context(), &access.User{UserID: callerID}))
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// A missing body is a rejection with no note: refusing over it would be
	// refusing the decision the caller already made.
	assert.Equal(t, http.StatusOK,
		serve(t, cfg, http.MethodPost, "/api/v1/portal/calls/call-1/reject", callerID).Code)
}

func TestActionsRequireAuthentication(t *testing.T) {
	store := &fakeStore{}
	cfg := Config{Calls: store, Promoter: callrecord.NewPromoter(store, &queryWriter{}, nil)}

	for _, target := range []string{
		"/api/v1/portal/calls/call-1/promote",
		"/api/v1/portal/calls/call-1/reject",
	} {
		rec := serve(t, cfg, http.MethodPost, target, "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Empty(t, store.gotScope.ID, "the store is never reached")
	}
}

func TestReadFailureIsReported(t *testing.T) {
	store := &fakeStore{getErr: assertError{}}
	rec := serve(t, Config{Calls: store}, http.MethodGet, "/api/v1/portal/calls/call-1", callerID)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	listStore := &fakeStore{listErr: assertError{}}
	rec = serve(t, Config{Calls: listStore}, http.MethodGet, "/api/v1/portal/calls", callerID)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// notPromotableStore answers with a record nothing was built from.
type notPromotableStore struct{ fakeStore }

func (n *notPromotableStore) Get(_ context.Context, scope callrecord.Scope) (*callrecord.Record, error) {
	n.gotScope = scope
	return &callrecord.Record{ID: scope.ID, Kind: callrecord.KindSQL, Outcome: callrecord.OutcomeRan}, nil
}

// assertError is a plain read failure.
type assertError struct{}

func (assertError) Error() string { return "catalog unavailable" }
