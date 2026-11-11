package sitestore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// Media is the resource folders a site serves at URL prefixes (#2591), by
// prefix.
func (s *Store) Media(ctx context.Context, siteID string) ([]site.Mount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT prefix, scope, scope_id, folder, created_by, created_at FROM site_media WHERE site_id = $1 ORDER BY prefix`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list media: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []site.Mount{}
	for rows.Next() {
		var m site.Mount
		if err := rows.Scan(&m.Prefix, &m.Scope, &m.ScopeID, &m.Folder, &m.CreatedBy, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("read media: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list media: %w", err)
	}
	return out, nil
}

// SetMedia makes a site's mounts exactly these, in one transaction: a mount
// kept at its prefix keeps who added it and when.
func (s *Store) SetMedia(ctx context.Context, siteID string, mounts []site.Mount, actor string) error {
	prefixes := make([]string, 0, len(mounts))
	for _, m := range mounts {
		prefixes = append(prefixes, m.Prefix)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM site_media WHERE site_id = $1 AND NOT (prefix = ANY($2))`, siteID, pq.Array(prefixes)); err != nil {
			return fmt.Errorf("remove media: %w", err)
		}
		for _, m := range mounts {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO site_media (site_id, prefix, scope, scope_id, folder, created_by) VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (site_id, prefix) DO UPDATE SET scope = EXCLUDED.scope, scope_id = EXCLUDED.scope_id, folder = EXCLUDED.folder,
				    created_by = CASE WHEN (site_media.scope, site_media.scope_id, site_media.folder) = (EXCLUDED.scope, EXCLUDED.scope_id, EXCLUDED.folder)
				                      THEN site_media.created_by ELSE EXCLUDED.created_by END`,
				siteID, m.Prefix, m.Scope, m.ScopeID, m.Folder, actor); err != nil {
				return fmt.Errorf("mount %s: %w", m.Prefix, err)
			}
		}
		return nil
	})
}
