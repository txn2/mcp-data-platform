package calltask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/exportasync"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/mcptask"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// errNoCaller is a background export asked for by nobody who could read it
// again.
var errNoCaller = errors.New("a background export needs a signed-in caller to read it again")

// errShuttingDown is a background export asked for while the replica stops.
var errShuttingDown = errors.New("the server is shutting down; call the export again")

// Exports is the runner as the export tools start and find background exports
// through (#2212): the same call tasks, created for a client that does not
// declare the Tasks extension, marked as background exports and found again
// by their idempotency key.
type Exports struct{ r *Runner }

// Exports is the runner's background-export face.
func (r *Runner) Exports() *Exports { return &Exports{r: r} }

var _ exportasync.Runner = (*Exports)(nil)

// Start runs work as a background export under key, or answers with the one
// already working under it for this caller and tool.
func (e *Exports) Start(
	ctx context.Context, tool, key string, work func(ctx context.Context) (*mcp.CallToolResult, error),
) (*exportasync.Run, bool, error) {
	r := e.r
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || pc.UserID == "" {
		return nil, false, errNoCaller
	}
	id, err := newID()
	if err != nil {
		return nil, false, err
	}
	now := r.now().UTC()
	task := Task{
		ID: id, ToolName: tool, UserID: pc.UserID, Status: mcptask.StatusWorking, StatusMessage: msgWorking,
		CreatedAt: now, UpdatedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(r.retention),
		IdempotencyKey: key, Background: true,
	}
	dctx, ex := detach(ctx, pc)
	ex.cell = &exportasync.Cell{}
	dctx = exportasync.WithProgress(dctx, ex.cell)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		ex.cancel(errShutdown)
		return nil, false, errShuttingDown
	}
	existing, created, err := r.store.CreateBackground(ctx, task)
	if err != nil || !created {
		r.mu.Unlock()
		ex.cancel(errShutdown)
		if err != nil {
			return nil, false, fmt.Errorf("starting the background export: %w", err)
		}
		return r.runOf(existing), false, nil
	}
	r.running[id] = ex
	r.wg.Add(1)
	r.mu.Unlock()
	go r.execute(dctx, id, func(ctx context.Context) (mcp.Result, error) {
		res, err := work(ctx)
		if res == nil {
			return nil, err
		}
		return res, err
	})
	return r.runOf(&task), true, nil
}

// Find reads the caller's latest background export of tool under key. A
// working one whose replica stopped reporting is failed first.
func (e *Exports) Find(ctx context.Context, tool, key string) (*exportasync.Run, error) {
	r := e.r
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || pc.UserID == "" {
		return nil, exportasync.ErrNotFound
	}
	task, err := r.store.FindByKey(ctx, pc.UserID, tool, key)
	if errors.Is(err, errTaskNotFound) {
		return nil, exportasync.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading the background export: %w", err)
	}
	if task.Status == mcptask.StatusWorking && r.stale(task) {
		if task, err = r.failStale(ctx, task); err != nil {
			return nil, err
		}
	}
	return r.runOf(task), nil
}

// Ended reads the caller's background exports that ended after since, newest
// first, at most limit: what platform_info tells its caller (#2212).
func (e *Exports) Ended(ctx context.Context, userID string, since time.Time, limit int) ([]exportasync.Run, error) {
	tasks, err := e.r.store.EndedBackground(ctx, userID, since, limit)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names what it read
	}
	out := make([]exportasync.Run, 0, len(tasks))
	for i := range tasks {
		out = append(out, *e.r.runOf(&tasks[i]))
	}
	return out, nil
}

// runOf renders a task as a background export's caller reads it.
func (*Runner) runOf(task *Task) *exportasync.Run {
	run := &exportasync.Run{
		ID: task.ID, Message: task.StatusMessage, Result: task.Result,
		StartedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
	switch task.Status {
	case mcptask.StatusWorking:
		run.Status = exportasync.StatusRunning
	case mcptask.StatusCompleted:
		run.Status = exportasync.StatusCompleted
	default:
		run.Status = exportasync.StatusFailed
	}
	if len(task.Progress) > 0 {
		_ = json.Unmarshal(task.Progress, &run.Progress) // a progress that does not read is no progress
	}
	return run
}

// writeProgress records, for each background export this replica runs, how
// far it has come since the last heartbeat wrote it.
func (r *Runner) writeProgress(ctx context.Context) {
	type pending struct {
		id string
		p  exportasync.Progress
	}
	var due []pending
	r.mu.Lock()
	for id, ex := range r.running {
		if ex.cell == nil {
			continue
		}
		if p := ex.cell.Load(); p != ex.written {
			ex.written = p
			due = append(due, pending{id: id, p: p})
		}
	}
	r.mu.Unlock()
	for _, d := range due {
		raw, err := json.Marshal(d.p)
		if err == nil {
			err = r.store.SetProgress(ctx, d.id, raw)
		}
		if err != nil {
			slog.WarnContext(ctx, "recording a background export's progress failed", "task_id", d.id,
				"error", logsan.SanitizeForLog(err.Error()))
		}
	}
}
