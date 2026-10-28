package auditapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

func TestListAuditEvents(t *testing.T) {
	now := time.Now()
	events := []audit.Event{
		{ID: "ev-1", Timestamp: now, ToolName: "trino_query", UserID: "user-1", Success: true},
		{ID: "ev-2", Timestamp: now, ToolName: "search", UserID: "user-2", Success: false},
	}

	t.Run("returns paginated events", func(t *testing.T) {
		aq := &mockAuditQuerier{queryResult: events, countResult: 10}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events?per_page=2&page=1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body auditEventResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, 10, body.Total)
		assert.Equal(t, 1, body.Page)
		assert.Equal(t, 2, body.PerPage)
		assert.Len(t, body.Data, 2)
	})

	t.Run("applies filters", func(t *testing.T) {
		aq := &mockAuditQuerier{queryResult: events[:1], countResult: 1}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events?user_id=user-1&tool_name=trino_query&success=true", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("passes event_kind into filter", func(t *testing.T) {
		aq := &mockAuditQuerier{queryResult: events[:1], countResult: 1}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events?event_kind=apigateway_invoke", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "apigateway_invoke", aq.lastQueryFilter.EventKind)
	})

	t.Run("returns empty list on no results", func(t *testing.T) {
		aq := &mockAuditQuerier{queryResult: nil, countResult: 0}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body auditEventResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Len(t, body.Data, 0)
		assert.Equal(t, 0, body.Total)
	})

	t.Run("returns 500 on query error", func(t *testing.T) {
		aq := &mockAuditQuerier{queryErr: fmt.Errorf("db error")}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("returns 500 on count error", func(t *testing.T) {
		aq := &mockAuditQuerier{queryResult: events, countErr: fmt.Errorf("count error")}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("defaults per_page to 50", func(t *testing.T) {
		aq := &mockAuditQuerier{queryResult: nil, countResult: 0}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body auditEventResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, defaultAuditLimit, body.PerPage)
	})

	t.Run("passes search param to filter", func(t *testing.T) {
		aq := &mockAuditQuerier{queryResult: nil, countResult: 0}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events?search=trino", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("passes sort params to filter", func(t *testing.T) {
		aq := &mockAuditQuerier{queryResult: nil, countResult: 0}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events?sort_by=duration_ms&sort_order=asc", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("ignores invalid sort_order", func(t *testing.T) {
		aq := &mockAuditQuerier{queryResult: nil, countResult: 0}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events?sort_by=timestamp&sort_order=invalid", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("passes toolkit_kind filter", func(t *testing.T) {
		aq := &recordingAuditQuerier{}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events?toolkit_kind=api", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "api", aq.lastQueryFilter.ToolkitKind)
	})

	t.Run("passes source filter", func(t *testing.T) {
		aq := &recordingAuditQuerier{}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events?source=mcp", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "mcp", aq.lastQueryFilter.Source)
	})
}

func TestListAuditEventFilters(t *testing.T) {
	t.Run("returns distinct users and tools", func(t *testing.T) {
		aq := &mockAuditQuerier{
			distinctResult: []string{"alice@acme.com", "bob@acme.com"},
		}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events/filters", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body auditFiltersResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, []string{"alice@acme.com", "bob@acme.com"}, body.Users)
		assert.Equal(t, []string{"alice@acme.com", "bob@acme.com"}, body.Tools)
		assert.Equal(t, []string{"alice@acme.com", "bob@acme.com"}, body.EventKinds)
	})

	t.Run("returns empty arrays when no events", func(t *testing.T) {
		aq := &mockAuditQuerier{distinctResult: nil}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events/filters", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body auditFiltersResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, []string{}, body.Users)
		assert.Equal(t, []string{}, body.Tools)
	})

	t.Run("returns 500 on distinct error", func(t *testing.T) {
		aq := &mockAuditQuerier{distinctErr: fmt.Errorf("db error")}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events/filters", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("returns toolkit_kinds and sources", func(t *testing.T) {
		aq := &recordingAuditQuerier{
			distinctResults: map[string][]string{
				"user_id":      {"user-1"},
				"tool_name":    {"trino_query"},
				"toolkit_kind": {"api", "trino"},
				"source":       {"mcp"},
			},
		}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events/filters", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body auditFiltersResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, []string{"api", "trino"}, body.ToolkitKinds)
		assert.Equal(t, []string{"mcp"}, body.Sources)
	})
}

func TestGetAuditEvent(t *testing.T) {
	t.Run("returns event when found", func(t *testing.T) {
		event := audit.Event{ID: "ev-123", ToolName: "trino_query", Success: true}
		aq := &mockAuditQuerier{queryResult: []audit.Event{event}}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events/ev-123", http.NoBody)
		req.SetPathValue("id", "ev-123")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body audit.Event
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "ev-123", body.ID)
	})

	t.Run("returns 404 when not found", func(t *testing.T) {
		aq := &mockAuditQuerier{queryResult: nil}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events/missing", http.NoBody)
		req.SetPathValue("id", "missing")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "audit event not found", pd.Detail)
	})

	t.Run("returns 500 on query error", func(t *testing.T) {
		aq := &mockAuditQuerier{queryErr: fmt.Errorf("db error")}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/events/ev-123", http.NoBody)
		req.SetPathValue("id", "ev-123")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestGetAuditStats(t *testing.T) {
	t.Run("returns stats", func(t *testing.T) {
		aq := &mockAuditQuerier{countResult: 100}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/stats", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body auditStatsResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		// Both total and success count come from the same mock value
		assert.Equal(t, 100, body.Total)
		assert.Equal(t, 100, body.Success)
		assert.Equal(t, 0, body.Failures)
	})

	t.Run("returns 500 on total count error", func(t *testing.T) {
		aq := &mockAuditQuerier{countErr: fmt.Errorf("db error")}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/stats", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("accepts filter parameters", func(t *testing.T) {
		aq := &mockAuditQuerier{countResult: 5}
		h := testMux(Config{Events: aq})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/audit/stats?user_id=user-1&tool_name=trino_query", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// boundedQuerier is a store that can stop counting, as the PostgreSQL one can.
type boundedQuerier struct {
	mockAuditQuerier
	matching  int
	limit     int
	boundErr  error
	countUsed bool
}

func (b *boundedQuerier) CountUpTo(_ context.Context, _ audit.QueryFilter, limit int) (int, error) {
	b.limit = limit
	return min(b.matching, limit), b.boundErr
}

func (b *boundedQuerier) Count(ctx context.Context, f audit.QueryFilter) (int, error) {
	b.countUsed = true
	return b.mockAuditQuerier.Count(ctx, f)
}

// TestListAuditEventsCapsTheTotal is #2272: the total is counted no further
// than 10,000, and the response says when it stopped there.
func TestListAuditEventsCapsTheTotal(t *testing.T) {
	get := func(t *testing.T, q EventQuerier) (auditEventResponse, int) {
		t.Helper()
		w := httptest.NewRecorder()
		testMux(Config{Events: q}).ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/v1/admin/audit/events", http.NoBody))
		var body auditEventResponse
		if w.Code == http.StatusOK {
			require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		}
		return body, w.Code
	}

	many := &boundedQuerier{matching: 20_000_000}
	body, code := get(t, many)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, totalCap, body.Total)
	assert.True(t, body.TotalCapped)
	assert.Equal(t, totalCap+1, many.limit, "one past the cap tells a full count from a cut one")
	assert.False(t, many.countUsed, "a store that can stop is never counted in full")

	exact := &boundedQuerier{matching: totalCap}
	body, _ = get(t, exact)
	assert.Equal(t, totalCap, body.Total)
	assert.False(t, body.TotalCapped, "exactly the cap is a full count")

	few := &boundedQuerier{matching: 7}
	body, _ = get(t, few)
	assert.Equal(t, 7, body.Total)
	assert.False(t, body.TotalCapped)

	_, code = get(t, &boundedQuerier{boundErr: errors.New("db down")})
	assert.Equal(t, http.StatusInternalServerError, code)

	plain := &mockAuditQuerier{countResult: 12_345}
	body, _ = get(t, plain)
	assert.Equal(t, 12_345, body.Total, "a store that cannot stop is counted in full")
	assert.False(t, body.TotalCapped)
}
