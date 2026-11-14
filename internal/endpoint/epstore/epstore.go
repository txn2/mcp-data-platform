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

// checkConstraint is the foreign key from script_endpoints.bot_check to
// bot_checks, which a binding naming a check that does not exist violates.
const checkConstraint = "script_endpoints_bot_check_fkey"

// columns is the column list every script_endpoints SELECT reads, in scan's
// order.
const columns = `id, script_id, name, methods, path, auth_mode, callers, COALESCE(bot_check, ''), checked_methods,
	timeout_seconds, max_memory_bytes, max_body_bytes, allowed_origins, rate_limit, caller_rate_limit, enabled,
	created_by, updated_by, created_at, updated_at, response_mode, page_cdns,
	page_frame_origins, page_connect_origins, page_form_origins, ip_rate_limit`

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
	err := r.Scan(&b.ID, &b.ScriptID, &b.Name, pq.Array(&b.Methods), &b.Path, &b.AuthMode, pq.Array(&b.Callers),
		&b.BotCheck, pq.Array(&b.CheckedMethods), &b.TimeoutSeconds, &b.MaxMemoryBytes, &b.MaxBodyBytes, pq.Array(&b.AllowedOrigins), &b.RateLimit,
		&b.CallerRateLimit, &b.Enabled, &b.CreatedBy, &b.UpdatedBy, &b.CreatedAt, &b.UpdatedAt, &b.ResponseMode, pq.Array(&b.PageCDNs),
		pq.Array(&b.PageFrameOrigins), pq.Array(&b.PageConnectOrigins), pq.Array(&b.PageFormOrigins), &b.IPRateLimit)
	if err != nil {
		return nil, err //nolint:wrapcheck // each caller says what it was reading
	}
	for _, list := range []*[]string{&b.AllowedOrigins, &b.Callers, &b.CheckedMethods, &b.PageCDNs, &b.PageFrameOrigins, &b.PageConnectOrigins, &b.PageFormOrigins} {
		if *list == nil {
			*list = []string{}
		}
	}
	return b, nil
}

// Set creates a binding when its ID is empty and replaces the one with its ID
// otherwise, keeping its creation stamp (#2441). The replace is held to the
// binding's own script, so an id read from another script's binding changes
// nothing.
func (s *Store) Set(ctx context.Context, b *epbinding.Binding) error {
	args := []any{
		b.ScriptID, b.Name, pq.Array(b.Methods), b.Path, b.AuthMode, pq.Array(nonNil(b.Callers)), b.BotCheck,
		pq.Array(nonNil(b.CheckedMethods)), b.TimeoutSeconds, b.MaxMemoryBytes, b.MaxBodyBytes,
		pq.Array(nonNil(b.AllowedOrigins)), b.RateLimit, b.CallerRateLimit, b.Enabled, b.UpdatedBy, b.ResponseMode, pq.Array(nonNil(b.PageCDNs)),
		pq.Array(nonNil(b.PageFrameOrigins)), pq.Array(nonNil(b.PageConnectOrigins)), pq.Array(nonNil(b.PageFormOrigins)), b.IPRateLimit,
	}
	var err error
	if b.ID == "" {
		err = s.db.QueryRowContext(ctx, insertBinding, args...).Scan(&b.ID, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt)
	} else {
		err = s.db.QueryRowContext(ctx, updateBinding, append(args, b.ID)...).Scan(&b.CreatedBy, &b.CreatedAt, &b.UpdatedAt)
	}
	var pqErr *pq.Error
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return epbinding.ErrNotFound
	case errors.As(err, &pqErr) && pqErr.Code == "23505" && pqErr.Constraint == nameConstraint:
		return epbinding.ErrNameTaken
	case errors.As(err, &pqErr) && pqErr.Code == "23503" && pqErr.Constraint == checkConstraint:
		return fmt.Errorf("bot check %q: %w", b.BotCheck, epbinding.ErrNoSuchCheck)
	case err != nil:
		return fmt.Errorf("set endpoint binding: %w", err)
	}
	return nil
}

