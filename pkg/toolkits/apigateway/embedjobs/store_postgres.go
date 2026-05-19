package embedjobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

// NotifyChannel is the postgres LISTEN/NOTIFY channel name
// producers and workers use to coordinate low-latency wake-ups.
// Workers LISTEN on this channel; producers issue
// `SELECT pg_notify($1, ”)` after a successful enqueue. The
// payload is intentionally empty because the worker re-queries
// the table on every wake; it does not trust the notification
// payload as the source of truth.
const NotifyChannel = "api_catalog_embedding_jobs"

// retryBackoffBase is the unit of exponential backoff applied to
// a retryable failure. After N attempts, the next run is delayed
// by retryBackoffBase * 2^N. With base=5s and MaxAttempts=5 the
// schedule is 5s, 10s, 20s, 40s, 80s — ~150s total grace period
// before the job moves to failed. Enough for a flaky provider to
// recover; not so long that an outage holds an indexing job for
// hours.
const retryBackoffBase = 5 * time.Second

// PostgresStore implements Store against PostgreSQL. The
// concrete type is exported so callers can inject the *sql.DB
// directly; the interface in types.go is the contract.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore returns a Store backed by db. The caller owns
// the connection lifecycle; closing db while the worker is
// running is the caller's responsibility (typically the platform
// lifecycle stops the worker before the DB is torn down).
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Compile-time interface check.
var _ Store = (*PostgresStore)(nil)

// pgUniqueViolation is the SQLSTATE Postgres returns when a
// unique-index constraint (including a partial unique index)
// rejects an INSERT. The producer treats this as "already
// enqueued" and returns created=false without an error.
const pgUniqueViolation = "23505"

// Enqueue inserts a new pending job row. The partial unique
// index `api_catalog_embedding_jobs_open` enforces "at most one
// pending or running job per (catalog, spec)" so a duplicate
// insert collapses to a no-op rather than stacking redundant
// work. Returns created=true when a new row was written,
// created=false when the index suppressed the insert. Producers
// fire-and-forget; the bool is for telemetry and tests.
//
// Issues NOTIFY on the queue channel after a successful insert
// so workers wake immediately instead of waiting for their
// fallback poll tick. NOTIFY is best-effort; the worker's poll
// tick covers the case where a notification is dropped.
func (s *PostgresStore) Enqueue(ctx context.Context, key SpecKey, kind Kind) (bool, error) {
	// ON CONFLICT references the partial unique index by its
	// inferred predicate, not by name. CREATE UNIQUE INDEX writes
	// to pg_index but not pg_constraint, so ON CONFLICT ON
	// CONSTRAINT <name> would fail at runtime with "constraint
	// ... does not exist". Index inference matches the same
	// partial index by its WHERE clause.
	const q = `
		INSERT INTO api_catalog_embedding_jobs
		    (catalog_id, spec_name, kind)
		VALUES ($1, $2, $3)
		ON CONFLICT (catalog_id, spec_name)
		    WHERE status IN ('pending', 'running')
		DO NOTHING
		RETURNING id
	`
	var id int64
	err := s.db.QueryRowContext(ctx, q, key.CatalogID, key.SpecName, string(kind)).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// ON CONFLICT DO NOTHING returned no row; the partial
		// unique index already had a pending or running job for
		// this spec. That is the desired idempotent behavior.
		return false, nil
	case err != nil:
		return false, fmt.Errorf("embedjobs: enqueue: %w", err)
	}
	// Fire NOTIFY outside the row scan so the listening workers
	// can race to claim. Errors here are non-fatal: a missed
	// notification just means a worker waits up to the poll
	// interval before picking up the row.
	if _, nerr := s.db.ExecContext(ctx, `SELECT pg_notify($1, '')`, NotifyChannel); nerr != nil {
		// Log via slog at the caller's discretion; do not fail
		// the enqueue because of a notification hiccup.
		_ = nerr
	}
	return true, nil
}

