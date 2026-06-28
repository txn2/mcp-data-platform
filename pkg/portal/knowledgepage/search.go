package knowledgepage

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/pgvector/pgvector-go"
)

// SearchQuery describes a relevance ranking request over canonical
// knowledge pages. Unlike asset search, there is NO owner scope: knowledge pages
// are org-shared, so every non-deleted page is rankable for every caller. A nil
// Embedding selects lexical-only ranking (graceful degradation when no embedding
// provider is configured); a non-nil Embedding selects hybrid ranking.
type SearchQuery struct {
	Embedding []float32 // query vector; nil selects lexical-only ranking
	QueryText string    // raw query text for the lexical arm
	Limit     int       // max results; clamped into [1, maxSearchLimit]
}

// EffectiveLimit clamps the requested limit into the search bounds.
func (q SearchQuery) EffectiveLimit() int { return clampSearchLimit(q.Limit) }

// ScoredPage pairs a page with its relevance score in [0,1].
type ScoredPage struct {
	Page  Page    `json:"page"`
	Score float64 `json:"score"`
}

// Searcher ranks knowledge pages by relevance to a query. It is a
// capability separate from Store so the feature degrades to absent
// (rather than forcing every store to carry a ranking query) on a deployment
// without pgvector.
type Searcher interface {
	Search(ctx context.Context, q SearchQuery) ([]ScoredPage, error)
}

// ftsExpr is the full-text expression the lexical arm matches and
// ranks against. It calls portal_knowledge_page_fts() (migration 000070) with the
// same argument order so the planner uses idx_portal_knowledge_pages_search_fts.
const ftsExpr = `portal_knowledge_page_fts(title, body, tags)`

// ftsQueryLexical binds the lexical-only query text at $1 (no
// vector parameter on that path). The hybrid arms inline the tsquery at $2.
const ftsQueryLexical = "plainto_tsquery('english', $1)"

// Search ranks non-deleted knowledge pages by relevance. A non-nil
// q.Embedding selects hybrid (semantic + lexical) ranking; a nil embedding
// selects the lexical-only fallback. Body content is indexed, so a query matches
// page CONTENT, not just the title.
func (s *postgresStore) Search(ctx context.Context, q SearchQuery) ([]ScoredPage, error) { //nolint:revive // interface impl
	if len(q.Embedding) > 0 {
		return s.searchPagesHybrid(ctx, q)
	}
	return s.searchPagesLexical(ctx, q)
}

