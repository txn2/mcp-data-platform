// Package portalpurge removes, for good, what the portal soft-deleted once it
// has been deleted longer than the retention (#1904), with every object it
// names.
package portalpurge

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// What the portal deletes it only hides: an asset, a collection, a feedback
// thread or a knowledge page is stamped deleted_at and drops out of every
// read, so a mistaken delete can be undone. Without a purge the rows, every
// version object and every tile stay for the life of the deployment (#1904).
// The Purger removes what has been deleted for longer than the grace period,
// objects first: a row is invisible once deleted, so an object whose delete
// failed leaves the row for the next sweep to try again, where the reverse
// order would leave an object nothing can find.

// purgeBatch bounds how many rows of one kind a sweep removes per statement,
// so one sweep over a large backlog never holds a long transaction.
const purgeBatch = 200

// ObjectDeleter removes one stored object.
type ObjectDeleter interface {
	DeleteObject(ctx context.Context, bucket, key string) error
}

// Purger hard-deletes what the portal soft-deleted before a cutoff.
type Purger struct {
	db      *sql.DB
	objects ObjectDeleter
	// mosaicBucket is the portal bucket, which every collection mosaic is
	// written to; the collection row does not record it.
	mosaicBucket string
}

// NewPurger builds a Purger. A nil objects deletes rows only, for a
// deployment with no portal storage, where no row names an object.
func NewPurger(db *sql.DB, objects ObjectDeleter, mosaicBucket string) *Purger {
	return &Purger{db: db, objects: objects, mosaicBucket: mosaicBucket}
}

// PurgeResult counts what one sweep removed.
type PurgeResult struct {
	Assets, Collections, Threads, KnowledgePages int
}

// Total is every row the sweep removed.
func (r PurgeResult) Total() int {
	return r.Assets + r.Collections + r.Threads + r.KnowledgePages
}

// Purge removes everything soft-deleted before cutoff. Assets go first, then
// collections, threads and knowledge pages. A failure stops the sweep and is
// returned with what was removed before it.
func (p *Purger) Purge(ctx context.Context, cutoff time.Time) (PurgeResult, error) {
	var res PurgeResult
	var err error
	if res.Assets, err = p.drain(ctx, cutoff, p.purgeAssets); err != nil {
		return res, err
	}
	if res.Collections, err = p.drain(ctx, cutoff, p.purgeCollections); err != nil {
		return res, err
	}
	if res.Threads, err = p.drain(ctx, cutoff, p.purgeRows(purgeThreadsQuery)); err != nil {
		return res, err
	}
	res.KnowledgePages, err = p.drain(ctx, cutoff, p.purgeRows(purgeKnowledgePagesQuery))
	return res, err
}

// batchFunc purges one batch, reporting how many rows it listed and how many
// it removed.
type batchFunc func(context.Context, time.Time) (listed, removed int, err error)

// drain runs one batch purge until a batch lists fewer rows than the batch
// size, which is the backlog exhausted, or removes none, which is every row
// it listed held back by an object that would not delete.
func (*Purger) drain(ctx context.Context, cutoff time.Time, batch batchFunc) (int, error) {
	total := 0
	for {
		listed, removed, err := batch(ctx, cutoff)
		total += removed
		if err != nil || listed < purgeBatch || removed == 0 {
			return total, err
		}
	}
}

// selectPurgeableAssetsQuery lists one batch of assets deleted before $1,
// with every object each one names: its content, its tiles, and each
// version's content and the tiles drawn beside it.
const selectPurgeableAssetsQuery = `
	SELECT a.id, a.s3_bucket, a.s3_key, a.thumbnail_s3_key, a.thumbnail_dark_s3_key,
	       COALESCE(array_agg(v.s3_bucket) FILTER (WHERE v.asset_id IS NOT NULL), '{}'),
	       COALESCE(array_agg(v.s3_key) FILTER (WHERE v.asset_id IS NOT NULL), '{}')
	  FROM portal_assets a
	  LEFT JOIN portal_asset_versions v ON v.asset_id = a.id
	 WHERE a.deleted_at IS NOT NULL AND a.deleted_at < $1
	 GROUP BY a.id
	 ORDER BY a.deleted_at
	 LIMIT $2`

