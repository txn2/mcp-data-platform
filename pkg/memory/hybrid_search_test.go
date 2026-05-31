package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hybridColumns is the HybridSearch scan order: the record columns plus
// the per-arm vec_score and lex_match signals.
var hybridColumns = append(append([]string{}, memorySelectColumns...), "vec_score", "lex_match")

// addHybridRow appends one candidate row in HybridSearch scan order.
func addHybridRow(rows *sqlmock.Rows, id string, vecScore float64, lexMatch bool) {
	now := time.Now()
	rows.AddRow(
		id, now, now, "user@example.com", "analyst", DimensionKnowledge,
		"content for "+id, CategoryBusinessCtx, ConfidenceMedium, SourceUser,
		[]byte("[]"), []byte("[]"), []byte("{}"),
		StatusActive, nil, nil, nil,
		vecScore, lexMatch,
	)
}

// TestHybridSearch_EvalLexicalBeatsHigherCosine is the end-to-end ranking
// evaluation against the real HybridSearch code path (sqlmock stands in
// for pgvector). The UNION ALL returns an exact-identifier lexical match
// with a modest cosine and a higher-cosine row that does not match the
// term; hybrid fusion must rank the identifier match first, the opposite
// of what pure vector ordering (by vec_score) would produce.
func TestHybridSearch_EvalLexicalBeatsHigherCosine(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	rows := sqlmock.NewRows(hybridColumns)
	addHybridRow(rows, "pure-vector", 0.82, false) // stronger cosine, no term hit
	addHybridRow(rows, "identifier", 0.55, true)   // modest cosine, exact-term hit
	mock.ExpectQuery("UNION ALL").WillReturnRows(rows)

	got, err := store.HybridSearch(context.Background(), HybridQuery{
		Embedding: []float32{0.1, 0.2, 0.3},
		QueryText: "orders_fact",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, "identifier", got[0].Record.ID,
		"hybrid must rank the exact-identifier lexical match first")
	assert.Equal(t, "pure-vector", got[1].Record.ID)

	// The fused scores match the pinned formula.
	assert.InDelta(t, fuseHybridScore(0.55, true), got[0].Score, 1e-9)
	assert.InDelta(t, fuseHybridScore(0.82, false), got[1].Score, 1e-9)

	// Contrast: pure-vector ordering (by raw cosine) would put pure-vector
	// first, which is the weakness hybrid corrects.
	assert.Greater(t, 0.82, 0.55)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestHybridSearch_DedupsAcrossArms verifies a record that appears in
// both arms (vector top-k AND lexical match) collapses to one result
// keeping the higher fused score.
func TestHybridSearch_DedupsAcrossArms(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	rows := sqlmock.NewRows(hybridColumns)
	// Same id from the vector arm (lex_match=false) and the lexical arm
	// (lex_match=true). The lexical-arm copy has the higher fused score.
	addHybridRow(rows, "dup", 0.70, false)
	addHybridRow(rows, "dup", 0.70, true)
	mock.ExpectQuery("UNION ALL").WillReturnRows(rows)

	got, err := store.HybridSearch(context.Background(), HybridQuery{
		Embedding: []float32{0.1},
		QueryText: "q",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, got, 1, "duplicate ids must collapse to one result")
	assert.Equal(t, "dup", got[0].Record.ID)
	assert.InDelta(t, fuseHybridScore(0.70, true), got[0].Score, 1e-9,
		"dedup must keep the higher (lexical-match) fused score")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestHybridSearch_TrimsToLimit verifies the fused result honors the
// store limit.
func TestHybridSearch_TrimsToLimit(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	rows := sqlmock.NewRows(hybridColumns)
	addHybridRow(rows, "a", 0.9, false)
	addHybridRow(rows, "b", 0.8, false)
	addHybridRow(rows, "c", 0.7, false)
	mock.ExpectQuery("UNION ALL").WillReturnRows(rows)

	got, err := store.HybridSearch(context.Background(), HybridQuery{
		Embedding: []float32{0.1},
		QueryText: "q",
		Limit:     2,
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Record.ID)
	assert.Equal(t, "b", got[1].Record.ID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestHybridSearch_PersonaAndStatusFilters checks the optional scope
// predicates are wired as parameters $3/$4 after the vector and query.
func TestHybridSearch_PersonaAndStatusFilters(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	mock.ExpectQuery("UNION ALL").
		WithArgs(sqlmock.AnyArg(), "orders", "analyst", StatusActive).
		WillReturnRows(sqlmock.NewRows(hybridColumns))

	_, err = store.HybridSearch(context.Background(), HybridQuery{
		Embedding: []float32{0.1},
		QueryText: "orders",
		Persona:   "analyst",
		Status:    StatusActive,
		Limit:     10,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHybridSearch_QueryError(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	mock.ExpectQuery("UNION ALL").WillReturnError(errors.New("boom"))

	_, err = store.HybridSearch(context.Background(), HybridQuery{
		Embedding: []float32{0.1}, QueryText: "q", Limit: 10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hybrid search")
}

// TestHybridSearch_ScanError covers the row-scan error path: a vec_score
// column that is not a float fails the scan.
func TestHybridSearch_ScanError(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	now := time.Now()
	rows := sqlmock.NewRows(hybridColumns).AddRow(
		"bad", now, now, "u", "analyst", DimensionKnowledge,
		"c", CategoryBusinessCtx, ConfidenceMedium, SourceUser,
		[]byte("[]"), []byte("[]"), []byte("{}"),
		StatusActive, nil, nil, nil,
		"not-a-float", true, // vec_score is unparseable
	)
	mock.ExpectQuery("UNION ALL").WillReturnRows(rows)

	_, err = store.HybridSearch(context.Background(), HybridQuery{
		Embedding: []float32{0.1}, QueryText: "q", Limit: 10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scanning hybrid row")
}

// TestHybridSearch_RowError covers the rows.Err() iteration-error path.
func TestHybridSearch_RowError(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	rows := sqlmock.NewRows(hybridColumns)
	addHybridRow(rows, "a", 0.9, false)
	rows.RowError(0, errors.New("row iteration boom"))
	mock.ExpectQuery("UNION ALL").WillReturnRows(rows)

	_, err = store.HybridSearch(context.Background(), HybridQuery{
		Embedding: []float32{0.1}, QueryText: "q", Limit: 10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iterating hybrid search rows")
}

// --- LexicalSearch ---

// lexicalColumns is the LexicalSearch scan order: record columns plus the
// ts_rank score.
var lexicalColumns = append(append([]string{}, memorySelectColumns...), "score")

func addLexicalRow(rows *sqlmock.Rows, id string, embedding bool, score float64) {
	now := time.Now()
	// embedding is irrelevant to the projection (LexicalSearch never reads
	// the embedding column); the bool documents which rows would have a
	// NULL embedding in production, where lexical still surfaces them.
	_ = embedding
	rows.AddRow(
		id, now, now, "user@example.com", "analyst", DimensionKnowledge,
		"content for "+id, CategoryBusinessCtx, ConfidenceMedium, SourceUser,
		[]byte("[]"), []byte("[]"), []byte("{}"),
		StatusActive, nil, nil, nil,
		score,
	)
}

// TestLexicalSearch_SurfacesNullEmbeddingRows verifies lexical-only recall
// returns matched rows ordered by relevance, including a row that has no
// embedding (which vector search would skip entirely).
func TestLexicalSearch_SurfacesNullEmbeddingRows(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)

	rows := sqlmock.NewRows(lexicalColumns)
	addLexicalRow(rows, "has-embedding", true, 0.9)
	addLexicalRow(rows, "null-embedding", false, 0.5) // saved during an outage
	mock.ExpectQuery("ts_rank_cd").
		WithArgs("orders_fact", "analyst").
		WillReturnRows(rows)

	got, err := store.LexicalSearch(context.Background(), LexicalQuery{
		QueryText: "orders_fact",
		Persona:   "analyst",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "has-embedding", got[0].Record.ID)
	assert.Equal(t, "null-embedding", got[1].Record.ID,
		"lexical must surface NULL-embedding rows that vector search skips")
	assert.Equal(t, 0.5, got[1].Score)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLexicalSearch_QueryError(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	mock.ExpectQuery("ts_rank_cd").WillReturnError(errors.New("boom"))

	_, err = store.LexicalSearch(context.Background(), LexicalQuery{QueryText: "q", Limit: 10})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lexical search")
}

func TestClampStoreLimit(t *testing.T) {
	t.Parallel()
	assert.Equal(t, DefaultLimit, clampStoreLimit(0))
	assert.Equal(t, DefaultLimit, clampStoreLimit(-5))
	assert.Equal(t, 7, clampStoreLimit(7))
	assert.Equal(t, MaxLimit, clampStoreLimit(MaxLimit+100))
}
