package scriptevents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// EventEnqueue is what enqueuing one source's events for one script did
// (#2332): runs made pending, events recorded as skipped past the bound, and
// events that already had a run, which made none.
type EventEnqueue struct {
	Enqueued int
	Skipped  int
	Already  int
}

// EventSummary is how a script's event runs stand: how many wait, and how
// many were skipped past the bound since a moment.
type EventSummary struct {
	Pending       int        `json:"pending"`
	SkippedSince  int        `json:"skipped_since"`
	LastEventRun  *time.Time `json:"last_event_run,omitempty"`
	LastSkippedAt *time.Time `json:"last_skipped_at,omitempty"`
}

// scriptsForSourceSQL lists the scripts that run for a webhook source.
const scriptsForSourceSQL = `SELECT id FROM scripts WHERE event_source = $1 ORDER BY id`

// lockScriptSQL holds a script's row for the transaction, so two replicas
// enqueuing events for one script count its pending event runs one after the
// other.
const lockScriptSQL = `SELECT id FROM scripts WHERE id = $1 FOR UPDATE`

// pendingEventRunsSQL counts a script's event runs that wait for a worker.
const pendingEventRunsSQL = `SELECT COUNT(*) FROM script_runs
	 WHERE script_id = $1 AND trigger_kind = 'event' AND status = 'pending'`

// insertEventRunSQL writes one event's run: pending, or, past the bound,
// skipped_backlog with its reason and finished. A run is queued when its
// script runs one at a time in either mode, so an event waits its turn rather
// than being refused: the event is stored, and its run is what the setting
// holds back. The index on (script_id, event_source, event_id) makes an event
// that already has a run insert nothing.
const insertEventRunSQL = `
		INSERT INTO script_runs (id, script_id, script_version_id, version, trigger_kind,
		                         status, params, requested_by, fire_time, scheduled_for,
		                         state_revision, state_read, exclusive, queued,
		                         event_source, event_id, event, error, finished_at)
		VALUES ($1, $2, $3, $4, 'event', $5, $6, $7, NOW(), NOW(),
		        ` + scriptstore.StateAtCreation + `, FALSE,
		        COALESCE((SELECT exclusive_mode <> 'off' FROM scripts WHERE id = $2), FALSE),
		        $8, $9, $10, $11, CASE WHEN $5 = 'skipped_backlog' THEN NOW() END)
		ON CONFLICT (script_id, event_source, event_id) WHERE event_id <> '' DO NOTHING`

// eventSummarySQL is a script's pending event runs, its skipped ones since
// $2, and the newest of each.
const eventSummarySQL = `
		SELECT COUNT(*) FILTER (WHERE status = 'pending'),
		       COUNT(*) FILTER (WHERE status = 'skipped_backlog' AND created_at >= $2),
		       MAX(created_at) FILTER (WHERE status <> 'skipped_backlog'),
		       MAX(created_at) FILTER (WHERE status = 'skipped_backlog')
		  FROM script_runs
		 WHERE script_id = $1 AND trigger_kind = 'event'`

// PostgresStore keeps event runs in script_runs and reads which scripts run
// for a source from scripts (#2332).
type PostgresStore struct{ db *sql.DB }

// NewStore is the PostgreSQL event run store.
func NewStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

// ScriptsForSource returns the ids of the scripts that run for source.
func (s *PostgresStore) ScriptsForSource(ctx context.Context, source string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, scriptsForSourceSQL, source)
	if err != nil {
		return nil, fmt.Errorf("listing the scripts that run for source %s: %w", source, err)
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning a script that runs for source %s: %w", source, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the scripts that run for source %s: %w", source, err)
	}
	return ids, nil
}

