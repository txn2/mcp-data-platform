package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

const (
	testYear          = 2025
	testMonth         = 6
	testDurationMS    = 42
	testResponseChars = 512
	testRequestChars  = 128
	testFilterLimit   = 10
	testFilterOffset  = 5
	testPageLimit     = 25
	testPageOffset    = 50
	testCountResult   = 42
	testCountFiltered = 7
)

// selectColumns lists the 22 SELECT column names in scan order.
var selectColumns = []string{
	"id", "timestamp", "duration_ms", "request_id", "session_id",
	"user_id", "user_email", "persona", "tool_name", "toolkit_kind",
	"toolkit_name", "connection", "parameters", "success", "error_message",
	"response_chars", "request_chars", "content_blocks",
	"transport", "source", "enrichment_applied", "authorized",
}

func newTestEvent() audit.Event {
	return audit.Event{
		ID:                "evt-123",
		Timestamp:         time.Date(testYear, testMonth, 15, 10, 30, 0, 0, time.UTC), //nolint:revive // test fixture date
		DurationMS:        testDurationMS,
		RequestID:         "req-456",
		SessionID:         "sess-789",
		UserID:            "user-abc",
		UserEmail:         "test@example.com",
		Persona:           "analyst",
		ToolName:          "trino_query",
		ToolkitKind:       "trino",
		ToolkitName:       "primary",
		Connection:        "default",
		Parameters:        map[string]any{"sql": "SELECT 1"},
		Success:           true,
		ErrorMessage:      "",
		ResponseChars:     testResponseChars,
		RequestChars:      testRequestChars,
		ContentBlocks:     2,
		Transport:         "http",
		Source:            "mcp",
		EnrichmentApplied: true,
		Authorized:        true,
	}
}

func TestNew(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	t.Run("custom retention", func(t *testing.T) {
		store := New(db, Config{RetentionDays: 30})
		assert.Equal(t, 30, store.retentionDays)
		assert.Equal(t, db, store.db)
	})

	t.Run("default retention when zero", func(t *testing.T) {
		store := New(db, Config{RetentionDays: 0})
		assert.Equal(t, defaultRetentionDays, store.retentionDays)
	})
}

func TestLog_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})
	event := newTestEvent()

	paramsJSON, err := json.Marshal(event.Parameters)
	require.NoError(t, err)

	mock.ExpectExec("INSERT INTO audit_logs").WithArgs(
		event.ID,
		event.Timestamp,
		event.DurationMS,
		event.RequestID,
		event.SessionID,
		event.UserID,
		event.UserEmail,
		event.Persona,
		event.ToolName,
		event.ToolkitKind,
		event.ToolkitName,
		event.Connection,
		paramsJSON,
		event.Success,
		event.ErrorMessage,
		event.Timestamp.Format("2006-01-02"),
		event.ResponseChars,
		event.RequestChars,
		event.ContentBlocks,
		event.Transport,
		event.Source,
		event.EnrichmentApplied,
		event.Authorized,
	).WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Log(context.Background(), event)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLog_NilParameters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})
	event := newTestEvent()
	event.Parameters = nil

	mock.ExpectExec("INSERT INTO audit_logs").WithArgs(
		event.ID, event.Timestamp, event.DurationMS,
		event.RequestID, event.SessionID,
		event.UserID, event.UserEmail, event.Persona,
		event.ToolName, event.ToolkitKind, event.ToolkitName,
		event.Connection,
		[]byte("null"),
		event.Success, event.ErrorMessage,
		event.Timestamp.Format("2006-01-02"),
		event.ResponseChars, event.RequestChars, event.ContentBlocks,
		event.Transport, event.Source, event.EnrichmentApplied, event.Authorized,
	).WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Log(context.Background(), event)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLog_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})
	event := newTestEvent()

	mock.ExpectExec("INSERT INTO audit_logs").
		WillReturnError(errors.New("connection refused"))

	err = store.Log(context.Background(), event)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting audit log")
	assert.Contains(t, err.Error(), "connection refused")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func testEventRows(mock sqlmock.Sqlmock, events ...audit.Event) {
	rows := sqlmock.NewRows(selectColumns)
	for _, event := range events {
		paramsJSON, _ := json.Marshal(event.Parameters)
		rows.AddRow(
			event.ID, event.Timestamp, event.DurationMS,
			event.RequestID, event.SessionID,
			event.UserID, event.UserEmail, event.Persona,
			event.ToolName, event.ToolkitKind, event.ToolkitName,
			event.Connection,
			paramsJSON,
			event.Success, event.ErrorMessage,
			event.ResponseChars, event.RequestChars, event.ContentBlocks,
			event.Transport, event.Source, event.EnrichmentApplied, event.Authorized,
		)
	}
	mock.ExpectQuery("SELECT .+ FROM audit_logs").WillReturnRows(rows)
}

