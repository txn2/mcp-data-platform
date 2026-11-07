package portalstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/reachstore"
	"github.com/txn2/mcp-data-platform/internal/sqlrank"
)

// Compile-time checks: the PostgreSQL stores rank, facet and resolve within a
// filter, a person's reach (#2500) among them.
var (
	_ portaldomain.AssetSearcher           = (*postgresAssetStore)(nil)
	_ portaldomain.ReachSearcher           = (*postgresAssetStore)(nil)
	_ portaldomain.AssetFaceter            = (*postgresAssetStore)(nil)
	_ portaldomain.ReachPermissions        = (*postgresAssetStore)(nil)
	_ portaldomain.CollectionSearcher      = (*postgresCollectionStore)(nil)
	_ portaldomain.CollectionReachSearcher = (*postgresCollectionStore)(nil)
	_ portaldomain.CollectionAuthorFaceter = (*postgresCollectionStore)(nil)
)

// searchFTS is the stored full-text column portal_asset_fts and
// portal_collection_fts compute into on write (migration 000220, #2292).
const searchFTS = "search_fts"

// rankedColumns is what a ranked statement reads of each candidate.
var rankedColumns = []string{"id", "name", "embedding", searchFTS}

// SearchAssets ranks the assets one owner or one producer reaches (a
// managed-script run ranks what it produced, #1579). A query scoped by
// neither reaches nothing.
func (s *postgresAssetStore) SearchAssets(ctx context.Context, q portaldomain.AssetSearchQuery) ([]portaldomain.ScoredAsset, error) { //nolint:revive // interface impl
	if !q.Owner.Identified() && !q.ProducedBy.Named() {
		return nil, nil
	}
	scored, _, err := s.rank(ctx, portaldomain.AssetFilter{Owner: q.Owner, ProducedBy: q.ProducedBy}, rankQuery{text: q.QueryText, emb: q.Embedding, limit: q.Limit})
	return scored, err
}

// SearchReach ranks the assets a person can open in a scope, filtered, one
// page at a time, and counts every result.
func (s *postgresAssetStore) SearchReach(ctx context.Context, q portaldomain.RankedAssetQuery) ([]portaldomain.ScoredAsset, int, error) { //nolint:revive // interface impl
	if !q.Reach.Identified() || strings.TrimSpace(q.QueryText) == "" {
		return []portaldomain.ScoredAsset{}, 0, nil
	}
	filter := portaldomain.AssetFilter{Reach: q.Reach, ContentType: q.ContentType, Tags: q.Tags, Owners: q.Owners}
	return s.rank(ctx, filter, rankQuery{text: q.QueryText, emb: q.Embedding, limit: q.Limit, offset: q.Offset})
}

// rankedCandidates is the listing's own statement over filter, selecting what
// a ranking reads: a row is ranked exactly when the listing would return it.
func rankedCandidates(filter portaldomain.AssetFilter) (query string, args []any, err error) {
	return applyAssetFilter(psq.Select(rankedColumns...).From("portal_assets"), filter). //nolint:wrapcheck // the caller wraps
												Where("deleted_at IS NULL").ToSql()
}

// rank scores the listing's candidates for filter and reads the page's rows
// through the listing itself, so a ranked row carries what a listed row does.
func (s *postgresAssetStore) rank(ctx context.Context, filter portaldomain.AssetFilter, q rankQuery) ([]portaldomain.ScoredAsset, int, error) {
	inner, args, err := rankedCandidates(filter)
	if err != nil {
		return nil, 0, fmt.Errorf("building ranked candidates: %w", err)
	}
	return rankPage(ctx, s.db, q.ranking(inner, args), pageRows[portaldomain.Asset, portaldomain.ScoredAsset]{
		load: func(ids []string) ([]portaldomain.Asset, error) {
			rows, _, err := s.List(ctx, portaldomain.AssetFilter{IDs: ids, Limit: len(ids)})
			return rows, err
		},
		idOf: func(a portaldomain.Asset) string { return a.ID },
		scoredOf: func(a portaldomain.Asset, v float64) portaldomain.ScoredAsset {
			return portaldomain.ScoredAsset{Asset: a, Score: v}
		},
	})
}

