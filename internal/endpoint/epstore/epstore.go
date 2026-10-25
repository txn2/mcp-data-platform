// Package epstore is the PostgreSQL store of endpoint bindings and the failed
// requests they answered (#2171, #2172): the script_endpoints and
// script_endpoint_failures tables of migration 000203.
package epstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
)

var _ epbinding.Store = (*Store)(nil)

// Listing bounds.
const (
	// MaxListLimit is the most bindings one page of a listing holds.
	MaxListLimit        = 200
	defaultFailureLimit = 50
)

// nameConstraint is the unique constraint PostgreSQL names for
// script_endpoints.name, which a second binding with the same name violates.
const nameConstraint = "script_endpoints_name_key"

// columns is the column list every script_endpoints SELECT reads, in scan's
// order.
const columns = `id, script_id, name, methods, path, auth_mode, open_reads, timeout_seconds,
	max_memory_bytes, max_body_bytes, allowed_origins, rate_limit, caller_rate_limit, enabled,
	created_by, updated_by, created_at, updated_at`

const selectBinding = "SELECT " + columns + " FROM script_endpoints"

// Store is the PostgreSQL binding store.
type Store struct {
	db *sql.DB
}

// New returns a store over db.
func New(db *sql.DB) *Store { return &Store{db: db} }

type rowScanner interface {
	Scan(dest ...any) error
}

func scan(r rowScanner) (*epbinding.Binding, error) {
	b := &epbinding.Binding{}
	err := r.Scan(&b.ID, &b.ScriptID, &b.Name, pq.Array(&b.Methods), &b.Path, &b.AuthMode, &b.OpenReads,
		&b.TimeoutSeconds, &b.MaxMemoryBytes, &b.MaxBodyBytes, pq.Array(&b.AllowedOrigins), &b.RateLimit,
		&b.CallerRateLimit, &b.Enabled, &b.CreatedBy, &b.UpdatedBy, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err //nolint:wrapcheck // each caller says what it was reading
	}
	if b.AllowedOrigins == nil {
		b.AllowedOrigins = []string{}
	}
	return b, nil
}

