package callrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// DefaultMaxQueued is how many runs one watch may have waiting; past it a
// call is recorded as skipped, as a webhook handler's backlog is bounded.
const DefaultMaxQueued = 100

// queueSize is how many queued watch runs wait in memory for the writer.
const queueSize = 1024

// watchRun is one run a watch is owed: the handler, its script's approved
// version and approver, and the call.
type watchRun struct {
	handler callhandler.Handler
	t       target
	call    map[string]any
}

// WatchQueue writes the runs watches are owed off the request path (#2635):
// a matched call that succeeded hands its runs here and answers at once, and
// one writer stores them. A replica lost between the answer and the write
// loses the runs as it loses the call's audit row, which the audit writer
// buffers the same way.
type WatchQueue struct {
	DB        *sql.DB
	MaxQueued int
	// Linger is how long the writer waits after a run arrives for more to
	// join its write (DefaultLinger): a burst of matched calls is then one
	// transaction and one wake of the workers rather than one per call, so
	// the writes compete less with the calls themselves. 0 writes at once.
	Linger time.Duration
	ch     chan watchRun
}

// DefaultLinger is how long a watch run waits for others to join its write.
const DefaultLinger = 100 * time.Millisecond

// NewWatchQueue is the queue over db, nil without one.
func NewWatchQueue(db *sql.DB, maxQueued int) *WatchQueue {
	if db == nil {
		return nil
	}
	if maxQueued <= 0 {
		maxQueued = DefaultMaxQueued
	}
	return &WatchQueue{DB: db, MaxQueued: maxQueued, Linger: DefaultLinger, ch: make(chan watchRun, queueSize)}
}

// batchSize is the most runs one write stores.
const batchSize = 64

// Run writes queued runs until ctx ends: each wake drains what has queued,
// up to batchSize, into one transaction, and tells each script's workers
// once, so a burst of matched calls is a few writes rather than one per call.
func (q *WatchQueue) Run(ctx context.Context) {
	bgloop.Consume(ctx, bgloop.Events[watchRun]{
		Name: bgloop.NameWatchQueue, C: q.ch,
		Body: func(ctx context.Context, first watchRun) error {
			batch := q.gather(ctx, first)
			if err := q.writeBatch(ctx, batch); err != nil {
				slog.WarnContext(ctx, "call handlers: queuing watch runs failed",
					"runs", len(batch), "error", logsan.SanitizeForLog(err.Error()))
				return err
			}
			return nil
		},
	})
}

// gather is first and the runs that arrive within the linger, up to
// batchSize; with no linger, the runs already waiting.
func (q *WatchQueue) gather(ctx context.Context, first watchRun) []watchRun {
	batch := []watchRun{first}
	var deadline <-chan time.Time
	if q.Linger > 0 {
		timer := time.NewTimer(q.Linger)
		defer timer.Stop()
		deadline = timer.C
	}
	for len(batch) < batchSize {
		if deadline == nil {
			select {
			case w := <-q.ch:
				batch = append(batch, w)
				continue
			default:
				return batch
			}
		}
		select {
		case w := <-q.ch:
			batch = append(batch, w)
		case <-deadline:
			return batch
		case <-ctx.Done():
			return batch
		}
	}
	return batch
}

// add hands one run to the writer, dropping it, logged, when the writer is
// that far behind: the call is never made to wait.
func (q *WatchQueue) add(w watchRun) {
	select {
	case q.ch <- w:
	default:
		slog.Warn("call handlers: the watch queue is full; a run was not queued", "handler", w.handler.ID)
	}
}

// pendingWatchSQL counts a watch's runs that wait for a worker.
const pendingWatchSQL = `SELECT COUNT(*) FROM script_runs WHERE call_handler_id = $1 AND trigger_kind = 'watch' AND status = 'pending'`

