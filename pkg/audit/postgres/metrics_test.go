package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// --- Timeseries tests ---

func TestTimeseries_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	now := time.Now()
	start := now.Add(-24 * time.Hour)

	rows := sqlmock.NewRows([]string{"bucket", "count", "success_count", "error_count", "avg_duration_ms"}).
		AddRow(now.Truncate(time.Hour), 10, 8, 2, 42.5).
		AddRow(now.Truncate(time.Hour).Add(time.Hour), 5, 5, 0, 30.0)

	mock.ExpectQuery("SELECT").
		WithArgs(start, now).
		WillReturnRows(rows)

	result, err := store.Timeseries(context.Background(), audit.TimeseriesFilter{
		Resolution: audit.ResolutionHour,
		StartTime:  &start,
		EndTime:    &now,
	})

	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, 10, result[0].Count)
	assert.Equal(t, 8, result[0].SuccessCount)
	assert.Equal(t, 2, result[0].ErrorCount)
	assert.InDelta(t, 42.5, result[0].AvgDurationMS, 0.01)
	assert.Equal(t, 5, result[1].Count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTimeseries_InvalidResolution(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	_, err = store.Timeseries(context.Background(), audit.TimeseriesFilter{
		Resolution: "invalid",
	})
	assert.ErrorContains(t, err, "invalid resolution")
}

func TestTimeseries_EmptyResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})

	rows := sqlmock.NewRows([]string{"bucket", "count", "success_count", "error_count", "avg_duration_ms"})
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := store.Timeseries(context.Background(), audit.TimeseriesFilter{
		Resolution: audit.ResolutionDay,
	})

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.NotNil(t, result) // must return empty slice, not nil
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTimeseries_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))

	_, err = store.Timeseries(context.Background(), audit.TimeseriesFilter{
		Resolution: audit.ResolutionHour,
	})
	assert.ErrorContains(t, err, "querying timeseries")
}

func TestTimeseries_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	rows := sqlmock.NewRows([]string{"bucket", "count", "success_count", "error_count", "avg_duration_ms"}).
		AddRow("not-a-time", "bad", "bad", "bad", "bad")
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	_, err = store.Timeseries(context.Background(), audit.TimeseriesFilter{
		Resolution: audit.ResolutionHour,
	})
	assert.Error(t, err)
}

func TestTimeseries_AllResolutions(t *testing.T) {
	for _, res := range []audit.Resolution{audit.ResolutionMinute, audit.ResolutionHour, audit.ResolutionDay} {
		t.Run(string(res), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()

			store := New(db, Config{})
			rows := sqlmock.NewRows([]string{"bucket", "count", "success_count", "error_count", "avg_duration_ms"})
			mock.ExpectQuery("SELECT").WillReturnRows(rows)

			result, err := store.Timeseries(context.Background(), audit.TimeseriesFilter{
				Resolution: res,
			})
			require.NoError(t, err)
			assert.NotNil(t, result)
		})
	}
}

func TestTimeseries_DefaultTimeRange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	rows := sqlmock.NewRows([]string{"bucket", "count", "success_count", "error_count", "avg_duration_ms"})
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	// No start/end time -- should use defaults (last 24h)
	result, err := store.Timeseries(context.Background(), audit.TimeseriesFilter{
		Resolution: audit.ResolutionHour,
	})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Breakdown tests ---

func TestBreakdown_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	now := time.Now()
	start := now.Add(-24 * time.Hour)

	rows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"}).
		AddRow("trino_query", 50, 0.96, 45.2).
		AddRow("datahub_search", 30, 1.0, 20.1)

	mock.ExpectQuery("SELECT").
		WithArgs(start, now).
		WillReturnRows(rows)

	result, err := store.Breakdown(context.Background(), audit.BreakdownFilter{
		GroupBy:   audit.BreakdownByToolName,
		StartTime: &start,
		EndTime:   &now,
	})

	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "trino_query", result[0].Dimension)
	assert.Equal(t, 50, result[0].Count)
	assert.InDelta(t, 0.96, result[0].SuccessRate, 0.01)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBreakdown_InvalidDimension(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	_, err = store.Breakdown(context.Background(), audit.BreakdownFilter{
		GroupBy: "invalid",
	})
	assert.ErrorContains(t, err, "invalid breakdown dimension")
}

func TestBreakdown_AllDimensions(t *testing.T) {
	dims := []audit.BreakdownDimension{
		audit.BreakdownByToolName,
		audit.BreakdownByUserID,
		audit.BreakdownByPersona,
		audit.BreakdownByToolkitKind,
		audit.BreakdownByConnection,
	}
	for _, dim := range dims {
		t.Run(string(dim), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()

			store := New(db, Config{})
			rows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"})
			mock.ExpectQuery("SELECT").WillReturnRows(rows)

			result, err := store.Breakdown(context.Background(), audit.BreakdownFilter{
				GroupBy: dim,
			})
			require.NoError(t, err)
			assert.NotNil(t, result)
		})
	}
}

