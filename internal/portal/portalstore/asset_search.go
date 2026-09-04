package portalstore

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
)

// Compile-time check: the PostgreSQL asset store provides ranked search.
var _ portaldomain.AssetSearcher = (*postgresAssetStore)(nil)

// assetSearchColumns is the column list every ranked-search SELECT reads, in
// assetScanDest order so the scan cannot drift from the query. It matches the
// list-path projection (queryAssets) plus the COALESCE on idempotency_key,
// which includes reading the provenance summary rather than the provenance
// itself: a ranked search is a listing and is bounded like one (#1623).
var assetSearchColumns = `id, owner_id, owner_email, name, description, content_type, ` +
	`s3_bucket, s3_key, thumbnail_s3_key, thumbnail_dark_s3_key, thumbnail_version, thumbnail_dark_version, ` +
	`size_bytes, tags, ` + provenanceSummaryExpr("provenance") + `, session_id, ` +
	`current_version, created_at, updated_at, deleted_at, COALESCE(idempotency_key, ''), max_versions`

// assetFTSExpr is the full-text expression the lexical arm matches and ranks
// against. It calls portal_asset_fts() (migration 000063) with the same
// argument order so the planner uses idx_portal_assets_search_fts, the GIN index
// built on that same call. portal_asset_fts composes the same corpus as
// AssetIndexText (name + description + tags).
const assetFTSExpr = `portal_asset_fts(name, description, tags)`

// Parameterized tsquery for the lexical predicate. $2 binds the query text in
// the hybrid arms; the lexical-only path rebinds it to $1 (no vector parameter).
// lexRankNormalization is the ts_rank_cd normalization bitmask for the lexical
// relevance score. Bit 1 divides the rank by 1 + log(document length) so a
// short, dense match outranks a long single-mention; without it every
// single-match record collapses to the weight-D 0.1 and lexical ranking is flat
// (#587, same root cause as #578). Bit 32 maps the result into (0,1). Applied
// only to the returned lex_rank score, not the hybrid ORDER BY, whose fused
// score uses a lexMatch boolean rather than the rank value.
const lexRankNormalization = 1 | 32

const (
	assetFTSQueryHybrid  = "plainto_tsquery('english', $2)"
	assetFTSQueryLexical = "plainto_tsquery('english', $1)"
)

// hybridScopePlaceholder and lexicalScopePlaceholder are the first placeholder
// each ranked statement has left for the scope arm: the hybrid arms bind the
// vector and the query text first, the lexical one only the query text.
const (
	hybridScopePlaceholder  = 3
	lexicalScopePlaceholder = 2
)