func TestQuery_NoFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})
	event := newTestEvent()
	testEventRows(mock, event)

	results, err := store.Query(context.Background(), audit.QueryFilter{})
	assert.NoError(t, err)
	require.Len(t, results, 1)
	assertEventEqual(t, event, results[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQuery_AllFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	startTime := time.Date(testYear, testMonth, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(testYear, testMonth, 30, 23, 59, 59, 0, time.UTC) //nolint:revive // test fixture date
	success := true

	filter := audit.QueryFilter{
		StartTime:   &startTime,
		EndTime:     &endTime,
		UserID:      "user-abc",
		SessionID:   "sess-789",
		ToolName:    "trino_query",
		ToolkitKind: "trino",
		Success:     &success,
		Limit:       testFilterLimit,
		Offset:      testFilterOffset,
	}

	event := newTestEvent()
	rows := sqlmock.NewRows(selectColumns)
	paramsJSON, _ := json.Marshal(event.Parameters)
	rows.AddRow(
		event.ID, event.Timestamp, event.DurationMS,
		event.RequestID, event.SessionID,
		event.UserID, event.UserEmail, event.Persona,
		event.ToolName, event.ToolkitKind, event.ToolkitName,
		event.Connection, paramsJSON,
		event.Success, event.ErrorMessage,
		event.ResponseChars, event.RequestChars, event.ContentBlocks,
		event.Transport, event.Source, event.EnrichmentApplied, event.Authorized,
	)

	mock.ExpectQuery("SELECT .+ FROM audit_logs").WithArgs(
		startTime,
		endTime,
		"user-abc",
		"sess-789",
		"trino_query",
		"trino",
		true,
	).WillReturnRows(rows)

	results, err := store.Query(context.Background(), filter)
	assert.NoError(t, err)
	require.Len(t, results, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQuery_SessionIDFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	filter := audit.QueryFilter{
		SessionID: "sess-specific",
	}

	rows := sqlmock.NewRows(selectColumns)
	mock.ExpectQuery("SELECT .+ FROM audit_logs").WithArgs(
		"sess-specific",
	).WillReturnRows(rows)

	results, err := store.Query(context.Background(), filter)
	assert.NoError(t, err)
	assert.Empty(t, results)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQuery_WithLimitOffset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	filter := audit.QueryFilter{
		Limit:  testPageLimit,
		Offset: testPageOffset,
	}

	rows := sqlmock.NewRows(selectColumns)
	mock.ExpectQuery("SELECT .+ FROM audit_logs").WillReturnRows(rows)

	results, err := store.Query(context.Background(), filter)
	assert.NoError(t, err)
	assert.Empty(t, results)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQuery_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	mock.ExpectQuery("SELECT .+ FROM audit_logs").
		WillReturnError(errors.New("db unavailable"))

	results, err := store.Query(context.Background(), audit.QueryFilter{})
	assert.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "querying audit logs")
	assert.Contains(t, err.Error(), "db unavailable")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQuery_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	rows := sqlmock.NewRows([]string{"id", "timestamp"}).
		AddRow("evt-1", "not-a-valid-timestamp")
	mock.ExpectQuery("SELECT .+ FROM audit_logs").WillReturnRows(rows)

	results, err := store.Query(context.Background(), audit.QueryFilter{})
	assert.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "scanning audit log row")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestScanEvent_AllFields(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})
	event := newTestEvent()

	paramsJSON, err := json.Marshal(event.Parameters)
	require.NoError(t, err)

	rows := sqlmock.NewRows(selectColumns).AddRow(
		event.ID,
		event.Timestamp,
		event.DurationMS,
		event.RequestID,
		event.SessionID,
		event.UserID,
		event.UserEmail,
		event.Persona,
		event.ToolName,
		event.ToolkitKind,
		event.ToolkitName,
		event.Connection,
		paramsJSON,
		event.Success,
		event.ErrorMessage,
		event.ResponseChars,
		event.RequestChars,
		event.ContentBlocks,
		event.Transport,
		event.Source,
		event.EnrichmentApplied,
		event.Authorized,
	)
	mock.ExpectQuery("SELECT .+ FROM audit_logs").WillReturnRows(rows)

	results, err := store.Query(context.Background(), audit.QueryFilter{})
	require.NoError(t, err)
	require.Len(t, results, 1)

	got := results[0]
	assertEventEqual(t, event, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanup(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		store := New(db, Config{RetentionDays: 30})

		mock.ExpectExec("DELETE FROM audit_logs WHERE timestamp").
			WillReturnResult(sqlmock.NewResult(0, 5))

		err = store.Cleanup(context.Background())
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		store := New(db, Config{RetentionDays: 30})

		mock.ExpectExec("DELETE FROM audit_logs WHERE timestamp").
			WillReturnError(errors.New("cleanup failed"))

		err = store.Cleanup(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cleaning up audit logs")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestClose_NilCancel_NoPanic(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	// Close without ever calling StartCleanupRoutine — must not panic.
	assert.NoError(t, store.Close())
}

func TestClose_StopsCleanupRoutine(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 7})

	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec("DELETE FROM audit_logs").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM audit_logs").
		WillReturnResult(sqlmock.NewResult(0, 0))

	store.StartCleanupRoutine(10 * time.Millisecond)

	// Let at least one cleanup tick fire.
	time.Sleep(50 * time.Millisecond)

	// Close should cancel and wait for the goroutine to exit.
	assert.NoError(t, store.Close())
}

func TestStartCleanupRoutine(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 7})

	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec("DELETE FROM audit_logs").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM audit_logs").
		WillReturnResult(sqlmock.NewResult(0, 0))

	store.StartCleanupRoutine(10 * time.Millisecond)

	time.Sleep(50 * time.Millisecond)
	assert.NoError(t, store.Close())
}

