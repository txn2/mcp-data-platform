// Package sqlrank ranks a candidate set of rows in one PostgreSQL statement
// (#2500): every row whose stored full-text column matches the query, and
// every row whose blended semantic score reaches opranking.ScoreFloor, scored
// the way every hybrid ranker on the platform scores and paged with a count of
// all results.
//
// The candidates are a statement the caller builds, which is how a caller's
// own filter (an owner, a producer, a person's reach) decides what can be
// ranked: a row is ranked exactly when that statement would return it. They
// are materialized and scored by exact distance, because an HNSW scan filters
// after its nearest-neighbor step and a narrow candidate set would keep few or
// none of the rows it returned.
package sqlrank

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/internal/opranking"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// Ranking is what a ranked statement scores a candidate set by.
type Ranking struct {
	// Candidates is a statement selecting id, name, embedding and the stored
	// full-text column named FTS, and its arguments ($-numbered).
	Candidates string
	Args       []any
	FTS        string
	QueryText  string
	Embedding  []float32 // nil ranks lexically
	Limit      int
	Offset     int
}

// hybridSemanticWeight and the lexical match value blend a row's normalized
// cosine with a lexical match, the way every hybrid ranker on the platform
// scores (opranking.SemanticWeight).
const hybridSemanticWeight = opranking.SemanticWeight

// lexRankNormalization divides the lexical rank by 1 + log(document length)
// and maps it into (0,1), so a short dense match outranks a long single
// mention (#587).
const lexRankNormalization = 1 | 32

// BuildRanked renders a ranked statement over the candidates. Every candidate
// whose text matches is a result, and so is one whose blended semantic score
// reaches opranking.ScoreFloor: where the embedding model stops telling
// related text from unrelated. The candidates are materialized and scored by
// exact distance, because an HNSW scan filters after its nearest-neighbor
// step and a narrow reach would keep few or none of the rows it returned.
// Rows come back as (id, score, total).
func BuildRanked(r Ranking) (query string, args []any) {
	limit, offset := portaldomain.RankedPage(r.Limit, r.Offset)
	textPos := len(r.Args) + 1
	args = append(append([]any{}, r.Args...), r.QueryText)
	tsq := fmt.Sprintf("plainto_tsquery('english', $%d)", textPos)

	var score, keep string
	if len(r.Embedding) > 0 {
		args = append(args, pgvector.NewVector(r.Embedding))
		semantic := fmt.Sprintf("CASE WHEN c.embedding IS NULL THEN 0.5 ELSE (2 - (c.embedding <=> $%d)) / 2 END", textPos+1)
		score = fmt.Sprintf("%g * (%s) + %g * CASE WHEN c.lex THEN 1 ELSE 0 END",
			hybridSemanticWeight, semantic, 1-hybridSemanticWeight)
		keep = fmt.Sprintf("c.lex OR %g * (%s) >= %g", hybridSemanticWeight, semantic, opranking.ScoreFloor)
	} else {
		score = fmt.Sprintf("ts_rank_cd(c.%s, %s, %d)", r.FTS, tsq, lexRankNormalization)
		keep = "c.lex"
	}
	// #nosec G201 -- the candidate statement is builder-rendered with bound
	// placeholders, FTS is a column name the store chooses, the weights and
	// floor are constants, and limit and offset are clamped ints. No caller
	// text reaches the statement.
	query = fmt.Sprintf(
		"WITH c AS MATERIALIZED (SELECT r.*, (r.%s @@ %s) AS lex FROM (%s) r) "+
			"SELECT c.id, %s AS score, COUNT(*) OVER () AS total FROM c WHERE %s "+
			"ORDER BY score DESC, c.name, c.id LIMIT %d OFFSET %d",
		r.FTS, tsq, r.Candidates, score, keep, limit, offset)
	return query, args
}

// Ranked is one ranked page: ids in rank order, each one's score, and how
// many results there are in all.
type Ranked struct {
	IDs    []string
	Scores map[string]float64
	Total  int
}

// Rank runs a ranked statement.
func Rank(ctx context.Context, db *sql.DB, r Ranking) (Ranked, error) {
	query, args := BuildRanked(r)
	out := Ranked{Scores: map[string]float64{}}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, fmt.Errorf("ranking: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query
	for rows.Next() {
		var (
			id    string
			score float64
		)
		if err := rows.Scan(&id, &score, &out.Total); err != nil {
			return out, fmt.Errorf("scanning ranked row: %w", err)
		}
		out.IDs = append(out.IDs, id)
		out.Scores[id] = score
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("iterating ranked rows: %w", err)
	}
	return out, nil
}
