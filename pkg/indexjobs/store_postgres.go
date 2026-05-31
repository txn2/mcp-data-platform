package indexjobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// defaultListLimit / maxListLimit bound the admin List query's page
// size. A caller-supplied limit outside (0, maxListLimit] falls back
// to defaultListLimit so a missing or hostile value cannot return the
// whole table.
const (
	defaultListLimit = 50
	maxListLimit     = 500
)

// retryBackoffBase is the unit of exponential backoff applied to a
// retryable failure. After N attempts the next run is delayed by
// retryBackoffBase * 2^N. With base=5s and MaxAttempts=5 the
// schedule is 5s, 10s, 20s, 40s, 80s — ~150s of grace before the
// job moves to failed.
const retryBackoffBase = 5 * time.Second

// jobColumns is the canonical column list every job-returning
// query selects, in the order scanJob expects. Declared once so a
// new nullable column cannot drift between the SELECT sites and
// the scan.
const jobColumns = `id, source_kind, source_id, trigger_kind, status, attempts,
	last_error, next_run_at, worker_id, lease_expires_at,
	created_at, started_at, completed_at, items_done`

// PostgresStore implements Store against PostgreSQL. The concrete
// type is exported so callers can inject the *sql.DB directly; the
// interface in types.go is the contract.
type PostgresStore struct {
	db            *sql.DB
	leaseDuration time.Duration
}

// PostgresStoreOption configures a PostgresStore at construction
// time. Functional options keep NewPostgresStore backward-compatible
// while letting operators tune fields the constructor would
// otherwise grow positional arguments for.
type PostgresStoreOption func(*PostgresStore)

// WithLeaseDuration sets the duration the store stamps on a
// successful Claim and the renewal window for RenewLease. The
// worker's heartbeat re-stamps lease_expires_at = NOW() + d so the
// reaper does not release a job that is actively running.
//
// d <= 0 falls back to DefaultLeaseDuration so a misconfigured
// caller never produces an instant-expire row.
func WithLeaseDuration(d time.Duration) PostgresStoreOption {
	return func(s *PostgresStore) {
		if d > 0 {
			s.leaseDuration = d
		}
	}
}

// NewPostgresStore returns a Store backed by db. The caller owns
// the connection lifecycle. Without options the store uses
// DefaultLeaseDuration for Claim and RenewLease windows.
func NewPostgresStore(db *sql.DB, opts ...PostgresStoreOption) *PostgresStore {
	s := &PostgresStore{db: db, leaseDuration: DefaultLeaseDuration}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// LeaseDuration returns the configured lease window. Exposed so
// the worker can size its claim-context timeout against the same
// value the store stamps on Claim.
func (s *PostgresStore) LeaseDuration() time.Duration {
	return s.leaseDuration
}

// Compile-time interface check.
var _ Store = (*PostgresStore)(nil)

// Enqueue inserts a new pending job row. The partial unique index
// index_jobs_open enforces "at most one pending or running job per
// (source_kind, source_id)" so a duplicate insert collapses to a
// no-op. Issues NOTIFY on the queue channel after a successful
// insert so workers wake immediately; NOTIFY is best-effort.
func (s *PostgresStore) Enqueue(ctx context.Context, key Key, trigger Trigger) (bool, error) {
	// ON CONFLICT references the partial unique index by its
	// inferred predicate, not by name: CREATE UNIQUE INDEX writes
	// to pg_index but not pg_constraint, so ON CONFLICT ON
	// CONSTRAINT <name> would fail at runtime. Index inference
	// matches the same partial index by its WHERE clause.
	const q = `
		INSERT INTO index_jobs
		    (source_kind, source_id, trigger_kind)
		VALUES ($1, $2, $3)
		ON CONFLICT (source_kind, source_id)
		    WHERE status IN ('pending', 'running')
		DO NOTHING
		RETURNING id
	`
	var id int64
	err := s.db.QueryRowContext(ctx, q, key.SourceKind, key.SourceID, string(trigger)).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// ON CONFLICT DO NOTHING returned no row; an open job for
		// this key already exists. That is the desired idempotent
		// behavior.
		return false, nil
	case err != nil:
		return false, fmt.Errorf("indexjobs: enqueue: %w", err)
	}
	s.notify(ctx)
	return true, nil
}

