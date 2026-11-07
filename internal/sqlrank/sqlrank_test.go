package sqlrank

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The query text and vector are numbered after the candidates' own
// arguments, and the lexical mode binds no vector.
func TestBuildRankedNumbering(t *testing.T) {
	r := Ranking{Candidates: "SELECT id, name, embedding, fts FROM t WHERE owner_id = $1", Args: []any{"u1"}, FTS: "fts", QueryText: "q", Embedding: []float32{0.1}, Limit: 5, Offset: 995}
	hybrid, args := BuildRanked(r)
	assert.Contains(t, hybrid, "plainto_tsquery('english', $2)")
	assert.Contains(t, hybrid, "c.embedding <=> $3")
	assert.Contains(t, hybrid, ">= 0.45")
	assert.Contains(t, hybrid, "LIMIT 5 OFFSET 995")
	assert.Len(t, args, 3)

	r.Embedding, r.Offset = nil, 5000
	lexical, args := BuildRanked(r)
	assert.Contains(t, lexical, "ts_rank_cd(c.fts, plainto_tsquery('english', $2), 33)")
	assert.Contains(t, lexical, "WHERE c.lex ORDER BY")
	assert.Contains(t, lexical, "LIMIT 5 OFFSET 995", "paging stops at the ranked depth")
	assert.Equal(t, []any{"u1", "q"}, args)
}

func TestRank(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectQuery("WITH c AS MATERIALIZED").
		WillReturnRows(sqlmock.NewRows([]string{"id", "score", "total"}).AddRow("a", 0.9, 4).AddRow("b", 0.5, 4))
	got, err := Rank(context.Background(), db, Ranking{Candidates: "SELECT 1", FTS: "fts", QueryText: "q"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, got.IDs)
	assert.Equal(t, 4, got.Total)
	assert.InDelta(t, 0.5, got.Scores["b"], 1e-9)

	mock.ExpectQuery("WITH c AS MATERIALIZED").WillReturnError(assert.AnError)
	_, err = Rank(context.Background(), db, Ranking{Candidates: "SELECT 1", FTS: "fts", QueryText: "q"})
	require.Error(t, err)

	mock.ExpectQuery("WITH c AS MATERIALIZED").WillReturnRows(sqlmock.NewRows([]string{"id", "score", "total"}).AddRow("a", "x", 1))
	_, err = Rank(context.Background(), db, Ranking{Candidates: "SELECT 1", FTS: "fts", QueryText: "q"})
	require.Error(t, err, "a row that does not scan fails the ranking")
	require.NoError(t, mock.ExpectationsWereMet())
}
