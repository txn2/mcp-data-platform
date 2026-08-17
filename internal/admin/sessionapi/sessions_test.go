package sessionapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/sessionview"
)

// fakeStore is a session read model with canned answers, recording the filter
// and timeline page the handlers derived from the request.
type fakeStore struct {
	sessions []sessionview.Summary
	total    int
	timeline []sessionview.TimelineEntry
	assets   []sessionview.AssetRef
	insights []sessionview.InsightRef
	missing  bool

	listErr  error
	countErr error
	getErr   error
	readErr  error

	gotFilter sessionview.Filter
	gotScope  sessionview.Scope
}

func (f *fakeStore) List(_ context.Context, filter sessionview.Filter) ([]sessionview.Summary, error) {
	f.gotFilter = filter
	return f.sessions, f.listErr
}

func (f *fakeStore) Count(context.Context, sessionview.Filter) (int, error) {
	return f.total, f.countErr
}

// Get models the read model's not-found contract: an id with no recorded
// calls is ErrNotFound, never a nil summary with a nil error.
func (f *fakeStore) Get(_ context.Context, scope sessionview.Scope) (*sessionview.Summary, error) {
	f.gotScope = scope
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.missing {
		return nil, sessionview.ErrNotFound
	}
	return &sessionview.Summary{SessionID: "dps_abc", CallCount: 5, FailureCount: 1}, nil
}

func (f *fakeStore) Timeline(_ context.Context, scope sessionview.Scope) ([]sessionview.TimelineEntry, int, error) {
	f.gotScope = scope
	return f.timeline, len(f.timeline), f.readErr
}

func (f *fakeStore) Assets(context.Context, string) ([]sessionview.AssetRef, error) {
	return f.assets, nil
}

func (f *fakeStore) Insights(context.Context, string) ([]sessionview.InsightRef, error) {
	return f.insights, nil
}

// serve registers the routes over store and runs one GET against target.
func serve(t *testing.T, store Store, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, Config{Sessions: store})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRegister_NilStoreLeavesRoutesOff(t *testing.T) {
	rec := serve(t, nil, "/api/v1/admin/sessions")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestListSessions_ReturnsPage(t *testing.T) {
	store := &fakeStore{
		sessions: []sessionview.Summary{{
			SessionID: "dps_abc",
			Kind:      sessionview.KindAgent,
			CallCount: 5,
			Tools:     []string{"search"},
		}},
		total: 1,
	}
	rec := serve(t, store, "/api/v1/admin/sessions")
	require.Equal(t, http.StatusOK, rec.Code)

	var got sessionListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Data, 1)
	assert.Equal(t, "dps_abc", got.Data[0].SessionID)
	assert.Equal(t, 1, got.Total)
	assert.Equal(t, 1, got.Page)
	assert.Equal(t, sessionview.DefaultPerPage, got.PerPage)
}

func TestListSessions_ParsesEveryFilter(t *testing.T) {
	store := &fakeStore{}
	rec := serve(t, store,
		"/api/v1/admin/sessions?user_id=u1&kind=script&has_assets=true&has_failures=1"+
			"&start_time=2026-08-16T00:00:00Z&end_time=2026-08-16T23:59:59Z&page=3&per_page=10")
	require.Equal(t, http.StatusOK, rec.Code)

	f := store.gotFilter
	assert.Equal(t, "u1", f.UserID)
	assert.Equal(t, sessionview.KindScript, f.Kind)
	assert.True(t, f.HasAssets)
	assert.True(t, f.HasFailures)
	require.NotNil(t, f.StartTime)
	assert.Equal(t, 2026, f.StartTime.Year())
	require.NotNil(t, f.EndTime)
	assert.Equal(t, 10, f.Limit)
	assert.Equal(t, 20, f.Offset, "page 3 at 10 per page starts at row 20")
}

// A malformed flag must not silently select the narrow set: has_assets=maybe
// is "no filter stated", not "only sessions with assets".
func TestListSessions_MalformedFlagIsUnset(t *testing.T) {
	store := &fakeStore{}
	rec := serve(t, store, "/api/v1/admin/sessions?has_assets=maybe")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, store.gotFilter.HasAssets)
}

func TestListSessions_ClampsPerPage(t *testing.T) {
	tests := []struct {
		query string
		want  int
	}{
		{"", sessionview.DefaultPerPage},
		{"?per_page=0", sessionview.DefaultPerPage},
		{"?per_page=-5", sessionview.DefaultPerPage},
		{"?per_page=5000", sessionview.MaxPerPage},
		{"?per_page=10", 10},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			store := &fakeStore{}
			rec := serve(t, store, "/api/v1/admin/sessions"+tt.query)
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.want, store.gotFilter.Limit)
		})
	}
}

func TestListSessions_StoreErrors(t *testing.T) {
	tests := []struct {
		name  string
		store *fakeStore
	}{
		{"list fails", &fakeStore{listErr: errors.New("boom")}},
		{"count fails", &fakeStore{countErr: errors.New("boom")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, tt.store, "/api/v1/admin/sessions")
			assert.Equal(t, http.StatusInternalServerError, rec.Code)
		})
	}
}

func TestGetSession_ReturnsDetail(t *testing.T) {
	store := &fakeStore{
		timeline: []sessionview.TimelineEntry{{
			EventID:   "evt-1",
			ToolName:  "trino_query",
			Purpose:   "Sizing Q3 revenue by region.",
			Timestamp: time.Now(),
			Success:   true,
		}},
		assets:   []sessionview.AssetRef{{ID: "ast_1", Name: "Q3 revenue"}},
		insights: []sessionview.InsightRef{{ID: "ins_1"}},
	}
	rec := serve(t, store, "/api/v1/admin/sessions/dps_abc?per_page=10&page=2")
	require.Equal(t, http.StatusOK, rec.Code)

	var got sessionview.Detail
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "dps_abc", got.SessionID)
	require.Len(t, got.Timeline, 1)
	assert.Equal(t, "Sizing Q3 revenue by region.", got.Timeline[0].Purpose)
	require.Len(t, got.Assets, 1)
	require.Len(t, got.Insights, 1)
	assert.Equal(t, 10, store.gotScope.Limit)
	assert.Equal(t, 10, store.gotScope.Offset, "the timeline pages on the same vocabulary as the list")
	assert.Empty(t, store.gotScope.UserID,
		"the operator surface is unrestricted: it reads sessions it did not run")
}

func TestGetSession_NotFound(t *testing.T) {
	rec := serve(t, &fakeStore{missing: true}, "/api/v1/admin/sessions/dps_missing")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
}

func TestGetSession_ReadErrors(t *testing.T) {
	tests := []struct {
		name  string
		store *fakeStore
	}{
		{"summary fails", &fakeStore{getErr: errors.New("boom")}},
		{"timeline fails", &fakeStore{readErr: errors.New("boom")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, tt.store, "/api/v1/admin/sessions/dps_abc")
			assert.Equal(t, http.StatusInternalServerError, rec.Code)
		})
	}
}
