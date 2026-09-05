package resource

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/pgvector/pgvector-go"
)

// Search result limits, mirroring the asset/prompt/memory ranked surfaces so
// every ranked surface clamps the same way.
const (
	// DefaultSearchLimit is the top-K returned when the caller specifies none.
	DefaultSearchLimit = 20
	maxSearchLimit     = 100
)

// MaxInlineContentBytes is the size at or below which a text resource's content
// is returned inline rather than as a pointer to the blob. It is shared by the
// MCP resources/read path and the search `fetch` reference so an agent gets the
// same answer for the same file whichever door it comes through.
const MaxInlineContentBytes = 1 << 20

// MaxContentIndexBytes bounds how much of a resource's content is extracted into
// content_text for search. The prefix has to be large enough to cover the part
// of a document that describes it (a data dictionary's column list, a runbook's
// procedure) while keeping the embedded text within one provider call and the
// row small enough that listing resources stays cheap.
const MaxContentIndexBytes = 32 << 10

// MaxContentReadBytes bounds the object size the index consumer will pull from
// blob storage to extract that prefix. The blob API has no range read, so
// extracting 32 KiB means holding the whole object in memory; an upload may be
// up to the deployment's upload ceiling -- 100 MB by default and higher where
// resources.managed.max_upload_bytes says so -- and several index workers run
// concurrently.
// A resource larger than this is indexed on its metadata alone rather than
// risking the sweep's memory on a file whose first 32 KiB is all that would have
// been kept anyway.
const MaxContentReadBytes = 8 << 20

// IndexText composes the text a resource is embedded and lexically indexed on:
// its display name, description, folder path, filename, tags, and contentText, the
// bounded text prefix the index consumer extracted from the uploaded file. The
// indexjobs resource consumer and the request-path search MUST agree on this
// composition so a stored embedding lives in the same space as the query; it is
// defined once here for both. Empty fields are skipped so a sparse resource does
// not pad the text with blank lines. The lexical arm's resource_fts (migration
// 000091) composes the same corpus from the same columns.
func IndexText(r Resource, contentText string) string {
	fields := []string{r.DisplayName, r.Description, r.Path, r.Filename, strings.Join(r.Tags, " "), contentText}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			parts = append(parts, f)
		}
	}
	return strings.Join(parts, "\n")
}

// SearchQuery describes a relevance ranking request over managed resources.
// Visibility is applied in SQL before ranking: Scopes is the caller's visible
// (scope, scope_id) set as VisibleScopes computes it, so a resource the caller
// could not list is never ranked. An empty Scopes returns nothing rather than
// searching unscoped. A nil Embedding selects lexical-only ranking (the
// graceful-degradation path when no embedding provider is configured); a non-nil
// Embedding selects hybrid ranking.
type SearchQuery struct {
	Embedding []float32     // query vector; nil selects lexical-only ranking
	QueryText string        // raw query text for the lexical arm
	Scopes    []ScopeFilter // caller's visible scopes; mandatory
	Limit     int           // max results; clamped into [1, maxSearchLimit]
}

// EffectiveLimit clamps the requested limit into the search bounds.
func (q SearchQuery) EffectiveLimit() int {
	if q.Limit <= 0 || q.Limit > maxSearchLimit {
		return DefaultSearchLimit
	}
	return q.Limit
}

// ScoredResource pairs a resource with its relevance score in [0,1].
type ScoredResource struct {
	Resource Resource `json:"resource"`
	Score    float64  `json:"score"`
}

// Searcher ranks the resources visible to a caller by relevance to a query. It
// is a capability separate from Store: only a backing store that can rank (the
// PostgreSQL store with pgvector) implements it, so the feature degrades to
// absent rather than forcing every Store implementation to carry a ranking
// query.
type Searcher interface {
	Search(ctx context.Context, q SearchQuery) ([]ScoredResource, error)
}

// Compile-time check: the PostgreSQL resource store provides ranked search.
var _ Searcher = (*postgresStore)(nil)

// searchColumns is the ranked search's projection. It must stay in step with
// resourceScan.dest, which is what scans it: the two arms append their score
// columns to the same destination list, so a column added to one and not the
// other misaligns the scan rather than failing to compile.
const searchColumns = `id, scope, scope_id, path, filename, display_name, description, ` +
	`mime_type, size_bytes, s3_key, uri, tags, uploader_sub, uploader_email, created_at, updated_at, last_read_at, ` +
	`thumbnail_s3_key, thumbnail_dark_s3_key, thumbnail_captured_at, thumbnail_dark_captured_at`

