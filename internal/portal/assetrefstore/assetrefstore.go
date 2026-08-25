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

// Attach adds one reference at the end of the asset's declared order,
// reporting whether it was added.
//
// The position is derived in the statement rather than passed in, so two
// callers attaching at once cannot be handed the same index. ON CONFLICT DO
// NOTHING makes the primary key the arbiter of "already referenced": a read
// then an insert would let both sides of a race believe they added it.
func (s *store) Attach(
	ctx context.Context, ref portaldomain.AssetResourceRef,
) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO portal_asset_resource_refs
		 (asset_id, resource_id, uri, ref_token, position, declared_by)
		 VALUES ($1, $2, $3, $4,
		         COALESCE((SELECT MAX(position) + 1 FROM portal_asset_resource_refs
		                    WHERE asset_id = $1), 0),
		         $5)
		 ON CONFLICT (asset_id, resource_id) DO NOTHING`,
		ref.AssetID, ref.ResourceID, ref.URI, ref.RefToken, ref.DeclaredBy)
	if err != nil {
		return false, fmt.Errorf("recording resource reference %q: %w", ref.URI, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("recording resource reference %q: %w", ref.URI, err)
	}
	return n > 0, nil
}

// Detach removes one reference, reporting whether there was one.
//
// It leaves a hole in the remaining positions, which is what the declared
// order can afford: position only orders the list a reader is shown and the
// rewrite consumes, and the rewrite sorts by URI length for its own reasons.
// Renumbering would mean rewriting every surviving row to no visible end.
func (s *store) Detach(ctx context.Context, assetID, resourceID string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM portal_asset_resource_refs
		  WHERE asset_id = $1 AND resource_id = $2`, assetID, resourceID)
	if err != nil {
		return false, fmt.Errorf("removing resource reference: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("removing resource reference: %w", err)
	}
	return n > 0, nil
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

	return scanRefs(rows)
}

// ListByResource returns at most limit references naming one resource, newest
// asset first, unscoped by design: the caller narrows the answer to the assets
// its reader may open, at a query per asset, which is what the limit bounds.
func (s *store) ListByResource(
	ctx context.Context, resourceID string, limit int,
) ([]portaldomain.AssetResourceRef, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT asset_id, resource_id, uri, ref_token, position, declared_by, created_at
		   FROM portal_asset_resource_refs
		  WHERE resource_id = $1
		  ORDER BY created_at DESC, asset_id
		  LIMIT $2`, resourceID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing assets referencing resource: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only

	return scanRefs(rows)
}

// scanRefs drains a reference query. Both list paths select the same columns in
// the same order and differ only in their predicate, so the scan is written
// once: a column added to one query and not the other would otherwise be a
// silent mismatch rather than a compile error.
func scanRefs(rows *sql.Rows) ([]portaldomain.AssetResourceRef, error) {
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
