package scriptstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Compile-time interface verification.
var _ script.RunStore = (*Store)(nil)

// NotifyChannel is the pg_notify channel a producer fires so a run worker
// wakes without waiting for its poll tick. Enqueue fires it best-effort; the
// worker polls regardless, so a missed notification costs latency and nothing
// else.
const NotifyChannel = "script_runs"

// defaultRunListLimit caps a run listing with no explicit limit.
const defaultRunListLimit = 50

// runColumns is the column list read by every script_runs SELECT, mirrored by
// scanRun so the scan order cannot drift from the query.
const runColumns = `id, script_id, script_version_id, version, trigger_kind, status,
	params, fire_time, requested_by, scheduled_for, started_at, finished_at, attempt,
	locked_until, locked_by, error, log_text, log_truncated, metrics, outputs,
	created_at, updated_at`

// runSelect is the base SELECT for the run columns.
const runSelect = "SELECT " + runColumns + " FROM script_runs"

// dueClause matches rows a worker may claim: pending rows whose schedule has
// arrived, plus running rows whose lease expired. Folding crashed-worker
// recovery into the claim predicate is what lets every replica run a worker
// with no reaper and no leader election.
const dueClause = `((status = 'pending' AND scheduled_for <= NOW())
	OR (status = 'running' AND locked_until < NOW()))`

// scanRun reads one row in runColumns order into a Run.
func scanRun(sc rowScanner) (*script.Run, error) {
	r := &script.Run{}
	var paramsJSON, metricsJSON, outputsJSON []byte
	err := sc.Scan(&r.ID, &r.ScriptID, &r.VersionID, &r.Version, &r.Trigger, &r.Status,
		&paramsJSON, &r.FireTime, &r.RequestedBy, &r.ScheduledFor, &r.StartedAt, &r.FinishedAt,
		&r.Attempt, &r.LockedUntil, &r.LockedBy, &r.Error, &r.Log, &r.LogTruncated,
		&metricsJSON, &outputsJSON, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("scanning script run row: %w", err)
	}
	if err := json.Unmarshal(paramsJSON, &r.Params); err != nil {
		return nil, fmt.Errorf("unmarshal run params: %w", err)
	}
	if err := json.Unmarshal(metricsJSON, &r.Metrics); err != nil {
		return nil, fmt.Errorf("unmarshal run metrics: %w", err)
	}
	if err := json.Unmarshal(outputsJSON, &r.Outputs); err != nil {
		return nil, fmt.Errorf("unmarshal run outputs: %w", err)
	}
	return r, nil
}

// Enqueue inserts a pending run and fires a best-effort wakeup so a worker
// claims it without waiting for the next poll.
//
// The run id is supplied by the caller rather than generated here: it is also
// the run's session id, minted before the run exists so every audit row the
// run produces carries it.
func (s *Store) Enqueue(ctx context.Context, r *script.Run) error {
	if r.ID == "" {
		return errors.New("a script run needs an id minted by its caller")
	}
	params, err := json.Marshal(orEmptyParams(r.Params))
	if err != nil {
		return fmt.Errorf("marshal run params: %w", err)
	}
	// A zero ScheduledFor or FireTime means "now", stamped with the database
	// clock so the claim predicate sees the row immediately regardless of
	// host/DB clock skew. The two are separate columns because a retry moves one
	// and must never move the other.
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO script_runs (id, script_id, script_version_id, version, trigger_kind,
		                         status, params, requested_by, fire_time, scheduled_for)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, NOW()), COALESCE($10, NOW()))
		RETURNING fire_time, scheduled_for, created_at, updated_at`,
		r.ID, r.ScriptID, r.VersionID, r.Version, r.Trigger, script.RunStatusPending,
		params, r.RequestedBy, orNilTime(r.FireTime), orNilTime(r.ScheduledFor))
	if err := row.Scan(&r.FireTime, &r.ScheduledFor, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return fmt.Errorf("enqueue script run: %w", err)
	}
	r.Status = script.RunStatusPending
	// Best-effort wakeup; the worker's poll ticker is the fallback.
	_, _ = s.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, r.ID)
	return nil
}

// orNilTime binds a zero time as NULL so the insert's COALESCE stamps the
// database clock instead.
func orNilTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// orEmptyParams normalizes nil params so the column stores {} rather than null.
func orEmptyParams(p map[string]any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return p
}

// GetRun returns one run by id.
func (s *Store) GetRun(ctx context.Context, id string) (*script.Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, runSelect+` WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, script.ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get script run: %w", err)
	}
	return r, nil
}

// ListRuns returns runs matching the filter, newest first.
func (s *Store) ListRuns(ctx context.Context, filter script.RunFilter) ([]script.Run, error) {
	query, args := buildRunListQuery(filter)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list script runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []script.Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate script runs: %w", err)
	}
	return out, nil
}