// The asset purge removes the rows that reference an asset without a cascade
// (its shares, its places in collections, its feedback threads, whose events
// cascade) and the producers recorded against it, then the asset, whose
// versions and content references cascade. Nothing restores a deleted asset
// or collection, so the ids a batch listed are still deleted when it removes
// them; the final statement repeats the cutoff all the same.
const (
	purgeAssetSharesQuery    = `DELETE FROM portal_shares WHERE asset_id = ANY($1)`
	purgeAssetItemsQuery     = `DELETE FROM portal_collection_items WHERE asset_id = ANY($1)`
	purgeAssetThreadsQuery   = `DELETE FROM portal_threads WHERE asset_id = ANY($1)`
	purgeAssetProducersQuery = `DELETE FROM content_producers WHERE target_kind = 'asset' AND target_id = ANY($1)`
	purgeAssetsQuery         = `DELETE FROM portal_assets WHERE id = ANY($1) AND deleted_at IS NOT NULL AND deleted_at < $2`
)

// purgeableAsset is one asset a batch removes and the objects it names.
type purgeableAsset struct {
	id      string
	objects []storedObject
}

type storedObject struct{ bucket, key string }

func (p *Purger) purgeAssets(ctx context.Context, cutoff time.Time) (listed, removed int, err error) {
	assets, err := p.purgeableAssets(ctx, cutoff)
	if err != nil || len(assets) == 0 {
		return 0, 0, err
	}
	ids := make([]string, 0, len(assets))
	for _, a := range assets {
		if p.deleteObjects(ctx, a.objects) {
			ids = append(ids, a.id)
		}
	}
	removed, err = p.deleteRows(ctx, []string{
		purgeAssetSharesQuery, purgeAssetItemsQuery, purgeAssetThreadsQuery, purgeAssetProducersQuery,
	}, purgeAssetsQuery, ids, cutoff)
	return len(assets), removed, err
}

