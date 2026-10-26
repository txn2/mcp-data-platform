package botcheck

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/lib/pq"
)

var _ Store = (*PostgresChecks)(nil)

// foreignKeyViolation is the PostgreSQL error a delete of a check an endpoint
// names raises: script_endpoints.bot_check references it ON DELETE RESTRICT.
const foreignKeyViolation = "23503"

const checkColumns = `name, provider, site_key, secret_name, difficulty, max_difficulty, rate_threshold,
	challenge_ttl_seconds, created_by, updated_by, created_at, updated_at`

// PostgresChecks is the Store over the bot_checks table of migration 000221.
type PostgresChecks struct {
	db *sql.DB
}

// NewStore is the check store over db.
func NewStore(db *sql.DB) *PostgresChecks { return &PostgresChecks{db: db} }

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCheck(r rowScanner) (Check, error) {
	var c Check
	err := r.Scan(&c.Name, &c.Provider, &c.SiteKey, &c.SecretName, &c.Difficulty, &c.MaxDifficulty,
		&c.RateThreshold, &c.ChallengeTTLSeconds, &c.CreatedBy, &c.UpdatedBy, &c.CreatedAt, &c.UpdatedAt)
	return c, err //nolint:wrapcheck // each caller says what it was reading
}

// List reads every check, by name.
func (p *PostgresChecks) List(ctx context.Context) ([]Check, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+checkColumns+` FROM bot_checks ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing bot checks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Check{}
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, fmt.Errorf("reading a bot check: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing bot checks: %w", err)
	}
	return out, nil
}

// Get reads one check.
func (p *PostgresChecks) Get(ctx context.Context, name string) (Check, error) {
	c, err := scanCheck(p.db.QueryRowContext(ctx, `SELECT `+checkColumns+` FROM bot_checks WHERE name = $1`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Check{}, ErrNotFound
	}
	if err != nil {
		return Check{}, fmt.Errorf("reading bot check %q: %w", name, err)
	}
	return c, nil
}

// Put upserts the check by name.
func (p *PostgresChecks) Put(ctx context.Context, c Check, author string) (Check, error) {
	out, err := scanCheck(p.db.QueryRowContext(ctx, `
		INSERT INTO bot_checks (name, provider, site_key, secret_name, difficulty, max_difficulty, rate_threshold,
		                        challenge_ttl_seconds, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		ON CONFLICT (name) DO UPDATE
		   SET provider = EXCLUDED.provider, site_key = EXCLUDED.site_key, secret_name = EXCLUDED.secret_name,
		       difficulty = EXCLUDED.difficulty, max_difficulty = EXCLUDED.max_difficulty,
		       rate_threshold = EXCLUDED.rate_threshold, challenge_ttl_seconds = EXCLUDED.challenge_ttl_seconds,
		       updated_by = EXCLUDED.updated_by, updated_at = NOW()
		RETURNING `+checkColumns,
		c.Name, c.Provider, c.SiteKey, c.SecretName, c.Difficulty, c.MaxDifficulty, c.RateThreshold,
		c.ChallengeTTLSeconds, author))
	if err != nil {
		return Check{}, fmt.Errorf("writing bot check %q: %w", c.Name, err)
	}
	return out, nil
}

// Delete removes the check. The foreign key refuses one an endpoint names, on
// every replica, and the refusal names those endpoints.
func (p *PostgresChecks) Delete(ctx context.Context, name string) error {
	res, err := p.db.ExecContext(ctx, `DELETE FROM bot_checks WHERE name = $1`, name)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == foreignKeyViolation {
		used, uerr := p.UsedBy(ctx)
		if uerr != nil {
			return uerr
		}
		return &InUseError{Name: name, Endpoints: used[name]}
	}
	if err != nil {
		return fmt.Errorf("deleting bot check %q: %w", name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UsedBy reads which endpoints name each check.
func (p *PostgresChecks) UsedBy(ctx context.Context) (map[string][]string, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT bot_check, name FROM script_endpoints WHERE bot_check IS NOT NULL ORDER BY bot_check, name`)
	if err != nil {
		return nil, fmt.Errorf("reading the endpoints each bot check is used by: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]string{}
	for rows.Next() {
		var check, endpoint string
		if err := rows.Scan(&check, &endpoint); err != nil {
			return nil, fmt.Errorf("reading an endpoint's bot check: %w", err)
		}
		out[check] = append(out[check], endpoint)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the endpoints each bot check is used by: %w", err)
	}
	for k := range out {
		slices.Sort(out[k])
	}
	return out, nil
}
