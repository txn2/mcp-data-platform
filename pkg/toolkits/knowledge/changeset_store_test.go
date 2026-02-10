package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// changesetSelectColumns lists the columns returned by GetChangeset and ListChangesets queries.
var changesetSelectColumns = []string{
	"id", "created_at", "target_urn", "change_type", "previous_value", "new_value",
	"source_insight_ids", "approved_by", "applied_by", "rolled_back",
	"rolled_back_by", "rolled_back_at",
}

// --- noopChangesetStore tests ---

func TestNoopChangesetStore_InsertChangeset(t *testing.T) {
	store := NewNoopChangesetStore()
	err := store.InsertChangeset(context.Background(), Changeset{
		ID:        "cs-1",
		TargetURN: "urn:li:dataset:foo",
	})
	assert.NoError(t, err)
}

func TestNoopChangesetStore_GetChangeset(t *testing.T) {
	store := NewNoopChangesetStore()
	cs, err := store.GetChangeset(context.Background(), "any-id")
	assert.Nil(t, cs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "changeset not found")
}

func TestNoopChangesetStore_ListChangesets(t *testing.T) {
	store := NewNoopChangesetStore()
	changesets, total, err := store.ListChangesets(context.Background(), ChangesetFilter{})
	assert.NoError(t, err)
	assert.Nil(t, changesets)
	assert.Equal(t, 0, total)
}

func TestNoopChangesetStore_RollbackChangeset(t *testing.T) {
	store := NewNoopChangesetStore()
	err := store.RollbackChangeset(context.Background(), "cs-1", "admin")
	assert.NoError(t, err)
}

// --- postgresChangesetStore constructor test ---

func TestNewPostgresChangesetStore(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)
	assert.NotNil(t, store)

	// Verify it implements ChangesetStore.
	var _ ChangesetStore = store //nolint:staticcheck // intentional interface compliance check
}

// --- postgresChangesetStore InsertChangeset tests ---

