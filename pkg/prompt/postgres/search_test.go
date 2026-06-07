package postgres

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

func TestFuseHybridScore(t *testing.T) {
	// Pure semantic, no lexical match: 0.6 * ((1+1)/2) = 0.6.
	assert.InDelta(t, 0.6, fuseHybridScore(1.0, false), 1e-9)
	// Perfect semantic + lexical match: 0.6 + 0.4 = 1.0.
	assert.InDelta(t, 1.0, fuseHybridScore(1.0, true), 1e-9)
	// Worst semantic (-1 -> 0), no lexical: 0.
	assert.InDelta(t, 0.0, fuseHybridScore(-1.0, false), 1e-9)
	// Worst semantic but lexical match: 0.4.
	assert.InDelta(t, 0.4, fuseHybridScore(-1.0, true), 1e-9)
	// A lexical match always beats the same row without one.
	assert.Greater(t, fuseHybridScore(0.2, true), fuseHybridScore(0.2, false))
}

func TestSortByScoreDesc(t *testing.T) {
	in := []prompt.ScoredPrompt{
		{Prompt: prompt.Prompt{Name: "b"}, Score: 0.5},
		{Prompt: prompt.Prompt{Name: "a"}, Score: 0.9},
		{Prompt: prompt.Prompt{Name: "c"}, Score: 0.5},
	}
	sortByScoreDesc(in)
	assert.Equal(t, "a", in[0].Prompt.Name) // highest score first
	// Ties broken by name ascending for determinism.
	assert.Equal(t, "b", in[1].Prompt.Name)
	assert.Equal(t, "c", in[2].Prompt.Name)
}

func TestTruncate(t *testing.T) {
	in := []prompt.ScoredPrompt{{}, {}, {}}
	assert.Len(t, truncate(in, 2), 2)
	assert.Len(t, truncate(in, 5), 3) // n larger than slice returns all
	assert.Len(t, truncate(in, 0), 0)
}

func TestPromptVisibilityClause(t *testing.T) {
	t.Run("admin sees all approved (no clause)", func(t *testing.T) {
		clause, args, next := promptVisibilityClause(prompt.SearchQuery{IsAdmin: true}, 3)
		assert.Empty(t, clause)
		assert.Empty(t, args)
		assert.Equal(t, 3, next)
	})

	t.Run("admin with explicit scope", func(t *testing.T) {
		clause, args, _ := promptVisibilityClause(prompt.SearchQuery{IsAdmin: true, Scope: "global"}, 3)
		assert.Contains(t, clause, "scope = $3")
		assert.Equal(t, []any{"global"}, args)
	})

	t.Run("non-admin without persona", func(t *testing.T) {
		clause, args, next := promptVisibilityClause(
			prompt.SearchQuery{OwnerEmail: "u@x.com"}, 3)
		assert.Contains(t, clause, "scope = 'global'")
		assert.Contains(t, clause, "scope = 'personal' AND owner_email = $3")
		assert.NotContains(t, clause, "ANY(personas)")
		assert.Equal(t, []any{"u@x.com"}, args)
		assert.Equal(t, 4, next)
	})

	t.Run("non-admin with persona", func(t *testing.T) {
		clause, args, next := promptVisibilityClause(
			prompt.SearchQuery{OwnerEmail: "u@x.com", Persona: "analyst"}, 3)
		assert.Contains(t, clause, "owner_email = $3")
		assert.Contains(t, clause, "$4 = ANY(personas)")
		assert.Equal(t, []any{"u@x.com", "analyst"}, args)
		assert.Equal(t, 5, next)
	})

	t.Run("non-admin with explicit scope and persona", func(t *testing.T) {
		clause, args, _ := promptVisibilityClause(
			prompt.SearchQuery{OwnerEmail: "u@x.com", Persona: "analyst", Scope: "persona"}, 3)
		// scope filter binds $3, then visibility binds $4 (owner) and $5 (persona).
		assert.Contains(t, clause, "scope = $3")
		assert.Contains(t, clause, "owner_email = $4")
		assert.Contains(t, clause, "$5 = ANY(personas)")
		assert.Equal(t, []any{"persona", "u@x.com", "analyst"}, args)
	})
}

