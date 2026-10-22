package extaccount

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store keeps the accounts and their sign-in links.
type Store interface {
	Insert(ctx context.Context, a Account) error
	Get(ctx context.Context, id string) (*Account, error)
	GetByEmail(ctx context.Context, email string) (*Account, error)
	List(ctx context.Context) ([]Account, error)
	SetExpiry(ctx context.Context, id string, expiresAt *time.Time) error
	SetSuspended(ctx context.Context, id string, at *time.Time) error
	Delete(ctx context.Context, id string) error
	// Convert closes the account for the address, if it has an open one,
	// and reports whether it did.
	Convert(ctx context.Context, email string, at time.Time) (bool, error)
	TouchSignIn(ctx context.Context, id string, at time.Time) error

	InsertLink(ctx context.Context, tokenHash, accountID string, expiresAt time.Time) error
	// ClaimLink marks an unclaimed, unexpired link claimed and returns its
	// account, in one statement so a link is claimed once on any replica.
	ClaimLink(ctx context.Context, tokenHash string, now time.Time) (string, bool, error)
	CountLinksSince(ctx context.Context, accountID string, since time.Time) (int, error)
}

// PostgresStore is the Store over external_accounts and external_signin_links.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore returns the store over db.
func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

const accountColumns = `id, email, name, persona, expires_at, suspended_at, created_by, created_at, last_sign_in_at, converted_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanAccount(r rowScanner) (*Account, error) {
	var a Account
	var expires, suspended, lastSignIn, converted sql.NullTime
	if err := r.Scan(&a.ID, &a.Email, &a.Name, &a.Persona, &expires, &suspended,
		&a.CreatedBy, &a.CreatedAt, &lastSignIn, &converted); err != nil {
		return nil, err //nolint:wrapcheck // the caller names the read
	}
	a.ExpiresAt, a.SuspendedAt = timePtr(expires), timePtr(suspended)
	a.LastSignInAt, a.ConvertedAt = timePtr(lastSignIn), timePtr(converted)
	return &a, nil
}

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// Insert stores a new account; an address with one already is ErrExists.
func (s *PostgresStore) Insert(ctx context.Context, a Account) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO external_accounts (id, email, name, persona, expires_at, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		a.ID, a.Email, a.Name, a.Persona, a.ExpiresAt, a.CreatedBy)
	if err != nil && strings.Contains(err.Error(), "external_accounts_email_idx") {
		return ErrExists
	}
	if err != nil {
		return fmt.Errorf("storing external account: %w", err)
	}
	return nil
}

// Get returns the account with id, or ErrNotFound.
func (s *PostgresStore) Get(ctx context.Context, id string) (*Account, error) {
	a, err := scanAccount(s.db.QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM external_accounts WHERE id = $1`, id))
	return a, notFound(err)
}

// GetByEmail returns the account for an address, compared case-insensitively.
func (s *PostgresStore) GetByEmail(ctx context.Context, email string) (*Account, error) {
	a, err := scanAccount(s.db.QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM external_accounts WHERE lower(email) = lower($1)`, email))
	return a, notFound(err)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("reading external account: %w", err)
	}
	return nil
}

// List returns every account, newest first.
func (s *PostgresStore) List(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+accountColumns+` FROM external_accounts ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("listing external accounts: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort close after the read
	out := make([]Account, 0)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("reading external account: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing external accounts: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) update(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating external account: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetExpiry sets or clears the account's expiry.
func (s *PostgresStore) SetExpiry(ctx context.Context, id string, expiresAt *time.Time) error {
	return s.update(ctx, `UPDATE external_accounts SET expires_at = $2 WHERE id = $1`, id, expiresAt)
}

// SetSuspended suspends the account at at, or resumes it when at is nil.
func (s *PostgresStore) SetSuspended(ctx context.Context, id string, at *time.Time) error {
	return s.update(ctx, `UPDATE external_accounts SET suspended_at = $2 WHERE id = $1`, id, at)
}

// Delete removes the account and its links.
func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	return s.update(ctx, `DELETE FROM external_accounts WHERE id = $1`, id)
}

// Convert closes the open account for an address and kills its links.
func (s *PostgresStore) Convert(ctx context.Context, email string, at time.Time) (bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`UPDATE external_accounts SET converted_at = $2
		 WHERE lower(email) = lower($1) AND converted_at IS NULL RETURNING id`, email, at).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("converting external account: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM external_signin_links WHERE account_id = $1`, id); err != nil {
		return true, fmt.Errorf("removing a converted account's links: %w", err)
	}
	return true, nil
}

// TouchSignIn records a sign-in.
func (s *PostgresStore) TouchSignIn(ctx context.Context, id string, at time.Time) error {
	return s.update(ctx, `UPDATE external_accounts SET last_sign_in_at = $2 WHERE id = $1`, id, at)
}

// InsertLink stores a link's hash.
func (s *PostgresStore) InsertLink(ctx context.Context, tokenHash, accountID string, expiresAt time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO external_signin_links (token_hash, account_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, accountID, expiresAt); err != nil {
		return fmt.Errorf("storing sign-in link: %w", err)
	}
	return nil
}

// ClaimLink claims a live link and returns its account id; claimed is false
// for a link that is unknown, used or expired.
func (s *PostgresStore) ClaimLink(ctx context.Context, tokenHash string, now time.Time) (accountID string, claimed bool, err error) {
	var id string
	err = s.db.QueryRowContext(ctx,
		`UPDATE external_signin_links SET claimed_at = $2
		 WHERE token_hash = $1 AND claimed_at IS NULL AND expires_at > $2
		 RETURNING account_id`, tokenHash, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("claiming sign-in link: %w", err)
	}
	return id, true, nil
}

// CountLinksSince counts the links issued for an account since a moment.
func (s *PostgresStore) CountLinksSince(ctx context.Context, accountID string, since time.Time) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM external_signin_links WHERE account_id = $1 AND created_at >= $2`,
		accountID, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting sign-in links: %w", err)
	}
	return n, nil
}

// RevokeSharesTo revokes every share to an address.
func (s *PostgresStore) RevokeSharesTo(ctx context.Context, email string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE portal_shares SET revoked = TRUE
		 WHERE revoked = FALSE AND LOWER(shared_with_email) = LOWER($1)`, email); err != nil {
		return fmt.Errorf("revoking shares: %w", err)
	}
	return nil
}

// CountSharesTo counts the live shares to an address.
func (s *PostgresStore) CountSharesTo(ctx context.Context, email string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM portal_shares
		 WHERE revoked = FALSE AND (expires_at IS NULL OR expires_at > NOW())
		   AND LOWER(shared_with_email) = LOWER($1)`, email).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting shares: %w", err)
	}
	return n, nil
}
