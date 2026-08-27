package scriptstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Compile-time check: the PostgreSQL store provides ranked search and the
// contract read behind the scripts discovery source.
var _ script.Searcher = (*Store)(nil)

// scriptFTSExpr is the full-text expression the lexical arm matches and ranks
// against. It calls the script_fts() function with the same argument order the
// migration defines it with (000102, extended by 000116 to carry the category),
// so the planner uses idx_scripts_search_fts (the GIN index built on that same
// call). Changing either without the other silently drops the index and leaves a
// sequential scan behind.
const scriptFTSExpr = `script_fts(display_name, name, description, category, tags, params)`

// scriptFTSQuery is the parameterized tsquery the predicate compares against.
// The lexical-only path binds the query text as $1; the hybrid arms bind $1 to
// the vector and the text to $2.
const (
	scriptFTSQuery       = "plainto_tsquery('english', $1)"
	scriptFTSQueryHybrid = "plainto_tsquery('english', $2)"
)

// hybridSemanticWeight is the alpha blending the semantic and lexical signals:
// score = alpha*semantic + (1-alpha)*lexical. It matches the prompt, memory and
// api-gateway rankers so every surface ranks hybrid results on one curve; keep
// them in step if any is tuned.
const hybridSemanticWeight = 0.6

// lexical component values before blending, named to keep the magic 0.0/1.0 out
// of the formula (matches pkg/memory/ranking.go).
const (
	lexicalMatchPresent = 1.0
	lexicalMatchAbsent  = 0.0
)

// lexRankNormalization is the ts_rank_cd normalization bitmask. Bit 1 divides
// the rank by 1 + log(document length) so a short, dense match outranks a long
// single-mention; bit 32 maps the result into (0,1), which is the [0,1] range
// ScoredScript declares and the router normalizes across sources.
const lexRankNormalization = 1 | 32

// discoverableStatuses are the lifecycle states a script is offered from
// discovery in. Deprecated and superseded are excluded because both name a
// dead end — one must not be executed, the other names its replacement — and
// ranking them would spend an agent's attention on work it must not do.
//
// This is a ranking rule, not an access rule. The contract read applies no
// lifecycle filter: a caller holding a reference to a retired script gets the
// document, which states the refusal, rather than a not-found that reads as
// though the script never existed.
var discoverableStatuses = []string{script.StatusActive}

// NewDiscoveryStore returns the store the search federation reads to rank
// scripts and to resolve an mcp:script:<id> reference (#1302), or nil when the
// deployment has no database and therefore no scripts to find.
//
// It exists so the composition root asks for the capability in one expression.
// Discovery builds its own handle rather than borrowing the one the script
// feature assembles, because search is federated during initialization while
// the feature is wired at Start; the store is a stateless reader over the same
// *sql.DB — the tool layer and the run worker each construct their own for the
// same reason — so this is a second handle on one database, not a second
// authority.
func NewDiscoveryStore(db *sql.DB) script.Store {
	if db == nil {
		return nil
	}
	return New(db)
}

// Search ranks scripts by relevance to the query within the caller's
// visibility. Visibility is applied in SQL, before ranking, so a script the
// caller cannot see never reaches the ranker; a script is its owner's, so that
// predicate is the caller's own address.
//
// A non-nil q.Embedding selects hybrid (semantic + lexical) ranking over the
// vectors the indexjobs scripts consumer writes, so a script is found by what it
// does and not only by the words it was named with (#1370). A nil embedding
// selects the lexical-only path, which is exactly the behavior a deployment
// with no embedding provider has always had.
func (s *Store) Search(ctx context.Context, q script.SearchQuery) ([]script.ScoredScript, error) {
	if q.QueryText == "" {
		return nil, nil
	}
	if len(q.Embedding) > 0 {
		return s.searchHybrid(ctx, q)
	}
	return s.searchLexical(ctx, q)
}

// visibilityPredicate is the SQL the ranker applies before ranking: only
// enabled scripts, only the discoverable lifecycle states, and only the
// caller's own scripts. It is script.Script.OwnedBy expressed in SQL, down to
// requiring both sides to be identified, so an unnamed caller and an ownerless
// script never match each other. The status and owner placeholders are the
// caller's; the arm binding them starts at statusIdx.
func visibilityPredicate(statusIdx int) string {
	// #nosec G201 -- the only interpolation is a sanitized parameter index.
	return fmt.Sprintf(`enabled = true
		  AND status = ANY($%d)
		  AND owner_email <> ''
		  AND owner_email = $%d`,
		statusIdx, statusIdx+1)
}

