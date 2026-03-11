package portal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"
)

// psq is the PostgreSQL statement builder with dollar placeholders.
var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// AssetStore persists and queries portal assets.
type AssetStore interface {
	Insert(ctx context.Context, asset Asset) error
	Get(ctx context.Context, id string) (*Asset, error)
	List(ctx context.Context, filter AssetFilter) ([]Asset, int, error)
	Update(ctx context.Context, id string, updates AssetUpdate) error
	SoftDelete(ctx context.Context, id string) error
}

// ShareStore persists and queries share links.
type ShareStore interface {
	Insert(ctx context.Context, share Share) error
	GetByID(ctx context.Context, id string) (*Share, error)
	GetByToken(ctx context.Context, token string) (*Share, error)
	ListByAsset(ctx context.Context, assetID string) ([]Share, error)
	ListSharedWithUser(ctx context.Context, userID, email string, limit, offset int) ([]SharedAsset, int, error)
	ListActiveShareSummaries(ctx context.Context, assetIDs []string) (map[string]ShareSummary, error)
	Revoke(ctx context.Context, id string) error
	IncrementAccess(ctx context.Context, id string) error
}

// --- PostgreSQL AssetStore ---

type postgresAssetStore struct {
	db *sql.DB
}

// NewPostgresAssetStore creates a new PostgreSQL asset store.
func NewPostgresAssetStore(db *sql.DB) AssetStore {
	return &postgresAssetStore{db: db}
}

func (s *postgresAssetStore) Insert(ctx context.Context, asset Asset) error { //nolint:revive // interface impl
	tags, err := json.Marshal(asset.Tags)
	if err != nil {
		return fmt.Errorf("marshaling tags: %w", err)
	}
	prov, err := json.Marshal(asset.Provenance)
	if err != nil {
		return fmt.Errorf("marshaling provenance: %w", err)
	}

	query := `
		INSERT INTO portal_assets
		(id, owner_id, owner_email, name, description, content_type, s3_bucket, s3_key, size_bytes, tags, provenance, session_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`
	_, err = s.db.ExecContext(ctx, query,
		asset.ID, asset.OwnerID, asset.OwnerEmail, asset.Name, asset.Description,
		asset.ContentType, asset.S3Bucket, asset.S3Key, asset.SizeBytes,
		tags, prov, asset.SessionID,
	)
	if err != nil {
		return fmt.Errorf("inserting asset: %w", err)
	}
	return nil
}

