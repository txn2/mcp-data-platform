// Package portalstore holds the portal's PostgreSQL stores: assets, shares and
// collections, together with the ranked-search queries over them.
//
// The three sit together because they share one statement builder, one set of
// column constants and one family of row scanners; asset version history has
// none of that in common and lives in internal/portal/portalversions, and the
// no-database implementations of the same contracts in internal/portal/portalnoop.
package portalstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
)

// psq is the PostgreSQL statement builder with dollar placeholders.
var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// SQL column-name constants shared by the store's query builders. Reusing one
// constant per column avoids duplicate literals (goconst).
const (
	colOwnerID     = "owner_id"
	colOwnerEmail  = "owner_email"
	colName        = "name"
	colDescription = "description"
	colContentType = "content_type"

	// Target columns on portal_shares, interpolated into the aggregate the
	// asset and collection summary queries share.
	colAssetID      = "asset_id"
	colCollectionID = "collection_id"
)

// nullString maps an empty string to SQL NULL, which is how the share table
// records "this share does not target that kind of object" and "no recipient".
func nullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: v != ""}
}

// --- PostgreSQL AssetStore ---

type postgresAssetStore struct {
	db *sql.DB
	// index receives a write-path index-job enqueue after an asset write
	// commits, so a saved or renamed asset enters ranked search in roughly
	// the time one embed takes rather than on the reconciler's next sweep
	// (#1256). Nil when no queue is wired; every call on it is nil-safe.
	index *indexjobs.Producer
}

// NewPostgresAssetStore creates a new PostgreSQL asset store. Pass
// indexjobs.WithProducer to have asset writes enqueue their own index job;
// without it, assets are indexed on the reconciler's next sweep.
func NewPostgresAssetStore(db *sql.DB, opts ...indexjobs.StoreOption) portaldomain.AssetStore {
	return &postgresAssetStore{db: db, index: indexjobs.ResolveStoreOptions(opts).Producer}
}

func (s *postgresAssetStore) Insert(ctx context.Context, asset portaldomain.Asset) error { //nolint:revive // interface impl
	// Normalize nil to empty slice so the column never holds JSON
	// `null`. A nil tags slice serialized as JSON null is what blanked
	// the portal: the React asset list does `asset.tags.slice(...)`
	// and one null row kills the entire render tree.
	if asset.Tags == nil {
		asset.Tags = []string{}
	}
	tags, err := json.Marshal(asset.Tags)
	if err != nil {
		return fmt.Errorf("marshaling tags: %w", err)
	}
	prov, err := json.Marshal(asset.Provenance)
	if err != nil {
		return fmt.Errorf("marshaling provenance: %w", err)
	}

	// Zero is the correct initial value — CreateVersion increments it to 1.
	currentVersion := asset.CurrentVersion

	// Use NULL for empty idempotency keys so the partial unique index works correctly.
	var idempotencyKey *string
	if asset.IdempotencyKey != "" {
		idempotencyKey = &asset.IdempotencyKey
	}

	query := `
		INSERT INTO portal_assets
		(id, owner_id, owner_email, name, description, content_type, s3_bucket, s3_key, size_bytes, tags, provenance, session_id, current_version, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`
	_, err = s.db.ExecContext(ctx, query,
		asset.ID, asset.OwnerID, asset.OwnerEmail, asset.Name, asset.Description,
		asset.ContentType, asset.S3Bucket, asset.S3Key, asset.SizeBytes,
		tags, prov, asset.SessionID, currentVersion, idempotencyKey,
	)
	if err != nil {
		return fmt.Errorf("inserting asset: %w", err)
	}
	// After the write, never before: a job claimed while the row still holds its
	// pre-write text would have the worker stamp that snapshot as current.
	s.index.NotifyWrite(ctx, asset.ID)
	return nil
}

func (s *postgresAssetStore) Get(ctx context.Context, id string) (*portaldomain.Asset, error) { //nolint:revive // interface impl
	query := `
		SELECT id, owner_id, owner_email, name, description, content_type, s3_bucket, s3_key,
		       thumbnail_s3_key, thumbnail_dark_s3_key, size_bytes, tags, provenance, session_id, current_version,
		       created_at, updated_at, deleted_at, COALESCE(idempotency_key, '')
		FROM portal_assets WHERE id = $1
	`
	var asset portaldomain.Asset
	var tags, prov []byte
	var deletedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&asset.ID, &asset.OwnerID, &asset.OwnerEmail, &asset.Name, &asset.Description,
		&asset.ContentType, &asset.S3Bucket, &asset.S3Key, &asset.ThumbnailS3Key, &asset.ThumbnailDarkS3Key, &asset.SizeBytes,
		&tags, &prov, &asset.SessionID, &asset.CurrentVersion, &asset.CreatedAt, &asset.UpdatedAt, &deletedAt,
		&asset.IdempotencyKey,
	)
	if err != nil {
		return nil, fmt.Errorf("querying asset: %w", err)
	}

	if deletedAt.Valid {
		asset.DeletedAt = &deletedAt.Time
	}

	if err := unmarshalAssetJSON(&asset, tags, prov); err != nil {
		return nil, err
	}

	return &asset, nil
}