// insertBinding creates a binding.
const insertBinding = `
		INSERT INTO script_endpoints (script_id, name, methods, path, auth_mode, callers, bot_check, checked_methods,
		                              timeout_seconds, max_memory_bytes, max_body_bytes, allowed_origins, rate_limit,
		                              caller_rate_limit, enabled, created_by, updated_by, response_mode, page_cdns,
		                              page_frame_origins, page_connect_origins, page_form_origins, ip_rate_limit)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10, $11, $12, $13, $14, $15, $16, $16, COALESCE(NULLIF($17, ''), 'api'), $18,
		        $19, $20, $21, $22)
		RETURNING id, created_by, created_at, updated_at`

// updateBinding replaces the binding $23 of the script $1.
const updateBinding = `
		UPDATE script_endpoints
		   SET name = $2, methods = $3, path = $4, auth_mode = $5, callers = $6, bot_check = NULLIF($7, ''),
		       checked_methods = $8, timeout_seconds = $9, max_memory_bytes = $10, max_body_bytes = $11,
		       allowed_origins = $12, rate_limit = $13, caller_rate_limit = $14, enabled = $15,
		       updated_by = $16, updated_at = NOW(), response_mode = COALESCE(NULLIF($17, ''), 'api'), page_cdns = $18,
		       page_frame_origins = $19, page_connect_origins = $20, page_form_origins = $21, ip_rate_limit = $22
		 WHERE id = $23 AND script_id = $1
		RETURNING created_by, created_at, updated_at`

// nonNil is list, or an empty list for nil: a NOT NULL array column takes no
// NULL.
func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// GetByID returns one binding.
func (s *Store) GetByID(ctx context.Context, id string) (*epbinding.Binding, error) {
	return s.one(ctx, selectBinding+` WHERE id = $1`, id)
}

// ByScript returns a script's bindings by name.
func (s *Store) ByScript(ctx context.Context, scriptID string) ([]epbinding.Binding, error) {
	rows, err := s.db.QueryContext(ctx, selectBinding+` WHERE script_id = $1 ORDER BY name`, scriptID)
	if err != nil {
		return nil, fmt.Errorf("list a script's endpoint bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []epbinding.Binding{}
	for rows.Next() {
		b, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan endpoint binding: %w", err)
		}
		out = append(out, *b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate endpoint bindings: %w", err)
	}
	return out, nil
}

// Delete removes one binding; its failed and refused requests go with it.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM script_endpoints WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete endpoint binding: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return epbinding.ErrNotFound
	}
	return nil
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

// Enable turns one binding on or off.
func (s *Store) Enable(ctx context.Context, id string, enabled bool, actor string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE script_endpoints SET enabled = $2, updated_by = $3, updated_at = NOW()
		 WHERE id = $1`, id, enabled, actor)
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
// written, and the application the script is a part of.
func (s *Store) Stamp(ctx context.Context, name string) (epbinding.Stamp, error) {
	var st epbinding.Stamp
	err := s.db.QueryRowContext(ctx, `
		SELECT e.updated_at, s.updated_at,
		       COALESCE((SELECT MAX(a.version) FROM approval_requests a
		                  WHERE a.script_id = e.script_id AND a.purpose = 'form_public' AND a.status = 'approved'), 0),
		       COALESCE(app.id, ''), COALESCE(app.connection, '')
		  FROM script_endpoints e JOIN scripts s ON s.id = e.script_id
		  LEFT JOIN application_parts p ON p.kind = 'script' AND p.part_id = e.script_id::text
		  LEFT JOIN applications app ON app.id = p.application_id
		 WHERE e.name = $1`, name).Scan(&st.Binding, &st.Script, &st.ApprovedVersion, &st.Application, &st.AppConnection)
	if errors.Is(err, sql.ErrNoRows) {
		return st, epbinding.ErrNotFound
	}
	if err != nil {
		return st, fmt.Errorf("read endpoint stamp: %w", err)
	}
	return st, nil
}