func TestBreakdown_CustomLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	rows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"})
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	_, err = store.Breakdown(context.Background(), audit.BreakdownFilter{
		GroupBy: audit.BreakdownByToolName,
		Limit:   5,
	})
	require.NoError(t, err)
}

func TestBreakdown_LimitCapped(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	rows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"})
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	// Limit exceeds max -- should be capped to maxBreakdownLimit
	_, err = store.Breakdown(context.Background(), audit.BreakdownFilter{
		GroupBy: audit.BreakdownByToolName,
		Limit:   500,
	})
	require.NoError(t, err)
}

func TestBreakdown_EmptyResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	rows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"})
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := store.Breakdown(context.Background(), audit.BreakdownFilter{
		GroupBy: audit.BreakdownByUserID,
	})
	require.NoError(t, err)
	assert.Empty(t, result)
	assert.NotNil(t, result)
}

func TestBreakdown_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))

	_, err = store.Breakdown(context.Background(), audit.BreakdownFilter{
		GroupBy: audit.BreakdownByToolName,
	})
	assert.ErrorContains(t, err, "querying breakdown")
}

func TestBreakdown_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	rows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"}).
		AddRow("tool", "bad", "bad", "bad")
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	_, err = store.Breakdown(context.Background(), audit.BreakdownFilter{
		GroupBy: audit.BreakdownByToolName,
	})
	assert.Error(t, err)
}

// --- Overview tests ---

func TestOverview_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	now := time.Now()
	start := now.Add(-24 * time.Hour)

	rows := sqlmock.NewRows([]string{
		"total_calls", "success_rate", "avg_duration_ms",
		"unique_users", "unique_tools", "enrichment_rate", "error_count",
	}).AddRow(100, 0.95, 35.5, 5, 8, 0.80, 5)

	mock.ExpectQuery("SELECT").
		WithArgs(start, now).
		WillReturnRows(rows)

	result, err := store.Overview(context.Background(), &start, &now)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 100, result.TotalCalls)
	assert.InDelta(t, 0.95, result.SuccessRate, 0.01)
	assert.InDelta(t, 35.5, result.AvgDurationMS, 0.01)
	assert.Equal(t, 5, result.UniqueUsers)
	assert.Equal(t, 8, result.UniqueTools)
	assert.InDelta(t, 0.80, result.EnrichmentRate, 0.01)
	assert.Equal(t, 5, result.ErrorCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOverview_DefaultTimeRange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	rows := sqlmock.NewRows([]string{
		"total_calls", "success_rate", "avg_duration_ms",
		"unique_users", "unique_tools", "enrichment_rate", "error_count",
	}).AddRow(0, 0, 0, 0, 0, 0, 0)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := store.Overview(context.Background(), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.TotalCalls)
}

func TestOverview_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))

	_, err = store.Overview(context.Background(), nil, nil)
	assert.ErrorContains(t, err, "querying overview")
}

// --- Performance tests ---

func TestPerformance_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	now := time.Now()
	start := now.Add(-24 * time.Hour)

	rows := sqlmock.NewRows([]string{
		"p50_ms", "p95_ms", "p99_ms", "avg_ms", "max_ms",
		"avg_response_chars", "avg_request_chars",
	}).AddRow(25.0, 100.0, 250.0, 45.0, 500.0, 1024.0, 256.0)

	mock.ExpectQuery("SELECT").
		WithArgs(start, now).
		WillReturnRows(rows)

	result, err := store.Performance(context.Background(), &start, &now)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.InDelta(t, 25.0, result.P50MS, 0.01)
	assert.InDelta(t, 100.0, result.P95MS, 0.01)
	assert.InDelta(t, 250.0, result.P99MS, 0.01)
	assert.InDelta(t, 45.0, result.AvgMS, 0.01)
	assert.InDelta(t, 500.0, result.MaxMS, 0.01)
	assert.InDelta(t, 1024.0, result.AvgResponseChars, 0.01)
	assert.InDelta(t, 256.0, result.AvgRequestChars, 0.01)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPerformance_DefaultTimeRange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	rows := sqlmock.NewRows([]string{
		"p50_ms", "p95_ms", "p99_ms", "avg_ms", "max_ms",
		"avg_response_chars", "avg_request_chars",
	}).AddRow(0, 0, 0, 0, 0, 0, 0)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := store.Performance(context.Background(), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestPerformance_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))

	_, err = store.Performance(context.Background(), nil, nil)
	assert.ErrorContains(t, err, "querying performance")
}

// --- defaultTimeRange tests ---