func (s *postgresAssetStore) GetByIdempotencyKey(ctx context.Context, ownerID, key string) (*portaldomain.Asset, error) { //nolint:revive // interface impl
	query := `
		SELECT id, owner_id, owner_email, name, description, content_type, s3_bucket, s3_key,
		       thumbnail_s3_key, thumbnail_dark_s3_key, size_bytes, tags, provenance, session_id, current_version,
		       created_at, updated_at, deleted_at, COALESCE(idempotency_key, '')
		FROM portal_assets
		WHERE owner_id = $1 AND idempotency_key = $2 AND deleted_at IS NULL
	`
	var asset portaldomain.Asset
	var tags, prov []byte
	var deletedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, ownerID, key).Scan(
		&asset.ID, &asset.OwnerID, &asset.OwnerEmail, &asset.Name, &asset.Description,
		&asset.ContentType, &asset.S3Bucket, &asset.S3Key, &asset.ThumbnailS3Key, &asset.ThumbnailDarkS3Key, &asset.SizeBytes,
		&tags, &prov, &asset.SessionID, &asset.CurrentVersion, &asset.CreatedAt, &asset.UpdatedAt, &deletedAt,
		&asset.IdempotencyKey,
	)
	if err != nil {
		return nil, fmt.Errorf("querying asset by idempotency key: %w", err)
	}

	if deletedAt.Valid {
		asset.DeletedAt = &deletedAt.Time
	}

	if err := unmarshalAssetJSON(&asset, tags, prov); err != nil {
		return nil, err
	}

	return &asset, nil
}