// Set creates or replaces a script's binding, keyed by its script, keeping
// the id and creation stamp of the one it replaces.
func (s *Store) Set(ctx context.Context, b *epbinding.Binding) error {
	origins := b.AllowedOrigins
	if origins == nil {
		origins = []string{}
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO script_endpoints (script_id, name, methods, path, auth_mode, open_reads, timeout_seconds,
		                              max_memory_bytes, max_body_bytes, allowed_origins, rate_limit,
		                              caller_rate_limit, enabled, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
		ON CONFLICT (script_id) DO UPDATE
		   SET name = EXCLUDED.name, methods = EXCLUDED.methods, path = EXCLUDED.path,
		       auth_mode = EXCLUDED.auth_mode, open_reads = EXCLUDED.open_reads,
		       timeout_seconds = EXCLUDED.timeout_seconds, max_memory_bytes = EXCLUDED.max_memory_bytes,
		       max_body_bytes = EXCLUDED.max_body_bytes, allowed_origins = EXCLUDED.allowed_origins,
		       rate_limit = EXCLUDED.rate_limit, caller_rate_limit = EXCLUDED.caller_rate_limit,
		       enabled = EXCLUDED.enabled, updated_by = EXCLUDED.updated_by, updated_at = NOW()
		RETURNING id, created_by, created_at, updated_at`,
		b.ScriptID, b.Name, pq.Array(b.Methods), b.Path, b.AuthMode, b.OpenReads, b.TimeoutSeconds,
		b.MaxMemoryBytes, b.MaxBodyBytes, pq.Array(origins), b.RateLimit, b.CallerRateLimit, b.Enabled, b.UpdatedBy)
	err := row.Scan(&b.ID, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" && pqErr.Constraint == nameConstraint {
		return epbinding.ErrNameTaken
	}
	if err != nil {
		return fmt.Errorf("set endpoint binding: %w", err)
	}
	return nil
}

// Get returns a script's binding.
func (s *Store) Get(ctx context.Context, scriptID string) (*epbinding.Binding, error) {
	return s.one(ctx, selectBinding+` WHERE script_id = $1`, scriptID)
}

// GetByName returns the binding a name serves.
func (s *Store) GetByName(ctx context.Context, name string) (*epbinding.Binding, error) {
	return s.one(ctx, selectBinding+` WHERE name = $1`, name)
}

func (s *Store) one(ctx context.Context, query, arg string) (*epbinding.Binding, error) {
	b, err := scan(s.db.QueryRowContext(ctx, query, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, epbinding.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get endpoint binding: %w", err)
	}
	return b, nil
}

// listBindings is one page of the bindings a filter matches and how many it
// matches in all. A NULL script id array matches every script and an empty
// one none; an empty auth mode and a NULL enabled match every binding.
const listBindings = "SELECT " + columns + `, COUNT(*) OVER () FROM script_endpoints
 WHERE ($2::uuid[] IS NULL OR script_id = ANY($2::uuid[]))
   AND ($3::text = '' OR auth_mode = $3::text)
   AND ($4::boolean IS NULL OR enabled = $4::boolean)
 ORDER BY name LIMIT $1 OFFSET $5`

// List returns one page of the bindings matching the filter, by name, and
// how many match in all. A page past the last answers no bindings and the
// total, read with a second statement since no row carries it.
func (s *Store) List(ctx context.Context, filter epbinding.Filter) ([]epbinding.Binding, int, error) {
	limit := filter.Limit
	if limit <= 0 || limit > MaxListLimit {
		limit = MaxListLimit
	}
	offset := max(filter.Offset, 0)
	ids, enabled := filterArgs(filter)
	rows, err := s.db.QueryContext(ctx, listBindings, limit, ids, filter.AuthMode, enabled, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list endpoint bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []epbinding.Binding{}
	total := 0
	for rows.Next() {
		b, err := scan(withTotal{rows, &total})
		if err != nil {
			return nil, 0, fmt.Errorf("scan endpoint binding: %w", err)
		}
		out = append(out, *b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate endpoint bindings: %w", err)
	}
	if len(out) == 0 && offset > 0 {
		if err := s.db.QueryRowContext(ctx, countBindings, ids, filter.AuthMode, enabled).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count endpoint bindings: %w", err)
		}
	}
	return out, total, nil
}

// filterArgs is the filter's script ids and enabled as the listing binds them:
// NULL for a filter that does not narrow by them.
func filterArgs(filter epbinding.Filter) (ids any, enabled sql.NullBool) {
	if filter.ScriptIDs != nil {
		ids = pq.Array(filter.ScriptIDs)
	}
	if filter.Enabled != nil {
		enabled = sql.NullBool{Bool: *filter.Enabled, Valid: true}
	}
	return ids, enabled
}

// countBindings is how many bindings a filter matches, for a page past the
// last.
const countBindings = `SELECT COUNT(*) FROM script_endpoints
 WHERE ($1::uuid[] IS NULL OR script_id = ANY($1::uuid[]))
   AND ($2::text = '' OR auth_mode = $2::text)
   AND ($3::boolean IS NULL OR enabled = $3::boolean)`

// withTotal scans a listing row, whose last column is the match count.
type withTotal struct {
	rows  *sql.Rows
	total *int
}

// Scan reads the row's columns into dest and its count into total.
func (w withTotal) Scan(dest ...any) error {
	return w.rows.Scan(append(dest, w.total)...) //nolint:wrapcheck // scan's caller names the read
}

// SetEnabled turns a script's binding on or off.
func (s *Store) SetEnabled(ctx context.Context, scriptID string, enabled bool, actor string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE script_endpoints SET enabled = $2, updated_by = $3, updated_at = NOW()
		 WHERE script_id = $1`, scriptID, enabled, actor)
	if err != nil {
		return fmt.Errorf("set endpoint binding enabled: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return epbinding.ErrNotFound
	}
	return nil
}

// RecordFailure keeps one failed request.
func (s *Store) RecordFailure(ctx context.Context, f epbinding.Failure) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO script_endpoint_failures (endpoint_id, version, method, status, error, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		f.EndpointID, f.Version, f.Method, f.Status, f.Error, f.DurationMS); err != nil {
		return fmt.Errorf("record endpoint failure: %w", err)
	}
	return nil
}

// Failures returns an endpoint's most recent failed requests, newest first.
func (s *Store) Failures(ctx context.Context, endpointID string, limit int) ([]epbinding.Failure, error) {
	if limit <= 0 || limit > defaultFailureLimit {
		limit = defaultFailureLimit
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, endpoint_id, version, method, status, error, duration_ms, created_at
		  FROM script_endpoint_failures
		 WHERE endpoint_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`, endpointID, limit)
	if err != nil {
		return nil, fmt.Errorf("list endpoint failures: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []epbinding.Failure{}
	for rows.Next() {
		var f epbinding.Failure
		if err := rows.Scan(&f.ID, &f.EndpointID, &f.Version, &f.Method, &f.Status, &f.Error, &f.DurationMS, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan endpoint failure: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate endpoint failures: %w", err)
	}
	return out, nil
}

// SweepFailures deletes the failures recorded before cutoff.
func (s *Store) SweepFailures(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM script_endpoint_failures WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("sweep endpoint failures: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Stamp reads when the binding a name serves and its script were last
// written.
func (s *Store) Stamp(ctx context.Context, name string) (epbinding.Stamp, error) {
	var st epbinding.Stamp
	err := s.db.QueryRowContext(ctx, `
		SELECT e.updated_at, s.updated_at
		  FROM script_endpoints e JOIN scripts s ON s.id = e.script_id
		 WHERE e.name = $1`, name).Scan(&st.Binding, &st.Script)
	if errors.Is(err, sql.ErrNoRows) {
		return st, epbinding.ErrNotFound
	}
	if err != nil {
		return st, fmt.Errorf("read endpoint stamp: %w", err)
	}
	return st, nil
}
