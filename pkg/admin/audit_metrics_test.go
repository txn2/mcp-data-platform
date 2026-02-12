package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// --- Mock AuditMetricsQuerier ---

type mockAuditMetricsQuerier struct {
	timeseriesResult  []audit.TimeseriesBucket
	timeseriesErr     error
	breakdownResult   []audit.BreakdownEntry
	breakdownErr      error
	overviewResult    *audit.Overview
	overviewErr       error
	performanceResult *audit.PerformanceStats
	performanceErr    error
}

func (m *mockAuditMetricsQuerier) Timeseries(_ context.Context, _ audit.TimeseriesFilter) ([]audit.TimeseriesBucket, error) {
	return m.timeseriesResult, m.timeseriesErr
}

func (m *mockAuditMetricsQuerier) Breakdown(_ context.Context, _ audit.BreakdownFilter) ([]audit.BreakdownEntry, error) {
	return m.breakdownResult, m.breakdownErr
}

func (m *mockAuditMetricsQuerier) Overview(_ context.Context, _, _ *time.Time) (*audit.Overview, error) {
	return m.overviewResult, m.overviewErr
}

func (m *mockAuditMetricsQuerier) Performance(_ context.Context, _, _ *time.Time) (*audit.PerformanceStats, error) {
	return m.performanceResult, m.performanceErr
}

// Verify interface compliance.
var _ AuditMetricsQuerier = (*mockAuditMetricsQuerier)(nil)

// --- Timeseries handler tests ---

func TestGetAuditTimeseries_Success(t *testing.T) {
	now := time.Now().Truncate(time.Hour)
	mock := &mockAuditMetricsQuerier{
		timeseriesResult: []audit.TimeseriesBucket{
			{Bucket: now, Count: 10, SuccessCount: 8, ErrorCount: 2, AvgDurationMS: 42.5},
		},
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/metrics/timeseries?resolution=hour", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var result []audit.TimeseriesBucket
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&result))
	require.Len(t, result, 1)
	assert.Equal(t, 10, result[0].Count)
}

func TestGetAuditTimeseries_DefaultResolution(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		timeseriesResult: []audit.TimeseriesBucket{},
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/metrics/timeseries", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetAuditTimeseries_InvalidResolution(t *testing.T) {
	mock := &mockAuditMetricsQuerier{}
	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/metrics/timeseries?resolution=invalid", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	pd := decodeProblem(rec.Body.Bytes())
	assert.Contains(t, pd.Detail, "invalid resolution")
}

func TestGetAuditTimeseries_QueryError(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		timeseriesErr: fmt.Errorf("db error"),
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/metrics/timeseries", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetAuditTimeseries_WithTimeParams(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		timeseriesResult: []audit.TimeseriesBucket{},
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/audit/metrics/timeseries?resolution=day&start_time=2025-01-01T00:00:00Z&end_time=2025-01-02T00:00:00Z", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// --- Breakdown handler tests ---

func TestGetAuditBreakdown_Success(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		breakdownResult: []audit.BreakdownEntry{
			{Dimension: "trino_query", Count: 50, SuccessRate: 0.96, AvgDurationMS: 45.2},
		},
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/audit/metrics/breakdown?group_by=tool_name", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var result []audit.BreakdownEntry
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&result))
	require.Len(t, result, 1)
	assert.Equal(t, "trino_query", result[0].Dimension)
}

func TestGetAuditBreakdown_InvalidGroupBy(t *testing.T) {
	mock := &mockAuditMetricsQuerier{}
	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/audit/metrics/breakdown?group_by=invalid", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	pd := decodeProblem(rec.Body.Bytes())
	assert.Contains(t, pd.Detail, "invalid group_by")
}

func TestGetAuditBreakdown_MissingGroupBy(t *testing.T) {
	mock := &mockAuditMetricsQuerier{}
	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/audit/metrics/breakdown", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetAuditBreakdown_WithLimit(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		breakdownResult: []audit.BreakdownEntry{},
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/audit/metrics/breakdown?group_by=user_id&limit=5", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetAuditBreakdown_QueryError(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		breakdownErr: fmt.Errorf("db error"),
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/audit/metrics/breakdown?group_by=tool_name", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// --- Overview handler tests ---

func TestGetAuditOverview_Success(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		overviewResult: &audit.Overview{
			TotalCalls:     100,
			SuccessRate:    0.95,
			AvgDurationMS:  35.5,
			UniqueUsers:    5,
			UniqueTools:    8,
			EnrichmentRate: 0.80,
			ErrorCount:     5,
		},
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/metrics/overview", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var result audit.Overview
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&result))
	assert.Equal(t, 100, result.TotalCalls)
	assert.InDelta(t, 0.95, result.SuccessRate, 0.01)
}

func TestGetAuditOverview_QueryError(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		overviewErr: fmt.Errorf("db error"),
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/metrics/overview", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetAuditOverview_WithTimeParams(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		overviewResult: &audit.Overview{},
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/audit/metrics/overview?start_time=2025-01-01T00:00:00Z&end_time=2025-01-02T00:00:00Z", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// --- Performance handler tests ---

func TestGetAuditPerformance_Success(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		performanceResult: &audit.PerformanceStats{
			P50MS:            25.0,
			P95MS:            100.0,
			P99MS:            250.0,
			AvgMS:            45.0,
			MaxMS:            500.0,
			AvgResponseChars: 1024.0,
			AvgRequestChars:  256.0,
		},
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/metrics/performance", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var result audit.PerformanceStats
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&result))
	assert.InDelta(t, 25.0, result.P50MS, 0.01)
	assert.InDelta(t, 100.0, result.P95MS, 0.01)
}

func TestGetAuditPerformance_QueryError(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		performanceErr: fmt.Errorf("db error"),
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/metrics/performance", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetAuditPerformance_WithTimeParams(t *testing.T) {
	mock := &mockAuditMetricsQuerier{
		performanceResult: &audit.PerformanceStats{},
	}

	h := NewHandler(Deps{AuditMetricsQuerier: mock}, nil)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/audit/metrics/performance?start_time=2025-01-01T00:00:00Z&end_time=2025-01-02T00:00:00Z", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// --- Route registration tests ---

func TestAuditMetricsRoutes_NotRegisteredWhenNil(t *testing.T) {
	h := NewHandler(Deps{}, nil)

	endpoints := []string{
		"/api/v1/admin/audit/metrics/timeseries",
		"/api/v1/admin/audit/metrics/breakdown",
		"/api/v1/admin/audit/metrics/overview",
		"/api/v1/admin/audit/metrics/performance",
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodGet, ep, http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		// Should return 404 when AuditMetricsQuerier is nil
		assert.NotEqual(t, http.StatusOK, rec.Code, "endpoint %s should not be available", ep)
	}
}
