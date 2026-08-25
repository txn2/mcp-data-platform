// Package assetrefstore is the PostgreSQL store for the managed resources an
// asset's content references (#1474).
//
// It is its own package rather than another file under internal/portal/
// portalstore because it shares nothing with the stores there -- no column
// list, no scan helper, no filter builder -- and a package whose declarations
// form unconnected islands is what the cohesion gate exists to catch. The
// reference table is read and written on its own terms, so it lives on its own.
package assetrefstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// store persists the managed resources an asset's content references.
type store struct {
	db *sql.DB
}

// New creates the PostgreSQL reference store.
func New(db *sql.DB) portaldomain.AssetResourceRefStore {
	return &store{db: db}
}

// Replace rewrites one asset's references to exactly refs.
//
// It runs in a transaction because a save declares the whole list: a delete
// that committed without its insert would leave a rendered asset with every
// image broken and no record of what it used to name.
func (s *store) Replace(
	ctx context.Context, assetID string, refs []portaldomain.AssetResourceRef,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning reference write: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM portal_asset_resource_refs WHERE asset_id = $1`, assetID); err != nil {
		return fmt.Errorf("clearing resource references: %w", err)
	}
	for i, ref := range refs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO portal_asset_resource_refs
			 (asset_id, resource_id, uri, ref_token, position, declared_by)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			assetID, ref.ResourceID, ref.URI, ref.RefToken, i, ref.DeclaredBy); err != nil {
			return fmt.Errorf("recording resource reference %q: %w", ref.URI, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing resource references: %w", err)
	}
	return nil
}

// ListByAsset returns one asset's references in declared order.
func (s *store) ListByAsset(
	ctx context.Context, assetID string,
) ([]portaldomain.AssetResourceRef, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT asset_id, resource_id, uri, ref_token, position, declared_by, created_at
		   FROM portal_asset_resource_refs
		  WHERE asset_id = $1
		  ORDER BY position, resource_id`, assetID)
	if err != nil {
		return nil, fmt.Errorf("listing resource references: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only

	var out []portaldomain.AssetResourceRef
	for rows.Next() {
		var ref portaldomain.AssetResourceRef
		if err := rows.Scan(&ref.AssetID, &ref.ResourceID, &ref.URI,
			&ref.RefToken, &ref.Position, &ref.DeclaredBy, &ref.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning resource reference: %w", err)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading resource references: %w", err)
	}
	return out, nil
}

// GetByToken resolves the reference a serving URL names, requiring the token
// and the asset in the path to agree. No such reference is (nil, nil).
func (s *store) GetByToken(
	ctx context.Context, assetID, token string,
) (*portaldomain.AssetResourceRef, error) {
	var ref portaldomain.AssetResourceRef
	err := s.db.QueryRowContext(ctx,
		`SELECT asset_id, resource_id, uri, ref_token, position, declared_by, created_at
		   FROM portal_asset_resource_refs
		  WHERE asset_id = $1 AND ref_token = $2`, assetID, token).
		Scan(&ref.AssetID, &ref.ResourceID, &ref.URI,
			&ref.RefToken, &ref.Position, &ref.DeclaredBy, &ref.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // interface contract: no such reference is (nil, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("reading resource reference: %w", err)
	}
	return &ref, nil
}
