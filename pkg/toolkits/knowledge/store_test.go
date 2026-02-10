package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insightSelectColumns lists the columns returned by Get and List queries.
var insightSelectColumns = []string{
	"id", "created_at", "session_id", "captured_by", "persona", "category",
	"insight_text", "confidence", "entity_urns", "related_columns",
	"suggested_actions", "status", "reviewed_by", "reviewed_at",
	"review_notes", "applied_by", "applied_at", "changeset_ref",
}

// --- noopStore tests ---

func TestNoopStore_Insert(t *testing.T) {
	store := NewNoopStore()
	err := store.Insert(context.Background(), Insight{
		ID:          "test-id",
		Category:    "correction",
		InsightText: "This is a test insight",
		Status:      "pending",
	})
	assert.NoError(t, err)
}

func TestNoopStore_Get(t *testing.T) {
	store := NewNoopStore()
	insight, err := store.Get(context.Background(), "any-id")
	assert.Nil(t, insight)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insight not found")
}

func TestNoopStore_List(t *testing.T) {
	store := NewNoopStore()
	insights, total, err := store.List(context.Background(), InsightFilter{})
	assert.NoError(t, err)
	assert.Nil(t, insights)
	assert.Equal(t, 0, total)
}

func TestNoopStore_UpdateStatus(t *testing.T) {
	store := NewNoopStore()
	err := store.UpdateStatus(context.Background(), "id", "approved", "admin", "looks good")
	assert.NoError(t, err)
}

func TestNoopStore_Update(t *testing.T) {
	store := NewNoopStore()
	err := store.Update(context.Background(), "id", InsightUpdate{InsightText: "updated"})
	assert.NoError(t, err)
}

func TestNoopStore_Stats(t *testing.T) {
	store := NewNoopStore()
	stats, err := store.Stats(context.Background(), InsightFilter{})
	require.NoError(t, err)
	require.NotNil(t, stats)
	assert.NotNil(t, stats.ByCategory)
	assert.NotNil(t, stats.ByConfidence)
	assert.NotNil(t, stats.ByStatus)
}

func TestNoopStore_MarkApplied(t *testing.T) {
	store := NewNoopStore()
	err := store.MarkApplied(context.Background(), "id", "admin", "cs-1")
	assert.NoError(t, err)
}

