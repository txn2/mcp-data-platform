// Package portalversions holds the PostgreSQL store for asset version history.
//
// Version history is its own table (portal_asset_versions) with its own
// projection and scanner; it touches portal_assets only to take the row lock
// that serializes version numbering and to read the retention cap that lock
// protects. It sits beside the asset store rather than inside it because it
// shares no query builder, column list or scanner with it.
//
// Retention is enforced here rather than by a sweeper because CreateVersion is
// the single door every asset write already passes through -- the portal
// handlers, the admin routes, the asset toolkit, managed-script output and both
// export adapters all reach the table through it -- so a cap applied at the
// write is a cap nothing can route around, and it needs no schedule to converge.
package portalversions

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// --- PostgreSQL VersionStore ---

// ObjectDeleter is the slice of the portal's blob client a prune needs. It is
// named here rather than taking portaldomain.S3Client whole so the store cannot
// read or write content -- deleting the objects of rows it just deleted is the
// only reason it touches storage at all.
type ObjectDeleter interface {
	DeleteObject(ctx context.Context, bucket, key string) error
}

type store struct {
	db *sql.DB
	// objects deletes the blobs of pruned versions. Nil in database-only mode,
	// where the prune still trims the table and there are no objects to lose.
	objects ObjectDeleter
	// platformMaxVersions is the deployment's portal.max_versions, applying to
	// every asset that carries no override of its own. Nil selects
	// portaldomain.DefaultMaxVersions.
	platformMaxVersions *int
}

// NewPostgres creates the PostgreSQL asset version store. objects deletes the
// blobs of versions the retention cap prunes and may be nil; platformMaxVersions
// is the deployment default a per-asset override supersedes.
func NewPostgres(db *sql.DB, objects ObjectDeleter, platformMaxVersions *int) portaldomain.VersionStore {
	return &store{db: db, objects: objects, platformMaxVersions: platformMaxVersions}
}

func (s *store) CreateVersion(ctx context.Context, version portaldomain.AssetVersion) (int, error) { //nolint:revive // interface impl
	nextVersion, pruned, err := s.createVersionTx(ctx, version)
	if err != nil {
		return 0, err
	}
	// After the commit, never before: the rows are already gone, and an object
	// that outlives its row is reclaimable while a row whose object was deleted
	// under a rolled-back transaction is a version that lists and cannot be read.
	s.deletePrunedObjects(ctx, version.AssetID, pruned)
	return nextVersion, nil
}

