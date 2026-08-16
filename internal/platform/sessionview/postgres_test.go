package sessionview

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSessionID = "dps_9f2c1a4b8e7d6c5a4b3e2d1c0f9e8a7b"
	testCallCount = 5
)

// summaryColumns lists the session-row SELECT columns in scan order.
var summaryColumns = []string{
	"session_id", "started_at", "last_active_at", "call_count", "failure_count",
	"user_id", "user_email", "persona", "tools", "connections",
	"asset_count", "insight_count",
}

func testTime() time.Time {
	return time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC) //nolint:revive // test fixture date
}

// newMock returns a store over a mocked database plus the mock controller.
func newMock(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	return NewPostgresStore(db), mock
}

func summaryRow() *sqlmock.Rows {
	return sqlmock.NewRows(summaryColumns).AddRow(
		testSessionID, testTime(), testTime().Add(time.Minute), testCallCount, 1,
		"user-1", "analyst@example.com", "data-engineer",
		"{search,trino_query}", "{acme-warehouse}",
		1, 2,
	)
}

func TestList_ProjectsSummary(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM audit_logs").WillReturnRows(summaryRow())

	got, err := store.List(context.Background(), Filter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 1)

	assert.Equal(t, testSessionID, got[0].SessionID)
	assert.Equal(t, KindAgent, got[0].Kind, "kind is derived from the id prefix")
	assert.Equal(t, testCallCount, got[0].CallCount)
	assert.Equal(t, 1, got[0].FailureCount)
	assert.Equal(t, []string{"search", "trino_query"}, got[0].Tools)
	assert.Equal(t, []string{"acme-warehouse"}, got[0].Connections)
	assert.Equal(t, 1, got[0].AssetCount)
	assert.Equal(t, 2, got[0].InsightCount)
	assert.Equal(t, "analyst@example.com", got[0].UserEmail)
	assert.Equal(t, "data-engineer", got[0].Persona)
}

// A session whose rows carry no caller, persona, or tool names rolls up to
// SQL NULLs. They must read as empty values and empty slices, never as a scan
// failure and never as a null array in the response.
func TestList_NullAggregates(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM audit_logs").WillReturnRows(
		sqlmock.NewRows(summaryColumns).AddRow(
			"abc123", testTime(), testTime(), 1, 0,
			nil, nil, nil, nil, nil, 0, 0,
		),
	)

	got, err := store.List(context.Background(), Filter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, KindTransport, got[0].Kind)
	assert.Empty(t, got[0].UserID)
	assert.Empty(t, got[0].Persona)
	assert.NotNil(t, got[0].Tools)
	assert.Empty(t, got[0].Tools)
	assert.NotNil(t, got[0].Connections)
}

func TestList_QueryError(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM audit_logs").WillReturnError(errors.New("boom"))

	_, err := store.List(context.Background(), Filter{})
	require.Error(t, err)
}

// Every filter must reach the SQL: an accepted-but-ignored filter is a lie the
// UI cannot see through.
func TestList_FiltersReachSQL(t *testing.T) {
	start := testTime()
	end := testTime().Add(time.Hour)

	tests := []struct {
		name     string
		filter   Filter
		wantSQL  []string
		wantArgs []driverArg
	}{
		{
			name:     "user",
			filter:   Filter{UserID: "user-1"},
			wantSQL:  []string{"user_id = "},
			wantArgs: []driverArg{{"user-1"}},
		},
		{
			name:     "agent kind",
			filter:   Filter{Kind: KindAgent},
			wantSQL:  []string{"starts_with(session_id,"},
			wantArgs: []driverArg{{"dps_"}},
		},
		{
			name:    "transport kind excludes every prefix",
			filter:  Filter{Kind: KindTransport},
			wantSQL: []string{"NOT starts_with(session_id,"},
			wantArgs: []driverArg{
				{"dps_"}, {"dpp_"}, {"dpx_"},
			},
		},
		{
			name:    "unknown kind matches nothing",
			filter:  Filter{Kind: Kind("bogus")},
			wantSQL: []string{"false"},
		},
		{
			name:    "time range",
			filter:  Filter{StartTime: &start, EndTime: &end},
			wantSQL: []string{"timestamp >=", "timestamp <="},
		},
		{
			name:    "has failures",
			filter:  Filter{HasFailures: true},
			wantSQL: []string{"HAVING COUNT(*) FILTER (WHERE NOT success) > 0"},
		},
		{
			name:    "has assets",
			filter:  Filter{HasAssets: true},
			wantSQL: []string{"EXISTS (SELECT 1 FROM portal_assets"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args := listSQL(t, tt.filter)
			for _, want := range tt.wantSQL {
				assert.Contains(t, sql, want)
			}
			for _, want := range tt.wantArgs {
				assert.Contains(t, args, want.value)
			}
		})
	}
}

