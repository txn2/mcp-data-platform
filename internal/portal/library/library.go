// Package library is a person's way back to their work in the portal (#2504,
// #2509): the assets and collections they starred, what they opened recently,
// what they deleted, what nobody opens, and which of their assets look alike.
//
// Nothing here grants anything. Every read that names an item someone else
// may own joins it to that item under the portal's reach rule
// (internal/portal/reachstore), so a starred or opened item the person can no
// longer open is simply absent: no name, no count, no placeholder. The
// listings that are an owner's to see (trash, unused, possible duplicates)
// read only the owner's rows unless the caller is an administrator asking for
// everyone's, which the HTTP layer decides.
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/reachstore"
)

// Kind is what a library entry names: an asset or a collection.
type Kind string

// The kinds a favorite, an open and a trash entry name.
const (
	KindAsset      Kind = "asset"
	KindCollection Kind = "collection"
)

// ParseKind reads a kind from a request, reporting whether it is one.
func ParseKind(s string) (Kind, bool) {
	switch Kind(s) {
	case KindAsset, KindCollection:
		return Kind(s), true
	default:
		return "", false
	}
}

// Limits on the lists a person reads.
const (
	// DefaultLimit is how many entries a list returns when the caller names
	// no limit.
	DefaultLimit = 20
	// MaxLimit is the most entries one read returns.
	MaxLimit = 200
	// recountAfter is how long after an open a second open of the same item
	// by the same person is counted again, so a page that reports its open
	// twice while it loads counts once.
	recountAfter = "30 seconds"
)

// ErrNoUser is returned when a write names nobody.
var ErrNoUser = errors.New("a user id is required")