// createVersionTx records the version, moves the asset head, and prunes history
// past the asset's effective cap -- all under the asset row lock, in one
// transaction. It returns the assigned version number and the rows the prune
// removed, whose blobs the caller deletes once the transaction has committed.
func (s *store) createVersionTx(ctx context.Context, version portaldomain.AssetVersion) (int, pruneResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, pruneResult{}, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	// Lock the asset row and determine the next version number atomically. The
	// same read takes the retention override, so the cap applied is the one in
	// force under this lock rather than one read before another writer moved it.
	var currentVersion int
	var maxVersions sql.NullInt64
	lockQuery := `SELECT current_version, max_versions FROM portal_assets WHERE id = $1 FOR UPDATE`
	if err := tx.QueryRowContext(ctx, lockQuery, version.AssetID).Scan(&currentVersion, &maxVersions); err != nil {
		return 0, pruneResult{}, fmt.Errorf("locking asset row: %w", err)
	}
	nextVersion := currentVersion + 1

	insertQuery := `
		INSERT INTO portal_asset_versions
		(id, asset_id, version, s3_key, s3_bucket, content_type, size_bytes, created_by, change_summary)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err = tx.ExecContext(ctx, insertQuery,
		version.ID, version.AssetID, nextVersion,
		version.S3Key, version.S3Bucket, version.ContentType,
		version.SizeBytes, version.CreatedBy, version.ChangeSummary,
	)
	if err != nil {
		return 0, pruneResult{}, fmt.Errorf("inserting version: %w", err)
	}

	// The thumbnail pointers are deliberately untouched. Blanking them was what
	// sent an asset a managed script rewrites back to the placeholder icon on
	// every run and left it there: nothing regenerates a thumbnail on the
	// server, so the only capturer is a portal tab, and the asset had no image
	// until somebody happened to open the page it was listed on. The row now
	// keeps the capture it has and records the version it came from
	// (thumbnail_version, migration 000122), so it stays behind rather than
	// blank and the refresh queue can find it (#1431).
	updateQuery := `
		UPDATE portal_assets
		SET current_version = $1, s3_key = $2, content_type = $3, size_bytes = $4, updated_at = NOW()
		WHERE id = $5
	`
	_, err = tx.ExecContext(ctx, updateQuery,
		nextVersion, version.S3Key, version.ContentType, version.SizeBytes, version.AssetID,
	)
	if err != nil {
		return 0, pruneResult{}, fmt.Errorf("updating asset version: %w", err)
	}

	// Pruned after the head has moved, so the guard below reads the asset's new
	// s3_key and the version just written is the one key the delete can never
	// reach.
	pruned, err := prune(ctx, tx, version.AssetID, nextVersion, s.effectiveCap(maxVersions))
	if err != nil {
		return 0, pruneResult{}, err
	}
	// Asked inside the transaction, after the delete, so the answer is the set
	// of keys that outlive it.
	if len(pruned.removed) > 0 {
		pruned.retained, err = survivingObjectKeys(ctx, tx, version.AssetID)
		if err != nil {
			return 0, pruneResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, pruneResult{}, fmt.Errorf("committing version: %w", err)
	}
	return nextVersion, pruned, nil
}

// pruneResult is what a prune leaves for the caller to clean up once the
// transaction has committed: the version rows removed, and every object key the
// versions that survived still own. The second half is not an optimization --
// two version rows may legitimately name one object, so the cleanup has to ask
// what is still referenced rather than infer it from the key.
type pruneResult struct {
	removed  []portaldomain.AssetVersion
	retained map[string]struct{}
}

// effectiveCap resolves the retention cap for one asset from its stored
// override and the deployment default. A returned 0 means unlimited.
func (s *store) effectiveCap(stored sql.NullInt64) int {
	var override *int
	if stored.Valid {
		n := int(stored.Int64)
		override = &n
	}
	return portaldomain.EffectiveMaxVersions(override, s.platformMaxVersions)
}

// prune deletes every version of an asset older than the newest keep, returning
// the rows removed. keep <= 0 means unlimited and prunes nothing, and an asset
// whose history has not reached the cap issues no statement at all -- which is
// the common case on every write, since most assets never approach 100 versions.
//
// The cutoff is computed from the version just assigned rather than from a
// MAX(version) subquery: the insert above made it the highest, and it is already
// in hand.
//
// The join onto portal_assets excludes the key the asset row points at. Version
// 1 of an asset created before it had a second version carries the flat content
// key the asset itself still names, so pruning it by row would take the live
// content with it; every later version has a key of its own and is unaffected by
// the guard.
func prune(ctx context.Context, tx *sql.Tx, assetID string, latestVersion, keep int) (pruneResult, error) {
	if keep <= portaldomain.MaxVersionsUnlimited || latestVersion <= keep {
		return pruneResult{}, nil
	}
	rows, err := tx.QueryContext(ctx, `
		DELETE FROM portal_asset_versions v
		 USING portal_assets a
		 WHERE v.asset_id = $1
		   AND a.id = v.asset_id
		   AND v.s3_key <> a.s3_key
		   AND v.version <= $2
		RETURNING v.version, v.s3_bucket, v.s3_key`, assetID, latestVersion-keep)
	if err != nil {
		return pruneResult{}, fmt.Errorf("pruning asset versions: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var removed []portaldomain.AssetVersion
	for rows.Next() {
		var p portaldomain.AssetVersion
		if err := rows.Scan(&p.Version, &p.S3Bucket, &p.S3Key); err != nil {
			return pruneResult{}, fmt.Errorf("scanning pruned version row: %w", err)
		}
		removed = append(removed, p)
	}
	if err := rows.Err(); err != nil {
		return pruneResult{}, fmt.Errorf("iterating pruned version rows: %w", err)
	}
	return pruneResult{removed: removed}, nil
}

// survivingObjectKeys returns every object key the asset still owns: the
// content of the versions that outlived the prune, the thumbnails derived
// beside them, and the thumbnails the asset row itself points at.
//
// A version's key is not private to it. A managed script keys its output by run
// (internal/platform/scriptexec/export.go), so a run that exports a document and
// then publishes data into the same asset writes two version rows at one key,
// and every version of such an asset shares one directory -- which is also where
// its thumbnails are derived. Deleting a pruned version's objects by key shape
// alone would take the live content or the current thumbnail with them. The
// cleanup asks what is still referenced instead.
//
// The asset row is read for the same reason and a sharper one: since #1431 a
// version write leaves the thumbnail pointers alone, so the image an asset is
// serving can be one captured beside a version this prune just deleted. Its key
// is derivable from no surviving row, and without this it would be deleted out
// from under the pointer that still names it.
func survivingObjectKeys(ctx context.Context, tx *sql.Tx, assetID string) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT s3_key FROM portal_asset_versions WHERE asset_id = $1`, assetID)
	if err != nil {
		return nil, fmt.Errorf("reading surviving version keys: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	retained := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scanning surviving version key: %w", err)
		}
		retained[key] = struct{}{}
		for _, thumb := range portaldomain.ThumbnailKeysFor(key) {
			retained[thumb] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating surviving version keys: %w", err)
	}
	if err := addStoredThumbnailKeys(ctx, tx, assetID, retained); err != nil {
		return nil, err
	}
	return retained, nil
}

