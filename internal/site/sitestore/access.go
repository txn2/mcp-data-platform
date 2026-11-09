package sitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// Grants lists who has been given access to a site.
func (s *Store) Grants(ctx context.Context, siteID string) ([]site.Grant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT email, level, granted_by, created_at FROM site_grants WHERE site_id = $1 ORDER BY level, email`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []site.Grant{}
	for rows.Next() {
		var g site.Grant
		if err := rows.Scan(&g.Email, &g.Level, &g.GrantedBy, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("read grant: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	return out, nil
}

// GrantLevel is the person's level on the site, "" for none.
func (s *Store) GrantLevel(ctx context.Context, siteID, email string) (string, error) {
	var level string
	err := s.db.QueryRowContext(ctx, `SELECT level FROM site_grants WHERE site_id = $1 AND email = $2`, siteID, email).Scan(&level)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read grant: %w", err)
	}
	return level, nil
}

// SetGrant adds a person or changes their level, and fills in g.CreatedAt from
// the row it wrote.
func (s *Store) SetGrant(ctx context.Context, siteID string, g *site.Grant) error {
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO site_grants (site_id, email, level, granted_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (site_id, email) DO UPDATE SET level = EXCLUDED.level, granted_by = EXCLUDED.granted_by, created_at = NOW()
		RETURNING created_at`,
		siteID, g.Email, g.Level, g.GrantedBy).Scan(&g.CreatedAt); err != nil {
		return fmt.Errorf("set grant: %w", err)
	}
	return nil
}

// RemoveGrant takes a person's access away.
func (s *Store) RemoveGrant(ctx context.Context, siteID, email string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM site_grants WHERE site_id = $1 AND email = $2`, siteID, email)
	if err != nil {
		return fmt.Errorf("remove grant: %w", err)
	}
	return oneRow(res, site.ErrNoGrant)
}

// SiteAccess reads, in one statement, what decides whether email may open the
// site. It is read on every request and held nowhere, so removing a person
// takes effect on their next request on every replica.
func (s *Store) SiteAccess(ctx context.Context, siteID, email string) (*site.Access, error) {
	a := &site.Access{}
	err := s.db.QueryRowContext(ctx, `
		SELECT s.owner_email, s.internal_view, COALESCE(g.level, '')
		  FROM sites s
		  LEFT JOIN site_grants g ON g.site_id = s.id AND g.email = $2
		 WHERE s.id = $1`, siteID, site.NormalizeEmail(email)).Scan(&a.OwnerEmail, &a.InternalView, &a.Level)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, site.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read access: %w", err)
	}
	return a, nil
}