// Item is one entry of a person's recents or favorites.
type Item struct {
	Kind        Kind   `json:"kind" example:"asset"`
	ID          string `json:"id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	Name        string `json:"name" example:"Q3 revenue by region"`
	ContentType string `json:"content_type,omitempty" example:"text/html"`
	OwnerEmail  string `json:"owner_email,omitempty" example:"jane@example.com"`
	// At is when the person last opened the item (recents) or starred it
	// (favorites).
	At time.Time `json:"at"`
}

// Store reads and writes a person's library.
type Store struct {
	db *sql.DB
	// groups finds possible duplicates; nil lists none.
	groups Grouper
}

// New builds a Store over the platform database. groups finds the possible
// duplicates (internal/portal/assetsimilar); nil lists none.
func New(db *sql.DB, groups Grouper) *Store {
	return &Store{db: db, groups: groups}
}

// ClampLimit bounds a requested list length.
func ClampLimit(n int) int {
	switch {
	case n <= 0:
		return DefaultLimit
	case n > MaxLimit:
		return MaxLimit
	default:
		return n
	}
}

const recordOpenQuery = `
	INSERT INTO portal_opens (user_id, target_type, target_id)
	VALUES ($1, $2, $3)
	ON CONFLICT (user_id, target_type, target_id) DO UPDATE SET
	    open_count = portal_opens.open_count +
	        CASE WHEN portal_opens.last_opened_at < NOW() - INTERVAL '` + recountAfter + `' THEN 1 ELSE 0 END,
	    last_opened_at = NOW()`

// RecordOpen records that the person opened the item. The caller checks the
// person may open it first.
func (s *Store) RecordOpen(ctx context.Context, userID string, kind Kind, id string) error {
	if userID == "" {
		return ErrNoUser
	}
	if _, err := s.db.ExecContext(ctx, recordOpenQuery, userID, string(kind), id); err != nil {
		return fmt.Errorf("recording an open: %w", err)
	}
	return nil
}

const (
	starQuery = `INSERT INTO portal_favorites (user_id, target_type, target_id) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, target_type, target_id) DO NOTHING`
	unstarQuery = `DELETE FROM portal_favorites WHERE user_id = $1 AND target_type = $2 AND target_id = $3`
)

// Star adds the item to the person's favorites. The caller checks the person
// may open it first. Starring twice is starring once.
func (s *Store) Star(ctx context.Context, userID string, kind Kind, id string) error {
	return s.exec(ctx, starQuery, "starring", userID, ref{kind, id})
}

// Unstar removes the item from the person's favorites. Removing one that is
// not starred is not an error.
func (s *Store) Unstar(ctx context.Context, userID string, kind Kind, id string) error {
	return s.exec(ctx, unstarQuery, "removing a favorite", userID, ref{kind, id})
}

// ref names one item a per-person row is about.
type ref struct {
	kind Kind
	id   string
}

func (s *Store) exec(ctx context.Context, query, what, userID string, item ref) error {
	if userID == "" {
		return ErrNoUser
	}
	if _, err := s.db.ExecContext(ctx, query, userID, string(item.kind), item.id); err != nil {
		return fmt.Errorf("library: %s: %w", what, err)
	}
	return nil
}

// Recents lists the items the person opened, newest first, that they can
// still open.
func (s *Store) Recents(ctx context.Context, userID string, reach *portaldomain.Reach, limit int) ([]Item, error) {
	return s.items(ctx, perPerson{"portal_opens", "last_opened_at"}, userID, reach, limit)
}

// Favorites lists the items the person starred, newest first, that they can
// still open.
func (s *Store) Favorites(ctx context.Context, userID string, reach *portaldomain.Reach, limit int) ([]Item, error) {
	return s.items(ctx, perPerson{"portal_favorites", "created_at"}, userID, reach, limit)
}

// items reads one of the two per-person tables joined to the assets and
// collections it names, each under the reach.
// perPerson is one of the two per-person tables and the column it is
// ordered by.
type perPerson struct {
	table, at string
}

func (s *Store) items(ctx context.Context, src perPerson, userID string, reach *portaldomain.Reach, limit int) ([]Item, error) {
	if userID == "" || !reach.Identified() {
		return []Item{}, nil
	}
	all := *reach
	all.Scope = portaldomain.ScopeAll
	query, args, err := buildItems(src.table, src.at, userID, &all, ClampLimit(limit))
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading the library: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Item{}
	for rows.Next() {
		var it Item
		var kind string
		if err := rows.Scan(&kind, &it.ID, &it.Name, &it.ContentType, &it.OwnerEmail, &it.At); err != nil {
			return nil, fmt.Errorf("scanning a library entry: %w", err)
		}
		it.Kind = Kind(kind)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the library: %w", err)
	}
	return out, nil
}

// buildItems renders the union of a person's asset and collection entries in
// table, each joined to its item under the reach, newest first.
func buildItems(table, at, userID string, reach *portaldomain.Reach, limit int) (query string, args []any, err error) {
	assets := sq.Select("'asset'", "portal_assets.id", "portal_assets.name",
		"portal_assets.content_type", "portal_assets.owner_email", "e."+at+" AS at").
		From(table + " e").
		Join("portal_assets ON portal_assets.id = e.target_id").
		Where(sq.Eq{"e.user_id": userID, "e.target_type": string(KindAsset)}).
		Where("portal_assets.deleted_at IS NULL").
		Where(reachstore.AssetPredicate(reach))
	colls := sq.Select("'collection'", "portal_collections.id", "portal_collections.name",
		"''", "portal_collections.owner_email", "e."+at+" AS at").
		From(table + " e").
		Join("portal_collections ON portal_collections.id = e.target_id").
		Where(sq.Eq{"e.user_id": userID, "e.target_type": string(KindCollection)}).
		Where("portal_collections.deleted_at IS NULL").
		Where(reachstore.CollectionPredicate(reach))
	return union("ORDER BY at DESC LIMIT ?", []any{limit}, assets, colls)
}

// union renders parts as one UNION ALL followed by tail, numbered for
// PostgreSQL, with tailArgs after every part's arguments.
func union(tail string, tailArgs []any, parts ...sq.SelectBuilder) (query string, args []any, err error) {
	rendered := make([]string, 0, len(parts))
	for _, p := range parts {
		q, a, err := p.ToSql()
		if err != nil {
			return "", nil, fmt.Errorf("building a library query: %w", err)
		}
		rendered = append(rendered, "("+q+")")
		args = append(args, a...)
	}
	query, err = sq.Dollar.ReplacePlaceholders(strings.Join(rendered, " UNION ALL ") + " " + tail)
	if err != nil {
		return "", nil, fmt.Errorf("numbering a library query: %w", err)
	}
	return query, append(args, tailArgs...), nil
}

// UserKey is the identifier a person's favorites, opens and dismissals are
// recorded under: their user id, or their address, lowercased, when the
// credential carried no id. The portal and the asset toolkit both key by it,
// so an agent lists the recents the portal recorded.
func UserKey(userID, email string) string {
	if id := strings.TrimSpace(userID); id != "" {
		return id
	}
	return strings.ToLower(strings.TrimSpace(email))
}
