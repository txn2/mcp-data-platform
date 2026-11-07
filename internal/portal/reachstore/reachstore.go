// Package reachstore is the SQL of a person's reach over the portal (#2500):
// which assets and collections they can open, ranked by relevance to a query,
// the tags and authors those rows carry, and the share permission each grants.
//
// The reach is the per-row access rule (internal/portal/access) written once
// as a set condition: a row is the caller's when they own it on either
// identifier it records, and shared with them when an active share names their
// id, their address or the persona they resolved to, on the row itself or, for
// an asset, on a live collection holding it. Listing, ranking and every facet
// apply it in the statement, so a row the caller cannot open is never
// returned, ranked or counted. TestReach_RealDB_ListingIsTheAccessRule holds
// it to access.CanViewAsset row by row.
//
// The candidate rows a facet reads are a statement the caller builds (the
// portal store's listing filter), so this package knows nothing of listing
// columns or filters beyond the reach itself. Ranking a candidate set is
// internal/sqlrank's.
package reachstore

import (
	"context"
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// activeShare is the condition a share row under alias ps must meet to grant
// anything: not revoked and not expired. It is access.IsShareActive in SQL.
const activeShare = "ps.revoked = FALSE AND (ps.expires_at IS NULL OR ps.expires_at > NOW())"

// liveCollectionShare joins a collection share under alias ps to its
// collection, live only. Deleting a collection leaves its share rows in place,
// and its recipients must lose what it held.
// matchNothing is the condition a reach or owner naming nobody renders.
const matchNothing = "FALSE"

const liveCollectionShare = "portal_collections pc ON pc.id = ps.collection_id AND pc.deleted_at IS NULL"

// OwnerPredicate matches a row owned by owner on either identifier it records
// (owner_id, or owner_email compared case-insensitively). An owner naming
// nobody matches nothing.
func OwnerPredicate(owner portaldomain.AssetOwner) sq.Sqlizer {
	arms := owner.Arms()
	byEmail := sq.Expr("LOWER(owner_email) = LOWER(?)", arms.Email)
	byID := sq.Eq{"owner_id": arms.UserID}
	switch {
	case arms.UserID == "" && arms.Email == "":
		return sq.Expr(matchNothing)
	case arms.UserID == "":
		return byEmail
	case arms.Email == "":
		return byID
	default:
		return sq.Or{byID, byEmail}
	}
}

// shareAddressedTo matches a share row under alias ps addressed to the reach:
// by user id, by address, or to the persona the caller resolved to. It is
// access.shareAddresses in SQL. Each arm is guarded, so a blank identifier
// matches nothing rather than every share recorded with a blank one.
func shareAddressedTo(r *portaldomain.Reach) (cond string, args []any) {
	arms := sq.Or{sq.Expr(matchNothing)}
	if r.Owner.UserID != "" {
		arms = append(arms, sq.Expr("ps.shared_with_user_id = ?", r.Owner.UserID))
	}
	if r.Owner.Email != "" {
		arms = append(arms, sq.Expr("LOWER(ps.shared_with_email) = LOWER(?)", r.Owner.Email))
	}
	if r.Persona != "" {
		arms = append(arms, sq.Expr("ps.shared_with_persona = ?", r.Persona))
	}
	cond, args, _ = arms.ToSql()
	return cond, args
}

// narrow applies the reach's scope to its owned and shared arms.
func narrow(r *portaldomain.Reach, owned, shared sq.Sqlizer) sq.Sqlizer {
	switch r.Scope {
	case portaldomain.ScopeMine:
		return owned
	case portaldomain.ScopeShared:
		return sq.And{sq.Expr("NOT (?)", owned), shared}
	default:
		return sq.Or{owned, shared}
	}
}

// AssetPredicate matches the assets (portal_assets in the outer statement) the
// reach covers in its scope.
func AssetPredicate(r *portaldomain.Reach) sq.Sqlizer {
	if !r.Identified() {
		return sq.Expr(matchNothing)
	}
	owned := sq.Expr(matchNothing)
	if r.Owner.Identified() {
		owned = OwnerPredicate(r.Owner)
	}
	addressed, args := shareAddressedTo(r)
	direct := sq.Expr("EXISTS (SELECT 1 FROM portal_shares ps WHERE ps.asset_id = portal_assets.id AND "+
		activeShare+" AND ("+addressed+"))", args...)
	viaCollection := sq.Expr("EXISTS (SELECT 1 FROM portal_shares ps JOIN "+liveCollectionShare+
		" JOIN portal_collection_sections cs ON cs.collection_id = ps.collection_id"+
		" JOIN portal_collection_items ci ON ci.section_id = cs.id"+
		" WHERE ci.asset_id = portal_assets.id AND "+activeShare+" AND ("+addressed+"))", args...)
	return narrow(r, owned, sq.Or{direct, viaCollection})
}

// CollectionPredicate matches the collections (portal_collections in the outer
// statement) the reach covers: owned by id, which is how a collection records
// ownership (access.CanViewCollection), or named by an active share addressed
// to the reach.
func CollectionPredicate(r *portaldomain.Reach) sq.Sqlizer {
	if !r.Identified() {
		return sq.Expr(matchNothing)
	}
	owned := sq.Expr(matchNothing)
	if r.Owner.UserID != "" {
		owned = sq.Eq{"portal_collections.owner_id": r.Owner.UserID}
	}
	addressed, args := shareAddressedTo(r)
	shared := sq.Expr("EXISTS (SELECT 1 FROM portal_shares ps WHERE ps.collection_id = portal_collections.id AND "+
		activeShare+" AND ("+addressed+"))", args...)
	return narrow(r, owned, shared)
}

// OwnersPredicate matches a row whose owner address (column) is one of owners:
// the author filter.
func OwnersPredicate(column string, owners []string) sq.Sqlizer {
	return sq.Expr("LOWER("+column+") = ANY(?)", pq.Array(portaldomain.NormalizeOwners(owners)))
}

// BuildTagFacet renders the tag counts over a statement selecting a jsonb
// `tags` array per row.
func BuildTagFacet(inner string) string {
	// #nosec G201 -- inner is builder-rendered with bound placeholders; the
	// bound is a constant.
	return fmt.Sprintf("SELECT t.tag, COUNT(*) FROM (%s) a CROSS JOIN LATERAL jsonb_array_elements_text(a.tags) AS t(tag) "+
		"WHERE t.tag <> '' GROUP BY t.tag ORDER BY COUNT(*) DESC, t.tag LIMIT %d", inner, portaldomain.MaxFacetValues)
}

// BuildAuthorFacet renders the owner-address counts over a statement selecting
// owner_email per row.
func BuildAuthorFacet(inner string) string {
	// #nosec G201 -- as BuildTagFacet.
	return fmt.Sprintf("SELECT LOWER(a.owner_email), COUNT(*) FROM (%s) a WHERE a.owner_email <> '' "+
		"GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT %d", inner, portaldomain.MaxFacetValues)
}

// Facet runs a facet statement returning (value, count) rows.
func Facet(ctx context.Context, db *sql.DB, query string, args []any) ([]portaldomain.FacetCount, error) {
	out := []portaldomain.FacetCount{}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("computing facet: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query
	for rows.Next() {
		var fc portaldomain.FacetCount
		if err := rows.Scan(&fc.Value, &fc.Count); err != nil {
			return nil, fmt.Errorf("scanning facet row: %w", err)
		}
		out = append(out, fc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating facet rows: %w", err)
	}
	return out, nil
}

// BuildPermissions renders the highest share permission per asset id the
// reach holds, directly or through a live collection: the two grants
// access.ResolveAssetPermission combines, for a page of rows at once.
func BuildPermissions(ids []string, r *portaldomain.Reach) (query string, args []any, err error) {
	addressed, addrArgs := shareAddressedTo(r)
	direct := sq.Select("ps.asset_id AS asset_id", "ps.permission AS permission").From("portal_shares ps").
		Where(sq.Expr("ps.asset_id = ANY(?)", pq.Array(ids))).Where(activeShare).Where(sq.Expr(addressed, addrArgs...))
	viaCollection := sq.Select("ci.asset_id", "ps.permission").From("portal_shares ps").
		Join(liveCollectionShare).
		Join("portal_collection_sections cs ON cs.collection_id = ps.collection_id").
		Join("portal_collection_items ci ON ci.section_id = cs.id").
		Where(sq.Expr("ci.asset_id = ANY(?)", pq.Array(ids))).Where("ps.collection_id IS NOT NULL").
		Where(activeShare).Where(sq.Expr(addressed, addrArgs...))
	inner, args, err := direct.SuffixExpr(sq.ConcatExpr("UNION ALL ", viaCollection)).ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("building reach permissions: %w", err)
	}
	// Rendered with ? throughout, then numbered once, so the two arms never
	// number their placeholders independently.
	// #nosec G201 -- inner is builder-rendered with bound placeholders.
	query, err = sq.Dollar.ReplacePlaceholders(fmt.Sprintf(
		"SELECT g.asset_id, BOOL_OR(g.permission = '%s') FROM (%s) g GROUP BY g.asset_id",
		portaldomain.PermissionEditor, inner))
	if err != nil {
		return "", nil, fmt.Errorf("numbering reach permissions: %w", err)
	}
	return query, args, nil
}

// Permissions reports the highest share permission the reach holds on each
// asset id a share grants at all. Owned rows are not a share's to answer.
func Permissions(ctx context.Context, db *sql.DB, ids []string, r *portaldomain.Reach) (map[string]portaldomain.SharePermission, error) {
	out := map[string]portaldomain.SharePermission{}
	if len(ids) == 0 || !r.Identified() {
		return out, nil
	}
	query, args, err := BuildPermissions(ids, r)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading reach permissions: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query
	for rows.Next() {
		var (
			id     string
			editor bool
		)
		if err := rows.Scan(&id, &editor); err != nil {
			return nil, fmt.Errorf("scanning reach permission: %w", err)
		}
		out[id] = portaldomain.PermissionViewer
		if editor {
			out[id] = portaldomain.PermissionEditor
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating reach permissions: %w", err)
	}
	return out, nil
}