// SearchAssets ranks the caller's non-deleted assets by relevance to the query.
// A non-nil q.Embedding selects hybrid (semantic + lexical) ranking; a nil
// embedding selects the lexical-only fallback used when no embedding provider is
// configured. Owner scope is applied in SQL before ranking, so an asset the
// caller does not own is never returned.
//
// A query scoped by neither an owner nor a producer reaches nothing, and is
// answered with nothing rather than with every unattributed asset on the
// platform.
func (s *postgresAssetStore) SearchAssets(ctx context.Context, q portaldomain.AssetSearchQuery) ([]portaldomain.ScoredAsset, error) { //nolint:revive // interface impl
	var (
		scored []portaldomain.ScoredAsset
		err    error
	)
	if !q.Owner.Identified() && !q.ProducedBy.Named() {
		return nil, nil
	}
	if len(q.Embedding) > 0 {
		scored, err = s.searchAssetsHybrid(ctx, q)
	} else {
		scored, err = s.searchAssetsLexical(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	if err := s.populateScoredCollections(ctx, scored); err != nil {
		return nil, fmt.Errorf("populating collections: %w", err)
	}
	return scored, nil
}

// buildAssetHybridSearch renders the two-arm hybrid statement and its
// arguments. It is a function rather than inline SQL so a test can hand the
// statement to a real PostgreSQL to parse and plan (#1512).
func buildAssetHybridSearch(q portaldomain.AssetSearchQuery) (query string, args []any) {
	limit := q.EffectiveLimit()
	scope, scopeArgs := assetSearchScope(q, hybridScopePlaceholder)
	base := "deleted_at IS NULL AND " + scope
	args = append([]any{pgvector.NewVector(q.Embedding), q.QueryText}, scopeArgs...)

	// #nosec G201 -- column list and FTS expr are constants; base uses only
	// parameterized placeholders; limit is a sanitized int. No user input is
	// concatenated into the SQL.
	vecArm := fmt.Sprintf(
		"SELECT %s, 1 - (embedding <=> $1) AS vec_score, (%s @@ %s) AS lex_match "+
			"FROM portal_assets WHERE embedding IS NOT NULL AND %s "+
			"ORDER BY embedding <=> $1 LIMIT %d",
		assetSearchColumns, assetFTSExpr, assetFTSQueryHybrid, base, limit)
	lexArm := fmt.Sprintf(
		"SELECT %s, CASE WHEN embedding IS NOT NULL THEN 1 - (embedding <=> $1) ELSE 0 END AS vec_score, TRUE AS lex_match "+
			"FROM portal_assets WHERE %s @@ %s AND %s "+
			"ORDER BY ts_rank_cd(%s, %s) DESC LIMIT %d",
		assetSearchColumns, assetFTSExpr, assetFTSQueryHybrid, base, assetFTSExpr, assetFTSQueryHybrid, limit)
	// #nosec G202 -- both arms are assembled from constant column/expression
	// strings with parameterized placeholders; no user input is concatenated.
	return "(" + vecArm + ") UNION ALL (" + lexArm + ")", args
}

// searchAssetsHybrid runs two index-backed arms and fuses in Go, mirroring the
// prompt and memory hybrid search: the hnsw ANN index only accelerates a pure
// `ORDER BY embedding <=> $1 LIMIT k` and the GIN index only accelerates the
// tsquery match, so a single blended ORDER BY would forfeit both. The vector arm
// returns the cosine top-k; the lexical arm returns the full-text top-k
// (including NULL-embedding rows the vector arm cannot see). Their union is
// deduped by id (keeping the higher fused score) and sorted.
func (s *postgresAssetStore) searchAssetsHybrid(ctx context.Context, q portaldomain.AssetSearchQuery) ([]portaldomain.ScoredAsset, error) {
	limit := q.EffectiveLimit()
	sqlStr, args := buildAssetHybridSearch(q)

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("search assets (hybrid): %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	return collectHybridAssets(rows, limit)
}

// collectHybridAssets scans both UNION arms, fuses each row into a single score,
// dedups by asset id (a row matched by both arms appears twice) keeping the
// higher score, sorts by descending score (ties broken by name), and truncates
// to limit.
func collectHybridAssets(rows *sql.Rows, limit int) ([]portaldomain.ScoredAsset, error) {
	byID := make(map[string]portaldomain.ScoredAsset)
	for rows.Next() {
		var (
			asset         portaldomain.Asset
			tags, summary []byte
			deletedAt     sql.NullTime
			maxVersions   sql.NullInt64
			vecScore      float64
			lexMatch      bool
		)
		dest := append(assetScanDest(&asset, &tags, &summary, &deletedAt, &maxVersions), &vecScore, &lexMatch)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning hybrid asset row: %w", err)
		}
		if err := finishScannedListAsset(&asset, tags, summary, deletedAt, maxVersions); err != nil {
			return nil, err
		}
		score := fuseHybridScore(vecScore, lexMatch)
		if prev, ok := byID[asset.ID]; !ok || score > prev.Score {
			byID[asset.ID] = portaldomain.ScoredAsset{Asset: asset, Score: score}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating hybrid asset rows: %w", err)
	}

	scored := make([]portaldomain.ScoredAsset, 0, len(byID))
	for _, sa := range byID {
		scored = append(scored, sa)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Asset.Name < scored[j].Asset.Name
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

// buildAssetLexicalSearch renders the lexical statement and its arguments, for
// the same reason buildAssetHybridSearch exists.
func buildAssetLexicalSearch(q portaldomain.AssetSearchQuery) (query string, args []any) {
	scope, scopeArgs := assetSearchScope(q, lexicalScopePlaceholder)
	// #nosec G201 -- column list and FTS expr are constants; the owner scope
	// contributes only parameterized placeholders; limit and the normalization
	// bitmask are sanitized ints.
	query = fmt.Sprintf(
		"SELECT %s, ts_rank_cd(%s, %s, %d) AS lex_rank "+
			"FROM portal_assets WHERE deleted_at IS NULL AND %s "+
			"AND %s @@ %s ORDER BY lex_rank DESC LIMIT %d",
		assetSearchColumns, assetFTSExpr, assetFTSQueryLexical, lexRankNormalization,
		scope, assetFTSExpr, assetFTSQueryLexical, q.EffectiveLimit())
	return query, append([]any{q.QueryText}, scopeArgs...)
}

// producerScopeArgs is how many placeholders the producer arm binds, which is
// where the ownership arm's numbering resumes when a query carries both.
const producerScopeArgs = 3

// assetSearchScope renders the arm that limits a ranked-search statement to the
// rows this query may return, and the arguments it binds, numbering its
// placeholders from $next.
//
// It is the raw-SQL counterpart of assetOwnerPredicate and producedByPredicate
// and reads the same values, so what a search ranks and what a listing returns
// cannot disagree about whose row a row is.
//
// A managed script's output is stamped with the script principal as its owner
// id and the script owner's address as owner_email, so a search that scoped on
// the id alone could never return to a person what a run produced for them
// (#1551). A RUN is scoped by neither: the principal is shared by every
// same-named script on the platform and the address is the script owner's as of
// the row's insert, which a transfer does not rewrite, so the run's own outputs
// are found through the producer recorded for its writes instead (#1579). Each
// value is bound only when it is an arm, so the anonymous sentinel is never a
// parameter and an unattributed row is not matched by an unauthenticated caller.
func assetSearchScope(q portaldomain.AssetSearchQuery, next int) (scope string, args []any) {
	if q.ProducedBy.Named() {
		// #nosec G201 -- the table and column names are constants; the
		// producer's own values are bound.
		byProducer := fmt.Sprintf(
			"EXISTS (SELECT 1 FROM content_producers cp WHERE cp.target_kind = $%d "+
				"AND cp.target_id = portal_assets.id AND cp.producer_kind = $%d "+
				"AND cp.producer_id = $%d AND cp.created)", next, next+1, next+2)
		if !q.Owner.Identified() {
			return byProducer, []any{producedby.TargetAsset, q.ProducedBy.Kind, q.ProducedBy.ID}
		}
		// Both set narrows to their intersection, which is what the listing
		// does with the same pair (applyAssetFilter adds each as its own
		// WHERE). No in-tree caller sets both -- each surface scopes a person
		// or a run, never a mixture -- but AssetSearchQuery is on the supported
		// surface, and a search returning MORE than the listing for one query
		// would be a widening nobody asked for.
		ownerScope, ownerArgs := assetSearchOwnerScope(q.Owner, next+producerScopeArgs)
		args = make([]any, 0, producerScopeArgs+len(ownerArgs))
		args = append(args, producedby.TargetAsset, q.ProducedBy.Kind, q.ProducedBy.ID)
		return "(" + byProducer + " AND " + ownerScope + ")", append(args, ownerArgs...)
	}
	return assetSearchOwnerScope(q.Owner, next)
}

// assetSearchOwnerScope renders the ownership half of the scope: the row's
// owner id, the address it records, or neither.
func assetSearchOwnerScope(owner portaldomain.AssetOwner, next int) (scope string, args []any) {
	arms := owner.Arms()
	switch {
	case arms.UserID == "" && arms.Email == "":
		// SearchAssets refuses a query scoped by nothing before it gets here;
		// this is what keeps a caller that does not from ranking every
		// unattributed asset on the platform.
		return "FALSE", nil
	case arms.UserID == "":
		return fmt.Sprintf("LOWER(owner_email) = LOWER($%d)", next), []any{arms.Email}
	case arms.Email == "":
		return fmt.Sprintf("owner_id = $%d", next), []any{arms.UserID}
	default:
		return fmt.Sprintf("(owner_id = $%d OR LOWER(owner_email) = LOWER($%d))", next, next+1),
			[]any{arms.UserID, arms.Email}
	}
}

// searchAssetsLexical ranks the caller's non-deleted assets by full-text
// relevance only. It is the graceful-degradation path used when no embedding
// provider is available: it has no vector parameter, surfaces NULL-embedding
// rows, and orders by a length-normalized ts_rank_cd score (lexRankNormalization)
// so single-match records do not collapse to a flat 0.1.
func (s *postgresAssetStore) searchAssetsLexical(ctx context.Context, q portaldomain.AssetSearchQuery) ([]portaldomain.ScoredAsset, error) {
	query, args := buildAssetLexicalSearch(q)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search assets (lexical): %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var scored []portaldomain.ScoredAsset
	for rows.Next() {
		var (
			asset         portaldomain.Asset
			tags, summary []byte
			deletedAt     sql.NullTime
			maxVersions   sql.NullInt64
			lexRank       float64
		)
		dest := append(assetScanDest(&asset, &tags, &summary, &deletedAt, &maxVersions), &lexRank)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning lexical asset row: %w", err)
		}
		if err := finishScannedListAsset(&asset, tags, summary, deletedAt, maxVersions); err != nil {
			return nil, err
		}
		scored = append(scored, portaldomain.ScoredAsset{Asset: asset, Score: lexRank})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating lexical asset rows: %w", err)
	}
	return scored, nil
}

// populateScoredCollections fills the Collections field of each scored asset in
// one query, reusing the list-path helper so search results carry the same
// collection associations the list action returns.
func (s *postgresAssetStore) populateScoredCollections(ctx context.Context, scored []portaldomain.ScoredAsset) error {
	if len(scored) == 0 {
		return nil
	}
	assets := make([]portaldomain.Asset, len(scored))
	for i := range scored {
		assets[i] = scored[i].Asset
	}
	if err := s.populateCollections(ctx, assets); err != nil {
		return err
	}
	for i := range scored {
		scored[i].Asset = assets[i]
	}
	return nil
}

// hybridSemanticWeight is the alpha blending the semantic and lexical signals:
// score = alpha*semantic + (1-alpha)*lexical. It matches the prompt, memory, and
// api-gateway rankers (0.6) so every surface ranks hybrid results on the same
// curve; keep them in step if any is tuned.
const hybridSemanticWeight = 0.6

// lexical component values before blending, named to keep the magic 0.0/1.0 out
// of the formula (matches pkg/memory/ranking.go).
const (
	lexicalMatchPresent = 1.0
	lexicalMatchAbsent  = 0.0
)

// fuseHybridScore blends a row's cosine similarity (mapped from [-1,1] to [0,1])
// with a binary lexical-match flag into a rank score in [0,1]. The binary blend
// gives an exact-term match a decisive boost over a merely semantically-near
// row, matching the prompt/memory/api-gateway rankers. Shared by asset and
// collection search.
func fuseHybridScore(cosineSim float64, lexMatch bool) float64 {
	semantic := (cosineSim + 1) / 2
	lex := lexicalMatchAbsent
	if lexMatch {
		lex = lexicalMatchPresent
	}
	return hybridSemanticWeight*semantic + (1-hybridSemanticWeight)*lex
}