// notify fires a best-effort pg_notify so listening workers race
// to claim. A missed notification just means a worker waits up to
// its poll interval before picking the row up.
func (s *PostgresStore) notify(ctx context.Context) {
	if _, err := s.db.ExecContext(ctx, `SELECT pg_notify($1, '')`, NotifyChannel); err != nil {
		_ = err // non-fatal; poll tick is the backstop
	}
}

// Claim acquires the next runnable pending job across all kinds.
// Returns ErrNoJob when nothing is available. The query body is
// wrapped in a single transaction so the SELECT FOR UPDATE row
// lock persists across the UPDATE that flips status to running.
// SKIP LOCKED makes concurrent claims across pods non-blocking.
func (s *PostgresStore) Claim(ctx context.Context, workerID string) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("indexjobs: claim begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const sel = `
		SELECT id
		  FROM index_jobs
		 WHERE status = 'pending' AND next_run_at <= NOW()
		 ORDER BY next_run_at, id
		 LIMIT 1
		 FOR UPDATE SKIP LOCKED
	`
	var id int64
	if err := tx.QueryRowContext(ctx, sel).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoJob
		}
		return nil, fmt.Errorf("indexjobs: claim select: %w", err)
	}

	const upd = `
		UPDATE index_jobs
		   SET status = 'running',
		       worker_id = $2,
		       attempts = attempts + 1,
		       started_at = NOW(),
		       lease_expires_at = NOW() + ($3 || ' seconds')::INTERVAL,
		       items_done = 0
		 WHERE id = $1
		 RETURNING ` + jobColumns
	job, err := scanJob(tx.QueryRowContext(ctx, upd, id, workerID, leaseSeconds(s.leaseDuration)))
	if err != nil {
		return nil, fmt.Errorf("indexjobs: claim update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("indexjobs: claim commit: %w", err)
	}
	return job, nil
}

// Complete marks a running job succeeded. The worker_id predicate
// enforces lease ownership; a rotated worker gets ErrNotFound.
func (s *PostgresStore) Complete(ctx context.Context, id int64, workerID string) error {
	const q = `
		UPDATE index_jobs
		   SET status = 'succeeded',
		       completed_at = NOW(),
		       last_error = '',
		       lease_expires_at = NULL
		 WHERE id = $1 AND status = 'running' AND worker_id = $2
	`
	return s.execOwned(ctx, "complete", q, id, workerID)
}

// UpdateProgress publishes the worker's chunk-boundary counter.
// The (id, worker_id, status='running') predicate enforces
// ownership; if the lease rotated the UPDATE matches zero rows and
// returns nil so the calling worker carries on. An error from the
// DB itself is returned for the worker to log but not retry.
func (s *PostgresStore) UpdateProgress(ctx context.Context, id int64, workerID string, itemsDone int) error {
	const q = `
		UPDATE index_jobs
		   SET items_done = $3
		 WHERE id = $1 AND status = 'running' AND worker_id = $2
	`
	if _, err := s.db.ExecContext(ctx, q, id, workerID, itemsDone); err != nil {
		return fmt.Errorf("indexjobs: update_progress: %w", err)
	}
	return nil
}

// RenewLease extends the running job's lease window. The worker's
// heartbeat calls this at ~lease/3 cadence. The ownership
// predicate returns ErrNotFound on a rotated lease. duration <= 0
// falls back to the store's configured lease so a misconfigured
// caller never stamps an instant-expire lease.
func (s *PostgresStore) RenewLease(ctx context.Context, id int64, workerID string, duration time.Duration) error {
	if duration <= 0 {
		duration = s.leaseDuration
	}
	const q = `
		UPDATE index_jobs
		   SET lease_expires_at = NOW() + ($3 || ' seconds')::INTERVAL
		 WHERE id = $1 AND status = 'running' AND worker_id = $2
	`
	res, err := s.db.ExecContext(ctx, q, id, workerID, leaseSeconds(duration))
	if err != nil {
		return fmt.Errorf("indexjobs: renew lease: %w", err)
	}
	return rowsAffectedOwned(res, "renew lease")
}

// Retry releases the lease and reschedules the job with
// exponential backoff. attempts is NOT incremented here (Claim
// already did it); the column reflects "how many times a worker
// started this job", which is the right thing for MaxAttempts
// comparisons in the caller.
func (s *PostgresStore) Retry(ctx context.Context, id int64, workerID, errMsg string) error {
	const q = `
		UPDATE index_jobs
		   SET status = 'pending',
		       last_error = $3,
		       worker_id = '',
		       lease_expires_at = NULL,
		       next_run_at = NOW() + ($4 || ' seconds')::INTERVAL
		 WHERE id = $1 AND status = 'running' AND worker_id = $2
		 RETURNING attempts
	`
	var attempts int
	err := s.db.QueryRowContext(ctx, q, id, workerID, errMsg, computeBackoffSeconds(s.lastAttempts(ctx, id))).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("indexjobs: retry: %w", err)
	}
	return nil
}

// lastAttempts reads the current attempts count for Retry's
// backoff computation. Returns 0 on any error so the backoff
// degrades to the base interval rather than failing the call.
func (s *PostgresStore) lastAttempts(ctx context.Context, id int64) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT attempts FROM index_jobs WHERE id = $1`, id).Scan(&n)
	return n
}

