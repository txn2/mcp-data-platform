package portalpurge

import (
	"context"
	"errors"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// ErrNotInTrash is returned when a purge names an asset or collection that is
// not deleted, or not there at all.
var ErrNotInTrash = errors.New("not in the trash")

// ErrObjectsKept is returned when an item's stored objects could not all be
// removed, which keeps its row so a later attempt or sweep tries again.
var ErrObjectsKept = errors.New("its stored files could not all be removed; try again")

// purgeNowMargin puts the cutoff of an immediate purge past any deletion
// already recorded, so the final statement's cutoff admits the item.
const purgeNowMargin = time.Minute

// selectDeletedAssetQuery is selectPurgeableAssetsQuery for one deleted
// asset, whatever its age.
const selectDeletedAssetQuery = `
	SELECT a.id, a.s3_bucket, a.s3_key, a.thumbnail_s3_key, a.thumbnail_dark_s3_key,
	       COALESCE(array_agg(v.s3_bucket) FILTER (WHERE v.asset_id IS NOT NULL), '{}'),
	       COALESCE(array_agg(v.s3_key) FILTER (WHERE v.asset_id IS NOT NULL), '{}')
	  FROM portal_assets a
	  LEFT JOIN portal_asset_versions v ON v.asset_id = a.id
	 WHERE a.id = $1 AND a.deleted_at IS NOT NULL
	 GROUP BY a.id`

// selectDeletedCollectionQuery reads the mosaic of one deleted collection.
const selectDeletedCollectionQuery = `SELECT thumbnail_s3_key FROM portal_collections WHERE id = $1 AND deleted_at IS NOT NULL`

// PurgeAsset removes one deleted asset for good now, as the retention sweep
// would after its grace period (#2509's Delete permanently): its objects
// first, then its rows and the rows that reference it. The caller checks the
// person may manage it first.
func (p *Purger) PurgeAsset(ctx context.Context, id string) error {
	assets, err := p.scanAssets(ctx, selectDeletedAssetQuery, id)
	if err != nil {
		return err
	}
	if len(assets) == 0 {
		return ErrNotInTrash
	}
	if !p.deleteObjects(ctx, assets[0].objects) {
		return ErrObjectsKept
	}
	return removedOne(p.deleteRows(ctx, []string{
		purgeAssetSharesQuery, purgeAssetItemsQuery, purgeAssetThreadsQuery, purgeAssetProducersQuery,
	}, purgeAssetsQuery, []string{id}, time.Now().Add(purgeNowMargin)))
}

// PurgeCollection removes one deleted collection for good now, with its
// mosaic, shares and threads. The assets it held are untouched.
func (p *Purger) PurgeCollection(ctx context.Context, id string) error {
	var mosaic string
	if err := p.db.QueryRowContext(ctx, selectDeletedCollectionQuery, id).Scan(&mosaic); err != nil {
		return ErrNotInTrash
	}
	var objects []storedObject
	if mosaic != "" {
		objects = []storedObject{{p.bucket, mosaic}, {p.bucket, portaldomain.CollectionDarkThumbnailKey(mosaic)}}
	}
	if !p.deleteObjects(ctx, objects) {
		return ErrObjectsKept
	}
	return removedOne(p.deleteRows(ctx, []string{purgeCollectionSharesQuery, purgeCollectionThreadsQuery},
		purgeCollectionsQuery, []string{id}, time.Now().Add(purgeNowMargin)))
}

// removedOne reads a one-item deleteRows result: no row removed is the item
// restored or purged meanwhile.
func removedOne(n int, err error) error {
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotInTrash
	}
	return nil
}
