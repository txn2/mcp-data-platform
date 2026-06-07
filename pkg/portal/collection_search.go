package portal

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/pgvector/pgvector-go"
)

// Compile-time check: the PostgreSQL collection store provides ranked search.
var _ CollectionSearcher = (*postgresCollectionStore)(nil)

// collectionFTSExpr is the full-text expression the lexical arm matches and
// ranks against. It calls portal_collection_fts() (migration 000063) with the
// same argument order so the planner uses idx_portal_collections_search_fts.
// portal_collection_fts composes the same corpus as CollectionIndexText (name +
// description + sections_text).
const collectionFTSExpr = `portal_collection_fts(name, description, sections_text)`

// Parameterized tsquery for the lexical predicate. $2 binds the query text in
// the hybrid arms; the lexical-only path rebinds it to $1.
const (
	collectionFTSQueryHybrid  = "plainto_tsquery('english', $2)"
	collectionFTSQueryLexical = "plainto_tsquery('english', $1)"
)

// SearchCollections ranks the caller's non-deleted collections by relevance to
// the query. A non-nil q.Embedding selects hybrid (semantic + lexical) ranking;
// a nil embedding selects the lexical-only fallback. Owner scope is applied in
// SQL before ranking, so a collection the caller does not own is never returned.
// Results carry the collection header plus aggregated asset tags (no sections),
// mirroring the list projection.
func (s *postgresCollectionStore) SearchCollections(ctx context.Context, q CollectionSearchQuery) ([]ScoredCollection, error) { //nolint:revive // interface impl
	var (
		scored []ScoredCollection
		err    error
	)
	if len(q.Embedding) > 0 {
		scored, err = s.searchCollectionsHybrid(ctx, q)
	} else {
		scored, err = s.searchCollectionsLexical(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	if err := s.populateScoredAssetTags(ctx, scored); err != nil {
		return nil, fmt.Errorf("populating asset tags: %w", err)
	}
	return scored, nil
}

// searchCollectionsHybrid runs the vector and lexical arms and fuses in Go, the
// same two-index strategy as asset and prompt search.
func (s *postgresCollectionStore) searchCollectionsHybrid(ctx context.Context, q CollectionSearchQuery) ([]ScoredCollection, error) {
	limit := q.EffectiveLimit()
	base := "deleted_at IS NULL AND owner_id = $3"
	args := []any{pgvector.NewVector(q.Embedding), q.QueryText, q.OwnerID}

	// #nosec G201 -- column list and FTS expr are constants; base uses only
	// parameterized placeholders; limit is a sanitized int.
	vecArm := fmt.Sprintf(
		"SELECT %s, 1 - (embedding <=> $1) AS vec_score, (%s @@ %s) AS lex_match "+
			"FROM portal_collections WHERE embedding IS NOT NULL AND %s "+
			"ORDER BY embedding <=> $1 LIMIT %d",
		collectionColumns, collectionFTSExpr, collectionFTSQueryHybrid, base, limit)
	lexArm := fmt.Sprintf(
		"SELECT %s, CASE WHEN embedding IS NOT NULL THEN 1 - (embedding <=> $1) ELSE 0 END AS vec_score, TRUE AS lex_match "+
			"FROM portal_collections WHERE %s @@ %s AND %s "+
			"ORDER BY ts_rank_cd(%s, %s) DESC LIMIT %d",
		collectionColumns, collectionFTSExpr, collectionFTSQueryHybrid, base, collectionFTSExpr, collectionFTSQueryHybrid, limit)
	// #nosec G202 -- both arms are assembled from constant column/expression
	// strings with parameterized placeholders; no user input is concatenated.
	sqlStr := "(" + vecArm + ") UNION ALL (" + lexArm + ")"

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("search collections (hybrid): %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	byID := make(map[string]ScoredCollection)
	for rows.Next() {
		var (
			c          Collection
			configJSON []byte
			deletedAt  sql.NullTime
			vecScore   float64
			lexMatch   bool
		)
		dest := append(collectionScanDest(&c, &configJSON, &deletedAt), &vecScore, &lexMatch)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning hybrid collection row: %w", err)
		}
		finishScannedCollection(&c, configJSON, deletedAt)
		score := fuseHybridScore(vecScore, lexMatch)
		if prev, ok := byID[c.ID]; !ok || score > prev.Score {
			byID[c.ID] = ScoredCollection{Collection: c, Score: score}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating hybrid collection rows: %w", err)
	}

	scored := make([]ScoredCollection, 0, len(byID))
	for _, sc := range byID {
		scored = append(scored, sc)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Collection.Name < scored[j].Collection.Name
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

// searchCollectionsLexical ranks the caller's non-deleted collections by
// full-text relevance only (the no-embedder fallback).
func (s *postgresCollectionStore) searchCollectionsLexical(ctx context.Context, q CollectionSearchQuery) ([]ScoredCollection, error) {
	// #nosec G201 -- column list and FTS expr are constants; owner_id is a
	// parameterized placeholder; limit is a sanitized int.
	query := fmt.Sprintf(
		"SELECT %s, ts_rank_cd(%s, %s) AS lex_rank "+
			"FROM portal_collections WHERE deleted_at IS NULL AND owner_id = $2 "+
			"AND %s @@ %s ORDER BY lex_rank DESC LIMIT %d",
		collectionColumns, collectionFTSExpr, collectionFTSQueryLexical,
		collectionFTSExpr, collectionFTSQueryLexical, q.EffectiveLimit())

	rows, err := s.db.QueryContext(ctx, query, q.QueryText, q.OwnerID)
	if err != nil {
		return nil, fmt.Errorf("search collections (lexical): %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var scored []ScoredCollection
	for rows.Next() {
		var (
			c          Collection
			configJSON []byte
			deletedAt  sql.NullTime
			lexRank    float64
		)
		dest := append(collectionScanDest(&c, &configJSON, &deletedAt), &lexRank)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning lexical collection row: %w", err)
		}
		finishScannedCollection(&c, configJSON, deletedAt)
		scored = append(scored, ScoredCollection{Collection: c, Score: lexRank})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating lexical collection rows: %w", err)
	}
	return scored, nil
}

// populateScoredAssetTags fills the AssetTags of each scored collection in one
// query, reusing the list-path helper so search results carry the same
// aggregated tags the list action returns.
func (s *postgresCollectionStore) populateScoredAssetTags(ctx context.Context, scored []ScoredCollection) error {
	if len(scored) == 0 {
		return nil
	}
	collections := make([]Collection, len(scored))
	for i := range scored {
		collections[i] = scored[i].Collection
	}
	if err := s.populateAssetTags(ctx, collections); err != nil {
		return err
	}
	for i := range scored {
		scored[i].Collection = collections[i]
	}
	return nil
}