// addStoredThumbnailKeys adds the thumbnail keys the asset row points at to the
// retained set. An empty pointer records no object and adds nothing.
func addStoredThumbnailKeys(ctx context.Context, tx *sql.Tx, assetID string, retained map[string]struct{}) error {
	var light, dark string
	err := tx.QueryRowContext(ctx,
		`SELECT thumbnail_s3_key, thumbnail_dark_s3_key FROM portal_assets WHERE id = $1`, assetID).
		Scan(&light, &dark)
	if err != nil {
		return fmt.Errorf("reading stored thumbnail keys: %w", err)
	}
	for _, key := range []string{light, dark} {
		if key != "" {
			retained[key] = struct{}{}
		}
	}
	return nil
}

// deletePrunedObjects removes the content object of every pruned version and
// the thumbnails that sat beside it, skipping every key a surviving version
// still owns. It is best-effort on purpose: the version the caller asked for has
// already committed, and failing that write because an old object could not be
// removed would turn a storage-cleanup problem into a failed save. An object
// left behind is logged so it can be reclaimed.
//
// A pruned version's thumbnails are derived rather than looked up because no
// version row records one: a capture is written beside the content key that was
// current when it was taken, and only the asset row names it. Deriving is safe
// only because the retained set is consulted -- it holds the keys the asset row
// still points at, and on an asset whose versions share a directory a pruned
// version's derived thumbnail key is the current version's thumbnail key.
func (s *store) deletePrunedObjects(ctx context.Context, assetID string, pruned pruneResult) {
	if s.objects == nil {
		return
	}
	for _, p := range pruned.removed {
		for _, key := range p.ObjectKeys() {
			if _, stillOwned := pruned.retained[key]; stillOwned {
				continue
			}
			if err := s.objects.DeleteObject(ctx, p.S3Bucket, key); err != nil {
				slog.Warn("portal versions: pruned object not deleted",
					"asset_id", assetID, // #nosec G706 -- server-generated ID
					"version", p.Version, "error", err)
			}
		}
	}
}

func (s *store) ListByAsset(ctx context.Context, assetID string, limit, offset int) ([]portaldomain.AssetVersion, int, error) { //nolint:revive // interface impl
	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM portal_asset_versions WHERE asset_id = $1", assetID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting versions: %w", err)
	}

	if limit <= 0 {
		limit = portaldomain.DefaultLimit
	}
	if limit > portaldomain.MaxLimit {
		limit = portaldomain.MaxLimit
	}

	query := `
		SELECT id, asset_id, version, s3_key, s3_bucket, content_type, size_bytes,
		       created_by, change_summary, created_at
		FROM portal_asset_versions
		WHERE asset_id = $1
		ORDER BY version DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := s.db.QueryContext(ctx, query, assetID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("querying versions: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var versions []portaldomain.AssetVersion
	for rows.Next() {
		v, scanErr := scanVersionRow(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating version rows: %w", err)
	}

	return versions, total, nil
}

func (s *store) GetByVersion(ctx context.Context, assetID string, version int) (*portaldomain.AssetVersion, error) { //nolint:revive // interface impl
	query := `
		SELECT id, asset_id, version, s3_key, s3_bucket, content_type, size_bytes,
		       created_by, change_summary, created_at
		FROM portal_asset_versions
		WHERE asset_id = $1 AND version = $2
	`
	var v portaldomain.AssetVersion
	err := s.db.QueryRowContext(ctx, query, assetID, version).Scan(
		&v.ID, &v.AssetID, &v.Version, &v.S3Key, &v.S3Bucket,
		&v.ContentType, &v.SizeBytes, &v.CreatedBy, &v.ChangeSummary, &v.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("querying version: %w", err)
	}
	return &v, nil
}

func (s *store) GetLatest(ctx context.Context, assetID string) (*portaldomain.AssetVersion, error) { //nolint:revive // interface impl
	query := `
		SELECT id, asset_id, version, s3_key, s3_bucket, content_type, size_bytes,
		       created_by, change_summary, created_at
		FROM portal_asset_versions
		WHERE asset_id = $1
		ORDER BY version DESC
		LIMIT 1
	`
	var v portaldomain.AssetVersion
	err := s.db.QueryRowContext(ctx, query, assetID).Scan(
		&v.ID, &v.AssetID, &v.Version, &v.S3Key, &v.S3Bucket,
		&v.ContentType, &v.SizeBytes, &v.CreatedBy, &v.ChangeSummary, &v.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("querying latest version: %w", err)
	}
	return &v, nil
}

func scanVersionRow(rows *sql.Rows) (portaldomain.AssetVersion, error) {
	var v portaldomain.AssetVersion
	if err := rows.Scan(
		&v.ID, &v.AssetID, &v.Version, &v.S3Key, &v.S3Bucket,
		&v.ContentType, &v.SizeBytes, &v.CreatedBy, &v.ChangeSummary, &v.CreatedAt,
	); err != nil {
		return v, fmt.Errorf("scanning version row: %w", err)
	}
	return v, nil
}

// Verify interface compliance.
var _ portaldomain.VersionStore = (*store)(nil)