// Fail moves the job to the terminal failed state.
func (s *PostgresStore) Fail(ctx context.Context, id int64, workerID, errMsg string) error {
	const q = `
		UPDATE index_jobs
		   SET status = 'failed',
		       completed_at = NOW(),
		       last_error = $3,
		       lease_expires_at = NULL
		 WHERE id = $1 AND status = 'running' AND worker_id = $2
	`
	return s.execOwned(ctx, "fail", q, id, workerID, errMsg)
}

// ReleaseExpiredLeases is the reaper's sweep. Flips status=running
// rows whose lease has elapsed back to pending so another worker
// can claim them. Does NOT increment attempts; the next Claim does
// that, preserving the "attempts means worker-tried" invariant.
func (s *PostgresStore) ReleaseExpiredLeases(ctx context.Context) (int, error) {
	const q = `
		UPDATE index_jobs
		   SET status = 'pending', worker_id = '', lease_expires_at = NULL
		 WHERE status = 'running'
		   AND lease_expires_at IS NOT NULL
		   AND lease_expires_at <= NOW()
	`
	res, err := s.db.ExecContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("indexjobs: release expired leases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("indexjobs: release expired leases rows-affected: %w", err)
	}
	return int(n), nil
}

// Get returns one job by id.
func (s *PostgresStore) Get(ctx context.Context, id int64) (*Job, error) {
	q := `SELECT ` + jobColumns + ` FROM index_jobs WHERE id = $1`
	job, err := scanJob(s.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("indexjobs: get: %w", err)
	}
	return job, nil
}

// List returns jobs matching the filter, newest first.
func (s *PostgresStore) List(ctx context.Context, filter ListFilter) ([]Job, error) {
	predicates, args := buildListPredicates(filter)
	limit := filter.Limit
	if limit <= 0 || limit > maxListLimit {
		limit = defaultListLimit
	}
	args = append(args, limit)
	q := `SELECT ` + jobColumns + ` FROM index_jobs` + predicates +
		` ORDER BY id DESC LIMIT $` + intToStr(len(args)) // #nosec G202 -- predicates are closed-set literal fragments; only $N placeholders are dynamic
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("indexjobs: list: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("indexjobs: list scan: %w", err)
		}
		out = append(out, *j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("indexjobs: list rows: %w", err)
	}
	return out, nil
}

// Counts returns the per-state job roll-up for one source kind,
// computed from index_jobs alone. The "last status per unit"
// subquery uses DISTINCT ON so a unit with a long history counts
// once, under its most recent job's state — matching how the admin
// surface presents a unit's current status.
func (s *PostgresStore) Counts(ctx context.Context, sourceKind string) (*KindCounts, error) {
	const q = `
		WITH last AS (
		  SELECT DISTINCT ON (source_id) status
		    FROM index_jobs
		   WHERE source_kind = $1
		   ORDER BY source_id, id DESC
		)
		SELECT COUNT(*) FILTER (WHERE status = 'pending'),
		       COUNT(*) FILTER (WHERE status = 'running'),
		       COUNT(*) FILTER (WHERE status = 'succeeded'),
		       COUNT(*) FILTER (WHERE status = 'failed'),
		       (SELECT MAX(COALESCE(completed_at, started_at, created_at))
		          FROM index_jobs WHERE source_kind = $1)
		  FROM last
	`
	c := &KindCounts{SourceKind: sourceKind}
	var lastActivity sql.NullTime
	if err := s.db.QueryRowContext(ctx, q, sourceKind).Scan(
		&c.Pending, &c.Running, &c.Succeeded, &c.Failed, &lastActivity); err != nil {
		return nil, fmt.Errorf("indexjobs: counts: %w", err)
	}
	if lastActivity.Valid {
		c.LastActivity = &lastActivity.Time
	}
	return c, nil
}