// Claim acquires the next runnable pending job. Returns ErrNoJob
// when nothing is available; the worker loop treats that as the
// signal to block on LISTEN.
//
// The query body is wrapped in a single transaction so the row
// lock from SELECT FOR UPDATE persists across the UPDATE that
// flips status to running. SKIP LOCKED makes concurrent claims
// across pods non-blocking: each worker sees a different row.
func (s *PostgresStore) Claim(ctx context.Context, workerID string) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("embedjobs: claim begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const sel = `
		SELECT id
		  FROM api_catalog_embedding_jobs
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
		return nil, fmt.Errorf("embedjobs: claim select: %w", err)
	}

	const upd = `
		UPDATE api_catalog_embedding_jobs
		   SET status = 'running',
		       worker_id = $2,
		       attempts = attempts + 1,
		       started_at = NOW(),
		       lease_expires_at = NOW() + ($3 || ' seconds')::INTERVAL,
		       embedded_so_far = 0
		 WHERE id = $1
		 RETURNING id, catalog_id, spec_name, kind, status, attempts,
		           last_error, next_run_at, worker_id, lease_expires_at,
		           created_at, started_at, completed_at, embedded_so_far
	`
	leaseSeconds := int(LeaseDuration / time.Second)
	row := tx.QueryRowContext(ctx, upd, id, workerID, leaseSeconds)
	job, err := scanJob(row)
	if err != nil {
		return nil, fmt.Errorf("embedjobs: claim update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("embedjobs: claim commit: %w", err)
	}
	return job, nil
}

