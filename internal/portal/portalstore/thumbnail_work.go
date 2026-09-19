package portalstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// buildThumbnailClaim renders the statement that leases up to limit assets the
// renderer owes a tile and returns them as listing rows.
//
// An asset is skipped while another replica holds a lease on it, and while the
// renderer's last attempt at its current version failed: that failure stands
// until the content changes or the tile is asked for again, so a document the
// renderer cannot draw is not retried on every pass. SKIP LOCKED lets two
// replicas claim at once without waiting on each other or taking the same row.
//
// The owed predicate is built by squirrel so it is the one definition the
// store holds, and the statement around it is written out because squirrel
// has no form for an UPDATE whose target is a locking subquery.
func buildThumbnailClaim(renderer int, lease time.Duration, limit int) (stmt string, args []any, err error) {
	owed, owedArgs, err := sq.And{
		sq.Expr("deleted_at IS NULL"),
		thumbnailOwedPredicate(renderer),
		sq.Expr("thumbnail_failed_version < current_version"),
		sq.Expr("(thumbnail_claimed_until IS NULL OR thumbnail_claimed_until < now())"),
	}.ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("building the thumbnail claim: %w", err)
	}
	stmt = `UPDATE portal_assets SET thumbnail_claimed_until = now() + make_interval(secs => ?)
		WHERE id IN (
			SELECT id FROM portal_assets WHERE ` + owed + `
			ORDER BY updated_at DESC
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
		RETURNING ` + strings.Join(assetListColumns(), ", ")
	args = append([]any{lease.Seconds()}, owedArgs...)
	args = append(args, limit)
	stmt, err = sq.Dollar.ReplacePlaceholders(stmt)
	if err != nil {
		return "", nil, fmt.Errorf("numbering the thumbnail claim: %w", err)
	}
	return stmt, args, nil
}

// ClaimThumbnailWork leases up to limit assets the renderer owes a tile for
// lease and returns them. The lease is released when the result is recorded
// with ReleaseThumbnailClaim set, and lapses on its own if the replica that
// took it dies mid-render.
func (s *postgresAssetStore) ClaimThumbnailWork(ctx context.Context, renderer int, lease time.Duration, limit int) ([]portaldomain.Asset, error) {
	if limit <= 0 {
		return nil, nil
	}
	query, args, err := buildThumbnailClaim(renderer, lease, limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...) // #nosec G701 -- assembled by buildThumbnailClaim; every value is bound
	if err != nil {
		return nil, fmt.Errorf("claiming thumbnail work: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cursor

	var assets []portaldomain.Asset
	for rows.Next() {
		var a portaldomain.Asset
		var tags, summary []byte
		var deletedAt sql.NullTime
		var maxVersions sql.NullInt64
		if err := rows.Scan(assetScanDest(&a, &tags, &summary, &deletedAt, &maxVersions)...); err != nil {
			return nil, fmt.Errorf("scanning claimed asset: %w", err)
		}
		if err := finishScannedListAsset(&a, tags, summary, deletedAt, maxVersions); err != nil {
			return nil, err
		}
		assets = append(assets, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading claimed assets: %w", err)
	}
	return assets, nil
}

// collectionMosaicTiles is how many member tiles a collection's mosaic is
// composed from.
const collectionMosaicTiles = 4

// buildCollectionThumbnailClaim renders the statement that leases up to limit
// collections whose mosaic is out of date and returns each with the source it
// should be composed from.
//
// A collection's source names the member tiles its mosaic is drawn from: the
// first collectionMosaicTiles members, in section and item order, that have a
// tile, each with the version and renderer generation of that tile. A mosaic
// is owed when its recorded source differs -- a member was added or removed,
// or a member's tile was redrawn -- including when no member has a tile any
// more and a mosaic is still held, which is cleared.
//
// The lease and the limit are bound as $1 and $2 by the caller.
func buildCollectionThumbnailClaim() string {
	return `
		WITH member_tiles AS (
			SELECT s.collection_id, pa.id AS asset_id, pa.thumbnail_version, pa.thumbnail_renderer,
			       row_number() OVER (PARTITION BY s.collection_id ORDER BY s.position, ci.position) AS n
			FROM portal_collection_sections s
			JOIN portal_collection_items ci ON ci.section_id = s.id
			JOIN portal_assets pa ON pa.id = ci.asset_id AND pa.deleted_at IS NULL AND pa.thumbnail_s3_key <> ''
		),
		sources AS (
			SELECT collection_id,
			       string_agg(asset_id || ':' || thumbnail_version || ':' || thumbnail_renderer, ',' ORDER BY n) AS source
			FROM member_tiles WHERE n <= ` + fmt.Sprint(collectionMosaicTiles) + `
			GROUP BY collection_id
		),
		owed AS (
			SELECT c.id FROM portal_collections c
			LEFT JOIN sources g ON g.collection_id = c.id
			WHERE c.deleted_at IS NULL
			  AND COALESCE(g.source, '') <> c.thumbnail_source
			  AND (COALESCE(g.source, '') <> '' OR c.thumbnail_s3_key <> '')
			  AND (c.thumbnail_claimed_until IS NULL OR c.thumbnail_claimed_until < now())
			ORDER BY c.updated_at DESC
			LIMIT $2
			FOR UPDATE OF c SKIP LOCKED
		)
		UPDATE portal_collections c
		SET thumbnail_claimed_until = now() + make_interval(secs => $1)
		FROM owed LEFT JOIN sources g ON g.collection_id = owed.id
		WHERE c.id = owed.id
		RETURNING c.id, c.thumbnail_s3_key, COALESCE(g.source, '')`
}

// ClaimCollectionThumbnailWork leases up to limit collections whose mosaic is
// out of date and returns them with the source each should be composed from.
func (s *postgresCollectionStore) ClaimCollectionThumbnailWork(ctx context.Context, lease time.Duration, limit int) ([]portaldomain.CollectionThumbnailWork, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, buildCollectionThumbnailClaim(), lease.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("claiming collection thumbnail work: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cursor

	var out []portaldomain.CollectionThumbnailWork
	for rows.Next() {
		var w portaldomain.CollectionThumbnailWork
		if err := rows.Scan(&w.ID, &w.ThumbnailS3Key, &w.Source); err != nil {
			return nil, fmt.Errorf("scanning claimed collection: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading claimed collections: %w", err)
	}
	return out, nil
}

// recordCollectionThumbnailQuery records a composed mosaic and the source it
// was composed from, and ends the lease. It leaves updated_at alone: a mosaic
// is platform state, and re-dating a collection each time one of its members
// is redrawn would reorder the collection list for nothing (#1466).
const recordCollectionThumbnailQuery = `UPDATE portal_collections
	SET thumbnail_s3_key = $1, thumbnail_source = $2, thumbnail_claimed_until = NULL
	WHERE id = $3 AND deleted_at IS NULL`

// RecordCollectionThumbnail records the mosaic stored at key, composed from
// source; an empty key clears the collection's tile.
func (s *postgresCollectionStore) RecordCollectionThumbnail(ctx context.Context, id, key, source string) error {
	res, err := s.db.ExecContext(ctx, recordCollectionThumbnailQuery, key, source, id)
	if err != nil {
		return fmt.Errorf("recording collection thumbnail: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("collection not found or deleted: %s", id)
	}
	return nil
}
