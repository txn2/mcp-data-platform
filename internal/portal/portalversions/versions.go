// Package portalversions holds the PostgreSQL store for asset version history.
//
// Version history is its own table (portal_asset_versions) with its own
// projection and scanner; it touches portal_assets only to take the row lock
// that serializes version numbering. It sits beside the asset store rather
// than inside it because it shares no query builder, column list or scanner
// with it.
package portalversions

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// --- PostgreSQL VersionStore ---

type store struct {
	db *sql.DB
}

// NewPostgres creates the PostgreSQL asset version store.
func NewPostgres(db *sql.DB) portaldomain.VersionStore {
	return &store{db: db}
}

func (s *store) CreateVersion(ctx context.Context, version portaldomain.AssetVersion) (int, error) { //nolint:revive // interface impl
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	// Lock the asset row and determine the next version number atomically.
	var currentVersion int
	lockQuery := `SELECT current_version FROM portal_assets WHERE id = $1 FOR UPDATE`
	if err := tx.QueryRowContext(ctx, lockQuery, version.AssetID).Scan(&currentVersion); err != nil {
		return 0, fmt.Errorf("locking asset row: %w", err)
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
		return 0, fmt.Errorf("inserting version: %w", err)
	}

	updateQuery := `
		UPDATE portal_assets
		SET current_version = $1, s3_key = $2, content_type = $3, size_bytes = $4, thumbnail_s3_key = '', thumbnail_dark_s3_key = '', updated_at = NOW()
		WHERE id = $5
	`
	_, err = tx.ExecContext(ctx, updateQuery,
		nextVersion, version.S3Key, version.ContentType, version.SizeBytes, version.AssetID,
	)
	if err != nil {
		return 0, fmt.Errorf("updating asset version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing version: %w", err)
	}
	return nextVersion, nil
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
