package capacity

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

// Walker lists every object under a prefix of a bucket, and writes the
// listing's start marker. pkg/portal/s3adapter.ClientAdapter is one.
type Walker interface {
	Walk(ctx context.Context, bucket, prefix string, fn func(s3adapter.WalkedObject) error) error
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
}

// markerKey is the object a listing writes before it walks a bucket, under a
// prefix of its own outside every platform prefix (Layout.Classify refuses
// it). Its LastModified is when the listing began on the store's own clock:
// an object is compared with that, never with this process's clock, which an
// object store's servers need not agree with.
const markerKey = markerPrefix + "listing-start"

// markStart writes the marker and reads back its LastModified. A store that
// refuses the write (a read-only credential) or does not return it is timed
// by fallback, this process's clock.
func markStart(ctx context.Context, w Walker, bucket string, fallback time.Time) time.Time {
	if err := w.PutObject(ctx, bucket, markerKey, []byte("listing start"), "text/plain"); err != nil {
		return fallback
	}
	at := fallback
	_ = w.Walk(ctx, bucket, markerPrefix, func(o s3adapter.WalkedObject) error {
		if o.Key == markerKey && !o.LastModified.IsZero() {
			at = o.LastModified
		}
		return nil
	})
	return at
}

// The rows that reference an object, per bucket. Every key column the
// platform writes is read, a soft-deleted row's included: its object is kept
// until the purge removes both. A portal row with an empty bucket was written
// before the bucket was recorded and means the portal's bucket.
const (
	portalRefsQuery = `SELECT s3_key FROM portal_assets WHERE s3_bucket IN ('', $1) AND s3_key <> ''
UNION ALL SELECT thumbnail_s3_key FROM portal_assets WHERE s3_bucket IN ('', $1) AND thumbnail_s3_key <> ''
UNION ALL SELECT thumbnail_dark_s3_key FROM portal_assets WHERE s3_bucket IN ('', $1) AND thumbnail_dark_s3_key <> ''
UNION ALL SELECT s3_key FROM portal_asset_versions WHERE COALESCE(s3_bucket, '') IN ('', $1) AND s3_key <> ''
UNION ALL SELECT thumbnail_s3_key FROM portal_collections WHERE COALESCE(thumbnail_s3_key, '') <> ''
UNION ALL SELECT s3_key FROM script_tiles WHERE COALESCE(s3_key, '') <> ''`

	resourceRefsQuery = `SELECT s3_key FROM resources WHERE s3_key <> ''
UNION ALL SELECT thumbnail_s3_key FROM resources WHERE COALESCE(thumbnail_s3_key, '') <> ''
UNION ALL SELECT thumbnail_dark_s3_key FROM resources WHERE COALESCE(thumbnail_dark_s3_key, '') <> ''
UNION ALL SELECT s3_key FROM resource_versions WHERE s3_key <> ''
UNION ALL SELECT archive_key FROM map_regions WHERE COALESCE(archive_bucket, '') IN ('', $1) AND COALESCE(archive_key, '') <> ''`
)

// mapUploadsPrefix is where an operator puts a basemap archive by hand
// (internal/maps). The platform reads those and never wrote them, so one with
// no row is not an orphan.
const mapUploadsPrefix = "maps/uploads/"

// reconciled is every purpose a reconcile reports. Webhook segments are left
// out: no row records them one by one (a compaction window records its prefix),
// so there is nothing to compare a segment with.
//
//nolint:gochecknoglobals // a read-only list.
var reconciled = []string{
	observability.StoragePurposePortalAssets,
	observability.StoragePurposeExports,
	observability.StoragePurposeScriptOutputs,
	observability.StoragePurposeThumbnails,
	observability.StoragePurposeResources,
	observability.StoragePurposeMaps,
}

// ScanResult is what one full listing found. Usage counts the objects that
// existed when it began; Snapshot is the usage rows as they stood then, so a
// save keeps what the rows gained while the listing ran (Save).
type ScanResult struct {
	Usage    map[usageKey]usageDelta
	Snapshot map[usageKey]usageDelta
	Orphaned map[string]int64
	Dangling map[string]int64
	Objects  int64
	Duration time.Duration
	Buckets  []string
}

// Scanner lists every platform prefix and compares what it finds with the
// rows that reference objects.
type Scanner struct {
	DB     *sql.DB
	Layout Layout
	// Walkers maps a bucket to the client that lists it.
	Walkers map[string]Walker
	// OrphanGrace is how recently an object may have been written and still
	// not be counted as an orphan: a writer stores the object before the row
	// that references it.
	OrphanGrace time.Duration
	now         func() time.Time
}