func TestPostgresChangesetStore_InsertChangeset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)
	cs := Changeset{
		ID:               "cs-1",
		TargetURN:        "urn:li:dataset:foo",
		ChangeType:       "update_description",
		PreviousValue:    map[string]any{"description": "old desc"},
		NewValue:         map[string]any{"description": "new desc"},
		SourceInsightIDs: []string{"ins-1", "ins-2"},
		ApprovedBy:       "reviewer",
		AppliedBy:        "admin",
	}

	mock.ExpectExec("INSERT INTO knowledge_changesets").
		WithArgs(
			cs.ID, cs.TargetURN, cs.ChangeType,
			sqlmock.AnyArg(), // previous_value JSON
			sqlmock.AnyArg(), // new_value JSON
			sqlmock.AnyArg(), // source_insight_ids JSON
			cs.ApprovedBy, cs.AppliedBy,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.InsertChangeset(context.Background(), cs)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_InsertChangeset_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)
	cs := Changeset{
		ID:               "cs-2",
		TargetURN:        "urn:li:dataset:bar",
		ChangeType:       "add_tag",
		PreviousValue:    map[string]any{},
		NewValue:         map[string]any{"tag": "important"},
		SourceInsightIDs: []string{},
		ApprovedBy:       "reviewer",
		AppliedBy:        "admin",
	}

	mock.ExpectExec("INSERT INTO knowledge_changesets").
		WithArgs(
			cs.ID, cs.TargetURN, cs.ChangeType,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			cs.ApprovedBy, cs.AppliedBy,
		).
		WillReturnError(errors.New("connection refused"))

	err = store.InsertChangeset(context.Background(), cs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting changeset")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_InsertChangeset_EmptyMaps(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)
	cs := Changeset{
		ID:               "cs-3",
		TargetURN:        "urn:li:dataset:baz",
		ChangeType:       "add_tag",
		PreviousValue:    map[string]any{},
		NewValue:         map[string]any{},
		SourceInsightIDs: []string{},
	}

	mock.ExpectExec("INSERT INTO knowledge_changesets").
		WithArgs(
			cs.ID, cs.TargetURN, cs.ChangeType,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			cs.ApprovedBy, cs.AppliedBy,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.InsertChangeset(context.Background(), cs)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- postgresChangesetStore GetChangeset tests ---

func TestPostgresChangesetStore_GetChangeset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)
	now := time.Now().Truncate(time.Second)

	rows := sqlmock.NewRows(changesetSelectColumns).AddRow(
		"cs-1", now, "urn:li:dataset:foo", "update_description",
		`{"description":"old"}`, `{"description":"new"}`,
		`["ins-1","ins-2"]`,
		"reviewer", "admin", false, //nolint:revive // test values
		"", sql.NullTime{},
	)

	mock.ExpectQuery("SELECT .+ FROM knowledge_changesets WHERE id").
		WithArgs("cs-1"). //nolint:revive // test value
		WillReturnRows(rows)

	cs, err := store.GetChangeset(context.Background(), "cs-1") //nolint:revive // test value
	require.NoError(t, err)
	require.NotNil(t, cs)

	assert.Equal(t, "cs-1", cs.ID)
	assert.Equal(t, "urn:li:dataset:foo", cs.TargetURN)
	assert.Equal(t, "update_description", cs.ChangeType)
	assert.Equal(t, "old", cs.PreviousValue["description"])
	assert.Equal(t, "new", cs.NewValue["description"])
	assert.Equal(t, []string{"ins-1", "ins-2"}, cs.SourceInsightIDs)
	assert.Equal(t, "reviewer", cs.ApprovedBy) //nolint:revive // test value
	assert.Equal(t, "admin", cs.AppliedBy)     //nolint:revive // test value
	assert.False(t, cs.RolledBack)
	assert.Nil(t, cs.RolledBackAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_GetChangeset_RolledBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)
	now := time.Now().Truncate(time.Second)
	rolledBackAt := now.Add(-time.Hour)

	rows := sqlmock.NewRows(changesetSelectColumns).AddRow(
		"cs-2", now, "urn:li:dataset:bar", "add_tag",
		`{}`, `{"tag":"important"}`,
		`["ins-3"]`,
		"reviewer", "admin", true,
		"rollback-user", rolledBackAt,
	)

	mock.ExpectQuery("SELECT .+ FROM knowledge_changesets WHERE id").
		WithArgs("cs-2").
		WillReturnRows(rows)

	cs, err := store.GetChangeset(context.Background(), "cs-2")
	require.NoError(t, err)
	require.NotNil(t, cs)

	assert.True(t, cs.RolledBack)
	assert.Equal(t, "rollback-user", cs.RolledBackBy)
	assert.NotNil(t, cs.RolledBackAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_GetChangeset_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)

	mock.ExpectQuery("SELECT .+ FROM knowledge_changesets WHERE id").
		WithArgs("nonexistent").
		WillReturnError(sql.ErrNoRows)

	cs, err := store.GetChangeset(context.Background(), "nonexistent")
	assert.Nil(t, cs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying changeset")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_GetChangeset_InvalidJSON(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)
	now := time.Now()

	rows := sqlmock.NewRows(changesetSelectColumns).AddRow(
		"cs-bad", now, "urn:li:dataset:foo", "add_tag", //nolint:revive // test values
		`invalid-json`, `{}`, `[]`,
		"reviewer", "admin", false,
		"", sql.NullTime{},
	)

	mock.ExpectQuery("SELECT .+ FROM knowledge_changesets WHERE id").
		WithArgs("cs-bad").
		WillReturnRows(rows)

	cs, err := store.GetChangeset(context.Background(), "cs-bad")
	assert.Nil(t, cs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshaling previous_value")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- postgresChangesetStore ListChangesets tests ---

func TestPostgresChangesetStore_ListChangesets_NoFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)
	now := time.Now().Truncate(time.Second)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_changesets").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	rows := sqlmock.NewRows(changesetSelectColumns).AddRow(
		"cs-1", now, "urn:li:dataset:foo", "update_description", //nolint:revive // test values
		`{"description":"old"}`, `{"description":"new"}`,
		`["ins-1"]`,
		"reviewer", "admin", false,
		"", sql.NullTime{},
	)
	mock.ExpectQuery("SELECT .+ FROM knowledge_changesets ORDER BY").
		WillReturnRows(rows)

	changesets, total, err := store.ListChangesets(context.Background(), ChangesetFilter{})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, changesets, 1)
	assert.Equal(t, "cs-1", changesets[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_ListChangesets_WithEntityURNFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_changesets WHERE").
		WithArgs("urn:li:dataset:foo").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	mock.ExpectQuery("SELECT .+ FROM knowledge_changesets WHERE").
		WithArgs("urn:li:dataset:foo").
		WillReturnRows(sqlmock.NewRows(changesetSelectColumns))

	changesets, total, err := store.ListChangesets(context.Background(), ChangesetFilter{
		EntityURN: "urn:li:dataset:foo",
	})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, changesets)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_ListChangesets_MultipleFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)
	since := time.Now().Add(-24 * time.Hour)
	until := time.Now()
	rolledBack := false

	filter := ChangesetFilter{
		EntityURN:  "urn:li:dataset:foo",
		AppliedBy:  "admin",
		Since:      &since,
		Until:      &until,
		RolledBack: &rolledBack,
		Limit:      10, //nolint:revive // test value
	}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_changesets WHERE").
		WithArgs("urn:li:dataset:foo", "admin", since, until, false).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	mock.ExpectQuery("SELECT .+ FROM knowledge_changesets WHERE").
		WithArgs("urn:li:dataset:foo", "admin", since, until, false).
		WillReturnRows(sqlmock.NewRows(changesetSelectColumns))

	changesets, total, err := store.ListChangesets(context.Background(), filter)
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, changesets)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_ListChangesets_CountError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_changesets").
		WillReturnError(errors.New("db error"))

	changesets, total, err := store.ListChangesets(context.Background(), ChangesetFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "counting changesets")
	assert.Nil(t, changesets)
	assert.Equal(t, 0, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_ListChangesets_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_changesets").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5)) //nolint:revive // test value

	mock.ExpectQuery("SELECT .+ FROM knowledge_changesets").
		WillReturnError(errors.New("query failed"))

	changesets, total, err := store.ListChangesets(context.Background(), ChangesetFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying changesets")
	assert.Nil(t, changesets)
	assert.Equal(t, 0, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_ListChangesets_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_changesets").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	badRows := sqlmock.NewRows([]string{"id", "created_at"}).
		AddRow("cs-1", "not-a-time")
	mock.ExpectQuery("SELECT .+ FROM knowledge_changesets").
		WillReturnRows(badRows)

	changesets, total, err := store.ListChangesets(context.Background(), ChangesetFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scanning changeset row")
	assert.Nil(t, changesets)
	assert.Equal(t, 0, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_ListChangesets_InvalidJSON(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)
	now := time.Now()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_changesets").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	rows := sqlmock.NewRows(changesetSelectColumns).AddRow(
		"cs-bad", now, "urn:li:dataset:foo", "add_tag",
		`not-json`, `{}`, `[]`,
		"reviewer", "admin", false,
		"", sql.NullTime{},
	)
	mock.ExpectQuery("SELECT .+ FROM knowledge_changesets").
		WillReturnRows(rows)

	changesets, total, err := store.ListChangesets(context.Background(), ChangesetFilter{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshaling previous_value")
	assert.Nil(t, changesets)
	assert.Equal(t, 0, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_ListChangesets_MultipleRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)
	now := time.Now().Truncate(time.Second)
	rolledBackAt := now.Add(-30 * time.Minute)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_changesets").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	rows := sqlmock.NewRows(changesetSelectColumns).
		AddRow(
			"cs-1", now, "urn:li:dataset:foo", "update_description",
			`{}`, `{}`, `["ins-1"]`,
			"reviewer", "admin", false,
			"", sql.NullTime{},
		).
		AddRow(
			"cs-2", now, "urn:li:dataset:bar", "add_tag", //nolint:revive // test values
			`{}`, `{"tag":"x"}`, `["ins-2"]`, //nolint:revive // test values
			"reviewer2", "admin2", true,
			"rollback-user", rolledBackAt,
		)
	mock.ExpectQuery("SELECT .+ FROM knowledge_changesets").
		WillReturnRows(rows)

	changesets, total, err := store.ListChangesets(context.Background(), ChangesetFilter{})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, changesets, 2)
	assert.Equal(t, "cs-1", changesets[0].ID)
	assert.False(t, changesets[0].RolledBack)
	assert.Nil(t, changesets[0].RolledBackAt)
	assert.Equal(t, "cs-2", changesets[1].ID)
	assert.True(t, changesets[1].RolledBack)
	assert.NotNil(t, changesets[1].RolledBackAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- postgresChangesetStore RollbackChangeset tests ---

func TestPostgresChangesetStore_RollbackChangeset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)

	mock.ExpectExec("UPDATE knowledge_changesets").
		WithArgs("admin", sqlmock.AnyArg(), "cs-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.RollbackChangeset(context.Background(), "cs-1", "admin")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_RollbackChangeset_AlreadyRolledBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)

	mock.ExpectExec("UPDATE knowledge_changesets").
		WithArgs("admin", sqlmock.AnyArg(), "cs-rolled-back").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.RollbackChangeset(context.Background(), "cs-rolled-back", "admin")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "changeset not found or already rolled back")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_RollbackChangeset_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)

	mock.ExpectExec("UPDATE knowledge_changesets").
		WithArgs("admin", sqlmock.AnyArg(), "cs-1").
		WillReturnError(errors.New("connection lost"))

	err = store.RollbackChangeset(context.Background(), "cs-1", "admin")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rolling back changeset")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresChangesetStore_RollbackChangeset_RowsAffectedError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresChangesetStore(db)

	mock.ExpectExec("UPDATE knowledge_changesets").
		WithArgs("admin", sqlmock.AnyArg(), "cs-1").
		WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected error")))

	err = store.RollbackChangeset(context.Background(), "cs-1", "admin")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking rows affected")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- buildChangesetFilterWhere tests ---

func TestBuildChangesetFilterWhere(t *testing.T) {
	since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)      //nolint:revive // test value
	until := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC) //nolint:revive // test value
	rolledBackTrue := true
	rolledBackFalse := false

	tests := []struct {
		name         string
		filter       ChangesetFilter
		wantWhere    string
		wantArgCount int
		wantContains []string
	}{
		{
			name:         "empty filter",
			filter:       ChangesetFilter{},
			wantWhere:    "",
			wantArgCount: 0,
		},
		{
			name:         "entity_urn only",
			filter:       ChangesetFilter{EntityURN: "urn:li:dataset:foo"},
			wantArgCount: 1,
			wantContains: []string{"target_urn = $1"},
		},
		{
			name:         "applied_by only",
			filter:       ChangesetFilter{AppliedBy: "admin"},
			wantArgCount: 1,
			wantContains: []string{"applied_by = $1"},
		},
		{
			name:         "since only",
			filter:       ChangesetFilter{Since: &since},
			wantArgCount: 1,
			wantContains: []string{"created_at >= $1"},
		},
		{
			name:         "until only",
			filter:       ChangesetFilter{Until: &until},
			wantArgCount: 1,
			wantContains: []string{"created_at <= $1"},
		},
		{
			name:         "rolled_back true",
			filter:       ChangesetFilter{RolledBack: &rolledBackTrue},
			wantArgCount: 1,
			wantContains: []string{"rolled_back = $1"},
		},
		{
			name:         "rolled_back false",
			filter:       ChangesetFilter{RolledBack: &rolledBackFalse},
			wantArgCount: 1,
			wantContains: []string{"rolled_back = $1"},
		},
		{
			name: "all filters",
			filter: ChangesetFilter{
				EntityURN:  "urn:li:dataset:foo",
				AppliedBy:  "admin",
				Since:      &since,
				Until:      &until,
				RolledBack: &rolledBackFalse,
			},
			wantArgCount: 5, //nolint:revive // 5 filters
			wantContains: []string{
				"WHERE",
				"target_urn = $1",
				"applied_by = $2",
				"created_at >= $3",
				"created_at <= $4",
				"rolled_back = $5",
			},
		},
		{
			name: "two filters with correct arg numbering",
			filter: ChangesetFilter{
				EntityURN: "urn:li:dataset:foo",
				AppliedBy: "admin",
			},
			wantArgCount: 2,
			wantContains: []string{
				"target_urn = $1",
				"applied_by = $2",
				" AND ",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			where, args := buildChangesetFilterWhere(tc.filter)
			assert.Len(t, args, tc.wantArgCount)

			if tc.wantWhere != "" {
				assert.Equal(t, tc.wantWhere, where)
			}

			for _, s := range tc.wantContains {
				assert.Contains(t, where, s)
			}
		})
	}
}

// --- unmarshalChangesetJSON tests ---

func TestUnmarshalChangesetJSON(t *testing.T) {
	t.Run("valid JSON", func(t *testing.T) {
		cs := &Changeset{}
		err := unmarshalChangesetJSON(
			cs,
			[]byte(`{"description":"old"}`),
			[]byte(`{"description":"new"}`),
			[]byte(`["ins-1","ins-2"]`),
		)
		require.NoError(t, err)
		assert.Equal(t, "old", cs.PreviousValue["description"])
		assert.Equal(t, "new", cs.NewValue["description"])
		assert.Equal(t, []string{"ins-1", "ins-2"}, cs.SourceInsightIDs)
	})

	t.Run("invalid previous_value", func(t *testing.T) {
		cs := &Changeset{}
		err := unmarshalChangesetJSON(cs, []byte(`{bad`), []byte(`{}`), []byte(`[]`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling previous_value")
	})

	t.Run("invalid new_value", func(t *testing.T) {
		cs := &Changeset{}
		err := unmarshalChangesetJSON(cs, []byte(`{}`), []byte(`{bad`), []byte(`[]`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling new_value")
	})

	t.Run("invalid source_insight_ids", func(t *testing.T) {
		cs := &Changeset{}
		err := unmarshalChangesetJSON(cs, []byte(`{}`), []byte(`{}`), []byte(`{bad`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling source_insight_ids")
	})
}