// rankQuery is what a search asks a ranking for.
type rankQuery struct {
	text          string
	emb           []float32
	limit, offset int
}

func (q rankQuery) ranking(candidates string, args []any) sqlrank.Ranking {
	return sqlrank.Ranking{Candidates: candidates, Args: args, QueryText: q.text, Embedding: q.emb, Limit: q.limit, Offset: q.offset}
}

// pageRows reads a ranked page's rows and pairs each with its score.
type pageRows[Row, Scored any] struct {
	load     func(ids []string) ([]Row, error)
	idOf     func(Row) string
	scoredOf func(Row, float64) Scored
}

// rankPage runs a ranking and reads its page's rows, returning them in rank
// order with their scores and the count of all results.
func rankPage[Row, Scored any](ctx context.Context, db *sql.DB, r sqlrank.Ranking, rows pageRows[Row, Scored]) (scored []Scored, total int, err error) {
	r.FTS = searchFTS
	ranked, err := sqlrank.Rank(ctx, db, r)
	if err != nil {
		return nil, 0, fmt.Errorf("ranking: %w", err)
	}
	scored = []Scored{}
	if len(ranked.IDs) == 0 {
		return scored, ranked.Total, nil
	}
	loaded, err := rows.load(ranked.IDs)
	if err != nil {
		return nil, 0, err
	}
	byID := make(map[string]Row, len(loaded))
	for _, row := range loaded {
		byID[rows.idOf(row)] = row
	}
	for _, id := range ranked.IDs {
		if row, ok := byID[id]; ok {
			scored = append(scored, rows.scoredOf(row, ranked.Scores[id]))
		}
	}
	return scored, ranked.Total, nil
}

// buildFacets renders the tag facet (the filter's tags dropped) and the
// author facet (its owners dropped) over the listing's filter.
// facetStatements are the two facet statements and their arguments.
type facetStatements struct {
	tags, authors       string
	tagArgs, authorArgs []any
}

func buildFacets(filter portaldomain.AssetFilter) (facetStatements, error) {
	byTag := filter
	byTag.Tag, byTag.Tags = "", nil
	tagInner, tagArgs, err := applyAssetFilter(psq.Select("tags").From("portal_assets"), byTag).
		Where("deleted_at IS NULL").Where("jsonb_typeof(tags) = 'array'").ToSql()
	if err != nil {
		return facetStatements{}, fmt.Errorf("building tag facet: %w", err)
	}
	byAuthor := filter
	byAuthor.Owners = nil
	authorInner, authorArgs, err := applyAssetFilter(psq.Select("owner_email").From("portal_assets"), byAuthor).
		Where("deleted_at IS NULL").ToSql()
	if err != nil {
		return facetStatements{}, fmt.Errorf("building author facet: %w", err)
	}
	return facetStatements{
		tags: reachstore.BuildTagFacet(tagInner), authors: reachstore.BuildAuthorFacet(authorInner),
		tagArgs: tagArgs, authorArgs: authorArgs,
	}, nil
}

// AssetFacets computes the tag and author facets over a listing filter. A
// filter with no reach facets nothing: the facets are a person's.
func (s *postgresAssetStore) AssetFacets(ctx context.Context, filter portaldomain.AssetFilter) (portaldomain.AssetFacets, error) { //nolint:revive // interface impl
	out := portaldomain.AssetFacets{Tags: []portaldomain.FacetCount{}, Authors: []portaldomain.FacetCount{}}
	if !filter.Reach.Identified() {
		return out, nil
	}
	st, err := buildFacets(filter)
	if err != nil {
		return out, err
	}
	if out.Tags, err = reachstore.Facet(ctx, s.db, st.tags, st.tagArgs); err != nil {
		return out, fmt.Errorf("tag facet: %w", err)
	}
	if out.Authors, err = reachstore.Facet(ctx, s.db, st.authors, st.authorArgs); err != nil {
		return out, fmt.Errorf("author facet: %w", err)
	}
	return out, nil
}