// insertWatchSQL writes one watch run: pending, or past the bound
// skipped_backlog with its reason and finished. As an event's run is, a watch
// run is queued when its script runs one at a time in either mode, so it waits
// its turn rather than being refused, and is never the exclusive run the
// one-open-run index holds a script to.
const insertWatchSQL = `
	INSERT INTO script_runs (id, script_id, script_version_id, version, trigger_kind, status, params,
	                         requested_by, fire_time, scheduled_for, state_revision, state_read, exclusive, queued,
	                         call, run_as, call_handler_id, error, finished_at)
	VALUES ($1, $2, $3, $4, 'watch', $5, $6, $7, NOW(), NOW(),
	        ` + scriptstore.StateAtCreation + `, FALSE,
	        COALESCE((SELECT exclusive_mode <> 'off' FROM scripts WHERE id = $2), FALSE),
	        $8, $9, $10, $11, CASE WHEN $5 = 'skipped_backlog' THEN NOW() END)`

// writeBatch stores a batch of runs in one transaction: each watch's backlog
// is counted once, every run past the bound is stored skipped, and each
// script with a run waiting is told once after the commit.
func (q *WatchQueue) writeBatch(ctx context.Context, batch []watchRun) error {
	tx, err := q.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("queuing watch runs: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	pending := map[string]int{}
	wake := map[string]bool{}
	for _, w := range batch {
		n, counted := pending[w.handler.ID]
		if !counted {
			if err := tx.QueryRowContext(ctx, pendingWatchSQL, w.handler.ID).Scan(&n); err != nil {
				return fmt.Errorf("counting the watch's waiting runs: %w", err)
			}
		}
		status, err := q.insert(ctx, tx, w, n)
		if err != nil {
			return err
		}
		if status == script.RunStatusPending {
			n++
			wake[w.t.script.ID] = true
		}
		pending[w.handler.ID] = n
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("queuing watch runs: %w", err)
	}
	for scriptID := range wake {
		_, _ = q.DB.ExecContext(ctx, `SELECT pg_notify($1, $2)`, scriptstore.NotifyChannel, scriptID)
	}
	return nil
}

// insert stores one run, pending, or skipped past the bound when pending
// runs of its watch already wait.
func (q *WatchQueue) insert(ctx context.Context, tx *sql.Tx, w watchRun, pending int) (string, error) {
	status, reason := script.RunStatusPending, ""
	if pending >= q.MaxQueued {
		status = runstate.StatusSkippedBacklog
		reason = fmt.Sprintf("%d runs of this watch were already waiting, the most one may", pending)
	}
	params, err := script.BindParams(w.t.version.Params, w.handler.Args)
	if err != nil {
		return "", fmt.Errorf("binding the watch's arguments: %w", err)
	}
	paramsJSON, _ := json.Marshal(nonNil(params))
	callJSON, _ := json.Marshal(w.call)
	runAs, _ := json.Marshal(runstate.RunAs{Email: w.t.runAs.Approver, Roles: nonNilRoles(w.t.runAs.Roles)})
	id, err := pkgsession.GenerateScriptSessionID()
	if err != nil {
		return "", fmt.Errorf("naming the run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, insertWatchSQL, id, w.t.script.ID, w.t.version.ID, w.t.version.Version, status,
		paramsJSON, w.t.runAs.Approver, callJSON, runAs, w.handler.ID, reason); err != nil {
		return "", fmt.Errorf("storing the watch's run: %w", err)
	}
	return status, nil
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func nonNilRoles(r []string) []string {
	if r == nil {
		return []string{}
	}
	return r
}

// Watch queues a run of every watch that applies to a call that succeeded.
// Nothing here makes the call wait: resolving each watch's version reads
// through the cache, and the run is written by the queue.
func (e *Evaluator) Watch(ctx context.Context, c *callhandler.Call) []string {
	if e.Watches == nil {
		return nil
	}
	var queued []string
	for _, h := range e.Index.Match(c, callhandler.KindWatch) {
		t, err := e.targetFor(ctx, h)
		if err != nil {
			continue
		}
		e.Watches.add(watchRun{handler: h, t: t, call: CallMap(c, &h)})
		queued = append(queued, h.ID)
	}
	return queued
}
