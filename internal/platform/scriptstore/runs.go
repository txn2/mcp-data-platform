package scriptstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/runstate"
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
	COALESCE(schedule_id::text, ''), state_revision, state_read, state_written,
	state_revision_written, result, progress_message, progress_done, progress_total,
	progress_at, cancel_requested_at, cancel_requested_by, reclaims, attempts, claimed_at,
	heartbeat_at, failure_cause, created_at, updated_at`

// stateAtCreation is the VALUES fragment every run insert carries for the two
// state columns pinned at creation (#1537): the revision the script's state
// holds now and the object itself, or revision 0 and {} for a script with no
// state row. Read in the insert rather than by the caller so the row and the
// state it records cannot come from two different moments. $2 is the script
// id in both inserts that carry it (Enqueue and insertScheduledRun), which
// is what lets the fragment stay a constant the prepare gate can read.
const stateAtCreation = `COALESCE((SELECT revision FROM script_state WHERE script_id = $2), 0),
		        COALESCE((SELECT state FROM script_state WHERE script_id = $2), '{}'::jsonb)`

// runSelect is the base SELECT for the run columns.
const runSelect = "SELECT " + runColumns + " FROM script_runs"

// dueClause matches rows a worker may claim: pending rows whose schedule has
// arrived, plus running rows whose lease expired while their reclaims are
// under the cap ($3). Folding crashed-worker recovery into the claim predicate
// is what lets every replica run a worker with no reaper and no leader
// election; the cap is what keeps a run that kills its worker from killing
// every worker in turn (#1860). A row past the cap is never claimed, and
// FailAbandoned ends it.
const dueClause = `((status = 'pending' AND scheduled_for <= NOW())
	OR (status = 'running' AND locked_until < NOW() AND reclaims < $3))`

// attemptOpen begins the JSONB array of one history entry for the attempt the
// row currently records: its number, holder and claim time. Every write that
// ends an attempt appends one, closing it with the end time, the outcome and
// the error (#1860). It is a constant so every statement built from it is one
// the prepare gate reads as text.
const attemptOpen = `jsonb_build_array(jsonb_build_object(
	'attempt', attempt, 'worker', locked_by, 'claimed_at', claimed_at, `

// leaseExpiredEntry is the entry for an attempt whose worker stopped without
// reporting a result: it ended when its lease did.
const leaseExpiredEntry = attemptOpen + `'ended_at', locked_until, 'outcome', '` +
	runstate.AttemptLeaseExpired + `', 'error', ''))`

// liveColumns are the columns a run reports while it executes and hands back
// when it ends (#1845, #1847), read as their nullable forms.
type liveColumns struct {
	result      []byte
	message     string
	done, total sql.NullInt64
	at          *time.Time
}

// apply sets the run's result and, when it has reported any, its progress.
func (l liveColumns) apply(r *script.Run) {
	if len(l.result) > 0 {
		r.Result = l.result
	}
	if l.at == nil {
		return
	}
	p := &script.RunProgress{Message: l.message, At: *l.at}
	if l.done.Valid {
		p.Done = &l.done.Int64
	}
	if l.total.Valid {
		p.Total = &l.total.Int64
	}
	r.Progress = p
}

// scanRun reads one row in runColumns order into a Run.
func scanRun(sc rowScanner) (*script.Run, error) {
	r := &script.Run{}
	var (
		paramsJSON, metricsJSON, outputsJSON []byte
		stateRead, stateWritten              []byte
		attemptsJSON                         []byte
		revisionWritten                      sql.NullInt64
		live                                 liveColumns
	)
	err := sc.Scan(&r.ID, &r.ScriptID, &r.VersionID, &r.Version, &r.Trigger, &r.Status,
		&paramsJSON, &r.FireTime, &r.RequestedBy, &r.ScheduledFor, &r.StartedAt, &r.FinishedAt,
		&r.Attempt, &r.LockedUntil, &r.LockedBy, &r.Error, &r.Log, &r.LogTruncated,
		&metricsJSON, &outputsJSON, &r.ScheduleID, &r.StateRevision, &stateRead, &stateWritten,
		&revisionWritten, &live.result, &live.message, &live.done, &live.total, &live.at,
		&r.CancelRequestedAt, &r.CancelRequestedBy, &r.Reclaims, &attemptsJSON, &r.ClaimedAt,
		&r.HeartbeatAt, &r.Cause, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("scanning script run row: %w", err)
	}
	live.apply(r)
	if err := json.Unmarshal(attemptsJSON, &r.Attempts); err != nil {
		return nil, fmt.Errorf("unmarshal run attempts: %w", err)
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
	if err := json.Unmarshal(stateRead, &r.StateRead); err != nil {
		return nil, fmt.Errorf("unmarshal run state read: %w", err)
	}
	// state_written is NULL on a run that saved nothing, which scans as no
	// bytes; a run that saved {} has bytes and reads back as an empty object.
	if len(stateWritten) > 0 {
		if err := json.Unmarshal(stateWritten, &r.StateWritten); err != nil {
			return nil, fmt.Errorf("unmarshal run state written: %w", err)
		}
		if r.StateWritten == nil {
			r.StateWritten = map[string]any{}
		}
	}
	r.StateRevisionWritten = revisionWritten.Int64
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
	// and must never move the other. The state read is pinned in the same
	// statement, for the same reason the parameters are bound before the row
	// exists: it is an input of the run.
	var stateRead []byte
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO script_runs (id, script_id, script_version_id, version, trigger_kind,
		                         status, params, requested_by, fire_time, scheduled_for,
		                         state_revision, state_read)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, NOW()), COALESCE($10, NOW()),
		        `+stateAtCreation+`)
		RETURNING fire_time, scheduled_for, state_revision, state_read, created_at, updated_at`,
		r.ID, r.ScriptID, r.VersionID, r.Version, r.Trigger, script.RunStatusPending,
		params, r.RequestedBy, orNilTime(r.FireTime), orNilTime(r.ScheduledFor))
	if err := row.Scan(&r.FireTime, &r.ScheduledFor, &r.StateRevision, &stateRead, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return fmt.Errorf("enqueue script run: %w", err)
	}
	if err := json.Unmarshal(stateRead, &r.StateRead); err != nil {
		return fmt.Errorf("unmarshal run state read: %w", err)
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

// LatestRuns returns the most recent run of each named script, keyed by script
// id, omitting the scripts that have never been run.
//
// A listing that shows one row per script needs each script's last run, and
// asking for it script by script is a query per row. This is that answer in one
// query. It orders on creation rather than on completion — unlike the contract's
// last SUCCESSFUL run, which answers "what did this produce" and so must be a
// finished one — because a listing reports the state of the automation: a run
// that is pending or failed right now is the answer to "how is this going", and
// ordering by finished_at would hide it behind an older success.
func (s *Store) LatestRuns(ctx context.Context, scriptIDs []string) (map[string]script.Run, error) {
	out := map[string]script.Run{}
	if len(scriptIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT ON (script_id) `+runColumns+`
		  FROM script_runs
		 WHERE script_id = ANY($1)
		 ORDER BY script_id, created_at DESC, id DESC`, pq.Array(scriptIDs))
	if err != nil {
		return nil, fmt.Errorf("list latest script runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		r, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out[r.ScriptID] = *r
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest script runs: %w", err)
	}
	return out, nil
}

// buildRunListQuery assembles the run listing query and its arguments.
func buildRunListQuery(filter script.RunFilter) (query string, args []any) {
	q := &listQuery{}
	if filter.ScriptID != "" {
		q.add("script_id = $%d", filter.ScriptID)
	}
	if filter.ScriptIDs != nil {
		// pq.Array of an empty slice binds an empty array, so ANY(...) matches
		// nothing — which is exactly right for a caller who owns no scripts,
		// and is why the clause is added on non-nil rather than on non-empty.
		q.add("script_id = ANY($%d)", pq.Array(filter.ScriptIDs))
	}
	if filter.Status != "" {
		q.add("status = $%d", filter.Status)
	}
	if filter.RequestedBy != "" {
		q.add("requested_by = $%d", filter.RequestedBy)
	}
	if filter.Live {
		q.where = append(q.where, "status IN ('pending', 'running')")
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
// claim once its lease expires -- while its reclaims are under maxReclaims.
// Taking one over counts the reclaim and records the dead attempt in the run's
// history, in the same statement, so no worker of the dead one has to survive
// to say what happened (#1860).
func (s *Store) Claim(ctx context.Context, worker string, lease time.Duration, maxReclaims int) (*script.Run, error) {
	var reclaimed bool
	r, err := scanRun(runAndFlag{
		row: s.db.QueryRowContext(ctx, `
		WITH due AS (
		    SELECT id AS due_id, status AS prior_status FROM script_runs
		     WHERE `+dueClause+`
		     ORDER BY scheduled_for, created_at
		     LIMIT 1
		     FOR UPDATE SKIP LOCKED)
		UPDATE script_runs
		   SET status = 'running', attempt = attempt + 1, locked_by = $1,
		       locked_until = NOW() + ($2 || ' seconds')::INTERVAL,
		       reclaims = reclaims + CASE WHEN due.prior_status = 'running' THEN 1 ELSE 0 END,
		       attempts = CASE WHEN due.prior_status = 'running'
		                       THEN attempts || `+leaseExpiredEntry+`
		                       ELSE attempts END,
		       claimed_at = NOW(), heartbeat_at = NOW(),
		       started_at = COALESCE(started_at, NOW()), updated_at = NOW()
		  FROM due
		 WHERE id = due.due_id
		 RETURNING `+runColumns+`, due.prior_status = 'running'`, worker, int(lease.Seconds()), maxReclaims),
		flag: &reclaimed,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, script.ErrNoWork
	}
	if err != nil {
		return nil, fmt.Errorf("claim script run: %w", err)
	}
	r.Reclaimed = reclaimed
	return r, nil
}

// runAndFlag scans a row of the run columns followed by one boolean, which is
// how a claim reports whether it took the run over from a dead worker without
// a second query.
type runAndFlag struct {
	row  *sql.Row
	flag *bool
}

// Scan reads the run columns into dest and the trailing flag into flag.
func (f runAndFlag) Scan(dest ...any) error {
	return f.row.Scan(append(dest, f.flag)...) //nolint:wrapcheck // scanRun wraps it
}

// abandonedLimit bounds how many abandoned runs one sweep fails, so a backlog
// is worked through across ticks rather than in one long statement.
const abandonedLimit = 50

// failAbandonedSQL fails the running runs whose lease expired with their
// reclaims spent ($1), at most $2 of them.
const failAbandonedSQL = `
		UPDATE script_runs
		   SET status = 'failed', failure_cause = '` + runstate.CauseWorkerLost + `',
		       error = 'the worker executing this run stopped without reporting a result ' || (reclaims + 1)
		               || ' times (last held by ' || locked_by || ' until '
		               || to_char(locked_until AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		               || '); it is not run again, because a run that stops its worker stops the next one too. '
		               || 'The last attempts usually ran out of memory: page the work and export each page, '
		               || 'or ask for a larger memory limit',
		       attempts = attempts || ` + leaseExpiredEntry + `,
		       finished_at = NOW(), locked_until = NULL, locked_by = '', updated_at = NOW()
		 WHERE id IN (
		     SELECT id FROM script_runs
		      WHERE status = 'running' AND locked_until < NOW() AND reclaims >= $1
		      ORDER BY locked_until
		      LIMIT $2
		      FOR UPDATE SKIP LOCKED)
		 RETURNING ` + runColumns

// FailAbandoned fails every running run whose lease expired with its reclaims
// spent (#1860), and returns them for the caller to count and report.
//
// The error names how many times a worker stopped without reporting a result
// and the last holder, which is what an operator needs to find the replica
// that was killed. The lease is cleared, so a holder that was only slow and
// not dead is fenced out of every later write exactly as a reclaim fences it.
func (s *Store) FailAbandoned(ctx context.Context, maxReclaims int) ([]script.Run, error) {
	rows, err := s.db.QueryContext(ctx, failAbandonedSQL, maxReclaims, abandonedLimit)
	if err != nil {
		return nil, fmt.Errorf("failing abandoned script runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []script.Run{}
	for rows.Next() {
		r, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate abandoned script runs: %w", err)
	}
	if len(out) > 0 {
		// Wake anything waiting on these runs' completion.
		_, _ = s.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, out[0].ID)
	}
	return out, nil
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
//
// A succeeded run that staged state (#1537) has that state applied here, in
// the same transaction that marks it succeeded, predicated on the revision the
// run read. The two are one fact: a run recorded as succeeded whose state did
// not land, or state that landed for a run recorded as failed, would each make
// the run history lie about what the next run reads. A refused write turns
// THIS result into a failure naming the writer, and the run row records that;
// the run's outputs stand, because they were produced from the state it read.
func (s *Store) Finish(ctx context.Context, lease script.RunLease, result script.RunResult) error {
	metrics, err := json.Marshal(result.Metrics)
	if err != nil {
		return fmt.Errorf("marshal run metrics: %w", err)
	}
	if result.State == nil || result.Status != script.RunStatusSucceeded {
		if err := finishRow(ctx, s.db, terminalRow{lease: lease, result: result, metrics: metrics}); err != nil {
			return err
		}
	} else if err := s.finishWithState(ctx, lease, result, metrics); err != nil {
		return err
	}
	// Wake anything waiting on this run's completion.
	_, _ = s.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, lease.RunID)
	return nil
}

// finishWithState applies the run's staged state and records the outcome in
// one transaction. The script id and the revision the run read are taken from
// the run row under the lease, not from the caller, so a stale worker's write
// is refused before it reaches the state row.
func (s *Store) finishWithState(ctx context.Context, lease script.RunLease, result script.RunResult, metrics []byte) error {
	return s.withTx(ctx, "finish script run", func(tx *sql.Tx) error {
		w := runStateWrite{runID: lease.RunID, value: result.State.Value}
		err := tx.QueryRowContext(ctx,
			`SELECT script_id, state_revision FROM script_runs`+leaseClause+` FOR UPDATE`,
			lease.RunID, lease.Worker, lease.Attempt).Scan(&w.scriptID, &w.read)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("run %s attempt %d is no longer held by %s: %w",
				lease.RunID, lease.Attempt, lease.Worker, script.ErrLeaseLost)
		}
		if err != nil {
			return fmt.Errorf("reading the run's state revision: %w", err)
		}
		revision, err := writeRunState(ctx, tx, w)
		var conflict *script.StateConflictError
		switch {
		case errors.As(err, &conflict):
			// The interleaving is the run's failure, recorded on its row. Its
			// outputs are already recorded; nothing here touches them.
			result.Status, result.Error, result.Cause = script.RunStatusFailed, conflict.Error(), runstate.CauseStateConflict
			return finishRow(ctx, tx, terminalRow{lease: lease, result: result, metrics: metrics})
		case err != nil:
			return err
		}
		written, err := json.Marshal(orEmptyParams(result.State.Value))
		if err != nil {
			return fmt.Errorf("marshal run state written: %w", err)
		}
		return finishRow(ctx, tx, terminalRow{lease: lease, result: result, metrics: metrics, written: written, revision: revision})
	})
}

// terminalRow is one run's terminal write: the lease it is fenced on, the
// result, its encoded metrics, and the state it saved with the revision that
// produced, both NULL when it saved nothing.
type terminalRow struct {
	lease    script.RunLease
	result   script.RunResult
	metrics  []byte
	written  []byte
	revision int64
}

// execer is what finishRow writes through: the pool, or the transaction a
// state write shares.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// finishRow writes the terminal columns of the claimed run.
func finishRow(ctx context.Context, db execer, row terminalRow) error {
	var (
		stateWritten    any
		revisionWritten any
	)
	if row.written != nil {
		stateWritten, revisionWritten = row.written, row.revision
	}
	p := progressArgs(row.result.Progress)
	res, err := db.ExecContext(ctx, `
		UPDATE script_runs
		   SET status = $4, error = $5, log_text = $6, log_truncated = $7,
		       metrics = $8, state_written = $9, state_revision_written = $10,
		       result = $11,
		       progress_message = CASE WHEN $14::timestamptz IS NULL THEN progress_message ELSE $12 END,
		       progress_done = CASE WHEN $14::timestamptz IS NULL THEN progress_done ELSE $13 END,
		       progress_total = CASE WHEN $14::timestamptz IS NULL THEN progress_total ELSE $15 END,
		       progress_at = COALESCE($14::timestamptz, progress_at),
		       failure_cause = $16,
		       attempts = attempts || `+finishedEntry+`,
		       finished_at = NOW(), locked_until = NULL, updated_at = NOW()`+leaseClause,
		row.lease.RunID, row.lease.Worker, row.lease.Attempt,
		row.result.Status, row.result.Error, row.result.Log, row.result.LogTruncated, row.metrics,
		stateWritten, revisionWritten, nullJSON(row.result.Result),
		p.message, p.done, p.at, p.total, failureCause(row.result))
	if err != nil {
		return fmt.Errorf("finish script run: %w", err)
	}
	return requireLease(res, row.lease)
}

// finishedEntry is the history entry for an attempt that recorded the run's
// verdict; its error is the result's ($5).
const finishedEntry = attemptOpen + `'ended_at', NOW(), 'outcome', '` +
	runstate.AttemptFinished + `', 'error', $5::text))`

// failureCause is the cause a result is recorded with: its own, or the
// script's for a failure that names none, since every failure that reaches
// the interpreter and is not classified otherwise is the script's. A result
// that did not fail records none.
func failureCause(r script.RunResult) string {
	if r.Status != script.RunStatusFailed {
		return ""
	}
	if r.Cause == "" {
		return runstate.CauseScript
	}
	return r.Cause
}

// nullJSON binds an absent JSON value as NULL rather than as an empty string,
// which JSONB would refuse.
func nullJSON(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

// RecordProgress writes the claimed run's latest progress and the log it has
// printed so far, and reads back whether a cancel was requested and by whom
// (#1847). It is one statement, fenced on the lease like every other write a
// worker makes, so the report and the cancel check cannot come from different
// moments and a worker that lost its run neither writes nor learns anything.
// An unchanged report leaves what the run reported as it is, and still stamps
// heartbeat_at: the report is also the worker saying it is alive (#1860).
func (s *Store) RecordProgress(ctx context.Context, lease script.RunLease, live script.RunLive) (requested bool, by string, err error) {
	p := progressArgs(live.Progress)
	var cancelAt *time.Time
	err = s.db.QueryRowContext(ctx, `
		UPDATE script_runs
		   SET log_text = CASE WHEN $10 THEN log_text ELSE $4 END,
		       log_truncated = CASE WHEN $10 THEN log_truncated ELSE $5 END,
		       progress_message = CASE WHEN $10 OR $8::timestamptz IS NULL THEN progress_message ELSE $6 END,
		       progress_done = CASE WHEN $10 OR $8::timestamptz IS NULL THEN progress_done ELSE $7 END,
		       progress_total = CASE WHEN $10 OR $8::timestamptz IS NULL THEN progress_total ELSE $9 END,
		       progress_at = CASE WHEN $10 THEN progress_at ELSE COALESCE($8::timestamptz, progress_at) END,
		       heartbeat_at = NOW(), updated_at = NOW()`+leaseClause+`
		 RETURNING cancel_requested_at, cancel_requested_by`,
		lease.RunID, lease.Worker, lease.Attempt, live.Log, live.LogTruncated,
		p.message, p.done, p.at, p.total, live.Unchanged).Scan(&cancelAt, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", fmt.Errorf("run %s attempt %d is no longer held by %s: %w",
			lease.RunID, lease.Attempt, lease.Worker, script.ErrLeaseLost)
	}
	if err != nil {
		return false, "", fmt.Errorf("record script run progress: %w", err)
	}
	return cancelAt != nil, by, nil
}

// progressColumns are a progress report bound as the columns store it, each
// NULL when the report does not carry it.
type progressColumns struct {
	message         string
	done, total, at any
}

// progressArgs binds a progress report; nil binds as no report at all.
func progressArgs(p *script.RunProgress) progressColumns {
	var out progressColumns
	if p == nil {
		return out
	}
	out.message, out.at = p.Message, p.At
	if p.Done != nil {
		out.done = *p.Done
	}
	if p.Total != nil {
		out.total = *p.Total
	}
	return out
}

// CancelRun stops a run for by (#1847) and reports the status it had when
// the request arrived and the status it has now. The row is locked first and
// that status read under the lock, so it is the one the update acted on: a
// pending run is finished as canceled in the same statement, so it cannot be
// claimed in between; a running run is marked, and its worker stops it at its
// next report; anything else has already ended and is left alone.
//
// A running run whose worker is gone -- its lease expired, or it has not
// reported for runstate.HeartbeatStaleAfter -- is canceled here instead (#1860):
// no worker will ever read the mark, and waiting for one is how an orphaned
// run went on reading as running. Its lease is cleared with it, so a holder
// that was only slow finds every later write refused.
func (s *Store) CancelRun(ctx context.Context, id, by string) (prior, now string, err error) {
	err = s.db.QueryRowContext(ctx, `
		WITH prior AS (
		    SELECT id, status,
		           status = 'running' AND (locked_until IS NULL OR locked_until < NOW()
		               OR COALESCE(heartbeat_at, claimed_at, started_at) < NOW() - ($3 || ' seconds')::INTERVAL) AS orphaned
		      FROM script_runs WHERE id = $1 FOR UPDATE)
		UPDATE script_runs r
		   SET status = CASE WHEN prior.status = 'pending' OR prior.orphaned THEN 'canceled' ELSE r.status END,
		       finished_at = CASE WHEN prior.status = 'pending' OR prior.orphaned THEN NOW() ELSE r.finished_at END,
		       error = CASE WHEN prior.status = 'pending' THEN 'canceled by ' || $2
		                    WHEN prior.orphaned THEN 'canceled by ' || $2 || '; the worker executing it had stopped '
		                         || 'reporting, so the run was ended directly'
		                    ELSE r.error END,
		       attempts = CASE WHEN prior.orphaned THEN r.attempts || `+orphanedEntry+` ELSE r.attempts END,
		       locked_until = CASE WHEN prior.orphaned THEN NULL ELSE r.locked_until END,
		       locked_by = CASE WHEN prior.orphaned THEN '' ELSE r.locked_by END,
		       cancel_requested_at = CASE WHEN prior.status IN ('pending', 'running')
		                                  THEN COALESCE(r.cancel_requested_at, NOW())
		                                  ELSE r.cancel_requested_at END,
		       cancel_requested_by = CASE WHEN prior.status IN ('pending', 'running') AND r.cancel_requested_by = ''
		                                  THEN $2 ELSE r.cancel_requested_by END,
		       updated_at = NOW()
		  FROM prior
		 WHERE r.id = prior.id
		 RETURNING prior.status, r.status`,
		id, by, int(runstate.HeartbeatStaleAfter.Seconds())).Scan(&prior, &now)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", script.ErrRunNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("cancel script run: %w", err)
	}
	return prior, now, nil
}

// orphanedEntry is the history entry for the attempt a cancel ended because
// its worker was gone: lease_expired when the lease had run out, unresponsive
// when it still ran but the worker had stopped reporting. The columns are the
// row's (r), since the statement joins the locked prior row.
const orphanedEntry = `jsonb_build_array(jsonb_build_object(
	'attempt', r.attempt, 'worker', r.locked_by, 'claimed_at', r.claimed_at, 'ended_at', NOW(),
	'outcome', CASE WHEN r.locked_until IS NULL OR r.locked_until < NOW()
	                THEN '` + runstate.AttemptLeaseExpired + `' ELSE '` + runstate.AttemptUnresponsive + `' END,
	'error', 'canceled by ' || $2))`

// Retry returns the claimed run to pending, due after backoff, recording how
// the attempt ended in the run's history. It is for infrastructure failures
// only: a script error is deterministic and the same source on the same inputs
// fails the same way, so the worker never routes one here.
func (s *Store) Retry(ctx context.Context, lease script.RunLease, outcome, reason string, backoff time.Duration) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE script_runs
		   SET status = 'pending', locked_until = NULL, error = $4,
		       attempts = attempts || `+retriedEntry+`,
		       scheduled_for = NOW() + ($5 || ' seconds')::INTERVAL, updated_at = NOW()`+leaseClause,
		lease.RunID, lease.Worker, lease.Attempt, reason, int(backoff.Seconds()), outcome)
	if err != nil {
		return fmt.Errorf("retry script run: %w", err)
	}
	return requireLease(res, lease)
}

// retriedEntry is the history entry for an attempt that returned the run to
// the queue: outcome $6, reason $4.
const retriedEntry = attemptOpen + `'ended_at', NOW(), 'outcome', $6::text, 'error', $4::text))`

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
// Only terminal rows are swept, which now includes the skipped-overlap rows a
// schedule records: a skip is history the same way a failure is, and it carries
// a finished_at from the moment it exists so it ages out on the same clock. A
// pending or running row is live work, and a retention pass that could delete
// it would silently drop a run somebody is waiting on.
func (s *Store) PurgeRuns(ctx context.Context, retention time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM script_runs
		 WHERE status IN ('succeeded', 'failed', 'skipped_overlap', 'canceled')
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