// driverArg names an expected bound argument in a readable table.
type driverArg struct{ value any }

// listSQL renders the list statement for a filter without a database.
func listSQL(t *testing.T, filter Filter) (query string, args []any) {
	t.Helper()
	query, args, err := sessionRows(filter).ToSql()
	require.NoError(t, err)
	return query, args
}

func TestList_PagesWithLimitAndOffset(t *testing.T) {
	sql, _ := listSQL(t, Filter{})
	assert.NotContains(t, sql, "LIMIT")

	store, mock := newMock(t)
	mock.ExpectQuery("LIMIT 5 OFFSET 10").WillReturnRows(summaryRow())
	_, err := store.List(context.Background(), Filter{Limit: 5, Offset: 10})
	require.NoError(t, err)
}

func TestCount_CountsSessionsNotEvents(t *testing.T) {
	store, mock := newMock(t)
	// The count wraps the grouped rollup, so it counts one row per session
	// rather than one per audit event.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM \\(SELECT g.session_id").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	got, err := store.Count(context.Background(), Filter{Limit: 10, Offset: 20})
	require.NoError(t, err)
	assert.Equal(t, 3, got)
}

func TestCount_HasAssetsNarrows(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("EXISTS \\(SELECT 1 FROM portal_assets").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	got, err := store.Count(context.Background(), Filter{HasAssets: true})
	require.NoError(t, err)
	assert.Equal(t, 1, got)
}

func TestCount_QueryError(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("boom"))

	_, err := store.Count(context.Background(), Filter{})
	require.Error(t, err)
}

func TestGet_NotFoundWhenNoCalls(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM audit_logs").WillReturnRows(sqlmock.NewRows(summaryColumns))

	got, err := store.Get(context.Background(), Scope{SessionID: testSessionID})
	assert.ErrorIs(t, err, ErrNotFound, "an id with no calls is not a session")
	assert.Nil(t, got)
}

func TestGet_ReturnsSummary(t *testing.T) {
	store, mock := newMock(t)
	// The empty string is the "session_id <> ''" bound argument the rollup
	// always carries; the id follows it.
	mock.ExpectQuery("FROM audit_logs").WithArgs("", testSessionID).WillReturnRows(summaryRow())

	got, err := store.Get(context.Background(), Scope{SessionID: testSessionID})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, testSessionID, got.SessionID)
}

func TestTimeline_OrdersOldestFirstWithTotal(t *testing.T) {
	store, mock := newMock(t)
	rows := sqlmock.NewRows([]string{
		"id", "timestamp", "tool_name", "purpose", "toolkit_kind",
		"connection", "success", "error_message", "duration_ms",
	}).
		AddRow("evt-1", testTime(), "search", "Finding the revenue table.", "", "", true, "", 12).
		AddRow("evt-2", testTime().Add(time.Second), "trino_query", nil, "trino", "acme-warehouse", false, "syntax error", 40)

	mock.ExpectQuery("ORDER BY timestamp ASC, id ASC").WithArgs(testSessionID).WillReturnRows(rows)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM audit_logs").WithArgs(testSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(testCallCount))

	entries, total, err := store.Timeline(context.Background(), Scope{SessionID: testSessionID})
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, testCallCount, total, "total is the session's full call count, not the page")
	assert.Equal(t, "Finding the revenue table.", entries[0].Purpose)
	assert.Empty(t, entries[1].Purpose, "a NULL purpose reads as empty, not a scan failure")
	assert.False(t, entries[1].Success)
	assert.Equal(t, "syntax error", entries[1].ErrorMessage)
}

