package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/portal/assetsimilar"
)

// Grouper finds the groups of one owner's near-identical assets: the
// closeness save_asset's similar_assets reports, in one implementation
// (internal/portal/assetsimilar).
type Grouper interface {
	Groups(ctx context.Context, q assetsimilar.GroupsQuery) ([]assetsimilar.Group, error)
}

// DuplicateMember is one asset of a possible-duplicates group.
type DuplicateMember struct {
	ID         string    `json:"id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	Name       string    `json:"name" example:"Q3 revenue by region"`
	OwnerEmail string    `json:"owner_email,omitempty" example:"jane@example.com"`
	Version    int       `json:"version" example:"4"`
	UpdatedAt  time.Time `json:"updated_at"`
	Status     string    `json:"status,omitempty" example:"published"`
	Opens      int64     `json:"opens" example:"41"`
}

// DuplicateGroup is a set of one owner's assets whose content is nearly the
// same. Digest names the group as it stands: its members and the version each
// is at, so a dismissal lapses when one of them changes.
type DuplicateGroup struct {
	Digest string `json:"digest" example:"9f2c4e"`
	// Similarity is the closest pair's similarity, as a percentage.
	Similarity int               `json:"similarity" example:"96"`
	Members    []DuplicateMember `json:"members"`
}

// Duplicates lists the groups of near-identical assets in scope that the
// person has not dismissed, the most alike first, members newest first.
func (s *Store) Duplicates(ctx context.Context, userID string, scope Scope) ([]DuplicateGroup, error) {
	if s.groups == nil {
		return []DuplicateGroup{}, nil
	}
	q := assetsimilar.GroupsQuery{}
	if !scope.Everyone {
		owner := scope.Owner
		q.Owner = &owner
	}
	found, err := s.groups.Groups(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("finding possible duplicates: %w", err)
	}
	if len(found) == 0 {
		return []DuplicateGroup{}, nil
	}
	extra, err := s.memberCounts(ctx, found)
	if err != nil {
		return nil, err
	}
	groups := assemble(found, extra)
	dismissed, err := s.dismissed(ctx, userID, groups)
	if err != nil {
		return nil, err
	}
	out := make([]DuplicateGroup, 0, len(groups))
	for _, g := range groups {
		if !dismissed[g.Digest] {
			out = append(out, g)
		}
	}
	return out, nil
}

// memberExtra is what a group member carries beyond what the closeness
// query reads: its status and its opens.
type memberExtra struct {
	status string
	opens  int64
}

const memberCountsQuery = `
	SELECT a.id, COALESCE(st.status, ''), COALESCE(o.opens, 0)
	  FROM portal_assets a
	  LEFT JOIN ` + openTotals + `
	  LEFT JOIN portal_asset_status st ON st.asset_id = a.id
	 WHERE a.id = ANY($1)`

// memberCounts reads the status and opens of every member the groups name.
func (s *Store) memberCounts(ctx context.Context, groups []assetsimilar.Group) (map[string]memberExtra, error) {
	var ids []string
	for _, g := range groups {
		for _, m := range g.Members {
			ids = append(ids, m.AssetID)
		}
	}
	rows, err := s.db.QueryContext(ctx, memberCountsQuery, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("reading duplicate members: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]memberExtra, len(ids))
	for rows.Next() {
		var id string
		var e memberExtra
		if err := rows.Scan(&id, &e.status, &e.opens); err != nil {
			return nil, fmt.Errorf("scanning a duplicate member: %w", err)
		}
		out[id] = e
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading duplicate members: %w", err)
	}
	return out, nil
}

// assemble carries the groups into the library's shape, each named by its
// digest.
func assemble(found []assetsimilar.Group, extra map[string]memberExtra) []DuplicateGroup {
	groups := make([]DuplicateGroup, 0, len(found))
	for _, f := range found {
		g := DuplicateGroup{Similarity: int(math.Floor(f.Similarity * 100))}
		for _, m := range f.Members {
			e := extra[m.AssetID]
			g.Members = append(g.Members, DuplicateMember{
				ID: m.AssetID, Name: m.Name, OwnerEmail: m.OwnerEmail, Version: m.CurrentVersion,
				UpdatedAt: m.UpdatedAt, Status: e.status, Opens: e.opens,
			})
		}
		if len(g.Members) < 2 {
			continue
		}
		g.Digest = Digest(g.Members)
		groups = append(groups, g)
	}
	return groups
}

// Digest names a group by its members and the version each is at.
func Digest(members []DuplicateMember) string {
	keys := make([]string, 0, len(members))
	for _, m := range members {
		keys = append(keys, m.ID+":"+strconv.Itoa(m.Version))
	}
	slices.Sort(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, ",")))
	return hex.EncodeToString(sum[:])
}

const (
	dismissedQuery = `SELECT group_digest FROM portal_duplicate_dismissals WHERE user_id = $1 AND group_digest = ANY($2)`
	dismissQuery   = `INSERT INTO portal_duplicate_dismissals (user_id, group_digest) VALUES ($1, $2)
		ON CONFLICT (user_id, group_digest) DO NOTHING`
	undismissQuery = `DELETE FROM portal_duplicate_dismissals WHERE user_id = $1 AND group_digest = $2`
)

func (s *Store) dismissed(ctx context.Context, userID string, groups []DuplicateGroup) (map[string]bool, error) {
	out := map[string]bool{}
	if userID == "" || len(groups) == 0 {
		return out, nil
	}
	digests := make([]string, 0, len(groups))
	for _, g := range groups {
		digests = append(digests, g.Digest)
	}
	rows, err := s.db.QueryContext(ctx, dismissedQuery, userID, pq.Array(digests))
	if err != nil {
		return nil, fmt.Errorf("reading dismissed groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("scanning a dismissed group: %w", err)
		}
		out[d] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading dismissed groups: %w", err)
	}
	return out, nil
}

// Dismiss hides a group from the person until one of its members changes.
func (s *Store) Dismiss(ctx context.Context, userID, digest string) error {
	return s.digestExec(ctx, dismissQuery, "dismissing a group", userID, digest)
}

// Undismiss shows a dismissed group again.
func (s *Store) Undismiss(ctx context.Context, userID, digest string) error {
	return s.digestExec(ctx, undismissQuery, "undoing a dismissal", userID, digest)
}

func (s *Store) digestExec(ctx context.Context, query, what, userID, digest string) error {
	if userID == "" {
		return ErrNoUser
	}
	if _, err := s.db.ExecContext(ctx, query, userID, digest); err != nil {
		return fmt.Errorf("library: %s: %w", what, err)
	}
	return nil
}
