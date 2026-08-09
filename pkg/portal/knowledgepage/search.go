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
	Limit     int       // max results; clamped into [1, MaxHonoredLimit]
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

// chunkTable holds one vector per embeddable chunk of a page (#1242). A page's
// content is embedded as a SET of chunks, each within the provider's input
// budget, so no part of a large page is trimmed away before it is embedded.
const chunkTable = "portal_knowledge_page_embedding_chunks"

// bestChunkScore is the page-level semantic score: the similarity of the page's
// best-matching chunk. Used by the lexical arm, which starts from a page and
// needs its semantic score, unlike the vector arm which starts from the chunks.
// Correlated per page and bounded by that page's chunk count, so it costs a few
// rows per lexical hit rather than a scan.
const bestChunkScore = "COALESCE((SELECT MAX(1 - (c.embedding <=> $1)) FROM " + chunkTable +
	" c WHERE c.page_id = portal_knowledge_pages.id), 0)"

// chunkFanout multiplies the requested page limit to size the vector arm's chunk
// scan. The ANN scan ranks CHUNKS, and several chunks of one page can occupy the
// top of that ordering, so scanning exactly `limit` chunks could return fewer
// than `limit` distinct pages. Over-scanning by this factor keeps the arm a plain
// index-backed `ORDER BY ... LIMIT` (a per-page DISTINCT ON would forfeit the
// hnsw index) while leaving room for a page's chunks to collapse into one hit.
const chunkFanout = 4

// nearestChunkPages is the ANN half of a vector read: the pages owning the
// chunks nearest the query vector ($1). It is a plain `ORDER BY <=> LIMIT` over
// the chunk table so the hnsw index serves it. Soft-deleted pages are filtered by
// the enclosing query rather than joined in here, which keeps this a pure index
// scan; the chunkFanout over-scan absorbs the few slots a recently deleted page's
// chunks can occupy.
func nearestChunkPages(limit int) string {
	// #nosec G201 -- table name is a constant and the limit is a sanitized int.
	return fmt.Sprintf("SELECT page_id FROM %s ORDER BY embedding <=> $1 LIMIT %d",
		chunkTable, limit*chunkFanout)
}

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
// blended ORDER BY would forfeit both. The vector arm ranks CHUNKS and returns
// their pages scored by the best-matching chunk, so results stay page-granular
// while a match anywhere in a large page counts (#1242).
func (s *postgresStore) searchPagesHybrid(ctx context.Context, q SearchQuery) ([]ScoredPage, error) {
	limit := q.EffectiveLimit()
	// nosemgrep: semgrep.unbounded-make-slice-capacity -- fixed 2-element query-arg slice, not a make() with user-controlled capacity
	args := []any{pgvector.NewVector(q.Embedding), q.QueryText}

	// #nosec G201 -- column list, chunk table and FTS expr are constants; the
	// predicate uses only parameterized placeholders ($1 vector, $2 text); limit
	// is a sanitized int. No user input is concatenated into the SQL.
	vecArm := fmt.Sprintf(
		"SELECT %s, %s AS vec_score, (%s @@ plainto_tsquery('english', $2)) AS lex_match "+
			"FROM portal_knowledge_pages WHERE deleted_at IS NULL AND id IN (%s) "+
			"ORDER BY vec_score DESC LIMIT %d",
		pageColumns, bestChunkScore, ftsExpr, nearestChunkPages(limit), limit)
	lexArm := fmt.Sprintf(
		"SELECT %s, %s AS vec_score, TRUE AS lex_match "+
			"FROM portal_knowledge_pages WHERE deleted_at IS NULL AND %s @@ plainto_tsquery('english', $2) "+
			"ORDER BY ts_rank_cd(%s, plainto_tsquery('english', $2)) DESC LIMIT %d",
		pageColumns, bestChunkScore, ftsExpr, ftsExpr, limit)
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
// score (#705). A page scores as its best-matching chunk, so a page whose tail
// matches ranks on that tail (#1242).
//
// The dedup gate uses this rather than Search so its threshold is a
// true cosine similarity: Search returns fuseHybridScore (0.6*semantic + 0.4*binary
// lexical match), on which a near-duplicate paraphrase with no shared keywords caps
// below the threshold while two distinct pages sharing common words can exceed it.
// A nil/empty embedding returns no results (the gate then proceeds unguarded).
func (s *postgresStore) SemanticSearch(ctx context.Context, embedding []float32, limit int) ([]ScoredPage, error) { //nolint:revive // interface impl
	if len(embedding) == 0 {
		return nil, nil
	}
	effective := clampSearchLimit(limit)
	// #nosec G201 -- column list and chunk table are constants; the vector is a
	// parameterized placeholder ($1); limit is a sanitized int. No user input is
	// concatenated.
	query := fmt.Sprintf(
		"SELECT %s, %s AS cos "+
			"FROM portal_knowledge_pages WHERE deleted_at IS NULL AND id IN (%s) "+
			"ORDER BY cos DESC LIMIT %d",
		pageColumns, bestChunkScore, nearestChunkPages(effective), effective)

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