func (s *postgresAssetStore) GetByIDs(ctx context.Context, ids []string) (map[string]*portaldomain.Asset, error) { //nolint:revive // interface impl
	if len(ids) == 0 {
		return map[string]*portaldomain.Asset{}, nil
	}

	query := `
		SELECT id, owner_id, owner_email, name, description, content_type, s3_bucket, s3_key,
		       thumbnail_s3_key, thumbnail_dark_s3_key, size_bytes, tags, provenance, session_id, current_version,
		       created_at, updated_at, deleted_at, COALESCE(idempotency_key, '')
		FROM portal_assets WHERE id = ANY($1) AND deleted_at IS NULL
	`
	rows, err := s.db.QueryContext(ctx, query, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("querying assets by IDs: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	result := make(map[string]*portaldomain.Asset, len(ids))
	for rows.Next() {
		var asset portaldomain.Asset
		var tags, prov []byte
		var deletedAt sql.NullTime

		if err := rows.Scan(
			&asset.ID, &asset.OwnerID, &asset.OwnerEmail, &asset.Name, &asset.Description,
			&asset.ContentType, &asset.S3Bucket, &asset.S3Key, &asset.ThumbnailS3Key, &asset.ThumbnailDarkS3Key, &asset.SizeBytes,
			&tags, &prov, &asset.SessionID, &asset.CurrentVersion, &asset.CreatedAt, &asset.UpdatedAt, &deletedAt,
			&asset.IdempotencyKey,
		); err != nil {
			return nil, fmt.Errorf("scanning asset row: %w", err)
		}

		if deletedAt.Valid {
			asset.DeletedAt = &deletedAt.Time
		}
		if err := unmarshalAssetJSON(&asset, tags, prov); err != nil {
			return nil, err
		}

		a := asset
		result[a.ID] = &a
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating asset rows: %w", err)
	}

	return result, nil
}

func (s *postgresAssetStore) List(ctx context.Context, filter portaldomain.AssetFilter) ([]portaldomain.Asset, int, error) { //nolint:revive // interface impl
	total, err := s.countAssets(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	assets, err := s.queryAssets(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	// Populate collection associations for each asset.
	if err := s.populateCollections(ctx, assets); err != nil {
		return nil, 0, fmt.Errorf("populating collections: %w", err)
	}

	return assets, total, nil
}

func (s *postgresAssetStore) countAssets(ctx context.Context, filter portaldomain.AssetFilter) (int, error) {
	countQB := applyAssetFilter(psq.Select("COUNT(*)").From("portal_assets"), filter)
	countQB = countQB.Where("deleted_at IS NULL")
	countQuery, countArgs, err := countQB.ToSql()
	if err != nil {
		return 0, fmt.Errorf("building count query: %w", err)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return 0, fmt.Errorf("counting assets: %w", err)
	}
	return total, nil
}

func (s *postgresAssetStore) queryAssets(ctx context.Context, filter portaldomain.AssetFilter) ([]portaldomain.Asset, error) {
	limit := filter.EffectiveLimit()
	selectQB := applyAssetFilter(psq.Select(
		"id", "owner_id", "owner_email", "name", "description", "content_type", "s3_bucket", "s3_key",
		"thumbnail_s3_key", "thumbnail_dark_s3_key", "size_bytes", "tags", "provenance", "session_id", "current_version",
		"created_at", "updated_at", "deleted_at", "COALESCE(idempotency_key, '')",
	).From("portal_assets"), filter).
		Where("deleted_at IS NULL").
		OrderBy("created_at DESC")

	if limit > 0 {
		selectQB = selectQB.Limit(uint64(limit)) //nolint:gosec // validated positive
	}
	if filter.Offset > 0 {
		selectQB = selectQB.Offset(uint64(filter.Offset)) //nolint:gosec // validated positive
	}

	selectQuery, selectArgs, err := selectQB.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building select query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, selectQuery, selectArgs...) //nolint:gosec // builder-generated query
	if err != nil {
		return nil, fmt.Errorf("querying assets: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var assets []portaldomain.Asset
	for rows.Next() {
		asset, scanErr := scanAssetRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating asset rows: %w", err)
	}

	return assets, nil
}

// populateCollections fetches collection associations for a batch of assets in one query.
func (s *postgresAssetStore) populateCollections(ctx context.Context, assets []portaldomain.Asset) error {
	if len(assets) == 0 {
		return nil
	}

	ids := make([]string, len(assets))
	for i, a := range assets {
		ids[i] = a.ID
	}

	query := `
		SELECT ci.asset_id, pc.id, pc.name
		FROM portal_collection_items ci
		JOIN portal_collection_sections cs ON cs.id = ci.section_id
		JOIN portal_collections pc ON pc.id = cs.collection_id AND pc.deleted_at IS NULL
		WHERE ci.asset_id = ANY($1)
		ORDER BY pc.name
	`
	collRows, err := s.db.QueryContext(ctx, query, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("querying asset collections: %w", err)
	}
	defer collRows.Close() //nolint:errcheck // best-effort cleanup

	collMap := make(map[string][]portaldomain.AssetCollectionRef)
	for collRows.Next() {
		var assetID, collID, collName string
		if err := collRows.Scan(&assetID, &collID, &collName); err != nil {
			return fmt.Errorf("scanning collection ref: %w", err)
		}
		collMap[assetID] = append(collMap[assetID], portaldomain.AssetCollectionRef{ID: collID, Name: collName})
	}
	if err := collRows.Err(); err != nil {
		return fmt.Errorf("iterating collection refs: %w", err)
	}

	// Deduplicate (an asset can appear in multiple sections of the same collection)
	for i := range assets {
		refs := collMap[assets[i].ID]
		seen := make(map[string]bool)
		var deduped []portaldomain.AssetCollectionRef
		for _, ref := range refs {
			if !seen[ref.ID] {
				seen[ref.ID] = true
				deduped = append(deduped, ref)
			}
		}
		assets[i].Collections = deduped
	}
	return nil
}

func (s *postgresAssetStore) Update(ctx context.Context, id string, updates portaldomain.AssetUpdate) error { //nolint:revive // interface impl
	qb, err := applyUpdateFields(psq.Update("portal_assets"), updates)
	if err != nil {
		return err
	}

	qb = qb.Set("updated_at", time.Now()).Where(sq.Eq{"id": id}).Where("deleted_at IS NULL")

	query, args, err := qb.ToSql()
	if err != nil {
		return fmt.Errorf("building update query: %w", err)
	}

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating asset: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("asset not found or deleted: %s", id)
	}

	if assetIndexTextChanged(updates) {
		s.index.NotifyWrite(ctx, id)
	}
	return nil
}

// applyScalarUpdates sets the non-indexed scalar columns (content type, storage
// keys, size, and the light/dark thumbnail keys) that do not affect the search
// embedding. Returns the builder and whether any column was set.
func applyScalarUpdates(qb sq.UpdateBuilder, updates portaldomain.AssetUpdate) (sq.UpdateBuilder, bool) {
	changed := false
	if updates.ContentType != "" {
		qb = qb.Set("content_type", updates.ContentType)
		changed = true
	}
	if updates.S3Key != "" {
		qb = qb.Set("s3_key", updates.S3Key)
		changed = true
	}
	if updates.HasContent {
		qb = qb.Set("size_bytes", updates.SizeBytes)
		changed = true
	}
	if updates.ThumbnailS3Key != nil {
		qb = qb.Set("thumbnail_s3_key", *updates.ThumbnailS3Key)
		changed = true
	}
	if updates.ThumbnailDarkS3Key != nil {
		qb = qb.Set("thumbnail_dark_s3_key", *updates.ThumbnailDarkS3Key)
		changed = true
	}
	return qb, changed
}

// assetIndexTextChanged reports whether an update touches a field
// portal.AssetIndexText composes, which is both the signal to drop the stored
// vector and the signal to enqueue the row's re-embed. One definition so the
// clear and the enqueue cannot disagree about what "the indexed text moved"
// means.
func assetIndexTextChanged(updates portaldomain.AssetUpdate) bool {
	return updates.Name != nil || updates.Description != nil || updates.Tags != nil
}

func applyUpdateFields(qb sq.UpdateBuilder, updates portaldomain.AssetUpdate) (sq.UpdateBuilder, error) {
	hasUpdates := false
	if updates.Name != nil {
		qb = qb.Set("name", *updates.Name)
		hasUpdates = true
	}
	if updates.Description != nil {
		qb = qb.Set("description", *updates.Description)
		hasUpdates = true
	}
	if updates.Tags != nil {
		tags, err := json.Marshal(updates.Tags)
		if err != nil {
			return qb, fmt.Errorf("marshaling tags: %w", err)
		}
		qb = qb.Set("tags", tags)
		hasUpdates = true
	}
	var scalarChanged bool
	qb, scalarChanged = applyScalarUpdates(qb, updates)
	if scalarChanged {
		hasUpdates = true
	}
	if !hasUpdates {
		return qb, errors.New("no fields to update")
	}
	// When an indexed field (name, description, tags) changes, drop the
	// embedding; the write path then enqueues the row's own index job, so the
	// re-embed happens off the request path but within one embed rather than on
	// the reconciler's next sweep. A content-only or thumbnail edit leaves the
	// vector intact.
	// The embedding columns are added by migration 000063, which (like all
	// migrations) runs at startup before any request is served, so they always
	// exist when this code path executes.
	if assetIndexTextChanged(updates) {
		// Use literal NULL (not a bound nil parameter) for the vector and hash
		// columns so the clear matches the collection and prompt stores and never
		// relies on the driver inferring a parameter type for the vector column.
		qb = qb.Set("embedding", sq.Expr("NULL")).
			Set("embedding_model", "").
			Set("embedding_text_hash", sq.Expr("NULL"))
	}
	return qb, nil
}

func (s *postgresAssetStore) SoftDelete(ctx context.Context, id string) error { //nolint:revive // interface impl
	query := `UPDATE portal_assets SET deleted_at = $1 WHERE id = $2 AND deleted_at IS NULL`
	result, err := s.db.ExecContext(ctx, query, time.Now(), id)
	if err != nil {
		return fmt.Errorf("soft-deleting asset: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("asset not found or already deleted: %s", id)
	}

	return nil
}

// --- PostgreSQL ShareStore ---

type postgresShareStore struct {
	db *sql.DB
}

// NewPostgresShareStore creates a new PostgreSQL share store.
func NewPostgresShareStore(db *sql.DB) portaldomain.ShareStore {
	return &postgresShareStore{db: db}
}

func (s *postgresShareStore) Insert(ctx context.Context, share portaldomain.Share) error { //nolint:revive // interface impl
	query := `
		INSERT INTO portal_shares
		(id, asset_id, collection_id, prompt_id, token, created_by, expires_at, shared_with_user_id, shared_with_email, hide_expiration, notice_text, permission, origin, access_mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`

	assetID := nullString(share.AssetID)
	collectionID := nullString(share.CollectionID)
	promptID := nullString(share.PromptID)
	sharedWith := nullString(share.SharedWithUserID)
	sharedEmail := nullString(share.SharedWithEmail)

	var expiresAt sql.NullTime
	if share.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *share.ExpiresAt, Valid: true}
	}

	perm := share.Permission
	if perm == "" {
		perm = portaldomain.PermissionViewer
	}

	origin := share.Origin
	if origin == "" {
		origin = portaldomain.OriginExplicit
	}

	// A share with no explicit mode falls back to the safest one its shape
	// supports: restricted when it names a recipient, authenticated otherwise.
	// Anonymous access is never implied (#999).
	mode := share.AccessMode
	if mode == "" {
		mode = shareaccess.Default(share.SharedWithUserID != "" || share.SharedWithEmail != "")
	}

	_, err := s.db.ExecContext(ctx, query,
		share.ID, assetID, collectionID, promptID, share.Token, share.CreatedBy, expiresAt, sharedWith, sharedEmail, share.HideExpiration, share.NoticeText, string(perm), string(origin), string(mode),
	)
	if err != nil {
		return fmt.Errorf("inserting share: %w", err)
	}
	return nil
}

func (s *postgresShareStore) GetByID(ctx context.Context, id string) (*portaldomain.Share, error) { //nolint:revive // interface impl
	query := `
		SELECT id, asset_id, collection_id, prompt_id, token, created_by, shared_with_user_id, shared_with_email,
		       expires_at, revoked, hide_expiration, notice_text, access_count, last_accessed_at, created_at, permission, origin, access_mode
		FROM portal_shares WHERE id = $1
	`
	return s.scanShare(ctx, query, id)
}

func (s *postgresShareStore) GetByToken(ctx context.Context, token string) (*portaldomain.Share, error) { //nolint:revive // interface impl
	query := `
		SELECT id, asset_id, collection_id, prompt_id, token, created_by, shared_with_user_id, shared_with_email,
		       expires_at, revoked, hide_expiration, notice_text, access_count, last_accessed_at, created_at, permission, origin, access_mode
		FROM portal_shares WHERE token = $1
	`
	return s.scanShare(ctx, query, token)
}

func (s *postgresShareStore) ListByAsset(ctx context.Context, assetID string) ([]portaldomain.Share, error) { //nolint:revive // interface impl
	query := `
		SELECT id, asset_id, collection_id, prompt_id, token, created_by, shared_with_user_id, shared_with_email,
		       expires_at, revoked, hide_expiration, notice_text, access_count, last_accessed_at, created_at, permission, origin, access_mode
		FROM portal_shares WHERE asset_id = $1 ORDER BY created_at DESC
	`
	return s.listShares(ctx, query, assetID)
}

func (s *postgresShareStore) ListByCollection(ctx context.Context, collectionID string) ([]portaldomain.Share, error) { //nolint:revive // interface impl
	query := `
		SELECT id, asset_id, collection_id, prompt_id, token, created_by, shared_with_user_id, shared_with_email,
		       expires_at, revoked, hide_expiration, notice_text, access_count, last_accessed_at, created_at, permission, origin, access_mode
		FROM portal_shares WHERE collection_id = $1 ORDER BY created_at DESC
	`
	return s.listShares(ctx, query, collectionID)
}

func (s *postgresShareStore) ListByPrompt(ctx context.Context, promptID string) ([]portaldomain.Share, error) { //nolint:revive // interface impl
	query := `
		SELECT id, asset_id, collection_id, prompt_id, token, created_by, shared_with_user_id, shared_with_email,
		       expires_at, revoked, hide_expiration, notice_text, access_count, last_accessed_at, created_at, permission, origin, access_mode
		FROM portal_shares WHERE prompt_id = $1 ORDER BY created_at DESC
	`
	return s.listShares(ctx, query, promptID)
}

// ListSharedPromptsWithUser returns active (non-revoked, unexpired) prompt
// shares targeting the given user id or email, most recent first. Only share
// references are returned; the prompt bodies are fetched from the prompt store.
func (s *postgresShareStore) ListSharedPromptsWithUser(ctx context.Context, userID, email string) ([]portaldomain.SharedPromptRef, error) { //nolint:revive // interface impl
	query := `
		SELECT prompt_id, id, created_by, created_at, permission
		FROM portal_shares
		WHERE prompt_id IS NOT NULL AND revoked = FALSE
		  AND (expires_at IS NULL OR expires_at > NOW())
		  AND ( ($1 <> '' AND shared_with_user_id = $1) OR ($2 <> '' AND LOWER(shared_with_email) = LOWER($2)) )
		ORDER BY created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, userID, email)
	if err != nil {
		return nil, fmt.Errorf("querying shared prompts: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var refs []portaldomain.SharedPromptRef
	for rows.Next() {
		var r portaldomain.SharedPromptRef
		if err := rows.Scan(&r.PromptID, &r.ShareID, &r.SharedBy, &r.SharedAt, &r.Permission); err != nil {
			return nil, fmt.Errorf("scanning shared prompt: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating shared prompt rows: %w", err)
	}
	return refs, nil
}

func (s *postgresShareStore) GetUserCollectionPermission(ctx context.Context, collectionID, userID, email string) (portaldomain.SharePermission, error) { //nolint:revive // interface impl
	query := `
		SELECT permission FROM portal_shares
		WHERE collection_id = $1
		  AND revoked = FALSE
		  AND (expires_at IS NULL OR expires_at > NOW())
		  AND (shared_with_user_id = $2 OR ($3 != '' AND LOWER(shared_with_email) = LOWER($3)))
		ORDER BY CASE permission WHEN 'editor' THEN 0 ELSE 1 END
		LIMIT 1
	`
	var perm string
	err := s.db.QueryRowContext(ctx, query, collectionID, userID, email).Scan(&perm)
	if err != nil {
		return "", fmt.Errorf("querying user collection permission: %w", err)
	}
	return portaldomain.SharePermission(perm), nil
}

// GetActiveShareForTarget returns the caller's most-permissive active
// (non-revoked, unexpired) share for the given asset or collection target,
// or nil if none exists. Used by the public-link auto-promote path to decide
// whether to derive a viewer share — an existing editor must never be
// downgraded, so editor shares sort first. targetType must be "asset" or
// "collection"; any other value yields nil.
func (s *postgresShareStore) GetActiveShareForTarget(ctx context.Context, targetType, targetID, userID, email string) (*portaldomain.Share, error) { //nolint:revive // interface impl
	var column string
	switch targetType {
	case portaldomain.TargetTypeAsset:
		column = "asset_id"
	case portaldomain.TargetTypeCollection:
		column = "collection_id"
	default:
		return nil, nil //nolint:nilnil // unsupported target type → no share
	}

	query := fmt.Sprintf(`
		SELECT id, asset_id, collection_id, prompt_id, token, created_by, shared_with_user_id, shared_with_email,
		       expires_at, revoked, hide_expiration, notice_text, access_count, last_accessed_at, created_at, permission, origin, access_mode
		FROM portal_shares
		WHERE %s = $1
		  AND revoked = FALSE
		  AND (expires_at IS NULL OR expires_at > NOW())
		  AND (($2 <> '' AND shared_with_user_id = $2) OR ($3 <> '' AND LOWER(shared_with_email) = LOWER($3)))
		ORDER BY CASE permission WHEN 'editor' THEN 0 ELSE 1 END, created_at DESC
		LIMIT 1
	`, column)

	share, err := s.scanShare(ctx, query, targetID, userID, email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // no active share for this user/target
	}
	if err != nil {
		return nil, err
	}
	return share, nil
}

func (s *postgresShareStore) listShares(ctx context.Context, query, id string) ([]portaldomain.Share, error) {
	rows, err := s.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("querying shares: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var shares []portaldomain.Share
	for rows.Next() {
		share, scanErr := scanShareRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		shares = append(shares, share)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating share rows: %w", err)
	}

	return shares, nil
}

func (s *postgresShareStore) ListSharedWithUser(ctx context.Context, userID, email string, limit, offset int) ([]portaldomain.SharedAsset, int, error) { //nolint:revive // interface impl
	countQuery := `
		SELECT COUNT(*)
		FROM portal_shares ps
		JOIN portal_assets pa ON ps.asset_id = pa.id
		WHERE (ps.shared_with_user_id = $1 OR ($2 != '' AND LOWER(ps.shared_with_email) = LOWER($2)))
		  AND ps.revoked = FALSE AND pa.deleted_at IS NULL
		  AND (ps.expires_at IS NULL OR ps.expires_at > NOW())
	`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, userID, email).Scan(&total); err != nil { //nolint:gosec // query is a constant with parameterized placeholders
		return nil, 0, fmt.Errorf("counting shared assets: %w", err)
	}

	if limit <= 0 {
		limit = portaldomain.DefaultLimit
	}
	if limit > portaldomain.MaxLimit {
		limit = portaldomain.MaxLimit
	}

	selectQuery := `
		SELECT pa.id, pa.owner_id, pa.owner_email, pa.name, pa.description, pa.content_type,
		       pa.s3_bucket, pa.s3_key, pa.thumbnail_s3_key, pa.thumbnail_dark_s3_key, pa.size_bytes, pa.tags, pa.provenance,
		       pa.session_id, pa.current_version, pa.created_at, pa.updated_at, pa.deleted_at,
		       COALESCE(pa.idempotency_key, ''),
		       ps.id, COALESCE(NULLIF(pa.owner_email, ''), ps.created_by), ps.created_at, ps.permission
		FROM portal_shares ps
		JOIN portal_assets pa ON ps.asset_id = pa.id
		WHERE (ps.shared_with_user_id = $1 OR ($2 != '' AND LOWER(ps.shared_with_email) = LOWER($2)))
		  AND ps.revoked = FALSE AND pa.deleted_at IS NULL
		  AND (ps.expires_at IS NULL OR ps.expires_at > NOW())
		ORDER BY ps.created_at DESC
		LIMIT $3 OFFSET $4
	`

	rows, err := s.db.QueryContext(ctx, selectQuery, userID, email, limit, offset) //nolint:gosec // query is a constant with parameterized placeholders
	if err != nil {
		return nil, 0, fmt.Errorf("querying shared assets: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var results []portaldomain.SharedAsset
	for rows.Next() {
		var sa portaldomain.SharedAsset
		var tags, prov []byte
		var deletedAt sql.NullTime

		if err := rows.Scan(
			&sa.Asset.ID, &sa.Asset.OwnerID, &sa.Asset.OwnerEmail, &sa.Asset.Name, &sa.Asset.Description,
			&sa.Asset.ContentType, &sa.Asset.S3Bucket, &sa.Asset.S3Key, &sa.Asset.ThumbnailS3Key, &sa.Asset.ThumbnailDarkS3Key, &sa.Asset.SizeBytes,
			&tags, &prov, &sa.Asset.SessionID, &sa.Asset.CurrentVersion,
			&sa.Asset.CreatedAt, &sa.Asset.UpdatedAt, &deletedAt, &sa.Asset.IdempotencyKey,
			&sa.ShareID, &sa.SharedBy, &sa.SharedAt, &sa.Permission,
		); err != nil {
			return nil, 0, fmt.Errorf("scanning shared asset row: %w", err)
		}

		if deletedAt.Valid {
			sa.Asset.DeletedAt = &deletedAt.Time
		}
		if err := unmarshalAssetJSON(&sa.Asset, tags, prov); err != nil {
			return nil, 0, err
		}
		results = append(results, sa)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating shared asset rows: %w", err)
	}

	return results, total, nil
}

func (s *postgresShareStore) ListActiveShareSummaries(ctx context.Context, assetIDs []string) (map[string]portaldomain.ShareSummary, error) { //nolint:revive // interface impl
	return s.shareSummariesByTarget(ctx, portaldomain.TargetTypeAsset, assetIDs)
}

func (s *postgresShareStore) ListSharedCollectionsWithUser(ctx context.Context, userID, email string, limit, offset int) ([]portaldomain.SharedCollection, int, error) { //nolint:revive // interface impl
	countQuery := `
		SELECT COUNT(*)
		FROM portal_shares ps
		JOIN portal_collections pc ON ps.collection_id = pc.id
		WHERE (ps.shared_with_user_id = $1 OR ($2 != '' AND LOWER(ps.shared_with_email) = LOWER($2)))
		  AND ps.revoked = FALSE AND pc.deleted_at IS NULL
		  AND (ps.expires_at IS NULL OR ps.expires_at > NOW())
	`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, userID, email).Scan(&total); err != nil { //nolint:gosec // constant query
		return nil, 0, fmt.Errorf("counting shared collections: %w", err)
	}

	if limit <= 0 {
		limit = portaldomain.DefaultLimit
	}
	if limit > portaldomain.MaxLimit {
		limit = portaldomain.MaxLimit
	}

	selectQuery := `
		SELECT pc.id, pc.owner_id, pc.owner_email, pc.name, pc.description,
		       pc.thumbnail_s3_key, pc.config, pc.created_at, pc.updated_at, pc.deleted_at,
		       ps.id, COALESCE(NULLIF(pc.owner_email, ''), ps.created_by), ps.created_at, ps.permission
		FROM portal_shares ps
		JOIN portal_collections pc ON ps.collection_id = pc.id
		WHERE (ps.shared_with_user_id = $1 OR ($2 != '' AND LOWER(ps.shared_with_email) = LOWER($2)))
		  AND ps.revoked = FALSE AND pc.deleted_at IS NULL
		  AND (ps.expires_at IS NULL OR ps.expires_at > NOW())
		ORDER BY ps.created_at DESC
		LIMIT $3 OFFSET $4
	`

	rows, err := s.db.QueryContext(ctx, selectQuery, userID, email, limit, offset) //nolint:gosec // constant query
	if err != nil {
		return nil, 0, fmt.Errorf("querying shared collections: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var results []portaldomain.SharedCollection
	for rows.Next() {
		var sc portaldomain.SharedCollection
		var deletedAt sql.NullTime
		var configJSON []byte

		if err := rows.Scan(
			&sc.Collection.ID, &sc.Collection.OwnerID, &sc.Collection.OwnerEmail,
			&sc.Collection.Name, &sc.Collection.Description, &sc.Collection.ThumbnailS3Key, &configJSON,
			&sc.Collection.CreatedAt, &sc.Collection.UpdatedAt, &deletedAt,
			&sc.ShareID, &sc.SharedBy, &sc.SharedAt, &sc.Permission,
		); err != nil {
			return nil, 0, fmt.Errorf("scanning shared collection row: %w", err)
		}

		if deletedAt.Valid {
			sc.Collection.DeletedAt = &deletedAt.Time
		}
		if len(configJSON) > 0 {
			_ = json.Unmarshal(configJSON, &sc.Collection.Config)
		}
		results = append(results, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating shared collection rows: %w", err)
	}

	return results, total, nil
}

func (s *postgresShareStore) ListActiveCollectionShareSummaries(ctx context.Context, collectionIDs []string) (map[string]portaldomain.ShareSummary, error) { //nolint:revive // interface impl
	return s.shareSummariesByTarget(ctx, portaldomain.TargetTypeCollection, collectionIDs)
}

// shareSummariesByTarget reports, per target id, whether a live share names a
// recipient and whether one is an anonymous public link. Assets and collections
// differ only in the target column, so both go through here rather than through
// two copies of the same aggregate.
//
// targetType selects the column from this package's own constants; nothing a
// caller supplies reaches the query text.
func (s *postgresShareStore) shareSummariesByTarget(ctx context.Context, targetType string, ids []string) (map[string]portaldomain.ShareSummary, error) {
	if len(ids) == 0 {
		return map[string]portaldomain.ShareSummary{}, nil
	}

	column := colAssetID
	if targetType == portaldomain.TargetTypeCollection {
		column = colCollectionID
	}

	query := fmt.Sprintf(`
		SELECT %[1]s,
		       BOOL_OR(shared_with_user_id IS NOT NULL OR shared_with_email IS NOT NULL),
		       BOOL_OR(shared_with_user_id IS NULL AND shared_with_email IS NULL)
		FROM portal_shares
		WHERE %[1]s = ANY($1)
		  AND revoked = FALSE
		  AND (expires_at IS NULL OR expires_at > NOW())
		GROUP BY %[1]s
	`, column)

	rows, err := s.db.QueryContext(ctx, query, pq.Array(ids)) //nolint:gosec // column is a package constant; ids are parameterized
	if err != nil {
		return nil, fmt.Errorf("querying %s share summaries: %w", targetType, err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	result := make(map[string]portaldomain.ShareSummary)
	for rows.Next() {
		var id string
		var summary portaldomain.ShareSummary
		if err := rows.Scan(&id, &summary.HasUserShare, &summary.HasPublicLink); err != nil {
			return nil, fmt.Errorf("scanning %s share summary row: %w", targetType, err)
		}
		result[id] = summary
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating %s share summary rows: %w", targetType, err)
	}

	return result, nil
}

func (s *postgresShareStore) GetUserAssetPermissionViaCollection(ctx context.Context, assetID, userID, email string) (portaldomain.SharePermission, error) { //nolint:revive // interface impl
	query := `
		SELECT ps.permission FROM portal_shares ps
		JOIN portal_collection_sections cs ON cs.collection_id = ps.collection_id
		JOIN portal_collection_items ci ON ci.section_id = cs.id
		WHERE ci.asset_id = $1
		  AND ps.collection_id IS NOT NULL
		  AND ps.revoked = FALSE
		  AND (ps.expires_at IS NULL OR ps.expires_at > NOW())
		  AND (ps.shared_with_user_id = $2 OR ($3 != '' AND LOWER(ps.shared_with_email) = LOWER($3)))
		ORDER BY CASE ps.permission WHEN 'editor' THEN 0 ELSE 1 END
		LIMIT 1
	`
	var perm string
	err := s.db.QueryRowContext(ctx, query, assetID, userID, email).Scan(&perm)
	if err != nil {
		return "", fmt.Errorf("querying asset permission via collection: %w", err)
	}
	return portaldomain.SharePermission(perm), nil
}

func (s *postgresShareStore) Revoke(ctx context.Context, id string) error { //nolint:revive // interface impl
	query := `UPDATE portal_shares SET revoked = TRUE WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("revoking share: %w", err)
	}
	return nil
}

func (s *postgresShareStore) IncrementAccess(ctx context.Context, id string) error { //nolint:revive // interface impl
	query := `UPDATE portal_shares SET access_count = access_count + 1, last_accessed_at = $1 WHERE id = $2`
	_, err := s.db.ExecContext(ctx, query, time.Now(), id)
	if err != nil {
		return fmt.Errorf("incrementing access count: %w", err)
	}
	return nil
}

func (s *postgresShareStore) scanShare(ctx context.Context, query string, args ...any) (*portaldomain.Share, error) {
	var share portaldomain.Share
	var assetID, collectionID, promptID sql.NullString
	var expiresAt, lastAccessed sql.NullTime
	var sharedWith, sharedEmail sql.NullString

	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&share.ID, &assetID, &collectionID, &promptID, &share.Token, &share.CreatedBy,
		&sharedWith, &sharedEmail, &expiresAt, &share.Revoked,
		&share.HideExpiration, &share.NoticeText, &share.AccessCount, &lastAccessed, &share.CreatedAt, &share.Permission, &share.Origin, &share.AccessMode,
	)
	if err != nil {
		return nil, fmt.Errorf("querying share: %w", err)
	}

	if assetID.Valid {
		share.AssetID = assetID.String
	}
	if collectionID.Valid {
		share.CollectionID = collectionID.String
	}
	if promptID.Valid {
		share.PromptID = promptID.String
	}
	if sharedWith.Valid {
		share.SharedWithUserID = sharedWith.String
	}
	if sharedEmail.Valid {
		share.SharedWithEmail = sharedEmail.String
	}
	if expiresAt.Valid {
		share.ExpiresAt = &expiresAt.Time
	}
	if lastAccessed.Valid {
		share.LastAccessedAt = &lastAccessed.Time
	}

	return &share, nil
}

// --- Helpers ---

func unmarshalAssetJSON(asset *portaldomain.Asset, tags, prov []byte) error {
	if err := json.Unmarshal(tags, &asset.Tags); err != nil {
		return fmt.Errorf("unmarshaling tags: %w", err)
	}
	// Existing rows persisted from before the Insert normalization may
	// hold literal JSON `null`. Always hand a concrete slice to the
	// portal API so the JSON response is `[]`, never `null`.
	if asset.Tags == nil {
		asset.Tags = []string{}
	}
	if err := json.Unmarshal(prov, &asset.Provenance); err != nil {
		return fmt.Errorf("unmarshaling provenance: %w", err)
	}
	return nil
}

func applyAssetFilter(qb sq.SelectBuilder, filter portaldomain.AssetFilter) sq.SelectBuilder {
	if filter.OwnerID != "" {
		qb = qb.Where(sq.Eq{colOwnerID: filter.OwnerID})
	}
	if filter.ContentType != "" {
		qb = qb.Where(sq.Eq{colContentType: filter.ContentType})
	}
	if filter.Tag != "" {
		tagJSON, _ := json.Marshal([]string{filter.Tag})
		qb = qb.Where(sq.Expr("tags @> ?::jsonb", string(tagJSON)))
	}
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		qb = qb.Where(sq.Or{
			sq.ILike{colName: like},
			sq.ILike{colDescription: like},
			sq.ILike{colOwnerEmail: like},
			sq.Expr("tags::text ILIKE ?", like),
		})
	}
	return qb
}

