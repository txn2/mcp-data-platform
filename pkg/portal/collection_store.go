package portal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"
)

// CollectionStore persists and queries portal collections.
type CollectionStore interface {
	Insert(ctx context.Context, c Collection) error
	Get(ctx context.Context, id string) (*Collection, error)
	List(ctx context.Context, filter CollectionFilter) ([]Collection, int, error)
	Update(ctx context.Context, id, name, description string) error
	UpdateConfig(ctx context.Context, id string, config CollectionConfig) error
	UpdateThumbnail(ctx context.Context, id, thumbnailS3Key string) error
	SoftDelete(ctx context.Context, id string) error
	SetSections(ctx context.Context, collectionID string, sections []CollectionSection) error
}

// Sentinel errors for collection store operations.
var (
	errCollStoreNotFound    = errors.New("collection not found")
	errCollStoreNotFoundDel = errors.New("collection not found or already deleted")
)

// wrapRowsAffected wraps an error from RowsAffected with a standard prefix.
func wrapRowsAffected(err error) error {
	return fmt.Errorf("checking rows affected: %w", err)
}

// --- PostgreSQL CollectionStore ---

type postgresCollectionStore struct {
	db *sql.DB
}

// NewPostgresCollectionStore creates a new PostgreSQL collection store.
func NewPostgresCollectionStore(db *sql.DB) CollectionStore {
	return &postgresCollectionStore{db: db}
}

