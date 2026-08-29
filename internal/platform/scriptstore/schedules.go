package scriptstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Compile-time interface verification.
var _ script.ScheduleStore = (*Store)(nil)

// defaultScheduleListLimit caps a schedule listing with no explicit limit.
const defaultScheduleListLimit = 200

// defaultDueLimit caps how many due schedules one materialization pass reads.
// A pass that hits it leaves the rest for the next tick rather than holding one
// replica in a long loop while others idle.
const defaultDueLimit = 100

// scheduleColumns is the column list read by every script_schedules SELECT,
// mirrored by scanSchedule so the scan order cannot drift from the query.
const scheduleColumns = `id, script_id, cron_spec, timezone, params, enabled,
	next_run_at, last_fire_at, missed_fires, created_by, updated_by,
	created_at, updated_at`

// scheduleSelect is the base SELECT for the schedule columns.
const scheduleSelect = "SELECT " + scheduleColumns + " FROM script_schedules"

// scanSchedule reads one row in scheduleColumns order into a Schedule.
func scanSchedule(sc rowScanner) (*script.Schedule, error) {
	s := &script.Schedule{}
	var paramsJSON []byte
	var nextRunAt sql.NullTime
	err := sc.Scan(&s.ID, &s.ScriptID, &s.CronSpec, &s.Timezone, &paramsJSON, &s.Enabled,
		&nextRunAt, &s.LastFireAt, &s.MissedFires, &s.CreatedBy, &s.UpdatedBy,
		&s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("scanning script schedule row: %w", err)
	}
	if nextRunAt.Valid {
		s.NextRunAt = nextRunAt.Time
	}
	if err := json.Unmarshal(paramsJSON, &s.Params); err != nil {
		return nil, fmt.Errorf("unmarshal schedule params: %w", err)
	}
	return s, nil
}

