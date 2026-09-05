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
	"sink_class",
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
		SinkClass:  SinkSchemaEntity,
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
			record.ID, record.CreatedBy, record.Persona, record.Dimension, record.SinkClass,
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
	record.EmbeddingModel = "nomic-embed-text"
	record.EmbeddingTextHash = []byte{0xaa, 0xbb}

	mock.ExpectExec("INSERT INTO memory_records").
		WithArgs(
			record.ID, record.CreatedBy, record.Persona, record.Dimension, record.SinkClass,
			record.Content, record.Category, record.Confidence, record.Source,
			sqlmock.AnyArg(), // entity_urns JSON
			sqlmock.AnyArg(), // related_columns JSON
			sqlmock.AnyArg(), // metadata JSON
			record.Status,
			sqlmock.AnyArg(),         // embedding (pgvector)
			record.EmbeddingModel,    // embedding_model
			record.EmbeddingTextHash, // embedding_text_hash
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
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
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
		"mem-001", now, now, "user-abc", "analyst", DimensionKnowledge, "schema_entity",
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
		"mem-001", now, now, "user-abc", "analyst", DimensionKnowledge, "schema_entity",
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

func TestPostgresStore_List_SinkClassFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now().Truncate(time.Second)

	// The sink_class predicate must reach SQL as a bound arg, so business_knowledge
	// memories are not collapsed with schema_entity (both share the knowledge dim).
	mock.ExpectQuery("SELECT COUNT.+sink_class").
		WithArgs(SinkBusinessKnowledge).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	rows := sqlmock.NewRows(memorySelectColumns).AddRow(
		"mem-bk", now, now, "user-abc", "analyst", DimensionKnowledge, SinkBusinessKnowledge,
		"Loyalty points are not revenue.", CategoryBusinessCtx, ConfidenceHigh, SourceUser,
		`[]`, `[]`, `{}`,
		StatusActive, nil, nil, nil,
	)
	mock.ExpectQuery("SELECT .+ FROM memory_records WHERE sink_class").
		WithArgs(SinkBusinessKnowledge).
		WillReturnRows(rows)

	records, total, err := store.List(context.Background(), Filter{
		SinkClass: SinkBusinessKnowledge,
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, records, 1)
	assert.Equal(t, SinkBusinessKnowledge, records[0].SinkClass)
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

// TestPostgresStore_List_MalformedRowJSON pins the row-scan error path: a row
// whose JSON column does not unmarshal must fail the listing, not yield a
// half-populated record.
func TestPostgresStore_List_MalformedRowJSON(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now()

	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	rows := sqlmock.NewRows(memorySelectColumns).AddRow(
		"mem-bad", now, now, "user-abc", "analyst", DimensionKnowledge, "schema_entity",
		"content", CategoryGeneral, ConfidenceMedium, SourceUser,
		[]byte(`not-json`), []byte(`[]`), []byte(`{}`),
		StatusActive, nil, nil, nil,
	)
	mock.ExpectQuery("SELECT .+ FROM memory_records").WillReturnRows(rows)

	_, _, err = store.List(context.Background(), Filter{})
	require.Error(t, err, "a malformed JSON column must fail the scan")
}

// TestPostgresStore_List_ScanTypeMismatch pins the Scan-error branch itself:
// a column value that cannot convert to its destination type (a non-time
// created_at) must fail the listing.
func TestPostgresStore_List_ScanTypeMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	rows := sqlmock.NewRows(memorySelectColumns).AddRow(
		"mem-bad", "not-a-time", "not-a-time", "user-abc", "analyst", DimensionKnowledge, "schema_entity",
		"content", CategoryGeneral, ConfidenceMedium, SourceUser,
		[]byte(`[]`), []byte(`[]`), []byte(`{}`),
		StatusActive, nil, nil, nil,
	)
	mock.ExpectQuery("SELECT .+ FROM memory_records").WillReturnRows(rows)

	_, _, err = store.List(context.Background(), Filter{})
	require.Error(t, err, "a scan type mismatch must fail the listing")
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
		"mem-010", now, now, "user-abc", "analyst", DimensionKnowledge, "schema_entity",
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
		"mem-001", now, now, "user-abc", "analyst", DimensionKnowledge, "schema_entity",
		"Entity lookup result.", CategoryBusinessCtx, ConfidenceHigh, SourceUser,
		`["urn:li:dataset:foo"]`, `[]`, `{}`,
		StatusActive, nil, nil, nil,
	)

	mock.ExpectQuery("SELECT .+ FROM memory_records WHERE").
		WithArgs(
			sqlmock.AnyArg(), // entity_urns @> JSON
			StatusActive,
			"analyst",
			MetaKeyInsightStatus, // authoritative pending marker (push path)
			MetaKeyLegacyStatus,  // fallback marker (migrated candidates)
			InsightStatusPending,
		).
		WillReturnRows(rows)

	records, err := store.EntityLookup(context.Background(), "urn:li:dataset:foo", "analyst", "")
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.Equal(t, "mem-001", records[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_EntityLookup_WithoutPersona covers the persona-scoped enrichment
// push path (persona == "", createdBy == ""). It asserts the precedence-honoring
// COALESCE(insight_status, legacy_status) pending-exclusion predicate is emitted so an
// un-evaluated candidate (live or migrated) is never pushed into an agent's context
// before it is grounded (#745).
func TestPostgresStore_EntityLookup_WithoutPersona(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT .+ FROM memory_records WHERE .+COALESCE.NULLIF.metadata ->> .+ IS DISTINCT FROM").
		WithArgs(
			sqlmock.AnyArg(), // entity_urns @> JSON
			StatusActive,
			MetaKeyInsightStatus, // authoritative pending marker (push path)
			MetaKeyLegacyStatus,  // fallback marker (migrated candidates)
			InsightStatusPending,
		).
		WillReturnRows(sqlmock.NewRows(memorySelectColumns))

	records, err := store.EntityLookup(context.Background(), "urn:li:dataset:bar", "", "")
	require.NoError(t, err)
	assert.Empty(t, records)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_EntityLookup_UserScopedKeepsPending asserts the user-scoped
// search path (createdBy set) does NOT apply the pending exclusion: a caller may see
// their own un-evaluated candidates. The query filters by created_by instead (#745).
func TestPostgresStore_EntityLookup_UserScopedKeepsPending(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT .+ FROM memory_records WHERE").
		WithArgs(
			sqlmock.AnyArg(), // entity_urns @> JSON
			StatusActive,
			"user@example.com", // created_by scope, no pending exclusion
		).
		WillReturnRows(sqlmock.NewRows(memorySelectColumns))

	records, err := store.EntityLookup(context.Background(), "urn:li:dataset:qux", "", "user@example.com")
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

// listSQL runs one List and returns the statements it issued, so a test can
// assert what reached the database rather than what a builder was asked for.
func listSQL(t *testing.T, filter Filter) []string {
	t.Helper()
	var issued []string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(
		sqlmock.QueryMatcherFunc(func(expected, actual string) error {
			issued = append(issued, actual)
			return sqlmock.QueryMatcherRegexp.Match(expected, actual)
		})))
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectQuery(`SELECT COUNT`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows(recordColumns()))

	_, _, err = NewPostgresStore(db).List(context.Background(), filter)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	return issued
}

// The staleness watcher's batch is entity-linked records, and this is the
// predicate that makes it so. Containment rather than jsonb_array_length: a
// record written with no URNs holds the JSON scalar `null`, on which
// jsonb_array_length errors against a real database (#1625). Both statements
// carry it, since the count and the page must describe the same set.
func TestPostgresStore_List_EntityLinkedFiltersInSQL(t *testing.T) {
	const predicate = "entity_urns @> '[]'::jsonb AND entity_urns <> '[]'::jsonb"

	issued := listSQL(t, Filter{Status: StatusActive, EntityLinked: true})
	require.Len(t, issued, 2, "List issues a count and a page")
	for _, stmt := range issued {
		assert.Contains(t, stmt, predicate)
		assert.NotContains(t, stmt, "jsonb_array_length",
			"jsonb_array_length errors on the JSON scalar null this column can hold")
	}

	// Every other reader of this store is unaffected: the predicate appears
	// only when the filter asks for it.
	for _, stmt := range listSQL(t, Filter{Status: StatusActive}) {
		assert.NotContains(t, stmt, "entity_urns @>")
	}
}

// --- MarkVerified tests ---

func TestPostgresStore_MarkVerified_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	// The statement is matched whole, not by prefix: verification must set
	// last_verified and nothing else. A pass that also re-dated updated_at made
	// every memory record look edited minutes ago (#1625), and a prefix match
	// would accept that statement again.
	mock.ExpectExec(`^UPDATE memory_records SET last_verified = \$1 WHERE id IN \(\$2\)$`).
		WithArgs(
			sqlmock.AnyArg(), // last_verified
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

	// The metadata merge conditionally advances insight_status to superseded, so the
	// SQL binds the insight_status key (twice: jsonb_exists + jsonb_build_object) and
	// the superseded value alongside the lifecycle status and the superseded_by patch.
	mock.ExpectExec("UPDATE memory_records SET").
		WithArgs(
			StatusSuperseded,        // lifecycle status column
			sqlmock.AnyArg(),        // metadata superseded_by patch (jsonb)
			MetaKeyInsightStatus,    // jsonb_exists key
			MetaKeyInsightStatus,    // jsonb_build_object key
			InsightStatusSuperseded, // jsonb_build_object value
			sqlmock.AnyArg(),        // updated_at
			"old-id",                // where id
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
			name:       "status only",
			updates:    RecordUpdate{Status: StatusArchived},
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

func TestBuildUpdateColumns_SetsStatusColumn(t *testing.T) {
	qb, hasUpdates, err := buildUpdateColumns(RecordUpdate{Status: StatusArchived})
	require.NoError(t, err)
	require.True(t, hasUpdates)
	query, args, err := qb.ToSql()
	require.NoError(t, err)
	assert.Contains(t, query, colStatus+" =", "update must set the status column")
	assert.Contains(t, args, StatusArchived)
}

// --- applyPagination tests ---

func TestApplyPagination(t *testing.T) {
	t.Run("default order", func(t *testing.T) {
		qb := applyPagination(psq.Select("*").From(tableName), Filter{Limit: 10})
		query, _, err := qb.ToSql()
		require.NoError(t, err)
		assert.Contains(t, query, "ORDER BY created_at DESC, id DESC")
	})

	t.Run("custom order with nulls handling", func(t *testing.T) {
		qb := applyPagination(psq.Select("*").From(tableName), Filter{
			Limit:         10,
			SortBy:        "last_verified",
			SortDirection: SortAscNullsFirst,
		})
		query, _, err := qb.ToSql()
		require.NoError(t, err)
		assert.Contains(t, query, "ORDER BY last_verified ASC NULLS FIRST, id ASC")
	})

	t.Run("non-allowlisted column falls back to default", func(t *testing.T) {
		// Regression for the ORDER BY splice: a hostile SortBy must never
		// reach the SQL text.
		qb := applyPagination(psq.Select("*").From(tableName), Filter{
			Limit:  10,
			SortBy: "created_at; DROP TABLE memory_records--",
		})
		query, _, err := qb.ToSql()
		require.NoError(t, err)
		assert.Contains(t, query, "ORDER BY created_at DESC, id DESC")
		assert.NotContains(t, query, "DROP TABLE")
	})

	t.Run("non-allowlisted direction falls back to DESC", func(t *testing.T) {
		qb := applyPagination(psq.Select("*").From(tableName), Filter{
			Limit:         10,
			SortBy:        "updated_at",
			SortDirection: "ASC; DELETE FROM memory_records--",
		})
		query, _, err := qb.ToSql()
		require.NoError(t, err)
		assert.Contains(t, query, "ORDER BY updated_at DESC, id DESC")
		assert.NotContains(t, query, "DELETE FROM")
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

// TestPostgresStore_List_InsightStatusFilter covers the List-path counterpart of
// the search-arm predicate: the entity-keyed insight lookup pages the whole
// matching set, so pushing the exact insight status into SQL keeps that walk to
// the applied rows instead of reading every capturer's active knowledge records
// and discarding most of them per record (#980 B2).
func TestPostgresStore_List_InsightStatusFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	now := time.Now().Truncate(time.Second)

	mock.ExpectQuery("SELECT COUNT").
		WithArgs(DimensionKnowledge, StatusActive, "applied").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	rows := sqlmock.NewRows(memorySelectColumns).AddRow(
		"mem-applied", now, now, "alice@example.com", "analyst", DimensionKnowledge, "schema_entity",
		"Refunds are booked net of tax.", CategoryBusinessCtx, ConfidenceHigh, SourceUser,
		`[]`, `[]`, `{"insight_status":"applied"}`,
		StatusActive, nil, nil, nil,
	)
	mock.ExpectQuery("insight_status").
		WithArgs(DimensionKnowledge, StatusActive, "applied").
		WillReturnRows(rows)

	records, total, err := store.List(context.Background(), Filter{
		Dimension:     DimensionKnowledge,
		Status:        StatusActive,
		InsightStatus: "applied",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, records, 1)
	assert.Equal(t, "mem-applied", records[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}
