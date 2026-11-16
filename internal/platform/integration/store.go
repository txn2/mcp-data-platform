package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/advisorylock"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/runstate"
)

// ErrNotBound is an operation with no script chosen.
var ErrNotBound = errors.New("no script is chosen for this operation")

// Store keeps bindings and items in PostgreSQL.
type Store struct{ DB *sql.DB }

const bindingColumns = `id::text, operation, script_id::text, interval_seconds, COALESCE(current_run_id, ''), retry_requested,
	last_started_at, last_run_id, last_status, last_reason, last_error, last_finished_at, created_by, updated_by, created_at, updated_at`

func scanBinding(row interface{ Scan(...any) error }) (*Binding, error) {
	b := &Binding{}
	if err := row.Scan(&b.ID, &b.Operation, &b.ScriptID, &b.IntervalSeconds, &b.CurrentRunID, &b.RetryRequested,
		&b.LastStartedAt, &b.LastRunID, &b.LastStatus, &b.LastReason, &b.LastError, &b.LastFinishedAt,
		&b.CreatedBy, &b.UpdatedBy, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err //nolint:wrapcheck // callers wrap
	}
	return b, nil
}

// Binding is the operation's binding, nil when no script is chosen.
func (s *Store) Binding(ctx context.Context, op string) (*Binding, error) {
	b, err := scanBinding(s.DB.QueryRowContext(ctx, `SELECT `+bindingColumns+` FROM integration_bindings WHERE operation = $1`, op))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // no script chosen is no binding, not an error
	}
	if err != nil {
		return nil, fmt.Errorf("reading the %s binding: %w", op, err)
	}
	return b, nil
}

// Bind chooses the script for an operation, or changes it or its interval.
// The next pass calls the script with every item.
func (s *Store) Bind(ctx context.Context, op, scriptID string, intervalSeconds int, by string) (*Binding, error) {
	b, err := scanBinding(s.DB.QueryRowContext(ctx, `
		INSERT INTO integration_bindings (operation, script_id, interval_seconds, retry_requested, created_by, updated_by)
		VALUES ($1, $2, $3, TRUE, $4, $4)
		ON CONFLICT (operation) DO UPDATE SET script_id = $2, interval_seconds = $3, retry_requested = TRUE,
			updated_by = $4, updated_at = NOW()
		RETURNING `+bindingColumns, op, scriptID, intervalSeconds, by))
	if err != nil {
		return nil, fmt.Errorf("choosing the %s script: %w", op, err)
	}
	s.wake(ctx)
	return b, nil
}