func TestDefaultTimeRange_BothNil(t *testing.T) {
	before := time.Now()
	s, e := defaultTimeRange(nil, nil)
	after := time.Now()

	// End should be approximately now
	assert.True(t, e.After(before) || e.Equal(before))
	assert.True(t, e.Before(after) || e.Equal(after))

	// Start should be approximately 24h before now
	expectedStart := before.Add(-defaultMetricsWindow)
	assert.True(t, s.After(expectedStart.Add(-time.Second)))
	assert.True(t, s.Before(after.Add(-defaultMetricsWindow).Add(time.Second)))
}

func TestDefaultTimeRange_WithValues(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	s, e := defaultTimeRange(&start, &end)
	assert.Equal(t, start, s)
	assert.Equal(t, end, e)
}

func TestDefaultTimeRange_OnlyStart(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	s, e := defaultTimeRange(&start, nil)
	assert.Equal(t, start, s)
	assert.True(t, e.After(time.Now().Add(-time.Second)))
}

func TestDefaultTimeRange_OnlyEnd(t *testing.T) {
	end := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	s, _ := defaultTimeRange(nil, &end)
	// Start should be 24h before now (ignoring end)
	assert.True(t, s.After(time.Now().Add(-defaultMetricsWindow-time.Second)))
}

func TestTimeseries_RowsErr(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	rows := sqlmock.NewRows([]string{"bucket", "count", "success_count", "error_count", "avg_duration_ms"}).
		AddRow(time.Now(), 1, 1, 0, 10.0).
		RowError(0, fmt.Errorf("row iteration error"))
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	_, err = store.Timeseries(context.Background(), audit.TimeseriesFilter{
		Resolution: audit.ResolutionHour,
	})
	assert.Error(t, err)
}

func TestBreakdown_RowsErr(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	rows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"}).
		AddRow("tool", 1, 1.0, 10.0).
		RowError(0, fmt.Errorf("row iteration error"))
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	_, err = store.Breakdown(context.Background(), audit.BreakdownFilter{
		GroupBy: audit.BreakdownByToolName,
	})
	assert.Error(t, err)
}

// --- Enrichment tests ---

func TestEnrichment_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	now := time.Now()
	start := now.Add(-24 * time.Hour)

	rows := sqlmock.NewRows([]string{
		"total_calls", "enriched_calls", "enrichment_rate",
		"full_count", "summary_count", "reference_count", "none_count",
		"total_tokens_full", "total_tokens_dedup", "tokens_saved",
		"avg_tokens_full", "avg_tokens_dedup", "unique_sessions",
	}).AddRow(200, 150, 0.75, 80, 50, 20, 50, int64(100000), int64(40000), int64(60000), 500.0, 200.0, 25)

	mock.ExpectQuery("SELECT").
		WithArgs(start, now).
		WillReturnRows(rows)

	result, err := store.Enrichment(context.Background(), &start, &now)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 200, result.TotalCalls)
	assert.Equal(t, 150, result.EnrichedCalls)
	assert.InDelta(t, 0.75, result.EnrichmentRate, 0.01)
	assert.Equal(t, 80, result.FullCount)
	assert.Equal(t, 50, result.SummaryCount)
	assert.Equal(t, 20, result.ReferenceCount)
	assert.Equal(t, 50, result.NoneCount)
	assert.Equal(t, int64(100000), result.TotalTokensFull)
	assert.Equal(t, int64(40000), result.TotalTokensDedup)
	assert.Equal(t, int64(60000), result.TokensSaved)
	assert.InDelta(t, 500.0, result.AvgTokensFull, 0.01)
	assert.InDelta(t, 200.0, result.AvgTokensDedup, 0.01)
	assert.Equal(t, 25, result.UniqueSessions)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnrichment_DefaultTimeRange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	rows := sqlmock.NewRows([]string{
		"total_calls", "enriched_calls", "enrichment_rate",
		"full_count", "summary_count", "reference_count", "none_count",
		"total_tokens_full", "total_tokens_dedup", "tokens_saved",
		"avg_tokens_full", "avg_tokens_dedup", "unique_sessions",
	}).AddRow(0, 0, 0, 0, 0, 0, 0, int64(0), int64(0), int64(0), 0, 0, 0)

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := store.Enrichment(context.Background(), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.TotalCalls)
}

func TestEnrichment_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))

	_, err = store.Enrichment(context.Background(), nil, nil)
	assert.ErrorContains(t, err, "querying enrichment")
}

// --- Discovery tests ---

