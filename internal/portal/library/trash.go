package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/reachstore"
)

// ErrNotDeleted is returned when a restore names an item that is not in the
// trash.
var ErrNotDeleted = errors.New("not in the trash")

// TrashItem is one deleted asset or collection and the day it is purged.
type TrashItem struct {
	Kind        Kind      `json:"kind" example:"asset"`
	ID          string    `json:"id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	Name        string    `json:"name" example:"Old pricing model"`
	ContentType string    `json:"content_type,omitempty" example:"text/html"`
	OwnerEmail  string    `json:"owner_email,omitempty" example:"jane@example.com"`
	DeletedAt   time.Time `json:"deleted_at"`
	// PurgeAt is when the retention sweep removes it for good: its deletion
	// plus portal.deleted_retention_days.
	PurgeAt time.Time `json:"purge_at"`
}

// Scope is whose rows an owner listing reads: one owner's, or, for an
// administrator who asked, everyone's.
type Scope struct {
	Owner    portaldomain.AssetOwner
	Everyone bool
}

// Page is one page of an owner listing and the total it is a page of.
type Page[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

// Trash lists the deleted assets and collections in scope, most recently
// deleted first, each with the day it is purged after retention.
func (s *Store) Trash(ctx context.Context, scope Scope, retention time.Duration, limit, offset int) (Page[TrashItem], error) {
	query, args, err := buildTrash(scope, ClampLimit(limit), max(offset, 0))
	if err != nil {
		return Page[TrashItem]{}, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page[TrashItem]{}, fmt.Errorf("reading the trash: %w", err)
	}
	defer func() { _ = rows.Close() }()
	page := Page[TrashItem]{Items: []TrashItem{}}
	for rows.Next() {
		var it TrashItem
		var kind string
		if err := rows.Scan(&kind, &it.ID, &it.Name, &it.ContentType, &it.OwnerEmail, &it.DeletedAt, &page.Total); err != nil {
			return Page[TrashItem]{}, fmt.Errorf("scanning a trash entry: %w", err)
		}
		it.Kind, it.PurgeAt = Kind(kind), it.DeletedAt.Add(retention)
		page.Items = append(page.Items, it)
	}
	if err := rows.Err(); err != nil {
		return Page[TrashItem]{}, fmt.Errorf("reading the trash: %w", err)
	}
	return page, nil
}

// buildTrash renders the deleted assets and collections in scope with the
// total they number.
func buildTrash(scope Scope, limit, offset int) (query string, args []any, err error) {
	assets := sq.Select("'asset' AS kind", "id", "name", "content_type", "owner_email", "deleted_at").
		From("portal_assets").Where("deleted_at IS NOT NULL")
	colls := sq.Select("'collection'", "id", "name", "''", "owner_email", "deleted_at").
		From("portal_collections").Where("deleted_at IS NOT NULL")
	if !scope.Everyone {
		assets = assets.Where(reachstore.OwnerPredicate(scope.Owner))
		colls = colls.Where(collectionOwner(scope.Owner))
	}
	inner, args, err := union("", nil, assets, colls)
	if err != nil {
		return "", nil, err
	}
	// The parts are already numbered; the page's two arguments follow them.
	n := len(args)
	query = fmt.Sprintf("SELECT kind, id, name, content_type, owner_email, deleted_at, COUNT(*) OVER () FROM (%s) t "+
		"ORDER BY deleted_at DESC, id LIMIT $%d OFFSET $%d", inner, n+1, n+2)
	return query, append(args, limit, offset), nil
}

// collectionOwner matches a collection the owner owns. A collection records
// ownership by id (access.CanViewCollection); an owner with no id owns none.
func collectionOwner(owner portaldomain.AssetOwner) sq.Sqlizer {
	if owner.UserID == "" {
		return sq.Expr("FALSE")
	}
	return sq.Eq{"owner_id": owner.UserID}
}

const (
	restoreAssetQuery      = `UPDATE portal_assets SET deleted_at = NULL WHERE id = $1 AND deleted_at IS NOT NULL`
	restoreCollectionQuery = `UPDATE portal_collections SET deleted_at = NULL, updated_at = NOW() WHERE id = $1 AND deleted_at IS NOT NULL`
)

// Restore takes a deleted asset or collection out of the trash. Deleting only
// stamped it, so its versions, shares and collection memberships come back
// with it. The caller checks the person may manage it first.
func (s *Store) Restore(ctx context.Context, kind Kind, id string) error {
	query := restoreAssetQuery
	if kind == KindCollection {
		query = restoreCollectionQuery
	}
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("restoring from the trash: %w", err)
	}
	return affectedOne(res)
}

func affectedOne(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking the restore: %w", err)
	}
	if n == 0 {
		return ErrNotDeleted
	}
	return nil
}
