package capacity

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// usageKey is one bucket and purpose.
type usageKey struct{ bucket, purpose string }

// usageDelta is what changed under one key since the last flush.
type usageDelta struct{ objects, bytes int64 }

// Recorder collects the puts and deletes the platform makes (through
// internal/objectobs.DoObject) and adds them to the shared usage rows on
// Flush. Collecting first keeps a burst of writes to one bucket from becoming
// a burst of updates to one row.
type Recorder struct {
	layout  Layout
	mu      sync.Mutex
	pending map[usageKey]usageDelta
}

// NewRecorder returns a recorder that sorts keys by layout.
func NewRecorder(layout Layout) *Recorder {
	return &Recorder{layout: layout, pending: map[usageKey]usageDelta{}}
}

// RecordObject notes one object put (objects 1, bytes stored) or deleted
// (objects -1). A key outside the platform's prefixes is ignored, and so is a
// tile: the thumbnail worker redraws a tile in place at the same key, so a put
// is not a new object, and the full listing alone counts them. Every other
// purpose writes a new key per version, run, segment or build.
func (r *Recorder) RecordObject(bucket, key string, objects, bytes int64) {
	purpose, ok := r.layout.Classify(bucket, key)
	if !ok || purpose == observability.StoragePurposeThumbnails {
		return
	}
	k := usageKey{bucket, purpose}
	r.mu.Lock()
	d := r.pending[k]
	d.objects += objects
	d.bytes += bytes
	r.pending[k] = d
	r.mu.Unlock()
}

// take removes and returns what is pending.
func (r *Recorder) take() map[usageKey]usageDelta {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.pending
	r.pending = map[usageKey]usageDelta{}
	return out
}

// restore puts deltas a failed flush could not write back, so the next flush
// tries again.
func (r *Recorder) restore(deltas map[usageKey]usageDelta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, d := range deltas {
		p := r.pending[k]
		p.objects += d.objects
		p.bytes += d.bytes
		r.pending[k] = p
	}
}

// addUsageQuery adds a delta to a usage row, creating it when no listing has
// written it yet. A count never goes below zero: a delete of an object the
// row never counted (written before the row existed) cannot make it negative.
const addUsageQuery = `INSERT INTO storage_usage (bucket, purpose, bytes, objects)
VALUES ($1, $2, GREATEST($3::bigint, 0), GREATEST($4::bigint, 0))
ON CONFLICT (bucket, purpose) DO UPDATE SET
    bytes = GREATEST(storage_usage.bytes + $3::bigint, 0),
    objects = GREATEST(storage_usage.objects + $4::bigint, 0),
    updated_at = NOW()`

// Flush adds every pending delta to its row. A delta that fails to write is
// kept for the next flush.
func (r *Recorder) Flush(ctx context.Context, db *sql.DB) error {
	deltas := r.take()
	failed := map[usageKey]usageDelta{}
	var firstErr error
	for k, d := range deltas {
		if d.objects == 0 && d.bytes == 0 {
			continue
		}
		if _, err := db.ExecContext(ctx, addUsageQuery, k.bucket, k.purpose, d.bytes, d.objects); err != nil {
			failed[k] = d
			if firstErr == nil {
				firstErr = fmt.Errorf("adding to storage usage: %w", err)
			}
		}
	}
	if len(failed) > 0 {
		r.restore(failed)
	}
	return firstErr
}

// The reads of the shared rows every replica reports.
const (
	usageQuery     = `SELECT bucket, purpose, bytes, objects FROM storage_usage ORDER BY bucket, purpose`
	reconcileQuery = `SELECT purpose, orphaned, dangling FROM storage_reconciles ORDER BY purpose`
	scanQuery      = `SELECT duration_seconds, objects FROM storage_scans WHERE id = 1`
)

// ReadStorage reads the usage rows, the last reconcile and the last scan.
// backends names each bucket the deployment configures and the object store
// it is on, which labels its rows; a row for any other bucket (one the
// deployment no longer uses) is not reported. Nil reports every row, labeled
// other.
func ReadStorage(ctx context.Context, db *sql.DB, backends map[string]string) (observability.StorageCapacity, error) {
	var out observability.StorageCapacity
	var err error
	if out.Usage, err = readUsageRows(ctx, db, backends); err != nil {
		return out, err
	}
	if out.Reconcile, err = readReconcile(ctx, db); err != nil {
		return out, err
	}
	err = db.QueryRowContext(ctx, scanQuery).Scan(&out.ScanSeconds, &out.ScanObjects)
	switch {
	case err == nil:
		out.ScanKnown = true
	case err != sql.ErrNoRows: //nolint:errorlint // QueryRow returns sql.ErrNoRows unwrapped
		return out, fmt.Errorf("reading storage scan: %w", err)
	}
	return out, nil
}

// readUsageRows is the usage rows of the configured buckets, labeled.
func readUsageRows(ctx context.Context, db *sql.DB, backends map[string]string) ([]observability.BucketUsage, error) {
	rows, err := db.QueryContext(ctx, usageQuery)
	if err != nil {
		return nil, fmt.Errorf("reading storage usage: %w", err)
	}
	var out []observability.BucketUsage
	if err := scanRows(rows, func(r *sql.Rows) error {
		var u observability.BucketUsage
		if err := r.Scan(&u.Bucket, &u.Purpose, &u.Bytes, &u.Objects); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		kind, configured := backends[u.Bucket]
		if backends != nil && !configured {
			return nil
		}
		u.Backend = cmp.Or(kind, observability.StorageBackendOther)
		out = append(out, u)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("reading storage usage: %w", err)
	}
	return out, nil
}

// readReconcile is the last reconcile's counts.
func readReconcile(ctx context.Context, db *sql.DB) ([]observability.ReconcileCount, error) {
	rows, err := db.QueryContext(ctx, reconcileQuery)
	if err != nil {
		return nil, fmt.Errorf("reading storage reconcile: %w", err)
	}
	var out []observability.ReconcileCount
	if err := scanRows(rows, func(r *sql.Rows) error {
		var c observability.ReconcileCount
		if err := r.Scan(&c.Purpose, &c.Orphaned, &c.Dangling); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		out = append(out, c)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("reading storage reconcile: %w", err)
	}
	return out, nil
}