func (p *Purger) purgeableAssets(ctx context.Context, cutoff time.Time) ([]purgeableAsset, error) {
	rows, err := p.db.QueryContext(ctx, selectPurgeableAssetsQuery, cutoff, purgeBatch)
	if err != nil {
		return nil, fmt.Errorf("listing deleted assets to purge: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []purgeableAsset
	for rows.Next() {
		var (
			a                      purgeableAsset
			bucket, key, lit, dark string
			vBuckets, vKeys        []string
		)
		if err := rows.Scan(&a.id, &bucket, &key, &lit, &dark, pq.Array(&vBuckets), pq.Array(&vKeys)); err != nil {
			return nil, fmt.Errorf("scanning a deleted asset: %w", err)
		}
		a.objects = assetObjects(assetRow{bucket, key, lit, dark, vBuckets, vKeys})
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing deleted assets to purge: %w", err)
	}
	return out, nil
}

// assetRow is the asset columns and version columns a purge reads objects
// from.
type assetRow struct {
	bucket, key, light, dark string
	vBuckets, vKeys          []string
}

// assetObjects is every object an asset row and its version rows name, once
// each. A version's tiles are derived, as the version prune derives them: no
// version row records the tile drawn beside its content.
func assetObjects(r assetRow) []storedObject {
	seen := map[storedObject]bool{}
	var out []storedObject
	add := func(b, k string) {
		o := storedObject{b, k}
		if k == "" || seen[o] {
			return
		}
		seen[o] = true
		out = append(out, o)
	}
	for _, k := range append([]string{r.key, r.light, r.dark}, portaldomain.ThumbnailKeysFor(r.key)...) {
		add(r.bucket, k)
	}
	for i, k := range r.vKeys {
		b := r.bucket
		if i < len(r.vBuckets) {
			b = r.vBuckets[i]
		}
		for _, vk := range (portaldomain.AssetVersion{S3Key: k}).ObjectKeys() {
			add(b, vk)
		}
	}
	return out
}

// selectPurgeableCollectionsQuery lists one batch of collections deleted
// before $1 with the mosaic each one records. The dark mosaic sits beside it.
const selectPurgeableCollectionsQuery = `
	SELECT id, thumbnail_s3_key FROM portal_collections
	 WHERE deleted_at IS NOT NULL AND deleted_at < $1
	 ORDER BY deleted_at
	 LIMIT $2`

// The collection purge removes its shares and feedback threads, which
// reference it without a cascade, then the collection, whose items and
// sections cascade.
const (
	purgeCollectionSharesQuery  = `DELETE FROM portal_shares WHERE collection_id = ANY($1)`
	purgeCollectionThreadsQuery = `DELETE FROM portal_threads WHERE collection_id = ANY($1)`
	purgeCollectionsQuery       = `DELETE FROM portal_collections WHERE id = ANY($1) AND deleted_at IS NOT NULL AND deleted_at < $2`
)

func (p *Purger) purgeCollections(ctx context.Context, cutoff time.Time) (listed, removed int, err error) {
	collections, err := p.purgeableCollections(ctx, cutoff)
	if err != nil || len(collections) == 0 {
		return 0, 0, err
	}
	ids := make([]string, 0, len(collections))
	for id, mosaic := range collections {
		var objects []storedObject
		if mosaic != "" {
			objects = []storedObject{{p.mosaicBucket, mosaic}, {p.mosaicBucket, portaldomain.CollectionDarkThumbnailKey(mosaic)}}
		}
		if p.deleteObjects(ctx, objects) {
			ids = append(ids, id)
		}
	}
	removed, err = p.deleteRows(ctx, []string{purgeCollectionSharesQuery, purgeCollectionThreadsQuery}, purgeCollectionsQuery, ids, cutoff)
	return len(collections), removed, err
}

// purgeableCollections lists one batch of collections to purge, each with the
// mosaic it records.
func (p *Purger) purgeableCollections(ctx context.Context, cutoff time.Time) (map[string]string, error) {
	rows, err := p.db.QueryContext(ctx, selectPurgeableCollectionsQuery, cutoff, purgeBatch)
	if err != nil {
		return nil, fmt.Errorf("listing deleted collections to purge: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id, mosaic string
		if err := rows.Scan(&id, &mosaic); err != nil {
			return nil, fmt.Errorf("scanning a deleted collection: %w", err)
		}
		out[id] = mosaic
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing deleted collections to purge: %w", err)
	}
	return out, nil
}

// Threads and knowledge pages name no object. A thread's events, and a
// page's versions, embedding chunks, entity references and threads, cascade.
// A built-in knowledge page is soft-deleted when an administrator hides it and
// is brought back by restoring the built-in pages, so it is never purged.
const (
	purgeThreadsQuery = `DELETE FROM portal_threads WHERE id IN (
		SELECT id FROM portal_threads WHERE deleted_at IS NOT NULL AND deleted_at < $1 ORDER BY deleted_at LIMIT $2)`
	purgeKnowledgePagesQuery = `DELETE FROM portal_knowledge_pages WHERE id IN (
		SELECT id FROM portal_knowledge_pages WHERE deleted_at IS NOT NULL AND deleted_at < $1 AND NOT builtin
		 ORDER BY deleted_at LIMIT $2)`
)

func (p *Purger) purgeRows(query string) batchFunc {
	return func(ctx context.Context, cutoff time.Time) (listed, removed int, err error) {
		res, err := p.db.ExecContext(ctx, query, cutoff, purgeBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("purging deleted rows: %w", err)
		}
		n, _ := res.RowsAffected()
		return int(n), int(n), nil
	}
}

// deleteRows removes the dependents and then the rows of one batch in one
// transaction, and reports how many rows the final statement removed.
func (p *Purger) deleteRows(ctx context.Context, dependents []string, final string, ids []string, cutoff time.Time) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range dependents {
		if _, err := tx.ExecContext(ctx, q, pq.Array(ids)); err != nil {
			return 0, fmt.Errorf("purging dependents: %w", err)
		}
	}
	res, err := tx.ExecContext(ctx, final, pq.Array(ids), cutoff)
	if err != nil {
		return 0, fmt.Errorf("purging deleted rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing purge: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// deleteObjects removes one row's objects and reports whether every one is
// gone. An object that resists is logged and its row is kept, so a later
// sweep tries again; deleting an object that is already gone succeeds.
func (p *Purger) deleteObjects(ctx context.Context, objects []storedObject) bool {
	if p.objects == nil {
		return true
	}
	ok := true
	for _, o := range objects {
		if err := p.objects.DeleteObject(ctx, o.bucket, o.key); err != nil {
			slog.Warn("portal purge: object not deleted; the row is kept for the next sweep",
				"key", logsan.SanitizeForLog(o.key), "error", logsan.SanitizeForLog(err.Error()))
			ok = false
		}
	}
	return ok
}