// ftsExpr is the full-text expression the lexical arm matches and ranks against.
// It calls resource_fts() (migration 000091) with the same argument order so the
// planner uses idx_resources_search_fts, the GIN index built on that same call.
const ftsExpr = `resource_fts(display_name, description, path, filename, tags, content_text)`

// lexRankNormalization is the ts_rank_cd normalization bitmask for the lexical
// relevance score: bit 1 divides the rank by 1 + log(document length) so a
// short, dense match outranks a long single-mention (a resource's content_text
// makes documents long and this matters more here than anywhere else), and bit
// 32 maps the result into (0,1).
const lexRankNormalization = 1 | 32

// hybridSemanticWeight is the alpha blending the semantic and lexical signals:
// score = alpha*semantic + (1-alpha)*lexical. It matches the asset, prompt,
// memory, and api-gateway rankers so every surface ranks on the same curve.
const hybridSemanticWeight = 0.6

// Lexical component values before blending, named to keep the magic 0.0/1.0 out
// of the formula (matching pkg/portal/asset_search.go and pkg/memory/ranking.go).
const (
	lexicalMatchPresent = 1.0
	lexicalMatchAbsent  = 0.0
)

// Search ranks the resources visible to the caller by relevance to the query.
// A non-nil q.Embedding selects hybrid (semantic + lexical) ranking; a nil
// embedding selects the lexical-only fallback. It fails closed on an empty scope
// set: with no visible scopes there is nothing the caller may see, and searching
// unscoped would leak every resource on the platform.
func (s *postgresStore) Search(ctx context.Context, q SearchQuery) ([]ScoredResource, error) { //nolint:revive // interface impl
	if len(q.Scopes) == 0 {
		return nil, nil
	}
	if len(q.Embedding) > 0 {
		return s.searchHybrid(ctx, q)
	}
	return s.searchLexical(ctx, q)
}

// buildHybridSearch renders the two-arm hybrid statement and its arguments.
//
// It is a function rather than inline SQL so a test can hand the statement to a
// real PostgreSQL to parse and plan (#1512); the store methods that assemble
// SQL at run time are the ones no gate could reach.
func buildHybridSearch(q SearchQuery) (query string, args []any) {
	limit := q.EffectiveLimit()
	const boundArgs = 2 // $1 query vector, $2 query text
	scopeWhere, scopeArgs, _ := scopeVisibilityWhere(q.Scopes, boundArgs+1)
	args = make([]any, 0, boundArgs+len(scopeArgs))
	args = append(args, pgvector.NewVector(q.Embedding), q.QueryText)
	args = append(args, scopeArgs...)
	const tsQuery = "plainto_tsquery('english', $2)"

	// #nosec G201 -- column list and FTS expr are constants; the scope predicate
	// uses only parameterized placeholders; limit is a sanitized int. No user
	// input is concatenated into the SQL.
	vecArm := fmt.Sprintf(
		"SELECT %s, 1 - (embedding <=> $1) AS vec_score, (%s @@ %s) AS lex_match "+
			"FROM resources WHERE embedding IS NOT NULL AND %s "+
			"ORDER BY embedding <=> $1 LIMIT %d",
		searchColumns, ftsExpr, tsQuery, scopeWhere, limit)
	lexArm := fmt.Sprintf(
		"SELECT %s, CASE WHEN embedding IS NOT NULL THEN 1 - (embedding <=> $1) ELSE 0 END AS vec_score, TRUE AS lex_match "+
			"FROM resources WHERE %s @@ %s AND %s "+
			"ORDER BY ts_rank_cd(%s, %s) DESC LIMIT %d",
		searchColumns, ftsExpr, tsQuery, scopeWhere, ftsExpr, tsQuery, limit)
	// #nosec G202 -- both arms are assembled from constant column/expression
	// strings with parameterized placeholders; no user input is concatenated.
	return "(" + vecArm + ") UNION ALL (" + lexArm + ")", args
}