// SetSchedule creates or replaces one script's schedule.
//
// The upsert is keyed on script_id rather than on the schedule id because the
// script is what a caller names: "set the schedule of daily-sales" must not
// depend on whether one already exists. Replacing keeps the id and the creation
// stamp, so a schedule's identity — and the runs that point at it — survive an
// edit of its cadence.
func (s *Store) SetSchedule(ctx context.Context, sched *script.Schedule) error {
	params, err := json.Marshal(orEmptyParams(sched.Params))
	if err != nil {
		return fmt.Errorf("marshal schedule params: %w", err)
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO script_schedules (script_id, cron_spec, timezone, params, enabled,
		                              next_run_at, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (script_id) DO UPDATE
		   SET cron_spec = EXCLUDED.cron_spec, timezone = EXCLUDED.timezone,
		       params = EXCLUDED.params, enabled = EXCLUDED.enabled,
		       next_run_at = EXCLUDED.next_run_at, updated_by = EXCLUDED.updated_by,
		       updated_at = NOW()
		RETURNING id, created_at, updated_at`,
		sched.ScriptID, sched.CronSpec, sched.Timezone, params, sched.Enabled,
		orNilTime(sched.NextRunAt), sched.UpdatedBy)
	if err := row.Scan(&sched.ID, &sched.CreatedAt, &sched.UpdatedAt); err != nil {
		return fmt.Errorf("set script schedule: %w", err)
	}
	return nil
}

// GetSchedule returns one script's schedule.
func (s *Store) GetSchedule(ctx context.Context, scriptID string) (*script.Schedule, error) {
	sched, err := scanSchedule(s.db.QueryRowContext(ctx, scheduleSelect+` WHERE script_id = $1`, scriptID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, script.ErrScheduleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get script schedule: %w", err)
	}
	return sched, nil
}

// ListSchedules returns schedules matching the filter, newest first.
func (s *Store) ListSchedules(ctx context.Context, filter script.ScheduleFilter) ([]script.Schedule, error) {
	q := &listQuery{}
	if filter.ScriptID != "" {
		q.add("script_id = $%d", filter.ScriptID)
	}
	if filter.ScriptIDs != nil {
		// pq.Array of an empty slice binds an empty array, so ANY(...) matches
		// nothing — which is exactly right for a caller who may see no scripts,
		// and is why the clause is added on non-nil rather than on non-empty.
		q.add("script_id = ANY($%d)", pq.Array(filter.ScriptIDs))
	}
	if filter.Enabled != nil {
		q.add("enabled = $%d", *filter.Enabled)
	}
	query := scheduleSelect
	if len(q.where) > 0 {
		query += " WHERE " + joinAnd(q.where)
	}
	limit := filter.Limit
	if limit <= 0 || limit > defaultScheduleListLimit {
		limit = defaultScheduleListLimit
	}
	q.args = append(q.args, limit)
	return s.querySchedules(ctx,
		fmt.Sprintf("%s ORDER BY created_at DESC LIMIT $%d", query, len(q.args)), q.args...)
}

// querySchedules runs a schedule listing query.
func (s *Store) querySchedules(ctx context.Context, query string, args ...any) ([]script.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list script schedules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []script.Schedule{}
	for rows.Next() {
		sched, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sched)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate script schedules: %w", err)
	}
	return out, nil
}

// SetScheduleEnabled turns a schedule on or off.
//
// Enabling does not move next_run_at. A schedule re-enabled after a pause is
// therefore due for whatever fire it was parked on, which the misfire policy
// then collapses to one run — the same treatment downtime gets, which is what a
// pause is.
func (s *Store) SetScheduleEnabled(ctx context.Context, scriptID string, enabled bool, actor string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE script_schedules
		   SET enabled = $2, updated_by = $3, updated_at = NOW()
		 WHERE script_id = $1`, scriptID, enabled, actor)
	if err != nil {
		return fmt.Errorf("set script schedule enabled: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return script.ErrScheduleNotFound
	}
	return nil
}

// DueSchedules returns enabled schedules whose next fire has arrived.
//
// now is passed in rather than read from the database clock so a caller can
// walk a schedule deterministically in a test; the correctness of a fire does
// not depend on which clock decided it was due, only on the unique index the
// resulting insert conflicts against.
func (s *Store) DueSchedules(ctx context.Context, now time.Time, limit int) ([]script.Schedule, error) {
	if limit <= 0 || limit > defaultDueLimit {
		limit = defaultDueLimit
	}
	return s.querySchedules(ctx, scheduleSelect+`
		 WHERE enabled AND next_run_at IS NOT NULL AND next_run_at <= $1
		 ORDER BY next_run_at
		 LIMIT $2`, now.UTC(), limit)
}

// MaterializeRun inserts one scheduled run and reports what happened.
//
// The three outcomes are decided by the two unique indexes, not by the read
// that precedes them:
//
//   - the insert lands: this caller materialized the fire;
//   - it conflicts and a row for (schedule, fire time) exists: another replica
//     materialized the same fire, which is the normal outcome of racing
//     materializers and not a fault;
//   - it conflicts and no such row exists: the conflict was the one-open-run
//     index, so the previous run is still going and the overlap policy applies.
//     The skip is then recorded as its own terminal row, through the same
//     conflict-tolerant insert, so two replicas racing to record it also
//     produce exactly one.
func (s *Store) MaterializeRun(ctx context.Context, r *script.Run) (script.Materialization, error) {
	inserted, err := s.insertScheduledRun(ctx, r, script.RunStatusPending)
	if err != nil {
		return "", err
	}
	if inserted {
		r.Status = script.RunStatusPending
		// Best-effort wakeup, matching Enqueue: the worker polls regardless.
		_, _ = s.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, r.ID)
		return script.MaterializedRun, nil
	}
	taken, err := s.fireTaken(ctx, r.ScheduleID, r.FireTime)
	if err != nil {
		return "", err
	}
	if taken {
		return script.MaterializedDuplicate, nil
	}
	skipped, err := s.insertScheduledRun(ctx, r, script.RunStatusSkippedOverlap)
	if err != nil {
		return "", err
	}
	if !skipped {
		// Another replica recorded the same skip between the check above and
		// this insert. One row for the fire is the whole point, so this caller
		// reports the duplicate rather than claiming a skip it did not record.
		return script.MaterializedDuplicate, nil
	}
	r.Status = script.RunStatusSkippedOverlap
	return script.MaterializedSkippedOverlap, nil
}

// insertScheduledRun inserts one run row for a schedule fire, tolerating a
// conflict on either unique index, and reports whether the row landed.
//
// A skipped row is stamped finished on arrival: it is terminal from the moment
// it exists, and leaving finished_at empty would keep it out of the retention
// sweep forever.
func (s *Store) insertScheduledRun(ctx context.Context, r *script.Run, status string) (bool, error) {
	params, err := json.Marshal(orEmptyParams(r.Params))
	if err != nil {
		return false, fmt.Errorf("marshal run params: %w", err)
	}
	var stateRead []byte
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO script_runs (id, script_id, script_version_id, version, trigger_kind,
		                         status, params, requested_by, fire_time, scheduled_for,
		                         schedule_id, error, finished_at, state_revision, state_read)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $10, $11,
		        CASE WHEN $6 = 'skipped_overlap' THEN NOW() END,
		        `+stateAtCreation+`)
		ON CONFLICT DO NOTHING
		RETURNING state_revision, state_read, created_at, updated_at`,
		r.ID, r.ScriptID, r.VersionID, r.Version, script.TriggerSchedule,
		status, params, r.RequestedBy, r.FireTime, r.ScheduleID, overlapReason(status))
	err = row.Scan(&r.StateRevision, &stateRead, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("materialize scheduled run: %w", err)
	}
	if err := json.Unmarshal(stateRead, &r.StateRead); err != nil {
		return false, fmt.Errorf("unmarshal run state read: %w", err)
	}
	return true, nil
}

// overlapReason is the explanation a skipped row carries, so a reader of the
// run history is told why the fire produced nothing without having to correlate
// it against the run before it.
func overlapReason(status string) string {
	if status != script.RunStatusSkippedOverlap {
		return ""
	}
	return "the previous run of this schedule was still going when this one came due, so this fire was skipped"
}

// fireTaken reports whether a run already exists for a schedule's fire time.
// It is the question that separates "another replica won the race" from "the
// previous run is still open", which the insert alone cannot answer.
func (s *Store) fireTaken(ctx context.Context, scheduleID string, fire time.Time) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM script_runs WHERE schedule_id = $1 AND fire_time = $2)`,
		scheduleID, fire).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking a materialized fire: %w", err)
	}
	return exists, nil
}

// AdvanceSchedule moves a schedule forward, only if it is still where the
// caller found it.
//
// The From guard is what keeps two replicas that walked the same fire from
// double-counting the misses or moving the schedule twice: the second UPDATE
// matches no row and the caller learns it lost the race. It is an efficiency
// measure, not the single-fire guarantee — that one belongs to the unique index
// on the run — which is why losing here is silent.
func (s *Store) AdvanceSchedule(ctx context.Context, adv script.ScheduleAdvance) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE script_schedules
		   SET next_run_at = $3,
		       last_fire_at = COALESCE($4, last_fire_at),
		       missed_fires = missed_fires + $5,
		       updated_at = NOW()
		 WHERE id = $1 AND next_run_at = $2`,
		adv.ID, adv.From, orNilTime(adv.Next), orNilTime(adv.Fired), adv.Missed)
	if err != nil {
		return false, fmt.Errorf("advance script schedule: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("checking a schedule advance: %w", err)
	}
	return n > 0, nil
}