func (s *postgresCollectionStore) Insert(ctx context.Context, c Collection) error { //nolint:revive // interface impl
	configJSON, err := json.Marshal(c.Config)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	query := `
		INSERT INTO portal_collections (id, owner_id, owner_email, name, description, thumbnail_s3_key, config)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err = s.db.ExecContext(ctx, query, c.ID, c.OwnerID, c.OwnerEmail, c.Name, c.Description, c.ThumbnailS3Key, configJSON)
	if err != nil {
		return fmt.Errorf("inserting collection: %w", err)
	}
	return nil
}

func (s *postgresCollectionStore) Get(ctx context.Context, id string) (*Collection, error) { //nolint:revive // interface impl
	// Fetch the collection header.
	coll, err := s.getHeader(ctx, id)
	if err != nil {
		return nil, err
	}

	// Fetch sections.
	sections, err := s.getSections(ctx, id)
	if err != nil {
		return nil, err
	}

	// Fetch items for all sections in one query.
	sectionIDs := make([]string, len(sections))
	for i, sec := range sections {
		sectionIDs[i] = sec.ID
	}

	itemsBySection, err := s.getItemsBySections(ctx, sectionIDs)
	if err != nil {
		return nil, err
	}

	for i := range sections {
		sections[i].Items = itemsBySection[sections[i].ID]
	}
	coll.Sections = sections

	return coll, nil
}

func (s *postgresCollectionStore) getHeader(ctx context.Context, id string) (*Collection, error) {
	query := `
		SELECT id, owner_id, owner_email, name, description, thumbnail_s3_key, config,
		       created_at, updated_at, deleted_at
		FROM portal_collections WHERE id = $1
	`
	var c Collection
	var deletedAt sql.NullTime
	var configJSON []byte
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&c.ID, &c.OwnerID, &c.OwnerEmail, &c.Name, &c.Description, &c.ThumbnailS3Key, &configJSON,
		&c.CreatedAt, &c.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("querying collection: %w", err)
	}
	if deletedAt.Valid {
		c.DeletedAt = &deletedAt.Time
	}
	if len(configJSON) > 0 {
		_ = json.Unmarshal(configJSON, &c.Config) // best-effort; empty config on error
	}
	return &c, nil
}

func (s *postgresCollectionStore) getSections(ctx context.Context, collectionID string) ([]CollectionSection, error) {
	query := `
		SELECT id, collection_id, title, description, position, created_at
		FROM portal_collection_sections
		WHERE collection_id = $1
		ORDER BY position
	`
	rows, err := s.db.QueryContext(ctx, query, collectionID)
	if err != nil {
		return nil, fmt.Errorf("querying sections: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var sections []CollectionSection
	for rows.Next() {
		var sec CollectionSection
		if err := rows.Scan(&sec.ID, &sec.CollectionID, &sec.Title, &sec.Description, &sec.Position, &sec.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning section row: %w", err)
		}
		sections = append(sections, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating section rows: %w", err)
	}
	return sections, nil
}

func (s *postgresCollectionStore) getItemsBySections(ctx context.Context, sectionIDs []string) (map[string][]CollectionItem, error) {
	if len(sectionIDs) == 0 {
		return map[string][]CollectionItem{}, nil
	}

	query := `
		SELECT ci.id, ci.section_id, ci.asset_id, ci.position, ci.created_at,
		       COALESCE(pa.name, ''), COALESCE(pa.content_type, ''),
		       COALESCE(pa.thumbnail_s3_key, ''), COALESCE(pa.description, '')
		FROM portal_collection_items ci
		LEFT JOIN portal_assets pa ON ci.asset_id = pa.id AND pa.deleted_at IS NULL
		WHERE ci.section_id = ANY($1)
		ORDER BY ci.position
	`
	rows, err := s.db.QueryContext(ctx, query, pq.Array(sectionIDs))
	if err != nil {
		return nil, fmt.Errorf("querying items: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	result := make(map[string][]CollectionItem)
	for rows.Next() {
		var item CollectionItem
		if err := rows.Scan(
			&item.ID, &item.SectionID, &item.AssetID, &item.Position, &item.CreatedAt,
			&item.AssetName, &item.AssetContentType, &item.AssetThumbnail, &item.AssetDescription,
		); err != nil {
			return nil, fmt.Errorf("scanning item row: %w", err)
		}
		result[item.SectionID] = append(result[item.SectionID], item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating item rows: %w", err)
	}
	return result, nil
}

func (s *postgresCollectionStore) List(ctx context.Context, filter CollectionFilter) ([]Collection, int, error) { //nolint:revive // interface impl
	limit := filter.EffectiveLimit()

	countQB, selectQB := s.buildListQueries(filter, limit)

	total, err := s.countQuery(ctx, countQB)
	if err != nil {
		return nil, 0, err
	}

	collections, err := s.executeListQuery(ctx, selectQB)
	if err != nil {
		return nil, 0, err
	}

	if err := s.populateAssetTags(ctx, collections); err != nil {
		return nil, 0, fmt.Errorf("populating asset tags: %w", err)
	}

	return collections, total, nil
}

// buildListQueries constructs the count and select query builders for listing collections.
func (*postgresCollectionStore) buildListQueries(filter CollectionFilter, limit int) (countQB, selectQB sq.SelectBuilder) {
	countQB = psq.Select("COUNT(*)").From("portal_collections").Where("deleted_at IS NULL")
	selectQB = psq.Select(
		"id", "owner_id", "owner_email", "name", "description", "thumbnail_s3_key", "config",
		"created_at", "updated_at", "deleted_at",
	).From("portal_collections").Where("deleted_at IS NULL").
		OrderBy("created_at DESC").Limit(uint64(limit)).Offset(uint64(filter.Offset)) // #nosec G115 -- limit/offset are validated positive ints from EffectiveLimit()

	if filter.OwnerID != "" {
		countQB = countQB.Where(sq.Eq{"owner_id": filter.OwnerID})
		selectQB = selectQB.Where(sq.Eq{"owner_id": filter.OwnerID})
	}
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		cond := sq.Or{sq.ILike{"name": like}, sq.ILike{"description": like}}
		countQB = countQB.Where(cond)
		selectQB = selectQB.Where(cond)
	}

	return countQB, selectQB
}

// countQuery executes a count query and returns the total.
func (s *postgresCollectionStore) countQuery(ctx context.Context, qb sq.SelectBuilder) (int, error) {
	countSQL, countArgs, err := qb.ToSql()
	if err != nil {
		return 0, fmt.Errorf("building count query: %w", err)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, countSQL, countArgs...).Scan(&total); err != nil { // #nosec G701 -- builder-generated query
		return 0, fmt.Errorf("counting collections: %w", err)
	}
	return total, nil
}

// executeListQuery runs the select query and scans the results into a collection slice.
func (s *postgresCollectionStore) executeListQuery(ctx context.Context, qb sq.SelectBuilder) ([]Collection, error) {
	selectSQL, selectArgs, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building select query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, selectSQL, selectArgs...) // #nosec G701 -- builder-generated query
	if err != nil {
		return nil, fmt.Errorf("querying collections: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var collections []Collection
	for rows.Next() {
		var c Collection
		var deletedAt sql.NullTime
		var configJSON []byte
		if err := rows.Scan(
			&c.ID, &c.OwnerID, &c.OwnerEmail, &c.Name, &c.Description, &c.ThumbnailS3Key, &configJSON,
			&c.CreatedAt, &c.UpdatedAt, &deletedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning collection row: %w", err)
		}
		if deletedAt.Valid {
			c.DeletedAt = &deletedAt.Time
		}
		if len(configJSON) > 0 {
			_ = json.Unmarshal(configJSON, &c.Config)
		}
		collections = append(collections, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating collection rows: %w", err)
	}

	return collections, nil
}

// populateAssetTags fetches unique asset tags for a batch of collections in one query.
func (s *postgresCollectionStore) populateAssetTags(ctx context.Context, collections []Collection) error {
	if len(collections) == 0 {
		return nil
	}

	ids := make([]string, len(collections))
	for i, c := range collections {
		ids[i] = c.ID
	}

	query := `
		SELECT cs.collection_id, ARRAY_AGG(DISTINCT t.tag ORDER BY t.tag)
		FROM portal_collection_sections cs
		JOIN portal_collection_items ci ON ci.section_id = cs.id
		JOIN portal_assets pa ON pa.id = ci.asset_id AND pa.deleted_at IS NULL
		CROSS JOIN LATERAL jsonb_array_elements_text(pa.tags) AS t(tag)
		WHERE cs.collection_id = ANY($1)
		GROUP BY cs.collection_id
	`

	tagRows, err := s.db.QueryContext(ctx, query, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("querying asset tags: %w", err)
	}
	defer tagRows.Close() //nolint:errcheck // best-effort cleanup

	tagMap := make(map[string][]string)
	for tagRows.Next() {
		var collID string
		var tags []string
		if err := tagRows.Scan(&collID, pq.Array(&tags)); err != nil {
			return fmt.Errorf("scanning tag row: %w", err)
		}
		tagMap[collID] = tags
	}
	if err := tagRows.Err(); err != nil {
		return fmt.Errorf("iterating tag rows: %w", err)
	}

	for i := range collections {
		if tags, ok := tagMap[collections[i].ID]; ok {
			collections[i].AssetTags = tags
		}
	}
	return nil
}

func (s *postgresCollectionStore) Update(ctx context.Context, id, name, description string) error { //nolint:revive // interface impl
	query := `UPDATE portal_collections SET name = $1, description = $2, updated_at = $3 WHERE id = $4 AND deleted_at IS NULL`
	result, err := s.db.ExecContext(ctx, query, name, description, time.Now(), id)
	if err != nil {
		return fmt.Errorf("updating collection: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return wrapRowsAffected(err)
	}
	if affected == 0 {
		return errCollStoreNotFound
	}
	return nil
}

func (s *postgresCollectionStore) UpdateConfig(ctx context.Context, id string, config CollectionConfig) error { //nolint:revive // interface impl
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	query := `UPDATE portal_collections SET config = $1, updated_at = $2 WHERE id = $3 AND deleted_at IS NULL`
	result, err := s.db.ExecContext(ctx, query, configJSON, time.Now(), id)
	if err != nil {
		return fmt.Errorf("updating config: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return wrapRowsAffected(err)
	}
	if affected == 0 {
		return errCollStoreNotFound
	}
	return nil
}

func (s *postgresCollectionStore) UpdateThumbnail(ctx context.Context, id, thumbnailS3Key string) error { //nolint:revive // interface impl
	query := `UPDATE portal_collections SET thumbnail_s3_key = $1, updated_at = $2 WHERE id = $3 AND deleted_at IS NULL`
	result, err := s.db.ExecContext(ctx, query, thumbnailS3Key, time.Now(), id)
	if err != nil {
		return fmt.Errorf("updating thumbnail: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return wrapRowsAffected(err)
	}
	if affected == 0 {
		return errCollStoreNotFound
	}
	return nil
}

func (s *postgresCollectionStore) SoftDelete(ctx context.Context, id string) error { //nolint:revive // interface impl
	query := `UPDATE portal_collections SET deleted_at = $1, updated_at = $1 WHERE id = $2 AND deleted_at IS NULL`
	result, err := s.db.ExecContext(ctx, query, time.Now(), id)
	if err != nil {
		return fmt.Errorf("soft-deleting collection: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return wrapRowsAffected(err)
	}
	if affected == 0 {
		return errCollStoreNotFoundDel
	}
	return nil
}

func (s *postgresCollectionStore) SetSections(ctx context.Context, collectionID string, sections []CollectionSection) error { //nolint:revive // interface impl
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	if err := s.replaceSections(ctx, tx, collectionID, sections); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

// replaceSections deletes existing sections and inserts new ones within a transaction.
func (*postgresCollectionStore) replaceSections(ctx context.Context, tx *sql.Tx, collectionID string, sections []CollectionSection) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM portal_collection_sections WHERE collection_id = $1`, collectionID); err != nil {
		return fmt.Errorf("deleting existing sections: %w", err)
	}

	sectionStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO portal_collection_sections (id, collection_id, title, description, position) VALUES ($1, $2, $3, $4, $5)`)
	if err != nil {
		return fmt.Errorf("preparing section insert: %w", err)
	}
	defer sectionStmt.Close() //nolint:errcheck // best-effort cleanup

	itemStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO portal_collection_items (id, section_id, asset_id, position) VALUES ($1, $2, $3, $4)`)
	if err != nil {
		return fmt.Errorf("preparing item insert: %w", err)
	}
	defer itemStmt.Close() //nolint:errcheck // best-effort cleanup

	for i, sec := range sections {
		if _, err := sectionStmt.ExecContext(ctx, sec.ID, collectionID, sec.Title, sec.Description, i); err != nil {
			return fmt.Errorf("inserting section %d: %w", i, err)
		}
		for j, item := range sec.Items {
			if _, err := itemStmt.ExecContext(ctx, item.ID, sec.ID, item.AssetID, j); err != nil {
				return fmt.Errorf("inserting item %d in section %d: %w", j, i, err)
			}
		}
	}

	// Touch updated_at on the collection.
	if _, err := tx.ExecContext(ctx, `UPDATE portal_collections SET updated_at = $1 WHERE id = $2`, time.Now(), collectionID); err != nil {
		return fmt.Errorf("updating collection timestamp: %w", err)
	}

	return nil
}