// searchHybrid runs two index-backed arms and fuses in Go, mirroring the asset,
// prompt, and memory hybrid search: the hnsw ANN index only accelerates a pure
// `ORDER BY embedding <=> $1 LIMIT k` and the GIN index only accelerates the
// tsquery match, so a single blended ORDER BY would forfeit both. The vector arm
// returns the cosine top-k; the lexical arm returns the full-text top-k
// (including NULL-embedding rows the vector arm cannot see, which is how a
// just-uploaded resource is findable before the reconciler embeds it). Their
// union is deduped by id (keeping the higher fused score) and sorted.
func (s *postgresStore) searchHybrid(ctx context.Context, q SearchQuery) ([]ScoredResource, error) {
	limit := q.EffectiveLimit()
	query, args := buildHybridSearch(q)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search resources (hybrid): %w", err)
	}
	defer func() { _ = rows.Close() }()

	return collectHybrid(rows, limit)
}

// collectHybrid scans both UNION arms, fuses each row into a single score, dedups
// by resource id (a row matched by both arms appears twice) keeping the higher
// score, sorts by descending score (ties broken by display name), and truncates
// to limit.
func collectHybrid(rows *sql.Rows, limit int) ([]ScoredResource, error) {
	byID := make(map[string]ScoredResource)
	for rows.Next() {
		var (
			r        Resource
			sc       resourceScan
			vecScore float64
			lexMatch bool
		)
		dest := append(sc.dest(&r), &vecScore, &lexMatch)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning hybrid resource row: %w", err)
		}
		sc.finish(&r)
		score := fuseHybridScore(vecScore, lexMatch)
		if prev, ok := byID[r.ID]; !ok || score > prev.Score {
			byID[r.ID] = ScoredResource{Resource: r, Score: score}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating hybrid resource rows: %w", err)
	}

	scored := make([]ScoredResource, 0, len(byID))
	for _, sr := range byID {
		scored = append(scored, sr)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Resource.DisplayName < scored[j].Resource.DisplayName
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

// buildLexicalSearch renders the lexical statement and its arguments, for the
// same reason buildHybridSearch exists.
func buildLexicalSearch(q SearchQuery) (query string, args []any) {
	const boundArgs = 1 // $1 query text
	scopeWhere, scopeArgs, _ := scopeVisibilityWhere(q.Scopes, boundArgs+1)
	args = make([]any, 0, boundArgs+len(scopeArgs))
	args = append(args, q.QueryText)
	args = append(args, scopeArgs...)
	const tsQuery = "plainto_tsquery('english', $1)"

	// #nosec G201 -- column list and FTS expr are constants; the scope predicate
	// uses only parameterized placeholders; limit and the normalization bitmask
	// are sanitized ints.
	return fmt.Sprintf(
		"SELECT %s, ts_rank_cd(%s, %s, %d) AS lex_rank FROM resources "+
			"WHERE %s @@ %s AND %s ORDER BY lex_rank DESC LIMIT %d",
		searchColumns, ftsExpr, tsQuery, lexRankNormalization,
		ftsExpr, tsQuery, scopeWhere, q.EffectiveLimit()), args
}

// searchLexical ranks the caller's visible resources by full-text relevance
// only. It is the graceful-degradation path used when no embedding provider is
// available: it has no vector parameter, surfaces NULL-embedding rows, and
// orders by a length-normalized ts_rank_cd score.
func (s *postgresStore) searchLexical(ctx context.Context, q SearchQuery) ([]ScoredResource, error) {
	query, args := buildLexicalSearch(q)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search resources (lexical): %w", err)
	}
	defer func() { _ = rows.Close() }()

	var scored []ScoredResource
	for rows.Next() {
		var (
			r       Resource
			sc      resourceScan
			lexRank float64
		)
		dest := append(sc.dest(&r), &lexRank)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning lexical resource row: %w", err)
		}
		sc.finish(&r)
		scored = append(scored, ScoredResource{Resource: r, Score: lexRank})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating lexical resource rows: %w", err)
	}
	return scored, nil
}

// fuseHybridScore blends a row's cosine similarity (mapped from [-1,1] to [0,1])
// with a binary lexical-match flag into a rank score in [0,1]. The binary blend
// gives an exact-term match a decisive boost over a merely semantically-near
// row, matching the asset/prompt/memory rankers.
func fuseHybridScore(cosineSim float64, lexMatch bool) float64 {
	semantic := (cosineSim + 1) / 2
	lex := lexicalMatchAbsent
	if lexMatch {
		lex = lexicalMatchPresent
	}
	return hybridSemanticWeight*semantic + (1-hybridSemanticWeight)*lex
}