// buildRunListQuery assembles the run listing query and its arguments.
func buildRunListQuery(filter script.RunFilter) (query string, args []any) {
	q := &listQuery{}
	if filter.ScriptID != "" {
		q.add("script_id = $%d", filter.ScriptID)
	}
	if filter.Status != "" {
		q.add("status = $%d", filter.Status)
	}
	query = runSelect
	if len(q.where) > 0 {
		query += " WHERE " + joinAnd(q.where)
	}
	limit := filter.Limit
	if limit <= 0 || limit > defaultRunListLimit {
		limit = defaultRunListLimit
	}
	q.args = append(q.args, limit)
	return fmt.Sprintf("%s ORDER BY created_at DESC LIMIT $%d", query, len(q.args)), q.args
}

// Claim takes the next due run for worker and holds it for lease.
//
// The UPDATE is the claim: one statement that selects the oldest due row with
// FOR UPDATE SKIP LOCKED, marks it running, increments the attempt, and stamps
// the lease. Concurrent workers on other replicas skip each other's locked rows
// rather than blocking, and a run whose worker died is picked up by the next
// claim once its lease expires.
func (s *Store) Claim(ctx context.Context, worker string, lease time.Duration) (*script.Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `
		UPDATE script_runs
		   SET status = 'running', attempt = attempt + 1, locked_by = $1,
		       locked_until = NOW() + ($2 || ' seconds')::INTERVAL,
		       started_at = COALESCE(started_at, NOW()), updated_at = NOW()
		 WHERE id = (
		     SELECT id FROM script_runs
		      WHERE `+dueClause+`
		      ORDER BY scheduled_for, created_at
		      LIMIT 1
		      FOR UPDATE SKIP LOCKED)
		 RETURNING `+runColumns, worker, int(lease.Seconds())))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, script.ErrNoWork
	}
	if err != nil {
		return nil, fmt.Errorf("claim script run: %w", err)
	}
	return r, nil
}

// leaseClause fences a write to the worker that currently holds the run. A
// worker whose lease expired and whose run was reclaimed carries a stale
// (locked_by, attempt) pair, so its write matches no row.
const leaseClause = ` WHERE id = $1 AND locked_by = $2 AND attempt = $3`

// RecordOutput appends one persisted output to the claimed run.
//
// It is called as each output lands rather than once at the end, because the
// row is what a reclaimed run reads to know what it already wrote. The append
// is done in SQL (|| on the JSONB array) rather than read-modify-write in Go,
// so two writes cannot lose one another.
func (s *Store) RecordOutput(ctx context.Context, lease script.RunLease, out script.RunOutput) error {
	encoded, err := json.Marshal([]script.RunOutput{out})
	if err != nil {
		return fmt.Errorf("marshal run output: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE script_runs SET outputs = outputs || $4::jsonb, updated_at = NOW()`+leaseClause,
		lease.RunID, lease.Worker, lease.Attempt, encoded)
	if err != nil {
		return fmt.Errorf("record script run output: %w", err)
	}
	return requireLease(res, lease)
}

// Finish records a terminal result for the claimed run and clears its lease.
func (s *Store) Finish(ctx context.Context, lease script.RunLease, result script.RunResult) error {
	metrics, err := json.Marshal(result.Metrics)
	if err != nil {
		return fmt.Errorf("marshal run metrics: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE script_runs
		   SET status = $4, error = $5, log_text = $6, log_truncated = $7,
		       metrics = $8, finished_at = NOW(), locked_until = NULL,
		       updated_at = NOW()`+leaseClause,
		lease.RunID, lease.Worker, lease.Attempt,
		result.Status, result.Error, result.Log, result.LogTruncated, metrics)
	if err != nil {
		return fmt.Errorf("finish script run: %w", err)
	}
	if err := requireLease(res, lease); err != nil {
		return err
	}
	// Wake anything waiting on this run's completion.
	_, _ = s.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, lease.RunID)
	return nil
}

// Retry returns the claimed run to pending, due after backoff. It is for
// infrastructure failures only: a script error is deterministic and the same
// source on the same inputs fails the same way, so the worker never routes one
// here.
func (s *Store) Retry(ctx context.Context, lease script.RunLease, cause string, backoff time.Duration) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE script_runs
		   SET status = 'pending', locked_until = NULL, error = $4,
		       scheduled_for = NOW() + ($5 || ' seconds')::INTERVAL, updated_at = NOW()`+leaseClause,
		lease.RunID, lease.Worker, lease.Attempt, cause, int(backoff.Seconds()))
	if err != nil {
		return fmt.Errorf("retry script run: %w", err)
	}
	return requireLease(res, lease)
}

// requireLease turns a zero-row update into ErrLeaseLost, which is the signal
// that another worker reclaimed this run while this one was still working.
func requireLease(res sql.Result, lease script.RunLease) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking script run update: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("run %s attempt %d is no longer held by %s: %w",
			lease.RunID, lease.Attempt, lease.Worker, script.ErrLeaseLost)
	}
	return nil
}

// PurgeRuns deletes terminal runs older than retention.
//
// Only terminal rows are swept. A pending or running row is live work, and a
// retention pass that could delete it would silently drop a run somebody is
// waiting on.
func (s *Store) PurgeRuns(ctx context.Context, retention time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM script_runs
		 WHERE status IN ('succeeded', 'failed')
		   AND finished_at < NOW() - ($1 || ' seconds')::INTERVAL`,
		int(retention.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("purge script runs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting purged script runs: %w", err)
	}
	return n, nil
}
