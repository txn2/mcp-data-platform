package callrecord

import (
	"context"
	"fmt"
	"sort"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/pgvector/pgvector-go"
)

// A recorded call is findable two ways: by the words its author wrote (the
// purpose, the statement, the request line) and by what those words mean. The
// two arms run separately against the index each can use — the GIN full-text
// index and the hnsw vector index — and are fused in Go, which is the shape the
// asset, prompt and memory searches already take. A single blended ORDER BY
// would forfeit both indexes.

// SearchQuery is one search over the catalog.
type SearchQuery struct {
	// Text is the natural-language query, matched lexically.
	Text string
	// Embedding is the query vector. Empty selects lexical-only ranking,
	// which is what a deployment with no embedding provider gets.
	Embedding []float32
	// UserID scopes the search to one caller's records. A search with no
	// caller returns nothing rather than everyone's calls.
	UserID string
	Limit  int
}

// Scored is one record with its relevance.
type Scored struct {
	Record Record
	Score  float64
}

// defaultSearchLimit bounds a search that states no limit.
const defaultSearchLimit = 10

// EffectiveLimit returns the bounded limit for a query.
func (q SearchQuery) EffectiveLimit() int {
	if q.Limit <= 0 || q.Limit > MaxPerPage {
		return defaultSearchLimit
	}
	return q.Limit
}

// IndexText is what a record is searched and embedded by: the sentence its
// caller wrote about why, and what the call actually did. Both are needed — a
// purpose alone does not say which table, and a statement alone does not say
// what question it answers.
//
// It is deliberately the same corpus the lexical index covers (ftsExpr, and the
// GIN index migration 000107 builds on it). The two arms of a search must agree
// about what a record says, or a record found by its words would rank against a
// vector computed from something else. The targets are left out for that reason
// and because a statement already names the tables it reads.
func IndexText(rec Record) string {
	parts := []string{
		rec.Purpose,
		rec.Statement,
		strings.TrimSpace(rec.Method + " " + rec.Path),
		rec.OperationID,
	}
	return strings.TrimSpace(strings.Join(nonEmpty(parts), "\n"))
}

func nonEmpty(in []string) []string {
	out := in[:0:0]
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// ftsExpr is the full-text expression the lexical arm matches and ranks
// against. It is spelled exactly as migration 000107's GIN index, so the
// planner uses that index rather than recomputing the vector per row.
const ftsExpr = `to_tsvector('english', purpose || ' ' || statement || ' ' || method || ' ' || path || ' ' || operation_id)`

// searchScope is what a search will consider at all: the caller's own records,
// and only calls that succeeded. A failed call is a record worth reading on the
// catalog page, but it is never an answer to "how do I get this data".
const searchScope = `r.user_id = ? AND r.success`

// Search ranks the caller's successful records by relevance.
//
// The outcome is not a filter here but a signal on the hit: an agent looking
// for a query to reuse should see that one was satisfied and re-run by others
// while another merely ran, and choose. Filtering to satisfied records only
// would hide every good query nobody has cited yet.
func (s *PostgresStore) Search(ctx context.Context, q SearchQuery) ([]Scored, error) {
	if q.UserID == "" {
		return nil, nil
	}
	if len(q.Embedding) == 0 {
		return s.searchLexical(ctx, q)
	}
	return s.searchHybrid(ctx, q)
}

// searchLexical ranks by full-text relevance alone.
func (s *PostgresStore) searchLexical(ctx context.Context, q SearchQuery) ([]Scored, error) {
	if strings.TrimSpace(q.Text) == "" {
		return nil, nil
	}
	limit := q.EffectiveLimit()
	inner := derived(Filter{}).
		Column("ts_rank_cd("+ftsExpr+", plainto_tsquery('english', ?)) AS score", q.Text).
		Where(ftsExpr+" @@ plainto_tsquery('english', ?)", q.Text).
		Where(searchScope, q.UserID)

	query, args, err := paged(psq.Select("o.*").
		FromSelect(outcomeOver(inner), "o").
		OrderBy("o.score DESC", "o.reuse_count DESC"), limit).ToSql()
	if err != nil {
		return nil, fmt.Errorf("building call record search: %w", err)
	}
	return s.scanScored(ctx, query, args, limit)
}

// searchHybrid runs the vector arm and the lexical arm and fuses them.
func (s *PostgresStore) searchHybrid(ctx context.Context, q SearchQuery) ([]Scored, error) {
	limit := q.EffectiveLimit()
	vec := pgvector.NewVector(q.Embedding)

	vecInner := derived(Filter{}).
		Column("1 - (r.embedding <=> ?) AS score", vec).
		Where("r.embedding IS NOT NULL").
		Where(searchScope, q.UserID)
	vecQuery, vecArgs, err := paged(psq.Select("o.*").
		FromSelect(outcomeOver(vecInner), "o").
		OrderBy("o.score DESC"), limit).ToSql()
	if err != nil {
		return nil, fmt.Errorf("building call record vector search: %w", err)
	}
	hits, err := s.scanScored(ctx, vecQuery, vecArgs, limit)
	if err != nil {
		return nil, err
	}

	lexical, err := s.searchLexical(ctx, q)
	if err != nil {
		return nil, err
	}
	return fuse(hits, lexical, limit), nil
}

// paged applies a positive limit. The guard is what tells the compiler and the
// reader that the conversion is safe: a limit is a page size, never negative.
func paged(qb sq.SelectBuilder, limit int) sq.SelectBuilder {
	if limit <= 0 {
		return qb
	}
	return qb.Limit(uint64(limit))
}

// lexicalWeight is how much a lexical match contributes to a fused score. The
// vector arm's cosine already lands in (0,1); a lexical hit is scaled below it
// so that when both arms return a record the fused score exceeds either alone,
// and a lexical-only hit still ranks against semantic ones.
const lexicalWeight = 0.5

// fuse merges the two arms, keeping the better score per record.
func fuse(vector, lexical []Scored, limit int) []Scored {
	byID := make(map[string]Scored, len(vector)+len(lexical))
	for _, hit := range vector {
		byID[hit.Record.ID] = hit
	}
	for _, hit := range lexical {
		scaled := hit
		scaled.Score = lexicalWeight * hit.Score
		if existing, ok := byID[hit.Record.ID]; ok {
			existing.Score += scaled.Score
			byID[hit.Record.ID] = existing
			continue
		}
		byID[hit.Record.ID] = scaled
	}

	fused := make([]Scored, 0, len(byID))
	for _, hit := range byID {
		fused = append(fused, hit)
	}
	sort.SliceStable(fused, func(i, j int) bool {
		if fused[i].Score != fused[j].Score {
			return fused[i].Score > fused[j].Score
		}
		return fused[i].Record.ReuseCount > fused[j].Record.ReuseCount
	})
	if len(fused) > limit {
		fused = fused[:limit]
	}
	return fused
}

// scanScored reads a search statement's rows: a record and its relevance.
func (s *PostgresStore) scanScored(ctx context.Context, query string, args []any, limit int) ([]Scored, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching call records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	scored := make([]Scored, 0, listCapacity(limit))
	for rows.Next() {
		rec, score, err := scanRecordWithScore(rows)
		if err != nil {
			return nil, err
		}
		scored = append(scored, Scored{Record: rec, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating call record search: %w", err)
	}
	return scored, nil
}