func (s *postgresAssetStore) Get(ctx context.Context, id string) (*Asset, error) { //nolint:revive // interface impl
	query := `
		SELECT id, owner_id, owner_email, name, description, content_type, s3_bucket, s3_key,
		       size_bytes, tags, provenance, session_id, created_at, updated_at, deleted_at
		FROM portal_assets WHERE id = $1
	`
	var asset Asset
	var tags, prov []byte
	var deletedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&asset.ID, &asset.OwnerID, &asset.OwnerEmail, &asset.Name, &asset.Description,
		&asset.ContentType, &asset.S3Bucket, &asset.S3Key, &asset.SizeBytes,
		&tags, &prov, &asset.SessionID, &asset.CreatedAt, &asset.UpdatedAt, &deletedAt,
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

func (s *postgresAssetStore) List(ctx context.Context, filter AssetFilter) ([]Asset, int, error) { //nolint:revive // interface impl
	countQB := applyAssetFilter(psq.Select("COUNT(*)").From("portal_assets"), filter)
	countQB = countQB.Where("deleted_at IS NULL")
	countQuery, countArgs, err := countQB.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("building count query: %w", err)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting assets: %w", err)
	}

	limit := filter.EffectiveLimit()
	selectQB := applyAssetFilter(psq.Select(
		"id", "owner_id", "owner_email", "name", "description", "content_type", "s3_bucket", "s3_key",
		"size_bytes", "tags", "provenance", "session_id", "created_at", "updated_at", "deleted_at",
	).From("portal_assets"), filter).
		Where("deleted_at IS NULL").
		OrderBy("created_at DESC")

	if limit > 0 {
		selectQB = selectQB.Limit(uint64(limit))
	}
	if filter.Offset > 0 {
		selectQB = selectQB.Offset(uint64(filter.Offset))
	}

	selectQuery, selectArgs, err := selectQB.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("building select query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying assets: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var assets []Asset
	for rows.Next() {
		asset, scanErr := scanAssetRow(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating asset rows: %w", err)
	}

	return assets, total, nil
}

func (s *postgresAssetStore) Update(ctx context.Context, id string, updates AssetUpdate) error { //nolint:revive // interface impl
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

	return nil
}

func applyUpdateFields(qb sq.UpdateBuilder, updates AssetUpdate) (sq.UpdateBuilder, error) {
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
	if updates.ContentType != "" {
		qb = qb.Set("content_type", updates.ContentType)
		hasUpdates = true
	}
	if updates.S3Key != "" {
		qb = qb.Set("s3_key", updates.S3Key)
		hasUpdates = true
	}
	if updates.HasContent {
		qb = qb.Set("size_bytes", updates.SizeBytes)
		hasUpdates = true
	}
	if !hasUpdates {
		return qb, fmt.Errorf("no fields to update")
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
func NewPostgresShareStore(db *sql.DB) ShareStore {
	return &postgresShareStore{db: db}
}

func (s *postgresShareStore) Insert(ctx context.Context, share Share) error { //nolint:revive // interface impl
	query := `
		INSERT INTO portal_shares
		(id, asset_id, token, created_by, expires_at, shared_with_user_id, shared_with_email, hide_expiration)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	var expiresAt sql.NullTime
	if share.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *share.ExpiresAt, Valid: true}
	}

	var sharedWith sql.NullString
	if share.SharedWithUserID != "" {
		sharedWith = sql.NullString{String: share.SharedWithUserID, Valid: true}
	}

	var sharedEmail sql.NullString
	if share.SharedWithEmail != "" {
		sharedEmail = sql.NullString{String: share.SharedWithEmail, Valid: true}
	}

	_, err := s.db.ExecContext(ctx, query,
		share.ID, share.AssetID, share.Token, share.CreatedBy, expiresAt, sharedWith, sharedEmail, share.HideExpiration,
	)
	if err != nil {
		return fmt.Errorf("inserting share: %w", err)
	}
	return nil
}

func (s *postgresShareStore) GetByID(ctx context.Context, id string) (*Share, error) { //nolint:revive // interface impl
	query := `
		SELECT id, asset_id, token, created_by, shared_with_user_id, shared_with_email,
		       expires_at, revoked, hide_expiration, access_count, last_accessed_at, created_at
		FROM portal_shares WHERE id = $1
	`
	return s.scanShare(ctx, query, id)
}

func (s *postgresShareStore) GetByToken(ctx context.Context, token string) (*Share, error) { //nolint:revive // interface impl
	query := `
		SELECT id, asset_id, token, created_by, shared_with_user_id, shared_with_email,
		       expires_at, revoked, hide_expiration, access_count, last_accessed_at, created_at
		FROM portal_shares WHERE token = $1
	`
	return s.scanShare(ctx, query, token)
}

func (s *postgresShareStore) ListByAsset(ctx context.Context, assetID string) ([]Share, error) { //nolint:revive // interface impl
	query := `
		SELECT id, asset_id, token, created_by, shared_with_user_id, shared_with_email,
		       expires_at, revoked, hide_expiration, access_count, last_accessed_at, created_at
		FROM portal_shares WHERE asset_id = $1 ORDER BY created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, assetID)
	if err != nil {
		return nil, fmt.Errorf("querying shares: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var shares []Share
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

func (s *postgresShareStore) ListSharedWithUser(ctx context.Context, userID, email string, limit, offset int) ([]SharedAsset, int, error) { //nolint:revive // interface impl
	countQuery := `
		SELECT COUNT(*)
		FROM portal_shares ps
		JOIN portal_assets pa ON ps.asset_id = pa.id
		WHERE (ps.shared_with_user_id = $1 OR ($2 != '' AND LOWER(ps.shared_with_email) = LOWER($2)))
		  AND ps.revoked = FALSE AND pa.deleted_at IS NULL
	`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, userID, email).Scan(&total); err != nil { //nolint:gosec // query is a constant with parameterized placeholders
		return nil, 0, fmt.Errorf("counting shared assets: %w", err)
	}

	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	selectQuery := `
		SELECT pa.id, pa.owner_id, pa.owner_email, pa.name, pa.description, pa.content_type,
		       pa.s3_bucket, pa.s3_key, pa.size_bytes, pa.tags, pa.provenance,
		       pa.session_id, pa.created_at, pa.updated_at, pa.deleted_at,
		       ps.id, ps.created_by, ps.created_at
		FROM portal_shares ps
		JOIN portal_assets pa ON ps.asset_id = pa.id
		WHERE (ps.shared_with_user_id = $1 OR ($2 != '' AND LOWER(ps.shared_with_email) = LOWER($2)))
		  AND ps.revoked = FALSE AND pa.deleted_at IS NULL
		ORDER BY ps.created_at DESC
		LIMIT $3 OFFSET $4
	`

	rows, err := s.db.QueryContext(ctx, selectQuery, userID, email, limit, offset) //nolint:gosec // query is a constant with parameterized placeholders
	if err != nil {
		return nil, 0, fmt.Errorf("querying shared assets: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var results []SharedAsset
	for rows.Next() {
		var sa SharedAsset
		var tags, prov []byte
		var deletedAt sql.NullTime

		if err := rows.Scan(
			&sa.Asset.ID, &sa.Asset.OwnerID, &sa.Asset.OwnerEmail, &sa.Asset.Name, &sa.Asset.Description,
			&sa.Asset.ContentType, &sa.Asset.S3Bucket, &sa.Asset.S3Key, &sa.Asset.SizeBytes,
			&tags, &prov, &sa.Asset.SessionID,
			&sa.Asset.CreatedAt, &sa.Asset.UpdatedAt, &deletedAt,
			&sa.ShareID, &sa.SharedBy, &sa.SharedAt,
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

func (s *postgresShareStore) ListActiveShareSummaries(ctx context.Context, assetIDs []string) (map[string]ShareSummary, error) { //nolint:revive // interface impl
	if len(assetIDs) == 0 {
		return map[string]ShareSummary{}, nil
	}

	query := `
		SELECT asset_id,
		       BOOL_OR(shared_with_user_id IS NOT NULL OR shared_with_email IS NOT NULL),
		       BOOL_OR(shared_with_user_id IS NULL AND shared_with_email IS NULL)
		FROM portal_shares
		WHERE asset_id = ANY($1)
		  AND revoked = FALSE
		  AND (expires_at IS NULL OR expires_at > NOW())
		GROUP BY asset_id
	`

	rows, err := s.db.QueryContext(ctx, query, pq.Array(assetIDs)) //nolint:gosec // query is a constant with parameterized placeholders
	if err != nil {
		return nil, fmt.Errorf("querying share summaries: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	result := make(map[string]ShareSummary)
	for rows.Next() {
		var assetID string
		var summary ShareSummary
		if err := rows.Scan(&assetID, &summary.HasUserShare, &summary.HasPublicLink); err != nil {
			return nil, fmt.Errorf("scanning share summary row: %w", err)
		}
		result[assetID] = summary
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating share summary rows: %w", err)
	}

	return result, nil
}

func (s *postgresShareStore) Revoke(ctx context.Context, id string) error { //nolint:revive // interface impl
	query := `UPDATE portal_shares SET revoked = TRUE WHERE id = $1 AND revoked = FALSE`
	result, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("revoking share: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("share not found or already revoked: %s", id)
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

func (s *postgresShareStore) scanShare(ctx context.Context, query, arg string) (*Share, error) {
	var share Share
	var expiresAt, lastAccessed sql.NullTime
	var sharedWith, sharedEmail sql.NullString

	err := s.db.QueryRowContext(ctx, query, arg).Scan(
		&share.ID, &share.AssetID, &share.Token, &share.CreatedBy,
		&sharedWith, &sharedEmail, &expiresAt, &share.Revoked,
		&share.HideExpiration, &share.AccessCount, &lastAccessed, &share.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("querying share: %w", err)
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

// --- Noop AssetStore ---

type noopAssetStore struct{}

// NewNoopAssetStore creates a no-op AssetStore for use when no database is available.
func NewNoopAssetStore() AssetStore {
	return &noopAssetStore{}
}

//nolint:revive // interface implementation methods on unexported type need no doc comments
func (*noopAssetStore) Insert(_ context.Context, _ Asset) error { return nil }

func (*noopAssetStore) Get(_ context.Context, _ string) (*Asset, error) { //nolint:revive // interface impl
	return nil, fmt.Errorf("asset not found")
}

func (*noopAssetStore) List(_ context.Context, _ AssetFilter) ([]Asset, int, error) { //nolint:revive // interface impl
	return nil, 0, nil
}

func (*noopAssetStore) Update(_ context.Context, _ string, _ AssetUpdate) error { return nil } //nolint:revive // interface impl
func (*noopAssetStore) SoftDelete(_ context.Context, _ string) error            { return nil } //nolint:revive // interface impl

// --- Noop ShareStore ---

type noopShareStore struct{}

// NewNoopShareStore creates a no-op ShareStore for use when no database is available.
func NewNoopShareStore() ShareStore {
	return &noopShareStore{}
}

//nolint:revive // interface implementation methods on unexported type need no doc comments
func (*noopShareStore) Insert(_ context.Context, _ Share) error { return nil }

func (*noopShareStore) GetByID(_ context.Context, _ string) (*Share, error) { //nolint:revive // interface impl
	return nil, fmt.Errorf("share not found")
}

func (*noopShareStore) GetByToken(_ context.Context, _ string) (*Share, error) { //nolint:revive // interface impl
	return nil, fmt.Errorf("share not found")
}

func (*noopShareStore) ListByAsset(_ context.Context, _ string) ([]Share, error) { //nolint:revive // interface impl
	return nil, nil
}

func (*noopShareStore) ListSharedWithUser(_ context.Context, _, _ string, _, _ int) ([]SharedAsset, int, error) { //nolint:revive // interface impl
	return nil, 0, nil
}

func (*noopShareStore) ListActiveShareSummaries(_ context.Context, _ []string) (map[string]ShareSummary, error) { //nolint:revive // interface impl
	return map[string]ShareSummary{}, nil
}

func (*noopShareStore) Revoke(_ context.Context, _ string) error          { return nil } //nolint:revive // interface impl
func (*noopShareStore) IncrementAccess(_ context.Context, _ string) error { return nil } //nolint:revive // interface impl

// --- Helpers ---

func unmarshalAssetJSON(asset *Asset, tags, prov []byte) error {
	if err := json.Unmarshal(tags, &asset.Tags); err != nil {
		return fmt.Errorf("unmarshaling tags: %w", err)
	}
	if err := json.Unmarshal(prov, &asset.Provenance); err != nil {
		return fmt.Errorf("unmarshaling provenance: %w", err)
	}
	return nil
}

func applyAssetFilter(qb sq.SelectBuilder, filter AssetFilter) sq.SelectBuilder {
	if filter.OwnerID != "" {
		qb = qb.Where(sq.Eq{"owner_id": filter.OwnerID})
	}
	if filter.ContentType != "" {
		qb = qb.Where(sq.Eq{"content_type": filter.ContentType})
	}
	if filter.Tag != "" {
		tagJSON, _ := json.Marshal([]string{filter.Tag})
		qb = qb.Where(sq.Expr("tags @> ?::jsonb", string(tagJSON)))
	}
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		qb = qb.Where(sq.Or{
			sq.ILike{"name": like},
			sq.ILike{"description": like},
			sq.ILike{"owner_email": like},
			sq.Expr("tags::text ILIKE ?", like),
		})
	}
	return qb
}

func scanAssetRow(rows *sql.Rows) (Asset, error) {
	var asset Asset
	var tags, prov []byte
	var deletedAt sql.NullTime

	if err := rows.Scan(
		&asset.ID, &asset.OwnerID, &asset.OwnerEmail, &asset.Name, &asset.Description,
		&asset.ContentType, &asset.S3Bucket, &asset.S3Key, &asset.SizeBytes,
		&tags, &prov, &asset.SessionID, &asset.CreatedAt, &asset.UpdatedAt, &deletedAt,
	); err != nil {
		return asset, fmt.Errorf("scanning asset row: %w", err)
	}

	if deletedAt.Valid {
		asset.DeletedAt = &deletedAt.Time
	}

	if err := unmarshalAssetJSON(&asset, tags, prov); err != nil {
		return asset, err
	}
	return asset, nil
}

func scanShareRow(rows *sql.Rows) (Share, error) {
	var share Share
	var expiresAt, lastAccessed sql.NullTime
	var sharedWith, sharedEmail sql.NullString

	if err := rows.Scan(
		&share.ID, &share.AssetID, &share.Token, &share.CreatedBy,
		&sharedWith, &sharedEmail, &expiresAt, &share.Revoked,
		&share.HideExpiration, &share.AccessCount, &lastAccessed, &share.CreatedAt,
	); err != nil {
		return share, fmt.Errorf("scanning share row: %w", err)
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
	_ AssetStore = (*postgresAssetStore)(nil)
	_ AssetStore = (*noopAssetStore)(nil)
	_ ShareStore = (*postgresShareStore)(nil)
	_ ShareStore = (*noopShareStore)(nil)
)