// searchPagesHybrid runs an index-backed vector arm and lexical arm and fuses in
// Go, mirroring the asset hybrid search: the hnsw index only accelerates a pure
// vector ORDER BY and the GIN index only accelerates the tsquery, so a single
// blended ORDER BY would forfeit both.
func (s *postgresStore) searchPagesHybrid(ctx context.Context, q SearchQuery) ([]ScoredPage, error) {
	limit := q.EffectiveLimit()
	// nosemgrep: semgrep.unbounded-make-slice-capacity -- fixed 2-element query-arg slice, not a make() with user-controlled capacity
	args := []any{pgvector.NewVector(q.Embedding), q.QueryText}

	// #nosec G201 -- column list and FTS expr are constants; the predicate uses
	// only parameterized placeholders ($1 vector, $2 text); limit is a sanitized
	// int. No user input is concatenated into the SQL.
	vecArm := fmt.Sprintf(
		"SELECT %s, 1 - (embedding <=> $1) AS vec_score, (%s @@ plainto_tsquery('english', $2)) AS lex_match "+
			"FROM portal_knowledge_pages WHERE embedding IS NOT NULL AND deleted_at IS NULL "+
			"ORDER BY embedding <=> $1 LIMIT %d",
		pageColumns, ftsExpr, limit)
	lexArm := fmt.Sprintf(
		"SELECT %s, CASE WHEN embedding IS NOT NULL THEN 1 - (embedding <=> $1) ELSE 0 END AS vec_score, TRUE AS lex_match "+
			"FROM portal_knowledge_pages WHERE deleted_at IS NULL AND %s @@ plainto_tsquery('english', $2) "+
			"ORDER BY ts_rank_cd(%s, plainto_tsquery('english', $2)) DESC LIMIT %d",
		pageColumns, ftsExpr, ftsExpr, limit)
	// #nosec G202 -- both arms are assembled from constant column/expression
	// strings with parameterized placeholders; no user input is concatenated.
	sqlStr := "(" + vecArm + ") UNION ALL (" + lexArm + ")"

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("search knowledge pages (hybrid): %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	return collectHybridPages(rows, limit)
}

// collectHybridPages scans both UNION arms, fuses each row into a single score,
// dedups by page id (keeping the higher score), sorts by descending score (ties
// broken by title), and truncates to limit.
func collectHybridPages(rows *sql.Rows, limit int) ([]ScoredPage, error) {
	byID := make(map[string]ScoredPage)
	for rows.Next() {
		var (
			page      Page
			tagsJSON  []byte
			deletedAt sql.NullTime
			vecScore  float64
			lexMatch  bool
		)
		dest := append(scanDest(&page, &tagsJSON, &deletedAt), &vecScore, &lexMatch)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning hybrid knowledge page row: %w", err)
		}
		if err := finishScannedPage(&page, tagsJSON, deletedAt); err != nil {
			return nil, err
		}
		score := fuseHybridScore(vecScore, lexMatch)
		if prev, ok := byID[page.ID]; !ok || score > prev.Score {
			byID[page.ID] = ScoredPage{Page: page, Score: score}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating hybrid knowledge page rows: %w", err)
	}

	scored := make([]ScoredPage, 0, len(byID))
	for _, sp := range byID {
		scored = append(scored, sp)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Page.Title < scored[j].Page.Title
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

// SemanticSearch ranks non-deleted pages purely by embedding cosine similarity,
// with NO lexical arm and NO score fusion, returning the raw cosine in [0,1] as the
// score (#705). The dedup gate uses this rather than Search so its threshold is a
// true cosine similarity: Search returns fuseHybridScore (0.6*semantic + 0.4*binary
// lexical match), on which a near-duplicate paraphrase with no shared keywords caps
// below the threshold while two distinct pages sharing common words can exceed it.
// A nil/empty embedding returns no results (the gate then proceeds unguarded).
func (s *postgresStore) SemanticSearch(ctx context.Context, embedding []float32, limit int) ([]ScoredPage, error) { //nolint:revive // interface impl
	if len(embedding) == 0 {
		return nil, nil
	}
	// #nosec G201 -- column list is a constant; the vector is a parameterized
	// placeholder ($1); limit is a sanitized int. No user input is concatenated.
	query := fmt.Sprintf(
		"SELECT %s, 1 - (embedding <=> $1) AS cos "+
			"FROM portal_knowledge_pages WHERE embedding IS NOT NULL AND deleted_at IS NULL "+
			"ORDER BY embedding <=> $1 LIMIT %d",
		pageColumns, clampSearchLimit(limit))

	rows, err := s.db.QueryContext(ctx, query, pgvector.NewVector(embedding))
	if err != nil {
		return nil, fmt.Errorf("semantic search knowledge pages: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var scored []ScoredPage
	for rows.Next() {
		var (
			page      Page
			tagsJSON  []byte
			deletedAt sql.NullTime
			cos       float64
		)
		dest := append(scanDest(&page, &tagsJSON, &deletedAt), &cos)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning semantic knowledge page row: %w", err)
		}
		if err := finishScannedPage(&page, tagsJSON, deletedAt); err != nil {
			return nil, err
		}
		scored = append(scored, ScoredPage{Page: page, Score: cos})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating semantic knowledge page rows: %w", err)
	}
	return scored, nil
}

// searchPagesLexical ranks non-deleted pages by full-text relevance only (the
// no-embedding-provider fallback), ordered by a length-normalized ts_rank_cd
// score (lexRankNormalization) so single-match pages do not collapse to a flat 0.1.
func (s *postgresStore) searchPagesLexical(ctx context.Context, q SearchQuery) ([]ScoredPage, error) {
	// #nosec G201 -- column list and FTS expr are constants; the query text is a
	// parameterized placeholder ($1); limit and the normalization bitmask are
	// sanitized ints.
	query := fmt.Sprintf(
		"SELECT %s, ts_rank_cd(%s, %s, %d) AS lex_rank "+
			"FROM portal_knowledge_pages WHERE deleted_at IS NULL AND %s @@ %s "+
			"ORDER BY lex_rank DESC LIMIT %d",
		pageColumns, ftsExpr, ftsQueryLexical, lexRankNormalization,
		ftsExpr, ftsQueryLexical, q.EffectiveLimit())

	rows, err := s.db.QueryContext(ctx, query, q.QueryText)
	if err != nil {
		return nil, fmt.Errorf("search knowledge pages (lexical): %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var scored []ScoredPage
	for rows.Next() {
		var (
			page      Page
			tagsJSON  []byte
			deletedAt sql.NullTime
			lexRank   float64
		)
		dest := append(scanDest(&page, &tagsJSON, &deletedAt), &lexRank)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning lexical knowledge page row: %w", err)
		}
		if err := finishScannedPage(&page, tagsJSON, deletedAt); err != nil {
			return nil, err
		}
		scored = append(scored, ScoredPage{Page: page, Score: lexRank})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating lexical knowledge page rows: %w", err)
	}
	return scored, nil
}
