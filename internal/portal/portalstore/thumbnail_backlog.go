package portalstore

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"
)

// The renderer's backlog, read on a metrics scrape (#1897): the documents
// owed a tile that no replica holds (pending), and the ones a replica holds
// now or is holding back after an attempt that did not finish (waiting). The
// owed predicate is the claim's own, so the two cannot disagree about what
// is owed.

// buildThumbnailBacklog renders the count of assets owed a tile.
func buildThumbnailBacklog(renderer int) (stmt string, args []any, err error) {
	owed, args, err := sq.And{
		sq.Expr("deleted_at IS NULL"),
		thumbnailOwedPredicate(renderer),
		sq.Expr("thumbnail_failed_version < current_version"),
	}.ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("building the thumbnail backlog: %w", err)
	}
	stmt, err = sq.Dollar.ReplacePlaceholders(`SELECT
		COUNT(*) FILTER (WHERE thumbnail_claimed_until IS NULL OR thumbnail_claimed_until < now()),
		COUNT(*) FILTER (WHERE thumbnail_claimed_until >= now())
		FROM portal_assets WHERE ` + owed)
	if err != nil {
		return "", nil, fmt.Errorf("numbering the thumbnail backlog: %w", err)
	}
	return stmt, args, nil
}

// ThumbnailBacklog counts the assets the renderer owes a tile.
func (s *postgresAssetStore) ThumbnailBacklog(ctx context.Context, renderer int) (pending, waiting int64, err error) {
	query, args, err := buildThumbnailBacklog(renderer)
	if err != nil {
		return 0, 0, err
	}
	// #nosec G701 -- assembled by buildThumbnailBacklog; every value is bound
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&pending, &waiting); err != nil {
		return 0, 0, fmt.Errorf("counting thumbnail work: %w", err)
	}
	return pending, waiting, nil
}

// collectionThumbnailBacklog counts the collections whose mosaic is owed,
// with the claim's own sources and owed conditions.
func collectionThumbnailBacklog() string {
	return collectionMosaicSources() + `
		SELECT
			COUNT(*) FILTER (WHERE c.thumbnail_claimed_until IS NULL OR c.thumbnail_claimed_until < now()),
			COUNT(*) FILTER (WHERE c.thumbnail_claimed_until >= now())
		FROM portal_collections c
		LEFT JOIN sources g ON g.collection_id = c.id
		WHERE ` + collectionMosaicOwed
}

// CollectionThumbnailBacklog counts the collections whose mosaic is owed.
func (s *postgresCollectionStore) CollectionThumbnailBacklog(ctx context.Context) (pending, waiting int64, err error) {
	if err := s.db.QueryRowContext(ctx, collectionThumbnailBacklog()).Scan(&pending, &waiting); err != nil {
		return 0, 0, fmt.Errorf("counting collection thumbnail work: %w", err)
	}
	return pending, waiting, nil
}