// Clear removes the operation's binding and every status it kept.
func (s *Store) Clear(ctx context.Context, op string) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("clearing the %s script: %w", op, err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM integration_bindings WHERE operation = $1`, op)
	if err != nil {
		return false, fmt.Errorf("clearing the %s script: %w", op, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM integration_items WHERE operation = $1`, op); err != nil {
		return false, fmt.Errorf("clearing the %s statuses: %w", op, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("clearing the %s script: %w", op, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RequestRetry asks the next pass to call the operation's script with every
// item, and wakes the passes.
func (s *Store) RequestRetry(ctx context.Context, op string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE integration_bindings SET retry_requested = TRUE WHERE operation = $1`, op)
	if err != nil {
		return fmt.Errorf("asking for a %s retry: %w", op, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotBound
	}
	s.wake(ctx)
	return nil
}

// Channel is where a change to an operation's items is announced.
const Channel = "integration_changed"

// wake announces a change every replica's loop hears.
func (s *Store) wake(ctx context.Context) {
	_, _ = s.DB.ExecContext(ctx, `SELECT pg_notify($1, 'binding')`, Channel)
}

// Items is every item the operation's script was last handed.
func (s *Store) Items(ctx context.Context, op string) ([]StoredItem, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT item_key, attrs, status, message, removing, failed_run, checked_at
		FROM integration_items WHERE operation = $1 ORDER BY item_key`, op)
	if err != nil {
		return nil, fmt.Errorf("reading the %s items: %w", op, err)
	}
	defer func() { _ = rows.Close() }()
	out := []StoredItem{}
	for rows.Next() {
		var it StoredItem
		var attrs []byte
		if err := rows.Scan(&it.Key, &attrs, &it.Status, &it.Message, &it.Removing, &it.FailedRun, &it.CheckedAt); err != nil {
			return nil, fmt.Errorf("reading the %s items: %w", op, err)
		}
		if err := json.Unmarshal(attrs, &it.Entry); err != nil {
			return nil, fmt.Errorf("reading the %s item %s: %w", op, it.Key, err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the %s items: %w", op, err)
	}
	return out, nil
}

// ItemsByKey is the stored items among keys, by key.
func (s *Store) ItemsByKey(ctx context.Context, op string, keys []string) (map[string]StoredItem, error) {
	out := map[string]StoredItem{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT item_key, status, message, failed_run, checked_at
		FROM integration_items WHERE operation = $1 AND item_key = ANY($2::text[]) AND NOT removing`, op, pq.Array(keys))
	if err != nil {
		return nil, fmt.Errorf("reading the %s statuses: %w", op, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var it StoredItem
		if err := rows.Scan(&it.Key, &it.Status, &it.Message, &it.FailedRun, &it.CheckedAt); err != nil {
			return nil, fmt.Errorf("reading the %s statuses: %w", op, err)
		}
		out[it.Key] = it
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the %s statuses: %w", op, err)
	}
	return out, nil
}

// insertRunSQL writes an integration's run, queued as an event's or a watch's
// run is: when its script runs one at a time it waits its turn rather than
// being refused.
const insertRunSQL = `
	INSERT INTO script_runs (id, script_id, script_version_id, version, trigger_kind, status, params,
	                         requested_by, fire_time, scheduled_for, state_revision, state_read, exclusive, queued,
	                         call, run_as)
	VALUES ($1, $2, $3, $4, 'integration', 'pending', '{}'::jsonb, $5, NOW(), NOW(),
	        ` + scriptstore.StateAtCreation + `, FALSE,
	        COALESCE((SELECT exclusive_mode <> 'off' FROM scripts WHERE id = $2), FALSE),
	        $6, $7)`

// StartRun records the items a run adds (pending) and removes (removing),
// queues the run and marks it the operation's run in flight, in one
// transaction, then tells the run workers.
func (s *Store) StartRun(ctx context.Context, op string, r NewRun) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting the %s run: %w", op, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := markItems(ctx, tx, op, r); err != nil {
		return err
	}
	callJSON, err := json.Marshal(r.Call)
	if err != nil {
		return fmt.Errorf("encoding the %s request: %w", op, err)
	}
	roles := r.Roles
	if roles == nil {
		roles = []string{}
	}
	runAs, _ := json.Marshal(runstate.RunAs{Email: r.Approver, Roles: roles})
	if _, err := tx.ExecContext(ctx, insertRunSQL, r.ID, r.ScriptID, r.VersionID, r.Version, r.Approver, callJSON, runAs); err != nil {
		return fmt.Errorf("queuing the %s run: %w", op, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE integration_bindings SET current_run_id = $2, retry_requested = FALSE,
		last_started_at = NOW(), last_reason = $3 WHERE operation = $1`, op, r.ID, r.Reason); err != nil {
		return fmt.Errorf("marking the %s run: %w", op, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("starting the %s run: %w", op, err)
	}
	_, _ = s.DB.ExecContext(ctx, `SELECT pg_notify($1, $2)`, scriptstore.NotifyChannel, r.ScriptID)
	return nil
}

// Lock takes the advisory lock one replica's pass holds.
func (s *Store) Lock(ctx context.Context) (release func(), ok bool, err error) {
	return advisorylock.Try(ctx, s.DB, advisorylock.ID(LockKey)) //nolint:wrapcheck // the caller names the lock
}

// markItems records the items a run adds as pending and the ones it removes
// as removing, in the run's transaction.
func markItems(ctx context.Context, tx *sql.Tx, op string, r NewRun) error {
	for _, it := range r.Added {
		attrs, _ := json.Marshal(it.Entry)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO integration_items (operation, item_key, attrs, status) VALUES ($1, $2, $3, 'pending')
			ON CONFLICT (operation, item_key) DO UPDATE SET attrs = $3, status = 'pending', message = '',
				removing = FALSE, failed_run = '', checked_at = NULL`, op, it.Key, attrs); err != nil {
			return fmt.Errorf("recording the %s item %s: %w", op, it.Key, err)
		}
	}
	if len(r.Removed) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE integration_items SET removing = TRUE
			WHERE operation = $1 AND item_key = ANY($2::text[])`, op, pq.Array(r.Removed)); err != nil {
			return fmt.Errorf("recording the %s removals: %w", op, err)
		}
	}
	return nil
}

// RecordRefusal records why a pass could not call the script, and holds the
// next try to the operation's interval.
func (s *Store) RecordRefusal(ctx context.Context, op, reason string) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE integration_bindings SET retry_requested = FALSE, last_started_at = NOW(),
		last_finished_at = NOW(), last_status = 'failed', last_error = $2, last_run_id = '' WHERE operation = $1`, op, reason); err != nil {
		return fmt.Errorf("recording why %s did not run: %w", op, err)
	}
	return nil
}

// RunOutcome reads a run; Finished is false while it is pending or running.
func (s *Store) RunOutcome(ctx context.Context, runID string) (RunOutcome, error) {
	var out RunOutcome
	var finished *time.Time
	var callJSON []byte
	err := s.DB.QueryRowContext(ctx, `SELECT status, COALESCE(error, ''), result, call, finished_at FROM script_runs WHERE id = $1`, runID).
		Scan(&out.Status, &out.Error, &out.Result, &callJSON, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		// A run that is gone (its script was deleted) ended.
		return RunOutcome{Finished: true, Status: "failed", Error: "the run no longer exists"}, nil
	}
	if err != nil {
		return out, fmt.Errorf("reading run %s: %w", runID, err)
	}
	out.Finished = finished != nil
	if len(callJSON) > 0 {
		_ = json.Unmarshal(callJSON, &out.Call)
	}
	return out, nil
}

// Apply writes a finished run's statuses, deletes the items it removed, and
// clears the run in flight, in one transaction.
func (s *Store) Apply(ctx context.Context, op string, a Applied) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("applying the %s run: %w", op, err)
	}
	defer func() { _ = tx.Rollback() }()
	for key, r := range a.Results {
		if _, err := tx.ExecContext(ctx, `UPDATE integration_items SET status = $3, message = $4, failed_run = '', checked_at = NOW()
			WHERE operation = $1 AND item_key = $2 AND NOT removing`, op, key, r.Status, r.Message); err != nil {
			return fmt.Errorf("recording the %s status of %s: %w", op, key, err)
		}
	}
	if len(a.Failed) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE integration_items SET status = 'failed', message = $3, failed_run = $4, checked_at = NOW()
			WHERE operation = $1 AND item_key = ANY($2::text[]) AND NOT removing`, op, pq.Array(a.Failed), a.FailWith, a.RunID); err != nil {
			return fmt.Errorf("recording the %s failure: %w", op, err)
		}
	}
	if len(a.Removed) > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM integration_items WHERE operation = $1 AND item_key = ANY($2::text[]) AND removing`,
			op, pq.Array(a.Removed)); err != nil {
			return fmt.Errorf("removing the %s items: %w", op, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE integration_bindings SET current_run_id = NULL, last_run_id = $2, last_status = $3,
		last_error = $4, last_finished_at = NOW() WHERE operation = $1 AND current_run_id = $2`, op, a.RunID, a.Status, a.Error); err != nil {
		return fmt.Errorf("closing the %s run: %w", op, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("applying the %s run: %w", op, err)
	}
	return nil
}
