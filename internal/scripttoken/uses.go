package scripttoken

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Uses records the single-use tokens that have been verified, so a second use
// is refused on every replica.
type Uses interface {
	// Use records the token's id until it expires and reports whether this
	// was its first use.
	Use(ctx context.Context, id string, expires time.Time) (bool, error)
	// Sweep deletes the records of tokens that have expired.
	Sweep(ctx context.Context, now time.Time) (int64, error)
}

// PostgresUses is Uses over the script_token_uses table.
type PostgresUses struct {
	db *sql.DB
}

// NewUses is the use store over db.
func NewUses(db *sql.DB) *PostgresUses { return &PostgresUses{db: db} }

// Use inserts the id, and the primary key decides whether it was the first:
// two replicas verifying the same token at once both insert, and one insert
// finds the row the other wrote.
func (p *PostgresUses) Use(ctx context.Context, id string, expires time.Time) (bool, error) {
	var got string
	err := p.db.QueryRowContext(ctx, `
		INSERT INTO script_token_uses (token_id, expires_at)
		VALUES ($1, $2)
		ON CONFLICT (token_id) DO NOTHING
		RETURNING token_id`, id, expires).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("recording a token's use: %w", err)
	}
	return true, nil
}

// Sweep deletes the uses of tokens that can no longer verify.
func (p *PostgresUses) Sweep(ctx context.Context, now time.Time) (int64, error) {
	res, err := p.db.ExecContext(ctx, `DELETE FROM script_token_uses WHERE expires_at < $1`, now)
	if err != nil {
		return 0, fmt.Errorf("sweeping token uses: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
