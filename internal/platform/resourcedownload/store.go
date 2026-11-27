package resourcedownload

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// postgresUses records single downloads in resource_download_uses.
type postgresUses struct {
	DB *sql.DB
}

// NewUses is the record over db, nil (an empty Uses, so New hands out no
// links) without one.
func NewUses(db *sql.DB) Uses {
	if db == nil {
		return nil
	}
	return &postgresUses{DB: db}
}

// claim implements Uses: one statement records the download, so two
// downloads racing for one link cannot both have it. Rows long past their
// link's expiry are swept first.
func (u *postgresUses) claim(ctx context.Context, id, resourceID string, expires, now time.Time) (bool, error) {
	if _, err := u.DB.ExecContext(ctx, `DELETE FROM resource_download_uses WHERE expires_at < $1`,
		now.Add(-keptAfterExpiry).UTC()); err != nil {
		return false, fmt.Errorf("sweeping used download links: %w", err)
	}
	res, err := u.DB.ExecContext(ctx,
		`INSERT INTO resource_download_uses (link_id, resource_id, used_at, expires_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (link_id) DO NOTHING`, id, resourceID, now.UTC(), expires.UTC())
	if err != nil {
		return false, fmt.Errorf("recording a download: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("recording a download: %w", err)
	}
	return n == 1, nil
}