// buildHybridSearch renders the two-arm hybrid statement. It is a function
// rather than inline SQL so a test can hand the statement to a real PostgreSQL
// to parse and plan (#1512). Its four arguments -- the query vector, the query
// text, the discoverable statuses and the owner -- are bound by the caller.
func buildHybridSearch(q script.SearchQuery) string {
	limit := q.EffectiveLimit()
	base := visibilityPredicate(hybridStatusParam)
	// #nosec G201 -- scriptColumns, the FTS expression and the predicate are
	// constants or built from sanitized parameter indices; limit is a clamped
	// int. No user input is concatenated into the SQL.
	vecArm := fmt.Sprintf(
		"SELECT %s, 1 - (embedding <=> $1) AS vec_score, (%s @@ %s) AS lex_match "+
			"FROM scripts WHERE embedding IS NOT NULL AND %s "+
			"ORDER BY embedding <=> $1 LIMIT %d",
		scriptColumns, scriptFTSExpr, scriptFTSQueryHybrid, base, limit)
	lexArm := fmt.Sprintf(
		"SELECT %s, CASE WHEN embedding IS NOT NULL THEN 1 - (embedding <=> $1) ELSE 0 END AS vec_score, "+
			"TRUE AS lex_match FROM scripts WHERE %s @@ %s AND %s "+
			"ORDER BY ts_rank_cd(%s, %s) DESC LIMIT %d",
		scriptColumns, scriptFTSExpr, scriptFTSQueryHybrid, base,
		scriptFTSExpr, scriptFTSQueryHybrid, limit)
	// #nosec G202 -- both arms are assembled from constant column/expression
	// strings with parameterized placeholders; no user input is concatenated.
	return "(" + vecArm + ") UNION ALL (" + lexArm + ")"
}

// searchHybrid runs two index-backed arms and fuses in Go rather than ordering
// by a blended SQL expression, mirroring the prompt library and memory: the
// hnsw ANN index only accelerates a pure `ORDER BY embedding <=> $1 LIMIT k`
// and the GIN index only accelerates the tsquery match, so a single blended
// ORDER BY would forfeit both. The vector arm returns the cosine top-k; the
// lexical arm returns the full-text top-k, including the rows no worker has
// embedded yet, which is what keeps a freshly written script findable while its
// job is still in the queue. Their union is deduped by id, keeping the higher
// fused score.
func (s *Store) searchHybrid(ctx context.Context, q script.SearchQuery) ([]script.ScoredScript, error) {
	limit := q.EffectiveLimit()
	query := buildHybridSearch(q)

	rows, err := s.db.QueryContext(ctx, query,
		pgvector.NewVector(q.Embedding), q.QueryText, pq.Array(discoverableStatuses),
		q.OwnerEmail)
	if err != nil {
		return nil, fmt.Errorf("search scripts (hybrid): %w", err)
	}
	defer func() { _ = rows.Close() }()

	fused, err := collectHybridScored(rows)
	if err != nil {
		return nil, err
	}
	if len(fused) > limit {
		fused = fused[:limit]
	}
	return fused, nil
}

// Visibility-placeholder starting indices. The hybrid arms bind $1 to the query
// vector and $2 to the query text, so their predicate starts at $3; the
// lexical-only path binds only $1 to the query text, so it starts at $2.
const (
	hybridStatusParam  = 3
	lexicalStatusParam = 2
)

// collectHybridScored scans both arms, fuses each row into one score, and dedups
// by script id keeping the higher score (a script matched by both arms appears
// twice). The result is sorted by descending score, ties broken by name so the
// ordering is deterministic.
func collectHybridScored(rows *sql.Rows) ([]script.ScoredScript, error) {
	byID := make(map[string]script.ScoredScript)
	for rows.Next() {
		var vecScore float64
		var lexMatch bool
		sc, err := scanScript(hybridTrailingScanner{rows: rows, vecScore: &vecScore, lexMatch: &lexMatch})
		if err != nil {
			return nil, err
		}
		score := fuseHybridScore(vecScore, lexMatch)
		if prev, ok := byID[sc.ID]; !ok || score > prev.Score {
			byID[sc.ID] = script.ScoredScript{Script: *sc, Score: score}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hybrid scripts: %w", err)
	}
	scored := make([]script.ScoredScript, 0, len(byID))
	for _, ss := range byID {
		scored = append(scored, ss)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		if scored[i].Script.Name != scored[j].Script.Name {
			return scored[i].Script.Name < scored[j].Script.Name
		}
		// Names are unique only within an owner, so two scripts can share one.
		// The id breaks the last tie, because the fused set is collected from a
		// map and would otherwise order by map iteration.
		return scored[i].Script.ID < scored[j].Script.ID
	})
	return scored, nil
}

// fuseHybridScore blends a row's cosine similarity (mapped from [-1,1] to [0,1])
// with a binary lexical-match flag into a rank score in [0,1]. The binary blend
// gives an exact-term match a decisive boost over a merely semantically-near
// script, matching the prompt, memory and api-gateway rankers.
func fuseHybridScore(cosineSim float64, lexMatch bool) float64 {
	semantic := (cosineSim + 1) / 2
	lex := lexicalMatchAbsent
	if lexMatch {
		lex = lexicalMatchPresent
	}
	return hybridSemanticWeight*semantic + (1-hybridSemanticWeight)*lex
}