func TestTimeline_Errors(t *testing.T) {
	t.Run("page query fails", func(t *testing.T) {
		store, mock := newMock(t)
		mock.ExpectQuery("FROM audit_logs").WillReturnError(errors.New("boom"))
		_, _, err := store.Timeline(context.Background(), Scope{SessionID: testSessionID, Limit: 10})
		require.Error(t, err)
	})

	t.Run("count query fails", func(t *testing.T) {
		store, mock := newMock(t)
		mock.ExpectQuery("ORDER BY timestamp ASC").WillReturnRows(sqlmock.NewRows([]string{
			"id", "timestamp", "tool_name", "purpose", "toolkit_kind",
			"connection", "success", "error_message", "duration_ms",
		}))
		mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("boom"))
		_, _, err := store.Timeline(context.Background(), Scope{SessionID: testSessionID, Limit: 10})
		require.Error(t, err)
	})
}

// A scoped timeline narrows both the page and the total it is paged against.
// Narrowing only one of them would page a caller's own calls against someone
// else's count, which reads as a session with pages that are not there.
func TestTimeline_ScopedToTheCallerNarrowsPageAndTotal(t *testing.T) {
	store, mock := newMock(t)
	timelineCols := []string{
		"id", "timestamp", "tool_name", "purpose", "toolkit_kind",
		"connection", "success", "error_message", "duration_ms",
	}
	mock.ExpectQuery("ORDER BY timestamp ASC").WithArgs(testSessionID, "user-1").
		WillReturnRows(sqlmock.NewRows(timelineCols).
			AddRow("evt-1", testTime(), "search", nil, "", "", true, "", 12))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM audit_logs").WithArgs(testSessionID, "user-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	entries, total, err := store.Timeline(context.Background(), Scope{
		SessionID: testSessionID,
		UserID:    "user-1",
	})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 1, total)
}

func TestTimeline_OffsetPages(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("LIMIT 2 OFFSET 2").WillReturnRows(sqlmock.NewRows([]string{
		"id", "timestamp", "tool_name", "purpose", "toolkit_kind",
		"connection", "success", "error_message", "duration_ms",
	}))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(testCallCount))

	_, total, err := store.Timeline(context.Background(), Scope{SessionID: testSessionID, Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Equal(t, testCallCount, total)
}

func TestAssets_ExcludesDeleted(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("deleted_at IS NULL").WithArgs(testSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "content_type", "created_at"}).
			AddRow("ast_1", "Q3 revenue", "text/csv", testTime()))

	got, err := store.Assets(context.Background(), testSessionID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "ast_1", got[0].ID)
	assert.Equal(t, "text/csv", got[0].ContentType)
}

func TestAssets_EmptyIsNotNull(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM portal_assets").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "content_type", "created_at"}))

	got, err := store.Assets(context.Background(), testSessionID)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestAssets_QueryError(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM portal_assets").WillReturnError(errors.New("boom"))

	_, err := store.Assets(context.Background(), testSessionID)
	require.Error(t, err)
}

func TestInsights_ReadsCapturedRows(t *testing.T) {
	store, mock := newMock(t)
	// Every value the insight convention is defined by is bound, not
	// spliced: the session id first, then the metadata keys and statuses.
	mock.ExpectQuery("FROM memory_records").
		WithArgs(testSessionID, "insight_status", "legacy_status",
			"active", "pending", "archived", "rejected",
			"knowledge", "session_id").
		WillReturnRows(sqlmock.NewRows([]string{"id", "category", "content", "status", "created_at"}).
			AddRow("ins_1", "data_quality", "amount is null for canceled rows", "pending", testTime()))

	got, err := store.Insights(context.Background(), testSessionID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "data_quality", got[0].Category)
	assert.Equal(t, "pending", got[0].Status)
}

func TestInsights_QueryError(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM memory_records").WillReturnError(errors.New("boom"))

	_, err := store.Insights(context.Background(), testSessionID)
	require.Error(t, err)
}

func TestListCapacity(t *testing.T) {
	assert.Equal(t, 50, listCapacity(0))
	assert.Equal(t, 50, listCapacity(maxListCapacity+1), "a hostile page size does not preallocate")
	assert.Equal(t, 25, listCapacity(25))
}
