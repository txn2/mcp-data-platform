// Package assetlinks is links between assets (#2432): an asset's content
// links another asset by writing mcp:asset:<id> as a link target, and the
// target lists the assets whose current content links to it ("Linked from").
//
// A link grants nothing. It is not a declared reference (internal/portal/
// assetrefs), takes no reference slot and issues no token; saving one checks
// nothing about the target. Following it opens the target's portal page, whose
// own access check decides what the reader sees. The Linked from list is read
// through the reader's reach (internal/portal/reachstore), so an asset the
// reader cannot open is neither listed nor counted.
//
// The links are recorded every time a version of an asset is written: Indexed
// wraps the version store, so every write path (save_asset, manage_asset, the
// portal editor, a script's export, a revert) records them with no change of
// its own.
package assetlinks

import (
	"context"
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/reachstore"
)

// maxLinkedFrom bounds the Linked from list.
const maxLinkedFrom = 100

// Ref is one asset in a Linked from list.
type Ref struct {
	ID          string `json:"id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	Name        string `json:"name" example:"Reports index"`
	ContentType string `json:"content_type" example:"text/markdown"`
}

// Store records and reads links.
type Store struct{ db *sql.DB }

// New returns the store over db.
func New(db *sql.DB) *Store { return &Store{db: db} }

var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

const (
	deleteLinksSQL = `DELETE FROM portal_asset_links WHERE from_asset_id = $1`
	insertLinksSQL = `INSERT INTO portal_asset_links (from_asset_id, to_asset_id)
	SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`
	linksFromSQL = `SELECT to_asset_id FROM portal_asset_links WHERE from_asset_id = $1 ORDER BY to_asset_id`
)

// Replace records the assets fromID's current content links to, replacing
// what it linked before.
func (s *Store) Replace(ctx context.Context, fromID string, toIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, deleteLinksSQL, fromID); err != nil {
		return fmt.Errorf("clearing links: %w", err)
	}
	if len(toIDs) > 0 {
		if _, err := tx.ExecContext(ctx, insertLinksSQL, fromID, pq.Array(toIDs)); err != nil {
			return fmt.Errorf("recording links: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing links: %w", err)
	}
	return nil
}

// LinksFrom is the ids an asset's current content links to.
func (s *Store) LinksFrom(ctx context.Context, fromID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, linksFromSQL, fromID)
	if err != nil {
		return nil, fmt.Errorf("reading links: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cleanup
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning link: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating links: %w", err)
	}
	return ids, nil
}

// BuildLinkedFrom renders the Linked from statement: the live assets linking
// to toID that reach can open.
func BuildLinkedFrom(toID string, reach *portaldomain.Reach) (query string, args []any, err error) {
	return psq.Select("portal_assets.id", "portal_assets.name", "portal_assets.content_type"). //nolint:wrapcheck // the caller wraps
													From("portal_asset_links l").
													Join("portal_assets ON portal_assets.id = l.from_asset_id").
													Where(sq.Eq{"l.to_asset_id": toID}).
													Where("portal_assets.deleted_at IS NULL").
													Where(reachstore.AssetPredicate(reach)).
													OrderBy("portal_assets.name", "portal_assets.id").
													Limit(maxLinkedFrom).
													ToSql()
}

// LinkedFrom is the live assets linking to toID that reach can open.
func (s *Store) LinkedFrom(ctx context.Context, toID string, reach *portaldomain.Reach) ([]Ref, error) {
	refs := []Ref{}
	if !reach.Identified() {
		return refs, nil
	}
	query, args, err := BuildLinkedFrom(toID, reach)
	if err != nil {
		return nil, fmt.Errorf("building linked from: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading linked from: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cleanup
	for rows.Next() {
		var r Ref
		if err := rows.Scan(&r.ID, &r.Name, &r.ContentType); err != nil {
			return nil, fmt.Errorf("scanning linked from: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating linked from: %w", err)
	}
	return refs, nil
}