// hybridTrailingScanner adapts a hybrid-arm row, whose projection is the script
// columns followed by the vector score and the lexical-match flag, so scanScript
// stays the single reader of the column order.
type hybridTrailingScanner struct {
	rows     rowScanner
	vecScore *float64
	lexMatch *bool
}

// Scan appends the two hybrid destinations to the script column destinations.
func (s hybridTrailingScanner) Scan(dest ...any) error {
	return s.rows.Scan(append(dest, s.vecScore, s.lexMatch)...) //nolint:wrapcheck // wrapped by scanScript
}

// buildLexicalSearch renders the lexical statement, for the same reason
// buildHybridSearch exists. Its four arguments -- the query text, the
// discoverable statuses, the owner and the limit -- are bound by the caller.
func buildLexicalSearch() string {
	// #nosec G201 -- scriptColumns, the FTS expression and the predicate are
	// constants or built from sanitized parameter indices; no user input is
	// interpolated.
	return fmt.Sprintf(`SELECT %s, ts_rank_cd(%s, %s, %d) AS score
		FROM scripts
		WHERE %s
		  AND %s @@ %s
		ORDER BY score DESC, updated_at DESC
		LIMIT $4`,
		scriptColumns, scriptFTSExpr, scriptFTSQuery, lexRankNormalization,
		visibilityPredicate(lexicalStatusParam), scriptFTSExpr, scriptFTSQuery)
}

// searchLexical ranks the caller's visible scripts by full-text relevance only.
// It is the graceful-degradation path used when no embedding provider is
// configured: it has no vector parameter and surfaces rows no worker has
// embedded.
func (s *Store) searchLexical(ctx context.Context, q script.SearchQuery) ([]script.ScoredScript, error) {
	query := buildLexicalSearch()

	rows, err := s.db.QueryContext(ctx, query,
		q.QueryText, pq.Array(discoverableStatuses), q.OwnerEmail, q.EffectiveLimit())
	if err != nil {
		return nil, fmt.Errorf("search scripts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []script.ScoredScript{}
	for rows.Next() {
		scored, scanErr := scanScoredScript(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, *scored)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scored scripts: %w", err)
	}
	return out, nil
}

// scanScoredScript reads one ranked row: the script columns followed by the
// relevance score.
func scanScoredScript(rows rowScanner) (*script.ScoredScript, error) {
	var score float64
	sc, err := scanScript(scoreTrailingScanner{rows: rows, score: &score})
	if err != nil {
		return nil, err
	}
	return &script.ScoredScript{Script: *sc, Score: score}, nil
}

// scoreTrailingScanner adapts a row whose projection is the script columns
// followed by one trailing score column, so scanScript stays the single reader
// of the column order and the ranked query cannot drift from the plain one.
type scoreTrailingScanner struct {
	rows  rowScanner
	score *float64
}

// Scan appends the score destination to the script column destinations.
func (s scoreTrailingScanner) Scan(dest ...any) error {
	return s.rows.Scan(append(dest, s.score)...) //nolint:wrapcheck // wrapped by scanScript
}

// Contract composes the contract document for one script: the live record and
// its parameter contract, the cadence when it has one, and the last successful
// run with what it produced.
//
// Returns nil, nil when no such script exists. A missing schedule is not an
// error (most scripts have none), and neither is a script that has never
// completed a run: both are ordinary states the document reports as absent.
func (s *Store) Contract(ctx context.Context, id string) (*script.Contract, error) {
	sc, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sc == nil {
		return nil, nil //nolint:nilnil // Searcher contract: nil, nil means not found
	}
	sched, err := s.GetSchedule(ctx, id)
	if err != nil && !errors.Is(err, script.ErrScheduleNotFound) {
		return nil, err
	}
	if errors.Is(err, script.ErrScheduleNotFound) {
		sched = nil
	}
	lastRun, err := s.lastSuccessfulRun(ctx, id)
	if err != nil {
		return nil, err
	}
	c := script.BuildContract(sc, sched, lastRun)
	return &c, nil
}

// lastSuccessfulRun reads the most recently FINISHED successful run, or nil
// when the script has never completed one.
//
// It orders on finished_at rather than reusing ListRuns' creation order,
// because an infrastructure retry pushes a run's due time out: an earlier
// request can finish after a later one, and "what did this last produce" means
// the most recent result, not the most recent request.
func (s *Store) lastSuccessfulRun(ctx context.Context, scriptID string) (*script.Run, error) {
	const q = runSelect + ` WHERE script_id = $1 AND status = $2
		ORDER BY finished_at DESC NULLS LAST LIMIT 1`
	r, err := scanRun(s.db.QueryRowContext(ctx, q, scriptID, script.RunStatusSucceeded))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // never having produced anything is an ordinary state
	}
	if err != nil {
		return nil, fmt.Errorf("last successful script run: %w", err)
	}
	return r, nil
}
