package portal

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/pgvector/pgvector-go"
)

// Compile-time check: the PostgreSQL asset store provides ranked search.
var _ AssetSearcher = (*postgresAssetStore)(nil)

// assetSearchColumns is the column list every ranked-search SELECT reads, in
// assetScanDest order so the scan cannot drift from the query. It matches the
// list-path projection (queryAssets) plus the COALESCE on idempotency_key.
const assetSearchColumns = `id, owner_id, owner_email, name, description, content_type, ` +
	`s3_bucket, s3_key, thumbnail_s3_key, thumbnail_dark_s3_key, size_bytes, tags, provenance, session_id, ` +
	`current_version, created_at, updated_at, deleted_at, COALESCE(idempotency_key, '')`

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

// SearchAssets ranks the caller's non-deleted assets by relevance to the query.
// A non-nil q.Embedding selects hybrid (semantic + lexical) ranking; a nil
// embedding selects the lexical-only fallback used when no embedding provider is
// configured. Owner scope is applied in SQL before ranking, so an asset the
// caller does not own is never returned.
func (s *postgresAssetStore) SearchAssets(ctx context.Context, q AssetSearchQuery) ([]ScoredAsset, error) { //nolint:revive // interface impl
	var (
		scored []ScoredAsset
		err    error
	)
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

// searchAssetsHybrid runs two index-backed arms and fuses in Go, mirroring the
// prompt and memory hybrid search: the hnsw ANN index only accelerates a pure
// `ORDER BY embedding <=> $1 LIMIT k` and the GIN index only accelerates the
// tsquery match, so a single blended ORDER BY would forfeit both. The vector arm
// returns the cosine top-k; the lexical arm returns the full-text top-k
// (including NULL-embedding rows the vector arm cannot see). Their union is
// deduped by id (keeping the higher fused score) and sorted.
func (s *postgresAssetStore) searchAssetsHybrid(ctx context.Context, q AssetSearchQuery) ([]ScoredAsset, error) {
	limit := q.EffectiveLimit()
	base := "deleted_at IS NULL AND owner_id = $3"
	args := []any{pgvector.NewVector(q.Embedding), q.QueryText, q.OwnerID}

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
	sqlStr := "(" + vecArm + ") UNION ALL (" + lexArm + ")"

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
func collectHybridAssets(rows *sql.Rows, limit int) ([]ScoredAsset, error) {
	byID := make(map[string]ScoredAsset)
	for rows.Next() {
		var (
			asset      Asset
			tags, prov []byte
			deletedAt  sql.NullTime
			vecScore   float64
			lexMatch   bool
		)
		dest := append(assetScanDest(&asset, &tags, &prov, &deletedAt), &vecScore, &lexMatch)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning hybrid asset row: %w", err)
		}
		if err := finishScannedAsset(&asset, tags, prov, deletedAt); err != nil {
			return nil, err
		}
		score := fuseHybridScore(vecScore, lexMatch)
		if prev, ok := byID[asset.ID]; !ok || score > prev.Score {
			byID[asset.ID] = ScoredAsset{Asset: asset, Score: score}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating hybrid asset rows: %w", err)
	}

	scored := make([]ScoredAsset, 0, len(byID))
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

// searchAssetsLexical ranks the caller's non-deleted assets by full-text
// relevance only. It is the graceful-degradation path used when no embedding
// provider is available: it has no vector parameter, surfaces NULL-embedding
// rows, and orders by a length-normalized ts_rank_cd score (lexRankNormalization)
// so single-match records do not collapse to a flat 0.1.
func (s *postgresAssetStore) searchAssetsLexical(ctx context.Context, q AssetSearchQuery) ([]ScoredAsset, error) {
	// #nosec G201 -- column list and FTS expr are constants; owner_id is a
	// parameterized placeholder; limit and the normalization bitmask are
	// sanitized ints.
	query := fmt.Sprintf(
		"SELECT %s, ts_rank_cd(%s, %s, %d) AS lex_rank "+
			"FROM portal_assets WHERE deleted_at IS NULL AND owner_id = $2 "+
			"AND %s @@ %s ORDER BY lex_rank DESC LIMIT %d",
		assetSearchColumns, assetFTSExpr, assetFTSQueryLexical, lexRankNormalization,
		assetFTSExpr, assetFTSQueryLexical, q.EffectiveLimit())

	rows, err := s.db.QueryContext(ctx, query, q.QueryText, q.OwnerID)
	if err != nil {
		return nil, fmt.Errorf("search assets (lexical): %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var scored []ScoredAsset
	for rows.Next() {
		var (
			asset      Asset
			tags, prov []byte
			deletedAt  sql.NullTime
			lexRank    float64
		)
		dest := append(assetScanDest(&asset, &tags, &prov, &deletedAt), &lexRank)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning lexical asset row: %w", err)
		}
		if err := finishScannedAsset(&asset, tags, prov, deletedAt); err != nil {
			return nil, err
		}
		scored = append(scored, ScoredAsset{Asset: asset, Score: lexRank})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating lexical asset rows: %w", err)
	}
	return scored, nil
}

// populateScoredCollections fills the Collections field of each scored asset in
// one query, reusing the list-path helper so search results carry the same
// collection associations the list action returns.
func (s *postgresAssetStore) populateScoredCollections(ctx context.Context, scored []ScoredAsset) error {
	if len(scored) == 0 {
		return nil
	}
	assets := make([]Asset, len(scored))
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