// Complete marks a running job as succeeded. The worker_id
// predicate enforces lease ownership: a worker whose lease was
// rotated by the reaper between claim and complete cannot
// overwrite the new holder's progress. Returns ErrNotFound on
// the rotated case so the caller can log and abandon gracefully.
func (s *PostgresStore) Complete(ctx context.Context, id int64, workerID string) error {
	const q = `
		UPDATE api_catalog_embedding_jobs
		   SET status = 'succeeded',
		       completed_at = NOW(),
		       last_error = '',
		       lease_expires_at = NULL
		 WHERE id = $1
		   AND status = 'running'
		   AND worker_id = $2
	`
	res, err := s.db.ExecContext(ctx, q, id, workerID)
	if err != nil {
		return fmt.Errorf("embedjobs: complete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("embedjobs: complete rows-affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateProgress publishes the worker's chunk-boundary progress
// counter to the row. The (id, worker_id, status='running')
// predicate enforces lease ownership: if the lease has rotated to
// another worker, the UPDATE matches zero rows and returns nil so
// the calling worker carries on toward its next chunk without
// caring whose count the row will surface (the new lease holder
// will publish its own count on its next chunk).
//
// Best-effort: an error from the DB itself (network, pool
// exhaustion) is returned for the worker to log, but the worker
// does NOT retry or fail the job on a progress write error. The
// final Complete is the authoritative success signal.
func (s *PostgresStore) UpdateProgress(ctx context.Context, id int64, workerID string, embeddedSoFar int) error {
	const q = `
		UPDATE api_catalog_embedding_jobs
		   SET embedded_so_far = $3
		 WHERE id = $1
		   AND status = 'running'
		   AND worker_id = $2
	`
	if _, err := s.db.ExecContext(ctx, q, id, workerID, embeddedSoFar); err != nil {
		return fmt.Errorf("embedjobs: update_progress: %w", err)
	}
	return nil
}

// Retry releases the lease and reschedules the job with
// exponential backoff. attempts is NOT incremented here (Claim
// already did it); the column reflects "how many times has a
// worker started this job", which is the right thing for
// MaxAttempts comparisons in the caller.
func (s *PostgresStore) Retry(ctx context.Context, id int64, workerID, errMsg string) error {
	const q = `
		UPDATE api_catalog_embedding_jobs
		   SET status = 'pending',
		       last_error = $3,
		       worker_id = '',
		       lease_expires_at = NULL,
		       next_run_at = NOW() + ($4 || ' seconds')::INTERVAL
		 WHERE id = $1
		   AND status = 'running'
		   AND worker_id = $2
		 RETURNING attempts
	`
	// next_run_at scales with the row's CURRENT attempts (already
	// bumped by Claim). The exponent uses the row's value rather
	// than a parameter so the caller doesn't have to read it
	// back; we trust the DB as the source of truth.
	//
	// We compute the backoff in seconds in the caller so the
	// SQL stays simple — the DB cannot do `2 ^ attempts` without
	// CASE WHEN or a function, and a server-side function adds
	// migration overhead.
	var attempts int
	if err := s.db.QueryRowContext(ctx, q, id, workerID, errMsg, computeBackoffSeconds(s.lastAttempts(ctx, id))).Scan(&attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("embedjobs: retry: %w", err)
	}
	return nil
}

// lastAttempts is a small helper for Retry's backoff
// computation. Returns 0 on any error so the backoff degrades to
// the base interval rather than failing the call.
func (s *PostgresStore) lastAttempts(ctx context.Context, id int64) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT attempts FROM api_catalog_embedding_jobs WHERE id = $1`, id).Scan(&n)
	return n
}

// maxBackoffShift caps the exponent in the backoff formula at
// `retryBackoffBase << maxBackoffShift`. attempts > maxBackoffShift
// is impossible in practice (MaxAttempts is 5) but the cap keeps
// a corrupted attempts column from producing a multi-day backoff.
const maxBackoffShift = 30

// computeBackoffSeconds applies the exponential formula. Pure
// function so unit tests don't need a DB.
func computeBackoffSeconds(attempts int) int {
	if attempts > maxBackoffShift {
		attempts = maxBackoffShift
	}
	if attempts < 0 {
		attempts = 0
	}
	return int(retryBackoffBase/time.Second) * (1 << attempts)
}

// Fail moves the job to the terminal failed state. last_error
// keeps the message for the admin UI; an operator who needs to
// retry uses the manual-retry endpoint, which enqueues a fresh
// row.
func (s *PostgresStore) Fail(ctx context.Context, id int64, workerID, errMsg string) error {
	const q = `
		UPDATE api_catalog_embedding_jobs
		   SET status = 'failed',
		       completed_at = NOW(),
		       last_error = $3,
		       lease_expires_at = NULL
		 WHERE id = $1
		   AND status = 'running'
		   AND worker_id = $2
	`
	res, err := s.db.ExecContext(ctx, q, id, workerID, errMsg)
	if err != nil {
		return fmt.Errorf("embedjobs: fail: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("embedjobs: fail rows-affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReleaseExpiredLeases is the reaper's sweep. A pod that
// claimed a job and then died never gets to call Complete or
// Retry, so its row sits in status=running with an
// lease_expires_at in the past. This UPDATE returns the row to
// the queue so another worker can claim it on the next loop.
//
// The reaper does NOT increment attempts. The next Claim does
// that as part of its own transaction, which keeps the
// "attempts means worker-tried" invariant: a job whose lease
// expired during a network blip still gets its full MaxAttempts
// budget on the next pickup.
func (s *PostgresStore) ReleaseExpiredLeases(ctx context.Context) (int, error) {
	const q = `
		UPDATE api_catalog_embedding_jobs
		   SET status = 'pending',
		       worker_id = '',
		       lease_expires_at = NULL
		 WHERE status = 'running'
		   AND lease_expires_at IS NOT NULL
		   AND lease_expires_at <= NOW()
	`
	res, err := s.db.ExecContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("embedjobs: release expired leases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("embedjobs: release expired leases rows-affected: %w", err)
	}
	return int(n), nil
}

// ReconcileGaps finds every (catalog, spec) where the persisted
// embedding row count does not match the spec's operation_count
// and enqueues a reconciler job for each one. The unique partial
// index makes the insert a no-op for specs already in the queue.
//
// The query is one statement: a SELECT that joins specs against
// a grouped count of embedding rows, filtered to mismatches,
// fed straight into an INSERT ... ON CONFLICT. This means the
// reconciler does not pull spec rows into the application and
// has no concurrency concern even with multiple pods running
// the same query simultaneously.
func (s *PostgresStore) ReconcileGaps(ctx context.Context) (int, error) {
	// Same index-inference shape as Enqueue. See the comment
	// there for why ON CONFLICT ON CONSTRAINT does not work.
	const q = `
		INSERT INTO api_catalog_embedding_jobs
		    (catalog_id, spec_name, kind)
		SELECT s.catalog_id, s.spec_name, $1
		  FROM api_catalog_specs s
		  LEFT JOIN (
		    SELECT catalog_id, spec_name, COUNT(*) AS embedded
		      FROM api_catalog_operation_embeddings
		     GROUP BY catalog_id, spec_name
		  ) e USING (catalog_id, spec_name)
		 WHERE s.operation_count <> COALESCE(e.embedded, 0)
		ON CONFLICT (catalog_id, spec_name)
		    WHERE status IN ('pending', 'running')
		DO NOTHING
	`
	res, err := s.db.ExecContext(ctx, q, string(KindReconciler))
	if err != nil {
		return 0, fmt.Errorf("embedjobs: reconcile gaps: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("embedjobs: reconcile gaps rows-affected: %w", err)
	}
	if n > 0 {
		// Nudge any idle workers so they pick up the newly
		// enqueued jobs immediately. Best-effort.
		_, _ = s.db.ExecContext(ctx, `SELECT pg_notify($1, '')`, NotifyChannel)
	}
	return int(n), nil
}

// Get returns one job by id.
func (s *PostgresStore) Get(ctx context.Context, id int64) (*Job, error) {
	const q = `
		SELECT id, catalog_id, spec_name, kind, status, attempts,
		       last_error, next_run_at, worker_id, lease_expires_at,
		       created_at, started_at, completed_at, embedded_so_far
		  FROM api_catalog_embedding_jobs
		 WHERE id = $1
	`
	job, err := scanJob(s.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("embedjobs: get: %w", err)
	}
	return job, nil
}

// List returns jobs matching the filter, newest first.
// nolint:gocyclo,revive // Function builds a dynamic predicate
// set against a closed argument list. Splitting into helpers
// would scatter the column-to-arg mapping; the linear shape
// keeps the SQL adjacent to the parameter list.
func (s *PostgresStore) List(ctx context.Context, filter ListFilter) ([]Job, error) {
	// Closed-set predicate builder. Every clause is a literal
	// the constructor controls; only the parameter values come
	// from the caller. Safe to concatenate.
	predicates, args := buildListPredicates(filter)
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	args = append(args, limit)
	q := `SELECT id, catalog_id, spec_name, kind, status, attempts,
	             last_error, next_run_at, worker_id, lease_expires_at,
	             created_at, started_at, completed_at, embedded_so_far
	        FROM api_catalog_embedding_jobs` + predicates +
		` ORDER BY id DESC LIMIT $` + intToStr(len(args)) // #nosec G202 -- predicates are closed-set literal fragments; only $N placeholders are dynamic
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("embedjobs: list: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("embedjobs: list scan: %w", err)
		}
		out = append(out, *j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("embedjobs: list rows: %w", err)
	}
	return out, nil
}

// SpecStatuses returns one row per spec in the catalog. The
// query joins the spec row against the embedding row count and
// the most recent job row, so the admin UI can render the
// per-spec badge without N+1 queries.
//
// The "most recent job" subquery uses DISTINCT ON to pick the
// highest id per (catalog_id, spec_name) — the same pattern the
// admin job-history view uses for a stable "last status" column.
func (s *PostgresStore) SpecStatuses(ctx context.Context, catalogID string) ([]SpecStatusRow, error) {
	const q = `
		SELECT s.catalog_id,
		       s.spec_name,
		       s.operation_count,
		       COALESCE(e.embedded, 0)            AS embedding_count,
		       COALESCE(j.status, '')             AS job_status,
		       COALESCE(j.attempts, 0)            AS job_attempts,
		       COALESCE(j.last_error, '')         AS job_last_error,
		       GREATEST(j.completed_at, j.started_at, j.created_at) AS job_updated_at,
		       COALESCE(j.embedded_so_far, 0)     AS embedded_so_far
		  FROM api_catalog_specs s
		  LEFT JOIN (
		    SELECT catalog_id, spec_name, COUNT(*) AS embedded
		      FROM api_catalog_operation_embeddings
		     GROUP BY catalog_id, spec_name
		  ) e USING (catalog_id, spec_name)
		  LEFT JOIN LATERAL (
		    SELECT status, attempts, last_error,
		           created_at, started_at, completed_at, embedded_so_far
		      FROM api_catalog_embedding_jobs
		     WHERE catalog_id = s.catalog_id
		       AND spec_name = s.spec_name
		     ORDER BY id DESC
		     LIMIT 1
		  ) j ON TRUE
		 WHERE s.catalog_id = $1
		 ORDER BY s.spec_name
	`
	rows, err := s.db.QueryContext(ctx, q, catalogID)
	if err != nil {
		return nil, fmt.Errorf("embedjobs: spec statuses: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var out []SpecStatusRow
	for rows.Next() {
		var r SpecStatusRow
		var status string
		var updatedAt sql.NullTime
		if err := rows.Scan(&r.CatalogID, &r.SpecName, &r.OperationCount,
			&r.EmbeddingCount, &status, &r.JobAttempts, &r.JobLastError, &updatedAt,
			&r.EmbeddedSoFar); err != nil {
			return nil, fmt.Errorf("embedjobs: spec statuses scan: %w", err)
		}
		r.JobStatus = Status(status)
		if updatedAt.Valid {
			r.JobUpdatedAt = &updatedAt.Time
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("embedjobs: spec statuses rows: %w", err)
	}
	return out, nil
}

// Health is the catalog-level roll-up. The portal renders this
// at the top of the catalog editor so an operator can see "all
// 47 specs indexed" or "3 pending, 1 failed" at a glance.
func (s *PostgresStore) Health(ctx context.Context, catalogID string) (*CatalogHealth, error) {
	// Single statement: counts specs and joins per-spec status
	// just enough to bucket them. The CASE WHEN over the
	// embedding count classifies "indexed" without needing a
	// separate query.
	const q = `
		WITH spec_state AS (
		  SELECT s.catalog_id,
		         s.spec_name,
		         s.operation_count,
		         COALESCE(e.embedded, 0)         AS embedded,
		         COALESCE(j.status, '')          AS job_status
		    FROM api_catalog_specs s
		    LEFT JOIN (
		      SELECT catalog_id, spec_name, COUNT(*) AS embedded
		        FROM api_catalog_operation_embeddings
		       GROUP BY catalog_id, spec_name
		    ) e USING (catalog_id, spec_name)
		    LEFT JOIN LATERAL (
		      SELECT status FROM api_catalog_embedding_jobs
		       WHERE catalog_id = s.catalog_id AND spec_name = s.spec_name
		       ORDER BY id DESC LIMIT 1
		    ) j ON TRUE
		   WHERE s.catalog_id = $1
		)
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE operation_count = embedded AND operation_count > 0),
		       COUNT(*) FILTER (WHERE job_status = 'pending'),
		       COUNT(*) FILTER (WHERE job_status = 'running'),
		       COUNT(*) FILTER (WHERE job_status = 'failed')
		  FROM spec_state
	`
	var h CatalogHealth
	h.CatalogID = catalogID
	err := s.db.QueryRowContext(ctx, q, catalogID).Scan(
		&h.SpecsTotal, &h.SpecsIndexed,
		&h.SpecsPending, &h.SpecsRunning, &h.SpecsFailed)
	if err != nil {
		return nil, fmt.Errorf("embedjobs: health: %w", err)
	}
	return &h, nil
}

// scanJob is shared by Claim, Get, and List. rows is a row-like
// reader (*sql.Row or *sql.Rows; both have Scan). Centralizing
// the column-to-field mapping keeps drift impossible when a new
// nullable column is added.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(r rowScanner) (*Job, error) {
	var (
		j              Job
		kind, status   string
		leaseExpiresAt sql.NullTime
		startedAt      sql.NullTime
		completedAt    sql.NullTime
	)
	if err := r.Scan(&j.ID, &j.CatalogID, &j.SpecName, &kind, &status,
		&j.Attempts, &j.LastError, &j.NextRunAt, &j.WorkerID,
		&leaseExpiresAt, &j.CreatedAt, &startedAt, &completedAt,
		&j.EmbeddedSoFar); err != nil {
		return nil, fmt.Errorf("embedjobs: scan job row: %w", err)
	}
	j.Kind = Kind(kind)
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
// empty string (no WHERE clause).
func buildListPredicates(f ListFilter) (where string, args []any) {
	conds := []string{}
	if f.CatalogID != "" {
		args = append(args, f.CatalogID)
		conds = append(conds, "catalog_id = $"+intToStr(len(args)))
	}
	if f.SpecName != "" {
		args = append(args, f.SpecName)
		conds = append(conds, "spec_name = $"+intToStr(len(args)))
	}
	if f.Status != "" {
		args = append(args, string(f.Status))
		conds = append(conds, "status = $"+intToStr(len(args)))
	}
	if f.Kind != "" {
		args = append(args, string(f.Kind))
		conds = append(conds, "kind = $"+intToStr(len(args)))
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// intToStr formats n as decimal. Thin wrapper around
// strconv.Itoa kept under a local name so callers in this file
// read consistently with the rest of the dynamic-predicate
// builder.
func intToStr(n int) string {
	return strconv.Itoa(n)
}

// isPGCode reports whether err is a *pq.Error with the given
// SQLSTATE.
func isPGCode(err error, code string) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code) == code
	}
	return false
}

// Ensure pgUniqueViolation and isPGCode are reachable from this
// file even though they are only used by tests or future code
// paths. Keeps the symbols from being marked dead by the linter.
var (
	_ = pgUniqueViolation
	_ = isPGCode
)
