// Package assetrefstore is the PostgreSQL store for the things an asset's
// content references (#1474, #1488).
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

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

// refColumns is the column list every read selects, in the order scanRefs
// expects. It is written once so a column added to one query and not the other
// is a compile-time mismatch rather than a silent one.
const refColumns = `asset_id, target_kind, target_id, uri, ref_token, position, declared_by, created_at`

// store persists the things an asset's content references.
type store struct {
	db *sql.DB
}

// New creates the PostgreSQL reference store.
func New(db *sql.DB) assetrefs.Store {
	return &store{db: db}
}

// Replace rewrites one asset's references to exactly refs.
//
// It runs in a transaction because a save declares the whole list: a delete
// that committed without its insert would leave a rendered asset with every
// image broken and no record of what it used to name.
func (s *store) Replace(
	ctx context.Context, assetID string, refs []assetrefs.Ref,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning reference write: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM portal_asset_refs WHERE asset_id = $1`, assetID); err != nil {
		return fmt.Errorf("clearing references: %w", err)
	}
	for i, ref := range refs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO portal_asset_refs
			 (asset_id, target_kind, target_id, uri, ref_token, position, declared_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			assetID, string(ref.TargetKind), ref.TargetID, ref.URI,
			ref.RefToken, i, ref.DeclaredBy); err != nil {
			return fmt.Errorf("recording reference %q: %w", ref.URI, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing references: %w", err)
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
	ctx context.Context, ref assetrefs.Ref,
) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO portal_asset_refs
		 (asset_id, target_kind, target_id, uri, ref_token, position, declared_by)
		 VALUES ($1, $2, $3, $4, $5,
		         COALESCE((SELECT MAX(position) + 1 FROM portal_asset_refs
		                    WHERE asset_id = $1), 0),
		         $6)
		 ON CONFLICT (asset_id, target_kind, target_id) DO NOTHING`,
		ref.AssetID, string(ref.TargetKind), ref.TargetID, ref.URI,
		ref.RefToken, ref.DeclaredBy)
	if err != nil {
		return false, fmt.Errorf("recording reference %q: %w", ref.URI, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("recording reference %q: %w", ref.URI, err)
	}
	return n > 0, nil
}

// Detach removes one reference, reporting whether there was one.
//
// It leaves a hole in the remaining positions, which is what the declared
// order can afford: position only orders the list a reader is shown and the
// rewrite consumes, and the rewrite sorts by URI length for its own reasons.
// Renumbering would mean rewriting every surviving row to no visible end.
func (s *store) Detach(
	ctx context.Context, assetID string, kind assetrefs.TargetKind, targetID string,
) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM portal_asset_refs
		  WHERE asset_id = $1 AND target_kind = $2 AND target_id = $3`,
		assetID, string(kind), targetID)
	if err != nil {
		return false, fmt.Errorf("removing reference: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("removing reference: %w", err)
	}
	return n > 0, nil
}

// ListByAsset returns one asset's references in declared order.
func (s *store) ListByAsset(
	ctx context.Context, assetID string,
) ([]assetrefs.Ref, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+refColumns+`
		   FROM portal_asset_refs
		  WHERE asset_id = $1
		  ORDER BY position, target_kind, target_id`, assetID)
	if err != nil {
		return nil, fmt.Errorf("listing references: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only

	return scanRefs(rows)
}

// ListByTarget returns at most limit references naming one target, newest
// asset first, unscoped by design: the caller narrows the answer to the assets
// its reader may open, at a query per asset, which is what the limit bounds.
func (s *store) ListByTarget(
	ctx context.Context, kind assetrefs.TargetKind, targetID string, limit int,
) ([]assetrefs.Ref, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+refColumns+`
		   FROM portal_asset_refs
		  WHERE target_kind = $1 AND target_id = $2
		  ORDER BY created_at DESC, asset_id
		  LIMIT $3`, string(kind), targetID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing assets referencing target: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only

	return scanRefs(rows)
}

// scanRefs drains a reference query. Both list paths select refColumns and
// differ only in their predicate, so the scan is written once.
func scanRefs(rows *sql.Rows) ([]assetrefs.Ref, error) {
	var out []assetrefs.Ref
	for rows.Next() {
		ref, err := scanRef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading references: %w", err)
	}
	return out, nil
}

// rowScanner is the one method *sql.Row and *sql.Rows share, so the single-row
// and multi-row reads decode a reference the same way.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanRef decodes one row of refColumns.
func scanRef(row rowScanner) (assetrefs.Ref, error) {
	var ref assetrefs.Ref
	var kind string
	if err := row.Scan(&ref.AssetID, &kind, &ref.TargetID, &ref.URI,
		&ref.RefToken, &ref.Position, &ref.DeclaredBy, &ref.CreatedAt); err != nil {
		// Wrapped here rather than at each caller: GetByToken tests the chain
		// for sql.ErrNoRows, which %w preserves.
		return assetrefs.Ref{}, fmt.Errorf("scanning reference: %w", err)
	}
	ref.TargetKind = assetrefs.TargetKind(kind)
	return ref, nil
}

// GetByToken resolves the reference a serving URL names, requiring the token
// and the asset in the path to agree. No such reference is (nil, nil).
func (s *store) GetByToken(
	ctx context.Context, assetID, token string,
) (*assetrefs.Ref, error) {
	ref, err := scanRef(s.db.QueryRowContext(ctx,
		`SELECT `+refColumns+`
		   FROM portal_asset_refs
		  WHERE asset_id = $1 AND ref_token = $2`, assetID, token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // interface contract: no such reference is (nil, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("reading reference: %w", err)
	}
	return &ref, nil
}
