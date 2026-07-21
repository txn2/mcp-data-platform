package shareguest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Link is one issued one-time link. Only the token's SHA-256 is stored; the
// plaintext exists solely in the emailed URL.
type Link struct {
	ID        string
	ShareID   string
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// LinkStore persists one-time links and enforces their single use.
type LinkStore interface {
	// Insert records a freshly issued link.
	Insert(ctx context.Context, l Link) error
	// Claim atomically marks the link with tokenHash used, provided it belongs
	// to shareID, is unused, and is unexpired at now. ok reports whether the
	// claim won; a second claim of the same token loses.
	Claim(ctx context.Context, tokenHash, shareID string, now time.Time) (ok bool, err error)
	// CountSince returns how many links were issued for shareID after since,
	// backing the per-share issue cap.
	CountSince(ctx context.Context, shareID string, since time.Time) (int, error)
}

// PostgresLinkStore implements LinkStore on portal_share_guest_links.
type PostgresLinkStore struct {
	db *sql.DB
}

// NewPostgresLinkStore creates the production link store.
func NewPostgresLinkStore(db *sql.DB) *PostgresLinkStore {
	return &PostgresLinkStore{db: db}
}

// purgeAge is how long past expiry a link row is kept before Insert sweeps
// it. The margin keeps recent rows visible to the per-share issue cap
// (linkCapWindow) while bounding the table: without the sweep every request
// would add a row forever.
const purgeAge = 24 * time.Hour

// Insert records a freshly issued link, first sweeping rows long past their
// expiry so the table stays bounded by recent activity.
func (s *PostgresLinkStore) Insert(ctx context.Context, l Link) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM portal_share_guest_links WHERE expires_at < $1`,
		l.CreatedAt.Add(-purgeAge)); err != nil {
		return fmt.Errorf("purging expired share guest links: %w", err)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO portal_share_guest_links (id, share_id, token_hash, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		l.ID, l.ShareID, l.TokenHash, l.CreatedAt, l.ExpiresAt)
	if err != nil {
		return fmt.Errorf("inserting share guest link: %w", err)
	}
	return nil
}

// Claim marks the link used in one atomic statement, so a replayed token
// cannot win twice: the row's used_at is both the check and the write.
func (s *PostgresLinkStore) Claim(ctx context.Context, tokenHash, shareID string, now time.Time) (bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`UPDATE portal_share_guest_links
		 SET used_at = $3
		 WHERE token_hash = $1 AND share_id = $2 AND used_at IS NULL AND expires_at > $3
		 RETURNING id`,
		tokenHash, shareID, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claiming share guest link: %w", err)
	}
	return true, nil
}

// CountSince counts links issued for the share inside the cap window.
func (s *PostgresLinkStore) CountSince(ctx context.Context, shareID string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM portal_share_guest_links
		 WHERE share_id = $1 AND created_at > $2`,
		shareID, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting share guest links: %w", err)
	}
	return n, nil
}

// Verify interface compliance.
var _ LinkStore = (*PostgresLinkStore)(nil)