// Scan reads the usage rows and the references, then lists every prefix. It
// changes nothing. An object modified after the listing began is not counted
// in its usage: the write that made it moved the row while the listing ran,
// and Save keeps that.
func (s *Scanner) Scan(ctx context.Context) (ScanResult, error) {
	start := s.clock()
	res := ScanResult{
		Usage: map[usageKey]usageDelta{}, Orphaned: map[string]int64{}, Dangling: map[string]int64{},
	}
	snapshot, err := readUsage(ctx, s.DB, snapshotQuery)
	if err != nil {
		return res, err
	}
	res.Snapshot = snapshot
	refs, err := s.loadRefs(ctx)
	if err != nil {
		return res, err
	}
	found := map[string]map[string]bool{}
	began := map[string]time.Time{}
	for _, bp := range s.Layout.Prefixes() {
		w := s.Walkers[bp.Bucket]
		if w == nil {
			continue
		}
		if found[bp.Bucket] == nil {
			found[bp.Bucket] = map[string]bool{}
			res.Buckets = append(res.Buckets, bp.Bucket)
			began[bp.Bucket] = markStart(ctx, w, bp.Bucket, start)
		}
		at := began[bp.Bucket]
		pass := walkState{refs: refs[bp.Bucket], found: found[bp.Bucket], start: at, cutoff: at.Add(-s.OrphanGrace), res: &res}
		if err := s.walk(ctx, w, bp, pass); err != nil {
			return res, err
		}
	}
	countDangling(refs, found, res.Dangling)
	res.Duration = s.clock().Sub(start)
	return res, nil
}

// walkState is what one prefix's listing reads and writes: the bucket's
// references and the referenced keys found so far, when the listing began
// and the orphan cutoff, and the result being built. Only referenced keys are
// remembered, so a bucket of millions of objects is never held in memory; the
// prefixes a listing walks do not overlap (Layout.Prefixes), so no object is
// met twice.
type walkState struct {
	refs   bucketRefs
	found  map[string]bool
	start  time.Time
	cutoff time.Time
	res    *ScanResult
}