// assetScanDest returns the scan destinations for one asset row in the column
// order shared by the list query (queryAssets) and the ranked-search queries
// (which append their score columns). It is the single definition of that order,
// so the scan cannot drift from the projection across call sites.
func assetScanDest(a *portaldomain.Asset, tags, prov *[]byte, deletedAt *sql.NullTime) []any {
	return []any{
		&a.ID, &a.OwnerID, &a.OwnerEmail, &a.Name, &a.Description,
		&a.ContentType, &a.S3Bucket, &a.S3Key, &a.ThumbnailS3Key, &a.ThumbnailDarkS3Key, &a.SizeBytes,
		tags, prov, &a.SessionID, &a.CurrentVersion, &a.CreatedAt, &a.UpdatedAt, deletedAt,
		&a.IdempotencyKey,
	}
}

// finishScannedAsset applies the nullable deleted_at and unmarshals the tags +
// provenance JSON for a freshly scanned asset. Shared by scanAssetRow and the
// ranked-search scanners.
func finishScannedAsset(asset *portaldomain.Asset, tags, prov []byte, deletedAt sql.NullTime) error {
	if deletedAt.Valid {
		asset.DeletedAt = &deletedAt.Time
	}
	return unmarshalAssetJSON(asset, tags, prov)
}

func scanAssetRow(rows *sql.Rows) (portaldomain.Asset, error) {
	var asset portaldomain.Asset
	var tags, prov []byte
	var deletedAt sql.NullTime

	if err := rows.Scan(assetScanDest(&asset, &tags, &prov, &deletedAt)...); err != nil {
		return asset, fmt.Errorf("scanning asset row: %w", err)
	}
	if err := finishScannedAsset(&asset, tags, prov, deletedAt); err != nil {
		return asset, err
	}
	return asset, nil
}

