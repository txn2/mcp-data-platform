package datasetindex

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

// Compile-time check: the store is the search federation's catalog index.
var _ knowledge.CatalogIndexSearcher = (*Store)(nil)

// ftsExpr is the full-text expression the lexical arm matches and ranks
// against. It calls catalog_dataset_fts() (migration 000096) with the same
// argument order so the planner uses idx_catalog_datasets_search_fts, the GIN
// index built on that same call.
const ftsExpr = `catalog_dataset_fts(name, description, tags, domain)`

// lexRankNormalization is the ts_rank_cd normalization bitmask for the lexical
// score: bit 1 divides by 1 + log(document length) so a short, dense match
// outranks a long single-mention, and bit 32 maps the result into (0,1).
const lexRankNormalization = 1 | 32

// hybridSemanticWeight blends the semantic and lexical signals:
// score = alpha*semantic + (1-alpha)*lexical. It matches the asset, prompt,
// memory, resource, and api-gateway rankers so every surface ranks on the same
// curve.
const hybridSemanticWeight = 0.6

// Lexical component values before blending, named to keep the magic 0.0/1.0 out
// of the formula (matching pkg/resource/search.go and pkg/memory/ranking.go).
const (
	lexicalMatchPresent = 1.0
	lexicalMatchAbsent  = 0.0
)

// maxSearchLimit bounds a single ranking request.
const maxSearchLimit = 100

// defaultSearchLimit is the top-K returned when the caller specifies none.
const defaultSearchLimit = 20

// SearchCatalogIndex ranks the mirrored catalog datasets by relevance to the
// query. A non-nil q.Embedding selects hybrid (semantic + lexical) ranking; a
// nil embedding selects lexical-only ranking, which is what `search` falls back
// to when the intent could not be embedded (a slow or unreachable embedder).
// The mirror itself is only ever populated by the index queue, which requires a
// configured embedding provider, so a deployment without one has an empty
// mirror and this returns nothing rather than a degraded ranking.
func (s *Store) SearchCatalogIndex(ctx context.Context, q knowledge.CatalogIndexQuery) ([]knowledge.CatalogIndexHit, error) {
	limit := effectiveLimit(q.Limit)
	if len(q.Embedding) > 0 {
		return s.searchHybrid(ctx, q, limit)
	}
	return s.searchLexical(ctx, q, limit)
}

// effectiveLimit clamps a requested limit into the ranking bounds.
func effectiveLimit(limit int) int {
	if limit <= 0 || limit > maxSearchLimit {
		return defaultSearchLimit
	}
	return limit
}