// execOwned runs an ownership-guarded UPDATE (the Complete / Fail
// shape) and translates a zero-row result to ErrNotFound.
func (s *PostgresStore) execOwned(ctx context.Context, op, q string, args ...any) error {
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("indexjobs: %s: %w", op, err)
	}
	return rowsAffectedOwned(res, op)
}

// rowsAffectedOwned maps a zero-row result from an ownership-guarded
// UPDATE to ErrNotFound (the lease rotated).
func rowsAffectedOwned(res sql.Result, op string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("indexjobs: %s rows-affected: %w", op, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// maxBackoffShift caps the exponent in the backoff formula so a
// corrupted attempts column cannot produce a multi-day backoff.
const maxBackoffShift = 30

// computeBackoffSeconds applies the exponential backoff formula.
// Pure function so unit tests don't need a DB.
func computeBackoffSeconds(attempts int) int {
	if attempts > maxBackoffShift {
		attempts = maxBackoffShift
	}
	if attempts < 0 {
		attempts = 0
	}
	return int(retryBackoffBase/time.Second) * (1 << attempts)
}

// leaseSeconds converts a lease duration to whole seconds for the
// interval arithmetic, flooring at 1. Without the floor a sub-second
// configured lease (e.g. 500ms) would compute to 0 and stamp
// lease_expires_at = NOW() + '0 seconds' = NOW(), which the reaper
// reclaims on its next sweep while the worker is still running the
// job: a claim/reap/re-claim doom loop. The store and worker both
// already reject d <= 0; this guards the sub-second remainder.
func leaseSeconds(d time.Duration) int {
	if s := int(d / time.Second); s > 0 {
		return s
	}
	return 1
}

// scanJob is shared by Claim, Get, and List. rows is a row-like
// reader (*sql.Row or *sql.Rows). Centralizing the column-to-field
// mapping keeps drift impossible when a new nullable column is
// added.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(r rowScanner) (*Job, error) {
	var (
		j               Job
		trigger, status string
		leaseExpiresAt  sql.NullTime
		startedAt       sql.NullTime
		completedAt     sql.NullTime
	)
	if err := r.Scan(&j.ID, &j.SourceKind, &j.SourceID, &trigger, &status,
		&j.Attempts, &j.LastError, &j.NextRunAt, &j.WorkerID,
		&leaseExpiresAt, &j.CreatedAt, &startedAt, &completedAt,
		&j.ItemsDone); err != nil {
		return nil, fmt.Errorf("indexjobs: scan job row: %w", err)
	}
	j.Trigger = Trigger(trigger)
	j.Status = Status(status)
	if leaseExpiresAt.Valid {
		j.LeaseExpiresAt = &leaseExpiresAt.Time
	}
	if startedAt.Valid {
		j.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		j.CompletedAt = &completedAt.Time
	}
	return &j, nil
}

// buildListPredicates returns the WHERE-clause fragment and the
// matching arg slice for a List query. Empty filter returns an
// empty string (no WHERE clause). Every clause is a literal the
// constructor controls; only the parameter values come from the
// caller, so the fragment is safe to concatenate.
func buildListPredicates(f ListFilter) (where string, args []any) {
	conds := []string{}
	add := func(clause string, val any) {
		args = append(args, val)
		conds = append(conds, clause+intToStr(len(args)))
	}
	if f.SourceKind != "" {
		add("source_kind = $", f.SourceKind)
	}
	if f.SourceID != "" {
		add("source_id = $", f.SourceID)
	}
	if f.SourceIDPrefix != "" {
		// LIKE with the prefix and a literal % suffix. The prefix
		// value is parameterized; only the column and operator are
		// literal. escapeLikePrefix neutralizes %/_ in the prefix so
		// a source_id containing them cannot widen the match.
		add("source_id LIKE $", escapeLikePrefix(f.SourceIDPrefix)+"%")
	}
	if f.Status != "" {
		add("status = $", string(f.Status))
	}
	if f.Trigger != "" {
		add("trigger_kind = $", string(f.Trigger))
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// escapeLikePrefix neutralizes the LIKE metacharacters so a prefix
// match treats %, _ and the escape char literally. Paired with the
// default backslash ESCAPE behavior of Postgres LIKE.
func escapeLikePrefix(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(p)
}

// intToStr formats n as decimal. Thin wrapper kept under a local
// name so callers in this file read consistently with the dynamic-
// predicate builder.
func intToStr(n int) string {
	return strconv.Itoa(n)
}
