package memory

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memorySelectColumns lists the SELECT column names for memory records in scan order.
var memorySelectColumns = []string{
	"id", "created_at", "updated_at", "created_by", "persona", "dimension",
	"content", "category", "confidence", "source",
	"entity_urns", "related_columns", "metadata",
	"status", "stale_reason", "stale_at", "last_verified",
}

func newTestRecord() Record {
	return Record{
		ID:         "mem-001",
		CreatedBy:  "user-abc",
		Persona:    "analyst",
		Dimension:  DimensionKnowledge,
		Content:    "This column represents monthly revenue.",
		Category:   CategoryBusinessCtx,
		Confidence: ConfidenceHigh,
		Source:     SourceUser,
		EntityURNs: []string{"urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)"},
		RelatedColumns: []RelatedColumn{
			{URN: "urn:li:dataset:foo", Column: "revenue", Relevance: "primary"},
		},
		Metadata: map[string]any{"context": "finance"},
		Status:   StatusActive,
	}
}

// --- Insert tests ---

func TestPostgresStore_Insert(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	record := newTestRecord()

	mock.ExpectExec("INSERT INTO memory_records").
		WithArgs(
			record.ID, record.CreatedBy, record.Persona, record.Dimension,
			record.Content, record.Category, record.Confidence, record.Source,
			sqlmock.AnyArg(), // entity_urns JSON
			sqlmock.AnyArg(), // related_columns JSON
			sqlmock.AnyArg(), // metadata JSON
			record.Status,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.Insert(context.Background(), record)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Insert_WithEmbedding(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	record := newTestRecord()
	record.Embedding = []float32{0.1, 0.2, 0.3}

	mock.ExpectExec("INSERT INTO memory_records").
		WithArgs(
			record.ID, record.CreatedBy, record.Persona, record.Dimension,
			record.Content, record.Category, record.Confidence, record.Source,
			sqlmock.AnyArg(), // entity_urns JSON
			sqlmock.AnyArg(), // related_columns JSON
			sqlmock.AnyArg(), // metadata JSON
			record.Status,
			sqlmock.AnyArg(), // embedding (pgvector)
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.Insert(context.Background(), record)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Insert_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	record := newTestRecord()

	mock.ExpectExec("INSERT INTO memory_records").
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnError(errors.New("connection refused"))

	err = store.Insert(context.Background(), record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting memory record")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Get tests ---

func TestPostgresStore_Get(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now().Truncate(time.Second)

	rows := sqlmock.NewRows(memorySelectColumns).AddRow(
		"mem-001", now, now, "user-abc", "analyst", DimensionKnowledge,
		"This is content about tables.", CategoryBusinessCtx, ConfidenceHigh, SourceUser,
		`["urn:li:dataset:foo"]`,
		`[{"urn":"urn:li:dataset:foo","column":"col1","relevance":"primary"}]`,
		`{"context":"finance"}`,
		StatusActive, nil, nil, nil,
	)

	mock.ExpectQuery("SELECT .+ FROM memory_records WHERE id").
		WithArgs("mem-001").
		WillReturnRows(rows)

	record, err := store.Get(context.Background(), "mem-001")
	require.NoError(t, err)
	require.NotNil(t, record)

	assert.Equal(t, "mem-001", record.ID)
	assert.Equal(t, "user-abc", record.CreatedBy)
	assert.Equal(t, "analyst", record.Persona)
	assert.Equal(t, DimensionKnowledge, record.Dimension)
	assert.Equal(t, CategoryBusinessCtx, record.Category)
	assert.Equal(t, ConfidenceHigh, record.Confidence)
	assert.Equal(t, SourceUser, record.Source)
	assert.Equal(t, StatusActive, record.Status)
	assert.Equal(t, []string{"urn:li:dataset:foo"}, record.EntityURNs)
	assert.Len(t, record.RelatedColumns, 1)
	assert.Equal(t, "col1", record.RelatedColumns[0].Column)
	assert.Nil(t, record.StaleAt)
	assert.Nil(t, record.LastVerified)
	assert.Empty(t, record.StaleReason)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Get_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT .+ FROM memory_records WHERE id").
		WithArgs("missing-id").
		WillReturnError(sql.ErrNoRows)

	record, err := store.Get(context.Background(), "missing-id")
	assert.Nil(t, record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "memory record not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Get_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT .+ FROM memory_records WHERE id").
		WithArgs("err-id").
		WillReturnError(errors.New("connection timeout"))

	record, err := store.Get(context.Background(), "err-id")
	assert.Nil(t, record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying memory record")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Update tests ---

func TestPostgresStore_Update_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE memory_records SET").
		WithArgs(
			sqlmock.AnyArg(), // content
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // id
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Update(context.Background(), "mem-001", RecordUpdate{
		Content: "Updated content for the record.",
	})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Update_NoFields(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	err = store.Update(context.Background(), "mem-001", RecordUpdate{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no fields to update")
}

func TestPostgresStore_Update_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE memory_records SET").
		WithArgs(
			sqlmock.AnyArg(), // confidence
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // id
		).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.Update(context.Background(), "missing-id", RecordUpdate{
		Confidence: ConfidenceLow,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "memory record not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Delete tests ---

func TestPostgresStore_Delete_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE memory_records SET").
		WithArgs(
			sqlmock.AnyArg(), // status (archived)
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // id
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Delete(context.Background(), "mem-001")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Delete_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE memory_records SET").
		WithArgs(
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.Delete(context.Background(), "missing-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "memory record not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- List tests ---

func TestPostgresStore_List_WithFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now().Truncate(time.Second)

	// Count query.
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(StatusActive).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// Select query.
	rows := sqlmock.NewRows(memorySelectColumns).AddRow(
		"mem-001", now, now, "user-abc", "analyst", DimensionKnowledge,
		"Memory content here.", CategoryBusinessCtx, ConfidenceHigh, SourceUser,
		`[]`, `[]`, `{}`,
		StatusActive, nil, nil, nil,
	)
	mock.ExpectQuery("SELECT .+ FROM memory_records").
		WithArgs(StatusActive).
		WillReturnRows(rows)

	records, total, err := store.List(context.Background(), Filter{
		Status: StatusActive,
		Limit:  10,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Len(t, records, 1)
	assert.Equal(t, "mem-001", records[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_List_EmptyResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	mock.ExpectQuery("SELECT .+ FROM memory_records").
		WillReturnRows(sqlmock.NewRows(memorySelectColumns))

	records, total, err := store.List(context.Background(), Filter{})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, records)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_List_Pagination(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now().Truncate(time.Second)

	mock.ExpectQuery("SELECT COUNT").
		WithArgs("analyst").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(50)) //nolint:revive // test value

	rows := sqlmock.NewRows(memorySelectColumns).AddRow(
		"mem-010", now, now, "user-abc", "analyst", DimensionKnowledge,
		"Paginated record content.", CategoryGeneral, ConfidenceMedium, SourceUser,
		`[]`, `[]`, `{}`,
		StatusActive, nil, nil, nil,
	)
	mock.ExpectQuery("SELECT .+ FROM memory_records").
		WithArgs("analyst").
		WillReturnRows(rows)

	records, total, err := store.List(context.Background(), Filter{
		Persona: "analyst",
		Limit:   10,
		Offset:  10,
	})
	require.NoError(t, err)
	assert.Equal(t, 50, total) //nolint:revive // test value
	assert.Len(t, records, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- EntityLookup tests ---

func TestPostgresStore_EntityLookup_WithPersona(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now().Truncate(time.Second)

	rows := sqlmock.NewRows(memorySelectColumns).AddRow(
		"mem-001", now, now, "user-abc", "analyst", DimensionKnowledge,
		"Entity lookup result.", CategoryBusinessCtx, ConfidenceHigh, SourceUser,
		`["urn:li:dataset:foo"]`, `[]`, `{}`,
		StatusActive, nil, nil, nil,
	)

	mock.ExpectQuery("SELECT .+ FROM memory_records WHERE").
		WithArgs(
			sqlmock.AnyArg(), // entity_urns @> JSON
			StatusActive,
			"analyst",
		).
		WillReturnRows(rows)

	records, err := store.EntityLookup(context.Background(), "urn:li:dataset:foo", "analyst")
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.Equal(t, "mem-001", records[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_EntityLookup_WithoutPersona(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT .+ FROM memory_records WHERE").
		WithArgs(
			sqlmock.AnyArg(), // entity_urns @> JSON
			StatusActive,
		).
		WillReturnRows(sqlmock.NewRows(memorySelectColumns))

	records, err := store.EntityLookup(context.Background(), "urn:li:dataset:bar", "")
	require.NoError(t, err)
	assert.Empty(t, records)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- MarkStale tests ---

func TestPostgresStore_MarkStale_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE memory_records SET").
		WithArgs(
			sqlmock.AnyArg(), // status
			sqlmock.AnyArg(), // stale_reason
			sqlmock.AnyArg(), // stale_at
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // id
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.MarkStale(context.Background(), []string{"mem-001"}, "entity deprecated")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_MarkStale_EmptyIDs(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	// Should return nil without executing any query.
	err = store.MarkStale(context.Background(), []string{}, "no reason")
	assert.NoError(t, err)
}

// --- MarkVerified tests ---

func TestPostgresStore_MarkVerified_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE memory_records SET").
		WithArgs(
			sqlmock.AnyArg(), // last_verified
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // id
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.MarkVerified(context.Background(), []string{"mem-001"})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_MarkVerified_EmptyIDs(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	err = store.MarkVerified(context.Background(), []string{})
	assert.NoError(t, err)
}

// --- Supersede tests ---

func TestPostgresStore_Supersede_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE memory_records SET").
		WithArgs(
			sqlmock.AnyArg(), // status
			sqlmock.AnyArg(), // metadata
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // old id
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Supersede(context.Background(), "old-id", "new-id")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Supersede_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE memory_records SET").
		WithArgs(
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.Supersede(context.Background(), "missing-old", "new-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "memory record not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- buildUpdateColumns tests ---

func TestBuildUpdateColumns(t *testing.T) {
	tests := []struct {
		name       string
		updates    RecordUpdate
		hasUpdates bool
		wantErr    bool
	}{
		{
			name:       "empty update",
			updates:    RecordUpdate{},
			hasUpdates: false,
		},
		{
			name:       "content only",
			updates:    RecordUpdate{Content: "new content"},
			hasUpdates: true,
		},
		{
			name:       "category only",
			updates:    RecordUpdate{Category: CategoryCorrection},
			hasUpdates: true,
		},
		{
			name:       "confidence only",
			updates:    RecordUpdate{Confidence: ConfidenceLow},
			hasUpdates: true,
		},
		{
			name:       "dimension only",
			updates:    RecordUpdate{Dimension: DimensionEvent},
			hasUpdates: true,
		},
		{
			name:       "metadata only",
			updates:    RecordUpdate{Metadata: map[string]any{"key": "val"}},
			hasUpdates: true,
		},
		{
			name:       "embedding only",
			updates:    RecordUpdate{Embedding: []float32{0.1, 0.2}},
			hasUpdates: true,
		},
		{
			name: "all fields",
			updates: RecordUpdate{
				Content:    "updated",
				Category:   CategoryDataQuality,
				Confidence: ConfidenceHigh,
				Dimension:  DimensionPreference,
				Metadata:   map[string]any{"k": "v"},
				Embedding:  []float32{0.5},
			},
			hasUpdates: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, hasUpdates, err := buildUpdateColumns(tt.updates)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.hasUpdates, hasUpdates)
		})
	}
}

// --- applyPagination tests ---

func TestApplyPagination(t *testing.T) {
	t.Run("default order", func(t *testing.T) {
		qb := applyPagination(psq.Select("*").From(tableName), Filter{Limit: 10})
		query, _, err := qb.ToSql()
		require.NoError(t, err)
		assert.Contains(t, query, "ORDER BY created_at DESC")
	})

	t.Run("custom order", func(t *testing.T) {
		qb := applyPagination(psq.Select("*").From(tableName), Filter{
			Limit:   10,
			OrderBy: "last_verified ASC NULLS FIRST",
		})
		query, _, err := qb.ToSql()
		require.NoError(t, err)
		assert.Contains(t, query, "ORDER BY last_verified ASC NULLS FIRST")
	})
}

// --- applyFilter tests ---

func TestApplyFilter(t *testing.T) {
	now := time.Now()

	t.Run("all filters", func(t *testing.T) {
		qb := applyFilter(psq.Select("*").From(tableName), Filter{
			CreatedBy: "user-1",
			Persona:   "analyst",
			Dimension: DimensionKnowledge,
			Category:  CategoryCorrection,
			Status:    StatusActive,
			Source:    SourceUser,
			EntityURN: "urn:li:dataset:foo",
			Since:     &now,
			Until:     &now,
		})
		query, args, err := qb.ToSql()
		require.NoError(t, err)
		assert.Contains(t, query, "created_by")
		assert.Contains(t, query, "persona")
		assert.Contains(t, query, "dimension")
		assert.Contains(t, query, "category")
		assert.Contains(t, query, "status")
		assert.Contains(t, query, "source")
		assert.Contains(t, query, "entity_urns")
		assert.Contains(t, query, "created_at")
		// 7 scalar filters + entity_urns JSON + since + until = 9 args.
		assert.Len(t, args, 9) //nolint:revive // 9 is the expected count
	})

	t.Run("no filters", func(t *testing.T) {
		qb := applyFilter(psq.Select("*").From(tableName), Filter{})
		_, args, err := qb.ToSql()
		require.NoError(t, err)
		assert.Empty(t, args)
	})

	t.Run("single filter", func(t *testing.T) {
		qb := applyFilter(psq.Select("*").From(tableName), Filter{
			Persona: "admin",
		})
		query, args, err := qb.ToSql()
		require.NoError(t, err)
		assert.Contains(t, query, "persona")
		assert.Len(t, args, 1)
	})
}

// --- NewPostgresStore test ---

func TestNewPostgresStore(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	assert.NotNil(t, store)
}