// EnqueueEvents writes one run per event for the script the runs name, in
// one transaction holding the script's row: each is pending while the
// script's pending event runs are under maxQueued, and recorded as
// skipped_backlog past it. An event that already has a run makes none. A
// worker is woken when any run was made pending. Every run carries the same
// script and version; its Event, EventSource and EventID name the event.
func (s *PostgresStore) EnqueueEvents(ctx context.Context, runs []*script.Run, maxQueued int) (EventEnqueue, error) {
	var out EventEnqueue
	if len(runs) == 0 {
		return out, nil
	}
	scriptID := runs[0].ScriptID
	err := withTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, lockScriptSQL, scriptID); err != nil {
			return fmt.Errorf("locking script %s: %w", scriptID, err)
		}
		var pending int
		if err := tx.QueryRowContext(ctx, pendingEventRunsSQL, scriptID).Scan(&pending); err != nil {
			return fmt.Errorf("counting the pending event runs of %s: %w", scriptID, err)
		}
		var err error
		out, err = insertEventRuns(ctx, tx, runs, pending, maxQueued)
		return err
	})
	if err != nil {
		return EventEnqueue{}, err
	}
	if out.Enqueued > 0 {
		_, _ = s.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, scriptstore.NotifyChannel, scriptID)
	}
	return out, nil
}

// insertEventRuns writes each event's run, pending until the script holds
// maxQueued pending event runs and skipped past it.
func insertEventRuns(ctx context.Context, tx *sql.Tx, runs []*script.Run, pending, maxQueued int) (EventEnqueue, error) {
	var out EventEnqueue
	for _, r := range runs {
		made, skipped, err := insertEventRun(ctx, tx, r, pending >= maxQueued, maxQueued)
		if err != nil {
			return EventEnqueue{}, err
		}
		switch {
		case !made:
			out.Already++
		case skipped:
			out.Skipped++
		default:
			out.Enqueued++
			pending++
		}
	}
	return out, nil
}

// insertEventRun writes one event's run, skipped when full, and reports
// whether a row was made and whether it was skipped.
func insertEventRun(ctx context.Context, tx *sql.Tx, r *script.Run, full bool, maxQueued int) (made, skipped bool, err error) {
	if r.ID == "" || r.EventID == "" {
		return false, false, errors.New("an event run needs a run id and an event id")
	}
	if r.Params == nil {
		r.Params = map[string]any{}
	}
	params, err := json.Marshal(r.Params)
	if err != nil {
		return false, false, fmt.Errorf("marshal run params: %w", err)
	}
	event, err := json.Marshal(r.Event)
	if err != nil {
		return false, false, fmt.Errorf("marshal run event: %w", err)
	}
	status, reason := script.RunStatusPending, ""
	if full {
		status = runstate.StatusSkippedBacklog
		reason = fmt.Sprintf("not run: %d event runs of this script were already waiting, the most scripts.events.max_queued allows; "+
			"the event is stored in source %s", maxQueued, r.EventSource)
	}
	res, err := tx.ExecContext(ctx, insertEventRunSQL,
		r.ID, r.ScriptID, r.VersionID, r.Version, status, params, r.RequestedBy,
		r.EventSource, r.EventID, event, reason)
	if err != nil {
		return false, false, fmt.Errorf("enqueue event run: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, false, fmt.Errorf("enqueue event run: %w", err)
	}
	return n > 0, full, nil
}

// EventSummary reports how the script's event runs stand, counting the ones
// skipped since since.
func (s *PostgresStore) EventSummary(ctx context.Context, scriptID string, since time.Time) (EventSummary, error) {
	var out EventSummary
	err := s.db.QueryRowContext(ctx, eventSummarySQL, scriptID, since).
		Scan(&out.Pending, &out.SkippedSince, &out.LastEventRun, &out.LastSkippedAt)
	if err != nil {
		return EventSummary{}, fmt.Errorf("reading the event runs of %s: %w", scriptID, err)
	}
	return out, nil
}

// withTx runs fn in a transaction, committing when it succeeds.
func withTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin enqueue event runs: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit enqueue event runs: %w", err)
	}
	return nil
}