func TestNoopStore_Supersede(t *testing.T) {
	store := NewNoopStore()
	count, err := store.Supersede(context.Background(), "urn:li:dataset:foo", "exclude-id")
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

// --- postgresStore Insert tests ---

func TestPostgresStore_Insert(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := NewPostgresStore(db)
	insight := Insight{
		ID:          "insight-1",
		SessionID:   "sess-1",
		CapturedBy:  "user-1",
		Persona:     "analyst",
		Category:    "correction",
		InsightText: "Column name is misleading",
		Confidence:  "high",
		EntityURNs:  []string{"urn:li:dataset:foo"},
		RelatedColumns: []RelatedColumn{
			{URN: "urn:li:dataset:foo", Column: "col1", Relevance: "primary"},
		},
		SuggestedActions: []SuggestedAction{
			{ActionType: "add_tag", Target: "urn:li:dataset:foo", Detail: "misleading"}, //nolint:revive // test value
		},
		Status: "pending",
	}

	mock.ExpectExec("INSERT INTO knowledge_insights").
		WithArgs(
			insight.ID, insight.SessionID, insight.CapturedBy, insight.Persona,
			insight.Category, insight.InsightText, insight.Confidence,
			sqlmock.AnyArg(), // entity_urns JSON
			sqlmock.AnyArg(), // related_columns JSON
			sqlmock.AnyArg(), // suggested_actions JSON
			insight.Status,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.Insert(context.Background(), insight)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Insert_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := NewPostgresStore(db)
	insight := Insight{
		ID:               "insight-2",
		Category:         "data_quality",
		InsightText:      "Timestamps are wrong",
		Confidence:       "medium",
		EntityURNs:       []string{},
		RelatedColumns:   []RelatedColumn{},
		SuggestedActions: []SuggestedAction{},
		Status:           "pending",
	}

	mock.ExpectExec("INSERT INTO knowledge_insights").
		WithArgs(
			insight.ID, insight.SessionID, insight.CapturedBy, insight.Persona,
			insight.Category, insight.InsightText, insight.Confidence,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			insight.Status,
		).
		WillReturnError(errors.New("connection refused"))

	err = store.Insert(context.Background(), insight)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting insight")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- postgresStore Get tests ---

func TestPostgresStore_Get(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now().Truncate(time.Second)
	reviewedAt := now.Add(-time.Hour)

	rows := sqlmock.NewRows(insightSelectColumns).AddRow(
		"ins-1", now, "sess-1", "user-1", "analyst", "correction",
		"Column misleading", "high",
		`["urn:li:dataset:foo"]`,
		`[{"urn":"urn:li:dataset:foo","column":"col1","relevance":"primary"}]`,
		`[{"action_type":"add_tag","target":"tgt","detail":"d"}]`,
		"approved", "reviewer", reviewedAt, "lgtm",
		"", sql.NullTime{}, "",
	)

	mock.ExpectQuery("SELECT .+ FROM knowledge_insights WHERE id").
		WithArgs("ins-1").
		WillReturnRows(rows)

	insight, err := store.Get(context.Background(), "ins-1")
	require.NoError(t, err)
	require.NotNil(t, insight)

	assert.Equal(t, "ins-1", insight.ID)
	assert.Equal(t, "sess-1", insight.SessionID)
	assert.Equal(t, "user-1", insight.CapturedBy)
	assert.Equal(t, "analyst", insight.Persona)
	assert.Equal(t, "correction", insight.Category)
	assert.Equal(t, "Column misleading", insight.InsightText)
	assert.Equal(t, "high", insight.Confidence)
	assert.Equal(t, []string{"urn:li:dataset:foo"}, insight.EntityURNs)
	assert.Len(t, insight.RelatedColumns, 1)
	assert.Len(t, insight.SuggestedActions, 1)
	assert.Equal(t, "approved", insight.Status)
	assert.Equal(t, "reviewer", insight.ReviewedBy)
	assert.NotNil(t, insight.ReviewedAt)
	assert.Equal(t, "lgtm", insight.ReviewNotes)
	assert.Nil(t, insight.AppliedAt) // NullTime not valid
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Get_WithAppliedAt(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now().Truncate(time.Second)
	appliedAt := now.Add(-30 * time.Minute)

	rows := sqlmock.NewRows(insightSelectColumns).AddRow(
		"ins-2", now, "sess-2", "user-2", "admin", "business_context",
		"MRR excludes trials", "medium",
		`[]`, `[]`, `[]`,
		"applied", "admin", now, "approved", //nolint:revive // test values
		"applier", appliedAt, "cs-1",
	)

	mock.ExpectQuery("SELECT .+ FROM knowledge_insights WHERE id").
		WithArgs("ins-2").
		WillReturnRows(rows)

	insight, err := store.Get(context.Background(), "ins-2")
	require.NoError(t, err)
	require.NotNil(t, insight)
	assert.NotNil(t, insight.AppliedAt)
	assert.Equal(t, "cs-1", insight.ChangesetRef)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Get_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT .+ FROM knowledge_insights WHERE id").
		WithArgs("nonexistent").
		WillReturnError(sql.ErrNoRows)

	insight, err := store.Get(context.Background(), "nonexistent")
	assert.Nil(t, insight)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying insight")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Get_InvalidJSON(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now()

	rows := sqlmock.NewRows(insightSelectColumns).AddRow(
		"ins-bad", now, "sess", "user", "analyst", "correction", //nolint:revive // test values
		"Some text", "high", //nolint:revive // test values
		`invalid-json`, `[]`, `[]`, //nolint:revive // test values
		"pending", "", sql.NullTime{}, "",
		"", sql.NullTime{}, "",
	)

	mock.ExpectQuery("SELECT .+ FROM knowledge_insights WHERE id").
		WithArgs("ins-bad").
		WillReturnRows(rows)

	insight, err := store.Get(context.Background(), "ins-bad")
	assert.Nil(t, insight)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshaling entity_urns")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- postgresStore List tests ---

func TestPostgresStore_List_NoFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now().Truncate(time.Second)

	// Count query
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// Select query
	rows := sqlmock.NewRows(insightSelectColumns).AddRow(
		"ins-1", now, "sess-1", "user-1", "analyst", "correction", //nolint:revive // test values
		"Column misleading", "high",
		`["urn:li:dataset:foo"]`, `[]`, `[]`,
		"pending", "", sql.NullTime{}, "", //nolint:revive // test values
		"", sql.NullTime{}, "",
	)
	mock.ExpectQuery("SELECT .+ FROM knowledge_insights ORDER BY").
		WillReturnRows(rows)

	insights, total, err := store.List(context.Background(), InsightFilter{})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, insights, 1)
	assert.Equal(t, "ins-1", insights[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_List_WithStatusFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_insights WHERE").
		WithArgs("pending").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	mock.ExpectQuery("SELECT .+ FROM knowledge_insights WHERE").
		WithArgs("pending").
		WillReturnRows(sqlmock.NewRows(insightSelectColumns))

	insights, total, err := store.List(context.Background(), InsightFilter{Status: "pending"})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, insights)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_List_MultipleFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	since := time.Now().Add(-24 * time.Hour)

	filter := InsightFilter{
		Status:     "pending",
		Category:   "correction",
		CapturedBy: "user-1",
		Confidence: "high",
		Since:      &since,
		Limit:      10, //nolint:revive // test value
	}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_insights WHERE").
		WithArgs("pending", "correction", "user-1", "high", since).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	mock.ExpectQuery("SELECT .+ FROM knowledge_insights WHERE").
		WithArgs("pending", "correction", "user-1", "high", since).
		WillReturnRows(sqlmock.NewRows(insightSelectColumns))

	insights, total, err := store.List(context.Background(), filter)
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, insights)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_List_CountError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnError(errors.New("db error"))

	insights, total, err := store.List(context.Background(), InsightFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "counting insights")
	assert.Nil(t, insights)
	assert.Equal(t, 0, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_List_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5)) //nolint:revive // test value

	mock.ExpectQuery("SELECT .+ FROM knowledge_insights").
		WillReturnError(errors.New("query failed"))

	insights, total, err := store.List(context.Background(), InsightFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying insights")
	assert.Nil(t, insights)
	assert.Equal(t, 0, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_List_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// Return rows with wrong number of columns to trigger scan error
	badRows := sqlmock.NewRows([]string{"id", "created_at"}).
		AddRow("ins-1", "not-a-time")
	mock.ExpectQuery("SELECT .+ FROM knowledge_insights").
		WillReturnRows(badRows)

	insights, total, err := store.List(context.Background(), InsightFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scanning insight row")
	assert.Nil(t, insights)
	assert.Equal(t, 0, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_List_MultipleRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now().Truncate(time.Second)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	rows := sqlmock.NewRows(insightSelectColumns).
		AddRow(
			"ins-1", now, "sess-1", "user-1", "analyst", "correction",
			"First insight", "high",
			`[]`, `[]`, `[]`,
			"pending", "", sql.NullTime{}, "",
			"", sql.NullTime{}, "",
		).
		AddRow(
			"ins-2", now, "sess-2", "user-2", "admin", "data_quality",
			"Second insight", "medium",
			`["urn:li:dataset:bar"]`, `[]`, `[]`,
			"approved", "reviewer", now, "ok",
			"", sql.NullTime{}, "",
		)
	mock.ExpectQuery("SELECT .+ FROM knowledge_insights").
		WillReturnRows(rows)

	insights, total, err := store.List(context.Background(), InsightFilter{})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, insights, 2)
	assert.Equal(t, "ins-1", insights[0].ID)
	assert.Equal(t, "ins-2", insights[1].ID) //nolint:revive // test value
	assert.NotNil(t, insights[1].ReviewedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_List_InvalidJSON(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	rows := sqlmock.NewRows(insightSelectColumns).AddRow(
		"ins-bad", now, "sess", "user", "analyst", "correction",
		"Some text", "high",
		`not-json`, `[]`, `[]`,
		"pending", "", sql.NullTime{}, "",
		"", sql.NullTime{}, "",
	)
	mock.ExpectQuery("SELECT .+ FROM knowledge_insights").
		WillReturnRows(rows)

	insights, total, err := store.List(context.Background(), InsightFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshaling entity_urns")
	assert.Nil(t, insights)
	assert.Equal(t, 0, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- postgresStore UpdateStatus tests ---

func TestPostgresStore_UpdateStatus(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs("approved", "admin", sqlmock.AnyArg(), "looks good", "ins-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.UpdateStatus(context.Background(), "ins-1", "approved", "admin", "looks good")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStatus_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs("approved", "admin", sqlmock.AnyArg(), "notes", "nonexistent").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.UpdateStatus(context.Background(), "nonexistent", "approved", "admin", "notes")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insight not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStatus_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs("approved", "admin", sqlmock.AnyArg(), "notes", "ins-1").
		WillReturnError(errors.New("connection lost"))

	err = store.UpdateStatus(context.Background(), "ins-1", "approved", "admin", "notes")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "updating insight status")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStatus_RowsAffectedError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs("approved", "admin", sqlmock.AnyArg(), "notes", "ins-1").
		WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected error")))

	err = store.UpdateStatus(context.Background(), "ins-1", "approved", "admin", "notes")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking rows affected")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- postgresStore Update tests ---

func TestPostgresStore_Update_AllFields(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	updates := InsightUpdate{
		InsightText: "Updated text here",
		Category:    "business_context",
		Confidence:  "high",
	}

	mock.ExpectExec("UPDATE knowledge_insights SET").
		WithArgs("Updated text here", "business_context", "high", "ins-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Update(context.Background(), "ins-1", updates)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Update_SingleField(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	updates := InsightUpdate{InsightText: "Updated text only"}

	mock.ExpectExec("UPDATE knowledge_insights SET").
		WithArgs("Updated text only", "ins-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Update(context.Background(), "ins-1", updates)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Update_NoFields(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	err = store.Update(context.Background(), "ins-1", InsightUpdate{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no fields to update")
}

func TestPostgresStore_Update_NotFoundOrApplied(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights SET").
		WithArgs("New text content", "ins-applied").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.Update(context.Background(), "ins-applied", InsightUpdate{InsightText: "New text content"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insight not found or already applied")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Update_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights SET").
		WithArgs("New text content", "ins-1").
		WillReturnError(errors.New("db error"))

	err = store.Update(context.Background(), "ins-1", InsightUpdate{InsightText: "New text content"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "updating insight")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Update_RowsAffectedError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights SET").
		WithArgs("Updated text", "ins-1").
		WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected error")))

	err = store.Update(context.Background(), "ins-1", InsightUpdate{InsightText: "Updated text"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking rows affected")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- postgresStore Stats tests ---

func TestPostgresStore_Stats(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	// Status query
	statusRows := sqlmock.NewRows([]string{"status", "count"}).
		AddRow("pending", 5).
		AddRow("approved", 3).
		AddRow("applied", 2)
	mock.ExpectQuery("SELECT status, COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(statusRows)

	// Category query
	catRows := sqlmock.NewRows([]string{"category", "count"}).
		AddRow("correction", 4).
		AddRow("business_context", 6) //nolint:revive // test value
	mock.ExpectQuery("SELECT category, COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(catRows)

	// Confidence query
	confRows := sqlmock.NewRows([]string{"confidence", "count"}).
		AddRow("high", 3).
		AddRow("medium", 7) //nolint:revive // test values
	mock.ExpectQuery("SELECT confidence, COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(confRows)

	stats, err := store.Stats(context.Background(), InsightFilter{})
	require.NoError(t, err)
	require.NotNil(t, stats)

	assert.Equal(t, 5, stats.TotalPending)         //nolint:revive // test value
	assert.Equal(t, 5, stats.ByStatus["pending"])  //nolint:revive // test value
	assert.Equal(t, 3, stats.ByStatus["approved"]) //nolint:revive // test value
	assert.Equal(t, 2, stats.ByStatus["applied"])
	assert.Equal(t, 4, stats.ByCategory["correction"])       //nolint:revive // test value
	assert.Equal(t, 6, stats.ByCategory["business_context"]) //nolint:revive // test value
	assert.Equal(t, 3, stats.ByConfidence["high"])           //nolint:revive // test value
	assert.Equal(t, 7, stats.ByConfidence["medium"])         //nolint:revive // test value
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Stats_WithFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	filter := InsightFilter{Status: "pending"}

	statusRows := sqlmock.NewRows([]string{"status", "count"}).
		AddRow("pending", 5) //nolint:revive // test value
	mock.ExpectQuery("SELECT status, COUNT\\(\\*\\) FROM knowledge_insights WHERE").
		WithArgs("pending").
		WillReturnRows(statusRows)

	catRows := sqlmock.NewRows([]string{"category", "count"})
	mock.ExpectQuery("SELECT category, COUNT\\(\\*\\) FROM knowledge_insights WHERE").
		WithArgs("pending").
		WillReturnRows(catRows)

	confRows := sqlmock.NewRows([]string{"confidence", "count"})
	mock.ExpectQuery("SELECT confidence, COUNT\\(\\*\\) FROM knowledge_insights WHERE").
		WithArgs("pending").
		WillReturnRows(confRows)

	stats, err := store.Stats(context.Background(), filter)
	require.NoError(t, err)
	assert.Equal(t, 5, stats.TotalPending) //nolint:revive // test value
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Stats_StatusQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT status, COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnError(errors.New("db error"))

	stats, err := store.Stats(context.Background(), InsightFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "counting by status")
	assert.Nil(t, stats)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Stats_CategoryQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	statusRows := sqlmock.NewRows([]string{"status", "count"}).
		AddRow("pending", 1)
	mock.ExpectQuery("SELECT status, COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(statusRows)

	mock.ExpectQuery("SELECT category, COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnError(errors.New("db error")) //nolint:revive // test value

	stats, err := store.Stats(context.Background(), InsightFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "counting by category")
	assert.Nil(t, stats)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Stats_ConfidenceQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	statusRows := sqlmock.NewRows([]string{"status", "count"}).
		AddRow("pending", 1)
	mock.ExpectQuery("SELECT status, COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(statusRows)

	catRows := sqlmock.NewRows([]string{"category", "count"})
	mock.ExpectQuery("SELECT category, COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(catRows)

	mock.ExpectQuery("SELECT confidence, COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnError(errors.New("db error"))

	stats, err := store.Stats(context.Background(), InsightFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "counting by confidence")
	assert.Nil(t, stats)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Stats_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	// Return rows with wrong types to trigger scan error
	statusRows := sqlmock.NewRows([]string{"status", "count"}).
		AddRow(nil, nil)
	mock.ExpectQuery("SELECT status, COUNT\\(\\*\\) FROM knowledge_insights").
		WillReturnRows(statusRows)

	stats, err := store.Stats(context.Background(), InsightFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "counting by status")
	assert.Nil(t, stats)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- postgresStore MarkApplied tests ---

func TestPostgresStore_MarkApplied(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs(StatusApplied, "admin", sqlmock.AnyArg(), "cs-1", "ins-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.MarkApplied(context.Background(), "ins-1", "admin", "cs-1") //nolint:revive // test values
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_MarkApplied_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs(StatusApplied, "admin", sqlmock.AnyArg(), "cs-1", "nonexistent").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.MarkApplied(context.Background(), "nonexistent", "admin", "cs-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insight not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_MarkApplied_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs(StatusApplied, "admin", sqlmock.AnyArg(), "cs-1", "ins-1").
		WillReturnError(errors.New("connection lost"))

	err = store.MarkApplied(context.Background(), "ins-1", "admin", "cs-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marking insight as applied")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_MarkApplied_RowsAffectedError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs(StatusApplied, "admin", sqlmock.AnyArg(), "cs-1", "ins-1").
		WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected error")))

	err = store.MarkApplied(context.Background(), "ins-1", "admin", "cs-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking rows affected")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- postgresStore Supersede tests ---

func TestPostgresStore_Supersede(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs(StatusSuperseded, StatusPending,
								fmt.Sprintf(`[%q]`, "urn:li:dataset:foo"), "exclude-id").
		WillReturnResult(sqlmock.NewResult(0, 3)) //nolint:revive // test value

	count, err := store.Supersede(context.Background(), "urn:li:dataset:foo", "exclude-id")
	assert.NoError(t, err)
	assert.Equal(t, 3, count) //nolint:revive // test value
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Supersede_NoneAffected(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs(StatusSuperseded, StatusPending,
			fmt.Sprintf(`[%q]`, "urn:li:dataset:bar"), "exclude-id").
		WillReturnResult(sqlmock.NewResult(0, 0))

	count, err := store.Supersede(context.Background(), "urn:li:dataset:bar", "exclude-id")
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Supersede_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs(StatusSuperseded, StatusPending,
			fmt.Sprintf(`[%q]`, "urn:li:dataset:foo"), "exclude-id").
		WillReturnError(errors.New("connection lost"))

	count, err := store.Supersede(context.Background(), "urn:li:dataset:foo", "exclude-id") //nolint:revive // test values
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "superseding insights")
	assert.Equal(t, 0, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Supersede_RowsAffectedError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE knowledge_insights").
		WithArgs(StatusSuperseded, StatusPending,
												fmt.Sprintf(`[%q]`, "urn:li:dataset:foo"), "exclude-id").
		WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected error"))) //nolint:revive // test value

	count, err := store.Supersede(context.Background(), "urn:li:dataset:foo", "exclude-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking rows affected") //nolint:revive // test value
	assert.Equal(t, 0, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- buildFilterWhere tests ---

func TestBuildFilterWhere(t *testing.T) {
	since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)      //nolint:revive // test value
	until := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC) //nolint:revive // test value

	tests := []struct {
		name           string
		filter         InsightFilter
		wantWhere      string
		wantArgCount   int
		wantContains   []string
		wantNoContains []string
	}{
		{
			name:         "empty filter",
			filter:       InsightFilter{},
			wantWhere:    "",
			wantArgCount: 0,
		},
		{
			name:         "status only",
			filter:       InsightFilter{Status: "pending"},
			wantArgCount: 1,
			wantContains: []string{"status = $1"},
		},
		{
			name:         "category only",
			filter:       InsightFilter{Category: "correction"},
			wantArgCount: 1,
			wantContains: []string{"category = $1"},
		},
		{
			name:         "entity_urn only",
			filter:       InsightFilter{EntityURN: "urn:li:dataset:foo"},
			wantArgCount: 1,
			wantContains: []string{"entity_urns @> $1::jsonb"},
		},
		{
			name:         "captured_by only",
			filter:       InsightFilter{CapturedBy: "user-1"},
			wantArgCount: 1,
			wantContains: []string{"captured_by = $1"},
		},
		{
			name:         "confidence only",
			filter:       InsightFilter{Confidence: "high"},
			wantArgCount: 1,
			wantContains: []string{"confidence = $1"},
		},
		{
			name:         "since only",
			filter:       InsightFilter{Since: &since},
			wantArgCount: 1,
			wantContains: []string{"created_at >= $1"},
		},
		{
			name:         "until only",
			filter:       InsightFilter{Until: &until},
			wantArgCount: 1,
			wantContains: []string{"created_at <= $1"},
		},
		{
			name: "all filters",
			filter: InsightFilter{
				Status:     "pending",
				Category:   "correction",
				EntityURN:  "urn:li:dataset:foo",
				CapturedBy: "user-1",
				Confidence: "high",
				Since:      &since,
				Until:      &until,
			},
			wantArgCount: 7, //nolint:revive // 7 filters
			wantContains: []string{
				"WHERE",
				"status = $1",
				"category = $2",
				"entity_urns @> $3::jsonb",
				"captured_by = $4",
				"confidence = $5",
				"created_at >= $6",
				"created_at <= $7",
			},
		},
		{
			name: "two filters with correct arg numbering",
			filter: InsightFilter{
				Status:   "approved",
				Category: "data_quality",
			},
			wantArgCount: 2,
			wantContains: []string{
				"status = $1",
				"category = $2",
				" AND ",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			where, args := buildFilterWhere(tc.filter)
			assert.Len(t, args, tc.wantArgCount)

			if tc.wantWhere != "" {
				assert.Equal(t, tc.wantWhere, where)
			}

			for _, s := range tc.wantContains {
				assert.Contains(t, where, s)
			}
			for _, s := range tc.wantNoContains {
				assert.NotContains(t, where, s)
			}
		})
	}
}

// --- unmarshalInsightJSON tests ---

func TestUnmarshalInsightJSON(t *testing.T) {
	t.Run("valid JSON", func(t *testing.T) {
		insight := &Insight{}
		err := unmarshalInsightJSON(
			insight,
			[]byte(`["urn:li:dataset:foo"]`),
			[]byte(`[{"urn":"u","column":"c","relevance":"r"}]`),
			[]byte(`[{"action_type":"add_tag","target":"t","detail":"d"}]`),
		)
		require.NoError(t, err)
		assert.Equal(t, []string{"urn:li:dataset:foo"}, insight.EntityURNs)
		assert.Len(t, insight.RelatedColumns, 1)
		assert.Len(t, insight.SuggestedActions, 1)
	})

	t.Run("invalid entity_urns", func(t *testing.T) {
		insight := &Insight{}
		err := unmarshalInsightJSON(insight, []byte(`{bad`), []byte(`[]`), []byte(`[]`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling entity_urns")
	})

	t.Run("invalid related_columns", func(t *testing.T) {
		insight := &Insight{}
		err := unmarshalInsightJSON(insight, []byte(`[]`), []byte(`{bad`), []byte(`[]`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling related_columns")
	})

	t.Run("invalid suggested_actions", func(t *testing.T) {
		insight := &Insight{}
		err := unmarshalInsightJSON(insight, []byte(`[]`), []byte(`[]`), []byte(`{bad`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling suggested_actions")
	})
}

// --- Constructor test ---

func TestNewPostgresStore(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	assert.NotNil(t, store)

	// Verify it implements InsightStore.
	var _ InsightStore = store //nolint:staticcheck // intentional interface compliance check
}
