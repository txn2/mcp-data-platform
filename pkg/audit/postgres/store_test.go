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
		testFilterLimit,
		testFilterOffset,
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
	mock.ExpectQuery("SELECT .+ FROM audit_logs").WithArgs(
		testPageLimit,
		testPageOffset,
	).WillReturnRows(rows)

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

func TestQueryBuilder_Internal(t *testing.T) {
	t.Run("empty where clause", func(t *testing.T) {
		b := newQueryBuilder()
		assert.Empty(t, b.whereClause())
	})

	t.Run("single condition", func(t *testing.T) {
		b := newQueryBuilder()
		b.addCondition("user_id", "test")
		assert.Equal(t, " WHERE user_id = $1", b.whereClause())
		assert.Equal(t, []any{"test"}, b.args)
	})

	t.Run("multiple conditions", func(t *testing.T) {
		b := newQueryBuilder()
		b.addCondition("user_id", "test")
		b.addCondition("tool_name", "query")
		assert.Equal(t, " WHERE user_id = $1 AND tool_name = $2", b.whereClause())
	})

	t.Run("time condition", func(t *testing.T) {
		b := newQueryBuilder()
		ts := time.Now()
		b.addTimeCondition("timestamp", ">=", ts)
		assert.Contains(t, b.whereClause(), "timestamp >= $1")
	})

	t.Run("limit clause zero", func(t *testing.T) {
		b := newQueryBuilder()
		assert.Empty(t, b.limitClause(0))
	})

	t.Run("limit clause positive", func(t *testing.T) {
		b := newQueryBuilder()
		assert.Contains(t, b.limitClause(testFilterLimit), "LIMIT $1")
	})

	t.Run("offset clause zero", func(t *testing.T) {
		b := newQueryBuilder()
		assert.Empty(t, b.offsetClause(0))
	})

	t.Run("offset clause positive", func(t *testing.T) {
		b := newQueryBuilder()
		assert.Contains(t, b.offsetClause(testFilterOffset), "OFFSET $1")
	})
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
	mock.ExpectQuery("SELECT .+ FROM audit_logs").WithArgs(
		filter.Limit,
	).WillReturnRows(rows)

	results, err := store.Query(context.Background(), filter)
	assert.NoError(t, err)
	assert.Empty(t, results)
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