func TestDiscovery_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	now := time.Now()
	start := now.Add(-24 * time.Hour)

	// Pattern query result
	patternRows := sqlmock.NewRows([]string{
		"total_sessions", "discovery_sessions", "query_sessions",
		"discovery_before_query", "discovery_rate", "query_without_discovery",
	}).AddRow(100, 60, 80, 50, 0.60, 20)

	mock.ExpectQuery("WITH session_tools").
		WithArgs(start, now).
		WillReturnRows(patternRows)

	// Top tools query result
	toolRows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"}).
		AddRow("datahub_search", 120, 0.98, 15.5).
		AddRow("datahub_get_entity", 80, 1.0, 25.0)

	mock.ExpectQuery("SELECT tool_name").
		WithArgs(start, now).
		WillReturnRows(toolRows)

	result, err := store.Discovery(context.Background(), &start, &now)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 100, result.TotalSessions)
	assert.Equal(t, 60, result.DiscoverySessions)
	assert.Equal(t, 80, result.QuerySessions)
	assert.Equal(t, 50, result.DiscoveryBeforeQuery)
	assert.InDelta(t, 0.60, result.DiscoveryRate, 0.01)
	assert.Equal(t, 20, result.QueryWithoutDiscovery)
	require.Len(t, result.TopDiscoveryTools, 2)
	assert.Equal(t, "datahub_search", result.TopDiscoveryTools[0].Dimension)
	assert.Equal(t, 120, result.TopDiscoveryTools[0].Count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDiscovery_DefaultTimeRange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})

	patternRows := sqlmock.NewRows([]string{
		"total_sessions", "discovery_sessions", "query_sessions",
		"discovery_before_query", "discovery_rate", "query_without_discovery",
	}).AddRow(0, 0, 0, 0, 0, 0)
	mock.ExpectQuery("WITH session_tools").WillReturnRows(patternRows)

	toolRows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"})
	mock.ExpectQuery("SELECT tool_name").WillReturnRows(toolRows)

	result, err := store.Discovery(context.Background(), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.TotalSessions)
	assert.NotNil(t, result.TopDiscoveryTools) // empty slice, not nil
}

func TestDiscovery_PatternQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	mock.ExpectQuery("WITH session_tools").WillReturnError(fmt.Errorf("db error"))

	_, err = store.Discovery(context.Background(), nil, nil)
	assert.ErrorContains(t, err, "querying discovery patterns")
}

func TestDiscovery_TopToolsQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})

	patternRows := sqlmock.NewRows([]string{
		"total_sessions", "discovery_sessions", "query_sessions",
		"discovery_before_query", "discovery_rate", "query_without_discovery",
	}).AddRow(10, 5, 8, 4, 0.50, 3)
	mock.ExpectQuery("WITH session_tools").WillReturnRows(patternRows)

	mock.ExpectQuery("SELECT tool_name").WillReturnError(fmt.Errorf("tool query error"))

	_, err = store.Discovery(context.Background(), nil, nil)
	assert.ErrorContains(t, err, "querying top discovery tools")
}

func TestDiscovery_TopToolsScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})

	patternRows := sqlmock.NewRows([]string{
		"total_sessions", "discovery_sessions", "query_sessions",
		"discovery_before_query", "discovery_rate", "query_without_discovery",
	}).AddRow(10, 5, 8, 4, 0.50, 3)
	mock.ExpectQuery("WITH session_tools").WillReturnRows(patternRows)

	toolRows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"}).
		AddRow("tool", "bad", "bad", "bad")
	mock.ExpectQuery("SELECT tool_name").WillReturnRows(toolRows)

	_, err = store.Discovery(context.Background(), nil, nil)
	assert.Error(t, err)
}

func TestDiscovery_TopToolsRowsErr(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})

	patternRows := sqlmock.NewRows([]string{
		"total_sessions", "discovery_sessions", "query_sessions",
		"discovery_before_query", "discovery_rate", "query_without_discovery",
	}).AddRow(10, 5, 8, 4, 0.50, 3)
	mock.ExpectQuery("WITH session_tools").WillReturnRows(patternRows)

	toolRows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"}).
		AddRow("datahub_search", 10, 1.0, 15.0).
		RowError(0, fmt.Errorf("row iteration error"))
	mock.ExpectQuery("SELECT tool_name").WillReturnRows(toolRows)

	_, err = store.Discovery(context.Background(), nil, nil)
	assert.Error(t, err)
}

func TestDiscovery_EmptyTopTools(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})

	patternRows := sqlmock.NewRows([]string{
		"total_sessions", "discovery_sessions", "query_sessions",
		"discovery_before_query", "discovery_rate", "query_without_discovery",
	}).AddRow(10, 0, 10, 0, 0, 10)
	mock.ExpectQuery("WITH session_tools").WillReturnRows(patternRows)

	toolRows := sqlmock.NewRows([]string{"dimension", "count", "success_rate", "avg_duration_ms"})
	mock.ExpectQuery("SELECT tool_name").WillReturnRows(toolRows)

	result, err := store.Discovery(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, result.TopDiscoveryTools) // empty slice, not nil
	assert.Empty(t, result.TopDiscoveryTools)
}