// walk lists one prefix into the result.
func (s *Scanner) walk(ctx context.Context, w Walker, bp BucketPrefix, st walkState) error {
	err := w.Walk(ctx, bp.Bucket, bp.Prefix, func(o s3adapter.WalkedObject) error {
		purpose, ok := s.Layout.Classify(bp.Bucket, o.Key)
		if !ok {
			return nil
		}
		st.res.Objects++
		if o.LastModified.IsZero() || !o.LastModified.After(st.start) {
			k := usageKey{bp.Bucket, purpose}
			u := st.res.Usage[k]
			u.objects++
			u.bytes += o.Size
			st.res.Usage[k] = u
		}
		if _, referenced := st.refs.keys[o.Key]; referenced {
			st.found[o.Key] = true
		} else if slices.Contains(reconciled, purpose) && isOrphan(st.refs, o, st.cutoff) {
			st.res.Orphaned[purpose]++
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("listing %s: %w", bp.Prefix, err)
	}
	return nil
}

// countDangling counts, per purpose, the referenced keys a listing did not
// find. A bucket this replica could not list says nothing about its rows.
func countDangling(refs map[string]bucketRefs, found map[string]map[string]bool, dangling map[string]int64) {
	for bucket, byKey := range refs {
		if found[bucket] == nil {
			continue
		}
		for key, purpose := range byKey.keys {
			if !found[bucket][key] && purpose != "" {
				dangling[purpose]++
			}
		}
	}
}

// isOrphan reports whether an object no row references, old enough that its
// row cannot still be on the way, and not one an operator put there by hand.
func isOrphan(refs bucketRefs, o s3adapter.WalkedObject, cutoff time.Time) bool {
	if strings.HasPrefix(o.Key, mapUploadsPrefix) || refs.ownsTile(o.Key) {
		return false
	}
	return o.LastModified.IsZero() || o.LastModified.Before(cutoff)
}

// bucketRefs is every key the rows of one bucket reference, by key and by the
// directory it sits in.
type bucketRefs struct {
	keys map[string]string
	dirs map[string]bool
}

// add records one referenced key.
func (b *bucketRefs) add(key, purpose string) {
	b.keys[key] = purpose
	b.dirs[path.Dir(key)] = true
}

// ownsTile reports whether key is a tile drawn beside an object a row
// references. A tile is found by its content's directory, the way the purge
// and the version prune find it (portaldomain.ThumbnailKeysFor,
// CollectionDarkThumbnailKey, scripttiles.DarkKey): no row records an asset
// version's tiles, a collection's dark mosaic or a script's dark tile.
func (b bucketRefs) ownsTile(key string) bool {
	switch path.Base(key) {
	case tile, darkTile, legacyTile, legacyDarkTile, scriptTile, scriptDarkTile:
		return b.dirs[path.Dir(key)]
	default:
		return false
	}
}

// loadRefs reads every referenced key, by bucket, with the purpose its key
// sorts under. The empty purpose marks a key outside the platform's prefixes:
// still a reference (its object is no orphan), never counted as dangling.
func (s *Scanner) loadRefs(ctx context.Context) (map[string]bucketRefs, error) {
	out := map[string]bucketRefs{}
	for _, src := range []struct{ bucket, query string }{
		{s.Layout.PortalBucket, portalRefsQuery},
		{s.Layout.ResourceBucket, resourceRefsQuery},
	} {
		if src.bucket == "" || s.Walkers[src.bucket] == nil {
			continue
		}
		if out[src.bucket].keys == nil {
			out[src.bucket] = bucketRefs{keys: map[string]string{}, dirs: map[string]bool{}}
		}
		rows, err := s.DB.QueryContext(ctx, src.query, src.bucket)
		if err != nil {
			return nil, fmt.Errorf("reading object references: %w", err)
		}
		byKey := out[src.bucket]
		if err := scanRows(rows, func(r *sql.Rows) error {
			var key string
			if err := r.Scan(&key); err != nil {
				return err //nolint:wrapcheck // wrapped below
			}
			purpose, _ := s.Layout.Classify(src.bucket, key)
			byKey.add(key, purpose)
			return nil
		}); err != nil {
			return nil, fmt.Errorf("reading object references: %w", err)
		}
	}
	return out, nil
}

// clock is now, overridable in tests.
func (s *Scanner) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// The reads and writes of one scan's save.
const (
	snapshotQuery     = `SELECT bucket, purpose, bytes, objects FROM storage_usage`
	lockUsageQuery    = `SELECT bucket, purpose, bytes, objects FROM storage_usage WHERE bucket = ANY($1::text[]) FOR UPDATE`
	dropUnlistedQuery = `DELETE FROM storage_usage WHERE NOT (bucket = ANY($1::text[]))`
	setUsageQuery     = `INSERT INTO storage_usage (bucket, purpose, bytes, objects, listed_at, updated_at)
VALUES ($1, $2, $3, $4, NOW(), NOW())
ON CONFLICT (bucket, purpose) DO UPDATE SET bytes = $3, objects = $4, listed_at = NOW(), updated_at = NOW()`
	setReconcileQuery = `INSERT INTO storage_reconciles (purpose, orphaned, dangling, reconciled_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (purpose) DO UPDATE SET orphaned = $2, dangling = $3, reconciled_at = NOW()`
	setScanQuery = `INSERT INTO storage_scans (id, duration_seconds, objects, finished_at) VALUES (1, $1, $2, NOW())
ON CONFLICT (id) DO UPDATE SET duration_seconds = $1, objects = $2, finished_at = NOW()`
)

// readUsage is every usage row, by bucket and purpose.
func readUsage(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any,
) (map[usageKey]usageDelta, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading storage usage: %w", err)
	}
	out := map[usageKey]usageDelta{}
	if err := scanRows(rows, func(r *sql.Rows) error {
		var k usageKey
		var d usageDelta
		if err := r.Scan(&k.bucket, &k.purpose, &d.bytes, &d.objects); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		out[k] = d
		return nil
	}); err != nil {
		return nil, fmt.Errorf("reading storage usage: %w", err)
	}
	return out, nil
}

// Save writes a scan's result in one transaction. Each usage row of a listed
// bucket becomes the listing's total plus what the row gained after the
// listing read it: a put made while the listing ran is kept, not overwritten,
// and not counted twice (the listing skipped objects modified after it began).
// A row for a bucket the deployment no longer lists is removed. Then the
// reconcile counts and the scan's own duration.
func (r ScanResult) Save(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("saving storage scan: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	buckets := pq.Array(append(make([]string, 0, len(r.Buckets)), r.Buckets...))
	if _, err := tx.ExecContext(ctx, dropUnlistedQuery, buckets); err != nil {
		return fmt.Errorf("saving storage usage: %w", err)
	}
	current, err := readUsage(ctx, tx, lockUsageQuery, buckets)
	if err != nil {
		return err
	}
	for k, total := range r.totals(current) {
		if _, err := tx.ExecContext(ctx, setUsageQuery, k.bucket, k.purpose, total.bytes, total.objects); err != nil {
			return fmt.Errorf("saving storage usage: %w", err)
		}
	}
	for _, p := range reconciled {
		if _, err := tx.ExecContext(ctx, setReconcileQuery, p, r.Orphaned[p], r.Dangling[p]); err != nil {
			return fmt.Errorf("saving storage reconcile: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, setScanQuery, r.Duration.Seconds(), r.Objects); err != nil {
		return fmt.Errorf("saving storage scan: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("saving storage scan: %w", err)
	}
	return nil
}

// totals is each row's new value: the listing's count plus the row's gain
// since the snapshot, never below zero.
func (r ScanResult) totals(current map[usageKey]usageDelta) map[usageKey]usageDelta {
	out := maps.Clone(r.Usage)
	if out == nil {
		out = map[usageKey]usageDelta{}
	}
	for k, now := range current {
		was := r.Snapshot[k]
		t := out[k]
		t.bytes += now.bytes - was.bytes
		t.objects += now.objects - was.objects
		out[k] = t
	}
	for k, t := range out {
		out[k] = usageDelta{objects: max(t.objects, 0), bytes: max(t.bytes, 0)}
	}
	return out
}