// ReachPermissions reports the share permission the reach holds on each id.
func (s *postgresAssetStore) ReachPermissions(ctx context.Context, ids []string, r *portaldomain.Reach) (map[string]portaldomain.SharePermission, error) { //nolint:revive // interface impl
	return reachstore.Permissions(ctx, s.db, ids, r) //nolint:wrapcheck // reachstore wraps
}

// SearchCollections ranks one owner's collections.
func (s *postgresCollectionStore) SearchCollections(ctx context.Context, q portaldomain.CollectionSearchQuery) ([]portaldomain.ScoredCollection, error) { //nolint:revive // interface impl
	if q.OwnerID == "" {
		return []portaldomain.ScoredCollection{}, nil
	}
	scored, _, err := s.rank(ctx, portaldomain.CollectionFilter{OwnerID: q.OwnerID}, rankQuery{text: q.QueryText, emb: q.Embedding, limit: q.Limit})
	return scored, err
}

// SearchCollectionReach ranks the collections a person can open in a scope.
func (s *postgresCollectionStore) SearchCollectionReach(ctx context.Context, q portaldomain.RankedCollectionQuery) ([]portaldomain.ScoredCollection, int, error) { //nolint:revive // interface impl
	if !q.Reach.Identified() || strings.TrimSpace(q.QueryText) == "" {
		return []portaldomain.ScoredCollection{}, 0, nil
	}
	return s.rank(ctx, portaldomain.CollectionFilter{Reach: q.Reach, Owners: q.Owners}, rankQuery{text: q.QueryText, emb: q.Embedding, limit: q.Limit, offset: q.Offset})
}

// collectionCandidates is the collection listing's own statement over filter,
// selecting what a ranking reads.
func collectionCandidates(filter portaldomain.CollectionFilter) (query string, args []any, err error) {
	_, candidates := (*postgresCollectionStore)(nil).buildListQueries(filter, portaldomain.MaxRankedDepth)
	return candidates.RemoveLimit().RemoveOffset().RemoveColumns().Columns(rankedColumns...).ToSql() //nolint:wrapcheck // the caller wraps
}

func (s *postgresCollectionStore) rank(ctx context.Context, filter portaldomain.CollectionFilter, q rankQuery) ([]portaldomain.ScoredCollection, int, error) {
	inner, args, err := collectionCandidates(filter)
	if err != nil {
		return nil, 0, fmt.Errorf("building ranked collection candidates: %w", err)
	}
	return rankPage(ctx, s.db, q.ranking(inner, args),
		pageRows[portaldomain.Collection, portaldomain.ScoredCollection]{
			load: func(ids []string) ([]portaldomain.Collection, error) { return s.collectionsByID(ctx, ids) },
			idOf: func(c portaldomain.Collection) string { return c.ID },
			scoredOf: func(c portaldomain.Collection, v float64) portaldomain.ScoredCollection {
				return portaldomain.ScoredCollection{Collection: c, Score: v}
			},
		})
}

// collectionsByID reads the listing's rows for ids.
func (s *postgresCollectionStore) collectionsByID(ctx context.Context, ids []string) ([]portaldomain.Collection, error) {
	colls, _, err := s.List(ctx, portaldomain.CollectionFilter{IDs: ids, Limit: len(ids)})
	return colls, err
}

// buildCollectionAuthors renders the authors of the collections a reach covers.
func buildCollectionAuthors(r *portaldomain.Reach) (query string, args []any, err error) {
	inner, args, err := psq.Select("owner_email").From("portal_collections").
		Where("deleted_at IS NULL").Where(reachstore.CollectionPredicate(r)).ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("building collection authors: %w", err)
	}
	return reachstore.BuildAuthorFacet(inner), args, nil
}

// CollectionAuthors lists the owner addresses of the collections the reach
// covers, with counts.
func (s *postgresCollectionStore) CollectionAuthors(ctx context.Context, r *portaldomain.Reach) ([]portaldomain.FacetCount, error) { //nolint:revive // interface impl
	if !r.Identified() {
		return []portaldomain.FacetCount{}, nil
	}
	query, args, err := buildCollectionAuthors(r)
	if err != nil {
		return nil, err
	}
	return reachstore.Facet(ctx, s.db, query, args) //nolint:wrapcheck // reachstore wraps
}