func scanShareRow(rows *sql.Rows) (portaldomain.Share, error) {
	var share portaldomain.Share
	var assetID, collectionID, promptID sql.NullString
	var expiresAt, lastAccessed sql.NullTime
	var sharedWith, sharedEmail sql.NullString

	if err := rows.Scan(
		&share.ID, &assetID, &collectionID, &promptID, &share.Token, &share.CreatedBy,
		&sharedWith, &sharedEmail, &expiresAt, &share.Revoked,
		&share.HideExpiration, &share.NoticeText, &share.AccessCount, &lastAccessed, &share.CreatedAt, &share.Permission, &share.Origin, &share.AccessMode,
	); err != nil {
		return share, fmt.Errorf("scanning share row: %w", err)
	}

	if assetID.Valid {
		share.AssetID = assetID.String
	}
	if collectionID.Valid {
		share.CollectionID = collectionID.String
	}
	if promptID.Valid {
		share.PromptID = promptID.String
	}
	if sharedWith.Valid {
		share.SharedWithUserID = sharedWith.String
	}
	if sharedEmail.Valid {
		share.SharedWithEmail = sharedEmail.String
	}
	if expiresAt.Valid {
		share.ExpiresAt = &expiresAt.Time
	}
	if lastAccessed.Valid {
		share.LastAccessedAt = &lastAccessed.Time
	}

	return share, nil
}

// Verify interface compliance.
var (
	_ portaldomain.AssetStore = (*postgresAssetStore)(nil)
	_ portaldomain.ShareStore = (*postgresShareStore)(nil)
)
