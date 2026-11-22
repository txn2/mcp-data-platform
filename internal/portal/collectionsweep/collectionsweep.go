// Package collectionsweep removes from each collection the items its owner
// cannot open (#2752). A collection's share opens every asset the collection
// holds, so an asset goes into a collection only when the person putting it
// there can open it (portaldomain.AddedAssets); this sweep holds the items a
// collection already has to the same rule: the owner can open the asset now,
// as an administrator, as its owner, through a share of the asset, or through
// another collection's share whose item is itself held to the rule. Items put
// in before the add rule existed, and items whose owner has since lost access,
// are removed; the asset itself is untouched.
//
// The collection share is followed rather than taken at its word, so two
// collections that each hold an asset only because the other's share opens it
// do not keep each other's item: an item stays when a chain of shares reaches
// someone who can open the asset without one.
//
// The sweep runs with the retention loop, under its own advisory lock, and
// records each removal (collection, asset, time; no content) in
// portal_collection_item_removals. A second pass over the same data removes
// nothing.
package collectionsweep

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// People builds the principal a collection's owner is judged as. An owner the
// platform holds no roles for is judged with none: only a grant addressed to
// their id or address opens an asset to them.
type People interface {
	Person(ctx context.Context, userID, email string) (user *access.User, persona string)
}

// Access is the authorization core an item is judged by.
type Access interface {
	IsAdmin(user *access.User) bool
	DirectViewGrant(ctx context.Context, assetID string, asset *portaldomain.Asset, user *access.User) (bool, error)
}

// Grants lists the collections whose share opens an asset to a person.
type Grants interface {
	CollectionsGrantingAsset(ctx context.Context, assetID, userID, email string) ([]string, error)
}

// Sweeper removes the items owners cannot open.
type Sweeper struct {
	db     *sql.DB
	people People
	access Access
	grants Grants
}

// New returns a Sweeper, or nil when any dependency is missing.
func New(db *sql.DB, people People, acc Access, grants Grants) *Sweeper {
	if db == nil || people == nil || acc == nil || grants == nil {
		return nil
	}
	return &Sweeper{db: db, people: people, access: acc, grants: grants}
}

// pair is one asset in one collection, however many of its sections hold it.
type pair struct{ collection, asset string }

// held is a pair as the sweep reads it: the collection's owner and the
// asset's.
type held struct {
	pair
	ownerID, ownerEmail    string
	assetOwner, assetEmail string
}

// listItems reads every collection item of a live asset, once per pair.
const listItems = `
SELECT DISTINCT c.id, ci.asset_id, c.owner_id, c.owner_email, a.owner_id, a.owner_email
  FROM portal_collection_items ci
  JOIN portal_collection_sections cs ON cs.id = ci.section_id
  JOIN portal_collections c ON c.id = cs.collection_id
  JOIN portal_assets a ON a.id = ci.asset_id
 WHERE a.deleted_at IS NULL
 ORDER BY c.id, ci.asset_id`

// removeItems deletes a pair's items and records the removal, in one
// statement, so a removal is recorded exactly when it happened.
const removeItems = `
WITH gone AS (
    DELETE FROM portal_collection_items ci
     USING portal_collection_sections cs
     WHERE ci.section_id = cs.id AND cs.collection_id = $1 AND ci.asset_id = $2
    RETURNING ci.id)
INSERT INTO portal_collection_item_removals (collection_id, asset_id, items)
SELECT $1, $2, COUNT(*) FROM gone HAVING COUNT(*) > 0`

// Run removes every item its collection's owner cannot open and reports how
// many pairs it removed. An item whose judgment could not complete (a store
// error) stays.
func (s *Sweeper) Run(ctx context.Context) (int64, error) {
	items, err := s.list(ctx)
	if err != nil {
		return 0, err
	}
	var removed int64
	for _, p := range s.unopenable(ctx, items) {
		res, err := s.db.ExecContext(ctx, removeItems, p.collection, p.asset)
		if err != nil {
			return removed, fmt.Errorf("removing a collection item: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			removed++
			slog.InfoContext(ctx, "collection sweep: removed an item its owner cannot open",
				"collection_id", logsan.SanitizeForLog(p.collection), "asset_id", logsan.SanitizeForLog(p.asset))
		}
	}
	return removed, nil
}

func (s *Sweeper) list(ctx context.Context) ([]held, error) {
	rows, err := s.db.QueryContext(ctx, listItems)
	if err != nil {
		return nil, fmt.Errorf("listing collection items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.collection, &h.asset, &h.ownerID, &h.ownerEmail, &h.assetOwner, &h.assetEmail); err != nil {
			return nil, fmt.Errorf("scanning a collection item: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading collection items: %w", err)
	}
	return out, nil
}

// unopenable judges each pair and returns those no chain of grants reaches.
// A pair is open when its owner administers the platform or can open the
// asset without a collection share; otherwise it waits on the collections
// whose share opens the asset to the owner, and opens once one of those
// collections' own pair has. Pairs still waiting when nothing more opens are
// unopenable.
func (s *Sweeper) unopenable(ctx context.Context, items []held) []pair {
	open := map[pair]bool{}
	waiting := map[pair][]string{}
	for _, h := range items {
		via, ok := s.judge(ctx, h)
		if ok {
			open[h.pair] = true
			continue
		}
		waiting[h.pair] = via
	}
	settle(open, waiting)
	out := make([]pair, 0, len(waiting))
	for _, h := range items {
		if _, ok := waiting[h.pair]; ok {
			out = append(out, h.pair)
		}
	}
	return out
}

// settle opens each waiting pair one of whose collections has its own pair
// open, pass after pass, until a pass opens nothing: what still waits then is
// reached by no chain from someone who opens the asset without a collection.
func settle(open map[pair]bool, waiting map[pair][]string) {
	for opened := true; opened; {
		opened = false
		for p, via := range waiting {
			if slices.ContainsFunc(via, func(c string) bool { return open[pair{collection: c, asset: p.asset}] }) {
				open[p] = true
				delete(waiting, p)
				opened = true
			}
		}
	}
}

// judge reports whether the pair's owner can open the asset without a
// collection share (a check that failed keeps the item), and otherwise the
// collections whose share opens it to them.
func (s *Sweeper) judge(ctx context.Context, h held) (via []string, open bool) {
	user, persona := s.people.Person(ctx, h.ownerID, h.ownerEmail)
	if s.access.IsAdmin(user) {
		return nil, true
	}
	ctx = portaldomain.WithCallerPersona(ctx, persona)
	asset := &portaldomain.Asset{ID: h.asset, OwnerID: h.assetOwner, OwnerEmail: h.assetEmail}
	direct, err := s.access.DirectViewGrant(ctx, h.asset, asset, user)
	if err != nil || direct {
		return nil, true
	}
	via, err = s.grants.CollectionsGrantingAsset(ctx, h.asset, user.UserID, user.Email)
	if err != nil {
		return nil, true
	}
	return via, false
}