func TestSearch_Hybrid(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := New(db)

	argsJSON, _ := json.Marshal([]prompt.Argument{})
	cols := append(append([]string{}, selectColumns...), "vec_score", "lex_match")
	hybridRow := func(id, name string, vec float64, lex bool) []driver.Value {
		r := append([]driver.Value(nil), promptRow(id, name, "global", argsJSON, "")...)
		return append(r, driver.Value(vec), driver.Value(lex))
	}
	rows := sqlmock.NewRows(cols).
		AddRow(hybridRow("id-near", "near", 0.95, false)...).
		AddRow(hybridRow("id-exact", "exact", 0.40, true)...)

	mock.ExpectQuery("SELECT .+ FROM prompts").
		WithArgs(sqlmock.AnyArg(), "sales report").
		WillReturnRows(rows)

	got, err := store.Search(context.Background(), prompt.SearchQuery{
		Embedding: []float32{0.1, 0.2, 0.3},
		QueryText: "sales report",
		IsAdmin:   true,
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	// near: 0.6*((0.95+1)/2) = 0.585; exact: 0.6*((0.4+1)/2)+0.4 = 0.82.
	// The lexical exact match outranks the merely-near row.
	assert.Equal(t, "exact", got[0].Prompt.Name)
	assert.Equal(t, "near", got[1].Prompt.Name)
	assert.Greater(t, got[0].Score, got[1].Score)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSearch_HybridDedupsAcrossArms(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := New(db)

	argsJSON, _ := json.Marshal([]prompt.Argument{})
	cols := append(append([]string{}, selectColumns...), "vec_score", "lex_match")
	hybridRow := func(id, name string, vec float64, lex bool) []driver.Value {
		r := append([]driver.Value(nil), promptRow(id, name, "global", argsJSON, "")...)
		return append(r, driver.Value(vec), driver.Value(lex))
	}
	// The same prompt id surfaces from both arms (vector arm: lex=false,
	// lexical arm: lex=true). Dedup must keep one entry with the higher
	// (lexical-boosted) score.
	rows := sqlmock.NewRows(cols).
		AddRow(hybridRow("dup", "dup", 0.50, false)...).
		AddRow(hybridRow("dup", "dup", 0.50, true)...)

	mock.ExpectQuery("SELECT .+ FROM prompts").
		WithArgs(sqlmock.AnyArg(), "x").
		WillReturnRows(rows)

	got, err := store.Search(context.Background(), prompt.SearchQuery{
		Embedding: []float32{0.1},
		QueryText: "x",
		IsAdmin:   true,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	// Higher (lexical-boosted) score retained: 0.6*0.75 + 0.4 = 0.85.
	assert.InDelta(t, 0.85, got[0].Score, 1e-9)
}

func TestSearch_Lexical(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := New(db)

	argsJSON, _ := json.Marshal([]prompt.Argument{})
	cols := append(append([]string{}, selectColumns...), "lex_rank")
	lexRow := append([]driver.Value(nil), promptRow("id-1", "match", "personal", argsJSON, "u@x.com")...)
	lexRow = append(lexRow, driver.Value(0.73))
	rows := sqlmock.NewRows(cols).AddRow(lexRow...)

	// Nil embedding selects the lexical-only path: $1 is the query text, $2 the
	// caller email for personal visibility.
	mock.ExpectQuery("SELECT .+ FROM prompts").
		WithArgs("inventory", "u@x.com").
		WillReturnRows(rows)

	got, err := store.Search(context.Background(), prompt.SearchQuery{
		QueryText:  "inventory",
		OwnerEmail: "u@x.com",
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "match", got[0].Prompt.Name)
	assert.InDelta(t, 0.73, got[0].Score, 1e-9)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSearch_LexicalQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := New(db)

	mock.ExpectQuery("SELECT .+ FROM prompts").
		WillReturnError(assert.AnError)

	_, err = store.Search(context.Background(), prompt.SearchQuery{QueryText: "x", IsAdmin: true})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "lexical"))
}

// ensure pq import is used (selectColumns rows use pq.Array via promptRow).
var _ = pq.Array