func TestApplyAuditFilter(t *testing.T) {
	startTime := time.Date(testYear, testMonth, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(testYear, testMonth, 30, 23, 59, 59, 0, time.UTC) //nolint:revive // test fixture date
	success := true

	tests := []struct {
		name         string
		filter       audit.QueryFilter
		wantArgCount int
		wantContains []string
	}{
		{
			name:         "empty filter",
			filter:       audit.QueryFilter{},
			wantArgCount: 0,
		},
		{
			name:         "id only",
			filter:       audit.QueryFilter{ID: "evt-1"},
			wantArgCount: 1,
			wantContains: []string{"id = $1"},
		},
		{
			name:         "start_time only",
			filter:       audit.QueryFilter{StartTime: &startTime},
			wantArgCount: 1,
			wantContains: []string{"timestamp >= $1"},
		},
		{
			name:         "end_time only",
			filter:       audit.QueryFilter{EndTime: &endTime},
			wantArgCount: 1,
			wantContains: []string{"timestamp <= $1"},
		},
		{
			name:         "user_id only",
			filter:       audit.QueryFilter{UserID: "user-1"},
			wantArgCount: 1,
			wantContains: []string{"user_id = $1"},
		},
		{
			name:         "session_id only",
			filter:       audit.QueryFilter{SessionID: "sess-1"},
			wantArgCount: 1,
			wantContains: []string{"session_id = $1"},
		},
		{
			name:         "tool_name only",
			filter:       audit.QueryFilter{ToolName: "trino_query"},
			wantArgCount: 1,
			wantContains: []string{"tool_name = $1"},
		},
		{
			name:         "toolkit_kind only",
			filter:       audit.QueryFilter{ToolkitKind: "trino"},
			wantArgCount: 1,
			wantContains: []string{"toolkit_kind = $1"},
		},
		{
			name:         "success only",
			filter:       audit.QueryFilter{Success: &success},
			wantArgCount: 1,
			wantContains: []string{"success = $1"},
		},
		{
			name:         "search only",
			filter:       audit.QueryFilter{Search: "trino"},
			wantArgCount: 6, //nolint:revive // 6 ILIKE columns
			wantContains: []string{"ILIKE"},
		},
		{
			name: "all filters",
			filter: audit.QueryFilter{
				ID:          "evt-1",
				StartTime:   &startTime,
				EndTime:     &endTime,
				UserID:      "user-1",
				SessionID:   "sess-1",
				ToolName:    "trino_query",
				ToolkitKind: "trino",
				Success:     &success,
			},
			wantArgCount: 8, //nolint:revive // 8 filters
			wantContains: []string{
				"id = $1",
				"timestamp >= $2",
				"timestamp <= $3",
				"user_id = $4",
				"session_id = $5",
				"tool_name = $6",
				"toolkit_kind = $7",
				"success = $8",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			qb := applyAuditFilter(psq.Select("*").From("audit_logs"), tc.filter)
			query, args, err := qb.ToSql()
			require.NoError(t, err)
			assert.Len(t, args, tc.wantArgCount)
			for _, s := range tc.wantContains {
				assert.Contains(t, query, s)
			}
		})
	}
}

func TestQuery_MultipleRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	event1 := newTestEvent()
	event1.ID = "evt-1"
	event1.ToolName = "trino_query"

	event2 := newTestEvent()
	event2.ID = "evt-2"
	event2.ToolName = "datahub_search"

	rows := sqlmock.NewRows(selectColumns)
	for _, ev := range []audit.Event{event1, event2} {
		p, _ := json.Marshal(ev.Parameters)
		rows.AddRow(
			ev.ID, ev.Timestamp, ev.DurationMS,
			ev.RequestID, ev.SessionID,
			ev.UserID, ev.UserEmail, ev.Persona,
			ev.ToolName, ev.ToolkitKind, ev.ToolkitName,
			ev.Connection, p,
			ev.Success, ev.ErrorMessage,
			ev.ResponseChars, ev.RequestChars, ev.ContentBlocks,
			ev.Transport, ev.Source, ev.EnrichmentApplied, ev.Authorized,
		)
	}
	mock.ExpectQuery("SELECT .+ FROM audit_logs").WillReturnRows(rows)

	results, err := store.Query(context.Background(), audit.QueryFilter{})
	assert.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "evt-1", results[0].ID)
	assert.Equal(t, "evt-2", results[1].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQuery_EmptyParameters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})
	event := newTestEvent()
	event.Parameters = nil

	rows := sqlmock.NewRows(selectColumns).AddRow(
		event.ID, event.Timestamp, event.DurationMS,
		event.RequestID, event.SessionID,
		event.UserID, event.UserEmail, event.Persona,
		event.ToolName, event.ToolkitKind, event.ToolkitName,
		event.Connection,
		[]byte{},
		event.Success, event.ErrorMessage,
		event.ResponseChars, event.RequestChars, event.ContentBlocks,
		event.Transport, event.Source, event.EnrichmentApplied, event.Authorized,
	)
	mock.ExpectQuery("SELECT .+ FROM audit_logs").WillReturnRows(rows)

	results, err := store.Query(context.Background(), audit.QueryFilter{})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Nil(t, results[0].Parameters)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCount_NoFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	rows := sqlmock.NewRows([]string{"count"}).AddRow(testCountResult)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(rows)

	count, err := store.Count(context.Background(), audit.QueryFilter{})
	assert.NoError(t, err)
	assert.Equal(t, testCountResult, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCount_WithFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	success := true
	filter := audit.QueryFilter{
		UserID:  "user-abc",
		Success: &success,
	}

	rows := sqlmock.NewRows([]string{"count"}).AddRow(testCountFiltered)
	mock.ExpectQuery("SELECT COUNT").WithArgs("user-abc", true).WillReturnRows(rows)

	count, err := store.Count(context.Background(), filter)
	assert.NoError(t, err)
	assert.Equal(t, testCountFiltered, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCount_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	mock.ExpectQuery("SELECT COUNT").
		WillReturnError(errors.New("count failed"))

	count, err := store.Count(context.Background(), audit.QueryFilter{})
	assert.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "counting audit logs")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQuery_IDFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	event := newTestEvent()
	event.ID = "evt-specific"

	rows := sqlmock.NewRows(selectColumns)
	paramsJSON, _ := json.Marshal(event.Parameters)
	rows.AddRow(
		event.ID, event.Timestamp, event.DurationMS,
		event.RequestID, event.SessionID,
		event.UserID, event.UserEmail, event.Persona,
		event.ToolName, event.ToolkitKind, event.ToolkitName,
		event.Connection, paramsJSON,
		event.Success, event.ErrorMessage,
		event.ResponseChars, event.RequestChars, event.ContentBlocks,
		event.Transport, event.Source, event.EnrichmentApplied, event.Authorized,
	)
	mock.ExpectQuery("SELECT .+ FROM audit_logs").WithArgs("evt-specific").WillReturnRows(rows)

	results, err := store.Query(context.Background(), audit.QueryFilter{ID: "evt-specific"})
	assert.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "evt-specific", results[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExecuteQuery_CapsCapacity(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	// Use an excessive limit that would cause a huge allocation without the cap.
	filter := audit.QueryFilter{
		Limit: maxQueryCapacity * 2,
	}

	rows := sqlmock.NewRows(selectColumns)
	mock.ExpectQuery("SELECT .+ FROM audit_logs").WillReturnRows(rows)

	results, err := store.Query(context.Background(), filter)
	assert.NoError(t, err)
	assert.Empty(t, results)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQuery_SortBy(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	rows := sqlmock.NewRows(selectColumns)
	mock.ExpectQuery("SELECT .+ FROM audit_logs ORDER BY duration_ms ASC").WillReturnRows(rows)

	results, err := store.Query(context.Background(), audit.QueryFilter{
		SortBy:    "duration_ms",
		SortOrder: audit.SortAsc,
	})
	assert.NoError(t, err)
	assert.Empty(t, results)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQuery_SortByInvalidColumn(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	// Invalid sort column falls back to timestamp, order still applied
	rows := sqlmock.NewRows(selectColumns)
	mock.ExpectQuery("SELECT .+ FROM audit_logs ORDER BY timestamp ASC").WillReturnRows(rows)

	results, err := store.Query(context.Background(), audit.QueryFilter{
		SortBy:    "password",
		SortOrder: audit.SortAsc,
	})
	assert.NoError(t, err)
	assert.Empty(t, results)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDistinct_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	rows := sqlmock.NewRows([]string{"user_id"}).
		AddRow("alice@acme.com").
		AddRow("bob@acme.com")
	mock.ExpectQuery("SELECT DISTINCT user_id FROM audit_logs").WillReturnRows(rows)

	values, err := store.Distinct(context.Background(), "user_id", nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, []string{"alice@acme.com", "bob@acme.com"}, values)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDistinct_WithTimeRange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})
	startTime := time.Date(testYear, testMonth, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(testYear, testMonth, 30, 23, 59, 59, 0, time.UTC) //nolint:revive // test fixture date

	rows := sqlmock.NewRows([]string{"tool_name"}).AddRow("trino_query")
	mock.ExpectQuery("SELECT DISTINCT tool_name FROM audit_logs").
		WithArgs(startTime, endTime).
		WillReturnRows(rows)

	values, err := store.Distinct(context.Background(), "tool_name", &startTime, &endTime)
	assert.NoError(t, err)
	assert.Equal(t, []string{"trino_query"}, values)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDistinct_InvalidColumn(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	values, err := store.Distinct(context.Background(), "password", nil, nil)
	assert.Error(t, err)
	assert.Nil(t, values)
	assert.Contains(t, err.Error(), "distinct not supported")
}

func TestDistinct_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	mock.ExpectQuery("SELECT DISTINCT user_id FROM audit_logs").
		WillReturnError(errors.New("db down"))

	values, err := store.Distinct(context.Background(), "user_id", nil, nil)
	assert.Error(t, err)
	assert.Nil(t, values)
	assert.Contains(t, err.Error(), "querying distinct user_id")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDistinct_EmptyResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	rows := sqlmock.NewRows([]string{"user_id"})
	mock.ExpectQuery("SELECT DISTINCT user_id FROM audit_logs").WillReturnRows(rows)

	values, err := store.Distinct(context.Background(), "user_id", nil, nil)
	assert.NoError(t, err)
	assert.Nil(t, values)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDistinctPairs_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	rows := sqlmock.NewRows([]string{"user_id", "user_email"}).
		AddRow("uid-1", "alice@acme.com").
		AddRow("uid-2", "bob@acme.com")
	mock.ExpectQuery("SELECT DISTINCT user_id, user_email FROM audit_logs").
		WillReturnRows(rows)

	result, err := store.DistinctPairs(context.Background(), "user_id", "user_email", nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, map[string]string{"uid-1": "alice@acme.com", "uid-2": "bob@acme.com"}, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDistinctPairs_WithTimeRange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})
	startTime := time.Date(testYear, testMonth, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(testYear, testMonth, 30, 23, 59, 59, 0, time.UTC) //nolint:revive // test fixture date

	rows := sqlmock.NewRows([]string{"user_id", "user_email"}).
		AddRow("uid-1", "alice@acme.com")
	mock.ExpectQuery("SELECT DISTINCT user_id, user_email FROM audit_logs").
		WithArgs("", startTime, endTime).
		WillReturnRows(rows)

	result, err := store.DistinctPairs(context.Background(), "user_id", "user_email", &startTime, &endTime)
	assert.NoError(t, err)
	assert.Equal(t, map[string]string{"uid-1": "alice@acme.com"}, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDistinctPairs_InvalidColumn(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	result, err := store.DistinctPairs(context.Background(), "password", "user_email", nil, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "distinct pairs not supported")
}

func TestDistinctPairs_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	mock.ExpectQuery("SELECT DISTINCT user_id, user_email FROM audit_logs").
		WillReturnError(errors.New("db down"))

	result, err := store.DistinctPairs(context.Background(), "user_id", "user_email", nil, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "querying distinct pairs")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDistinctPairs_EmptyResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{RetentionDays: 90})

	rows := sqlmock.NewRows([]string{"user_id", "user_email"})
	mock.ExpectQuery("SELECT DISTINCT user_id, user_email FROM audit_logs").
		WillReturnRows(rows)

	result, err := store.DistinctPairs(context.Background(), "user_id", "user_email", nil, nil)
	assert.NoError(t, err)
	assert.Empty(t, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInterfaceCompliance(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	var _ audit.Logger = store
}

func assertEventEqual(t *testing.T, expected, got audit.Event) {
	t.Helper()
	assert.Equal(t, expected.ID, got.ID)
	assert.Equal(t, expected.Timestamp.UTC(), got.Timestamp.UTC())
	assert.Equal(t, expected.DurationMS, got.DurationMS)
	assert.Equal(t, expected.RequestID, got.RequestID)
	assert.Equal(t, expected.SessionID, got.SessionID)
	assert.Equal(t, expected.UserID, got.UserID)
	assert.Equal(t, expected.UserEmail, got.UserEmail)
	assert.Equal(t, expected.Persona, got.Persona)
	assert.Equal(t, expected.ToolName, got.ToolName)
	assert.Equal(t, expected.ToolkitKind, got.ToolkitKind)
	assert.Equal(t, expected.ToolkitName, got.ToolkitName)
	assert.Equal(t, expected.Connection, got.Connection)
	assert.Equal(t, expected.Parameters, got.Parameters)
	assert.Equal(t, expected.Success, got.Success)
	assert.Equal(t, expected.ErrorMessage, got.ErrorMessage)
	assert.Equal(t, expected.ResponseChars, got.ResponseChars)
	assert.Equal(t, expected.RequestChars, got.RequestChars)
	assert.Equal(t, expected.ContentBlocks, got.ContentBlocks)
	assert.Equal(t, expected.Transport, got.Transport)
	assert.Equal(t, expected.Source, got.Source)
	assert.Equal(t, expected.EnrichmentApplied, got.EnrichmentApplied)
	assert.Equal(t, expected.Authorized, got.Authorized)
}