// searchHybrid runs two index-backed arms and fuses in Go, mirroring the asset,
// prompt, memory, and resource hybrid searches: the hnsw ANN index only
// accelerates a pure `ORDER BY embedding <=> $1 LIMIT k` and the GIN index only
// accelerates the tsquery match, so a single blended ORDER BY would forfeit
// both. The vector arm returns the cosine top-k; the lexical arm returns the
// full-text top-k, including rows whose vector the sweep has not written yet,
// which is how a freshly synced dataset is findable before it is embedded.
func (s *Store) searchHybrid(ctx context.Context, q knowledge.CatalogIndexQuery, limit int) ([]knowledge.CatalogIndexHit, error) {
	const tsQuery = "plainto_tsquery('english', $2)"
	// #nosec G201 -- the column list and FTS expression are constants and the
	// query text is bound as $2; limit is a clamped int. No user input is
	// concatenated into the SQL.
	vecArm := fmt.Sprintf(
		"SELECT urn, name, description, 1 - (embedding <=> $1) AS vec_score, (%s @@ %s) AS lex_match "+
			"FROM catalog_datasets WHERE embedding IS NOT NULL "+
			"ORDER BY embedding <=> $1 LIMIT %d",
		ftsExpr, tsQuery, limit)
	lexArm := fmt.Sprintf(
		"SELECT urn, name, description, "+
			"CASE WHEN embedding IS NOT NULL THEN 1 - (embedding <=> $1) ELSE 0 END AS vec_score, TRUE AS lex_match "+
			"FROM catalog_datasets WHERE %s @@ %s "+
			"ORDER BY ts_rank_cd(%s, %s) DESC LIMIT %d",
		ftsExpr, tsQuery, ftsExpr, tsQuery, limit)
	// #nosec G202 -- both arms are assembled from constant column/expression
	// strings with parameterized placeholders; no user input is concatenated.
	query := "(" + vecArm + ") UNION ALL (" + lexArm + ")"

	rows, err := s.db.QueryContext(ctx, query, pgvector.NewVector(q.Embedding), q.QueryText)
	if err != nil {
		return nil, fmt.Errorf("datasetindex: search (hybrid): %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	return collectHybrid(rows, limit)
}

// collectHybrid scans both UNION arms, fuses each row into a single score,
// dedups by URN (a dataset matched by both arms appears twice) keeping the
// higher score, sorts by descending score with a stable URN tiebreak, and
// truncates to limit.
func collectHybrid(rows *sql.Rows, limit int) ([]knowledge.CatalogIndexHit, error) {
	byURN := make(map[string]knowledge.CatalogIndexHit)
	for rows.Next() {
		var (
			h        knowledge.CatalogIndexHit
			vecScore float64
			lexMatch bool
		)
		if err := rows.Scan(&h.URN, &h.Name, &h.Description, &vecScore, &lexMatch); err != nil {
			return nil, fmt.Errorf("datasetindex: scanning hybrid row: %w", err)
		}
		h.Score = fuseHybridScore(vecScore, lexMatch)
		if prev, ok := byURN[h.URN]; !ok || h.Score > prev.Score {
			byURN[h.URN] = h
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("datasetindex: iterating hybrid rows: %w", err)
	}

	out := make([]knowledge.CatalogIndexHit, 0, len(byURN))
	for _, h := range byURN {
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].URN < out[j].URN
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// searchLexical ranks mirrored datasets by full-text relevance only. It is the
// graceful-degradation path for a query whose intent could not be embedded: no
// vector parameter, and rows with no embedding rank exactly like the rest.
func (s *Store) searchLexical(ctx context.Context, q knowledge.CatalogIndexQuery, limit int) ([]knowledge.CatalogIndexHit, error) {
	const tsQuery = "plainto_tsquery('english', $1)"
	// #nosec G201 -- the FTS expression is a constant, the query text is bound
	// as $1, and limit plus the normalization bitmask are sanitized ints.
	query := fmt.Sprintf(
		"SELECT urn, name, description, ts_rank_cd(%s, %s, %d) AS lex_rank "+
			"FROM catalog_datasets WHERE %s @@ %s ORDER BY lex_rank DESC LIMIT %d",
		ftsExpr, tsQuery, lexRankNormalization, ftsExpr, tsQuery, limit)

	rows, err := s.db.QueryContext(ctx, query, q.QueryText)
	if err != nil {
		return nil, fmt.Errorf("datasetindex: search (lexical): %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable

	var out []knowledge.CatalogIndexHit
	for rows.Next() {
		var h knowledge.CatalogIndexHit
		if err := rows.Scan(&h.URN, &h.Name, &h.Description, &h.Score); err != nil {
			return nil, fmt.Errorf("datasetindex: scanning lexical row: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("datasetindex: iterating lexical rows: %w", err)
	}
	return out, nil
}

// fuseHybridScore blends a row's cosine similarity (mapped from [-1,1] to
// [0,1]) with a binary lexical-match flag into a rank score in [0,1]. The
// binary blend gives an exact-term match a decisive boost over a merely
// semantically-near row, matching the asset/prompt/memory/resource rankers.
func fuseHybridScore(cosineSim float64, lexMatch bool) float64 {
	semantic := (cosineSim + 1) / 2
	lex := lexicalMatchAbsent
	if lexMatch {
		lex = lexicalMatchPresent
	}
	return hybridSemanticWeight*semantic + (1-hybridSemanticWeight)*lex
}