// --- Noop CollectionStore ---

type noopCollectionStore struct{}

// NewNoopCollectionStore creates a no-op CollectionStore for use when no database is available.
func NewNoopCollectionStore() CollectionStore {
	return &noopCollectionStore{}
}

//nolint:revive // interface implementation methods on unexported type need no doc comments
func (*noopCollectionStore) Insert(_ context.Context, _ Collection) error { return nil }

func (*noopCollectionStore) Get(_ context.Context, _ string) (*Collection, error) { //nolint:revive // interface impl
	return nil, errCollStoreNotFound
}

func (*noopCollectionStore) List(_ context.Context, _ CollectionFilter) ([]Collection, int, error) { //nolint:revive // interface impl
	return nil, 0, nil
}

func (*noopCollectionStore) Update(_ context.Context, _, _, _ string) error { return nil } //nolint:revive // interface impl
func (*noopCollectionStore) UpdateConfig(_ context.Context, _ string, _ CollectionConfig) error { //nolint:revive // interface impl
	return nil
}

func (*noopCollectionStore) UpdateThumbnail(_ context.Context, _, _ string) error { //nolint:revive // interface impl
	return nil
}
func (*noopCollectionStore) SoftDelete(_ context.Context, _ string) error { return nil } //nolint:revive // interface impl
func (*noopCollectionStore) SetSections(_ context.Context, _ string, _ []CollectionSection) error { //nolint:revive // interface impl
	return nil
}
