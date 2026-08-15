package scriptstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Compile-time check: the PostgreSQL store provides ranked search and the
// contract read behind the scripts discovery source.
var _ script.Searcher = (*Store)(nil)

// scriptFTSExpr is the full-text expression the lexical arm matches and ranks
// against. It calls the script_fts() function from migration 000102 with the
// same argument order, so the planner uses idx_scripts_search_fts (the GIN index
// built on that same call). Changing either without the other silently drops the
// index and leaves a sequential scan behind.
const scriptFTSExpr = `script_fts(display_name, name, description, tags, params)`

// scriptFTSQuery is the parameterized tsquery the predicate compares against.
const scriptFTSQuery = "plainto_tsquery('english', $1)"

// lexRankNormalization is the ts_rank_cd normalization bitmask. Bit 1 divides
// the rank by 1 + log(document length) so a short, dense match outranks a long
// single-mention; bit 32 maps the result into (0,1), which is the [0,1] range
// ScoredScript declares and the router normalizes across sources.
const lexRankNormalization = 1 | 32

// discoverableStatuses are the lifecycle states a script is offered from
// discovery in. A draft is included deliberately: an unapproved script is a
// solved process waiting for a reviewer, and the contract says plainly that
// nothing will execute it, which is more useful than hiding it. Deprecated and
// superseded are excluded because both name a dead end — one must not be
// executed, the other names its replacement — and ranking them would spend an
// agent's attention on work it must not do.
//
// This is a ranking rule, not an access rule. The contract read applies no
// lifecycle filter: a caller holding a reference to a retired script gets the
// document, which states the refusal, rather than a not-found that reads as
// though the script never existed.
//
// Built with append rather than a two-element composite literal, which a
// semgrep registry rule misflags as an unbounded make() capacity — the same
// reason promptschema.promotionRequestScopes is written this way.
var discoverableStatuses = append([]string{script.StatusDraft}, script.StatusActive)

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
// caller cannot see never reaches the ranker; see script.SearchQuery for why
// the persona arm scopes on membership rather than on the acting persona.
//
// Ranking is lexical only. Scripts carry no embedding: the corpus is small and
// its searchable text is a name, a sentence, and a parameter list, so the
// hybrid machinery the prompt library needs would be cost without a gain.
func (s *Store) Search(ctx context.Context, q script.SearchQuery) ([]script.ScoredScript, error) {
	if q.QueryText == "" {
		return nil, nil
	}
	query := fmt.Sprintf(`SELECT %s, ts_rank_cd(%s, %s, %d) AS score
		FROM scripts
		WHERE enabled = true
		  AND status = ANY($2)
		  AND (scope = 'global'
		       OR (scope = 'persona' AND personas && $3)
		       OR (scope = 'personal' AND owner_email <> '' AND owner_email = $4))
		  AND %s @@ %s
		ORDER BY score DESC, updated_at DESC
		LIMIT $5`,
		scriptColumns, scriptFTSExpr, scriptFTSQuery, lexRankNormalization,
		scriptFTSExpr, scriptFTSQuery)

	rows, err := s.db.QueryContext(ctx, query,
		q.QueryText, pq.Array(discoverableStatuses), pq.Array(q.Personas), q.OwnerEmail, q.EffectiveLimit())
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

// Contract composes the contract document for one script: the live record, the
// approved version behind the execution gate, the cadence when it has one, and
// the last successful run with what it produced.
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
	approved, err := s.approvedVersion(ctx, sc)
	if err != nil {
		return nil, err
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
	c := script.BuildContract(sc, approved, sched, lastRun)
	return &c, nil
}

// approvedVersion reads the version behind the execution gate, or nil when the
// script has none.
func (s *Store) approvedVersion(ctx context.Context, sc *script.Script) (*script.Version, error) {
	if sc.ApprovedVersionID == "" {
		return nil, nil //nolint:nilnil // no approved version is an ordinary state, not an error
	}
	return s.GetVersionByID(ctx, sc.ApprovedVersionID)
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
