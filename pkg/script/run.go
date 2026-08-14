package script

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Run lifecycle statuses.
//
//   - pending: enqueued, waiting for a worker to claim it.
//   - running: a worker holds the lease and the interpreter is executing.
//   - succeeded / failed: terminal. A failed run carries the reason in Error
//     and, when the script itself failed, the Starlark backtrace with it.
//   - skipped_overlap: terminal, and never executed. A schedule came due while
//     the previous run of the same schedule was still open, and the overlap
//     policy is to skip. It is recorded as a run rather than logged because a
//     report that stopped producing is precisely what a schedule's history has
//     to show; a skip is not an outage, but it is not a run either.
const (
	RunStatusPending        = "pending"
	RunStatusRunning        = "running"
	RunStatusSucceeded      = "succeeded"
	RunStatusFailed         = "failed"
	RunStatusSkippedOverlap = "skipped_overlap"
)

// Run triggers: what produced the run row.
const (
	// TriggerTool marks a run requested through the run_script tool.
	TriggerTool = "tool"
	// TriggerSchedule marks a run materialized by a script's schedule. The two
	// triggers produce identical rows and execute through the same worker
	// under the same grant; what differs is that nobody is waiting on this
	// one, which is why a failed scheduled run notifies and a failed tool run
	// answers its caller.
	TriggerSchedule = "schedule"
)

// Run queue and lifecycle errors.
var (
	// ErrNoWork reports that no run was due for a claiming worker. It is the
	// normal idle outcome, not a failure.
	ErrNoWork = errors.New("no script run is due")

	// ErrLeaseLost reports that a worker tried to write to a run it no longer
	// holds, because its lease expired and another worker reclaimed the run.
	// The write is refused rather than applied: a process that lost its lease
	// is, by definition, no longer the one whose result counts.
	ErrLeaseLost = errors.New("the lease on this script run was lost")

	// ErrRunNotFound reports a lookup for a run id that does not exist.
	ErrRunNotFound = errors.New("script run not found")
)

// RunMetrics is what one execution cost, recorded on the run for capacity
// review and for sizing an approved script's limits against what it actually
// uses.
type RunMetrics struct {
	Steps      uint64 `json:"steps"`
	DurationMS int64  `json:"duration_ms"`
	Queries    int    `json:"queries"`
	Exports    int    `json:"exports"`
}

// RunOutput is one persisted output of a run: where it went, and what landed
// there. An output written to the portal names the stable asset the output name
// maps to and the version this run created of it; one delivered to a bucket
// names the object it wrote.
//
// One run may write the same output name to more than one destination — the
// dashboard keeps its versioned asset while an external system receives its
// file — so a recorded output is identified by the PAIR, never by the name
// alone.
type RunOutput struct {
	Name string `json:"name"`
	// Destination is the granted destination's name. It is empty on rows
	// written before destinations existed, which Destination() reads as the
	// portal.
	Destination string `json:"destination,omitempty"`

	// AssetID and AssetVersion identify a portal output.
	AssetID      string `json:"asset_id,omitempty"`
	AssetVersion int    `json:"asset_version,omitempty"`

	// Bucket and Key locate a delivered object.
	Bucket string `json:"bucket,omitempty"`
	Key    string `json:"key,omitempty"`

	Format   string `json:"format"`
	RowCount int    `json:"row_count"`
	Bytes    int    `json:"bytes"`
}

// destinationOf reads a recorded output's destination, treating an unset one as
// the portal: before external delivery existed the portal was the only place an
// output could land, so a row without a destination is not ambiguous.
func destinationOf(o RunOutput) string {
	if o.Destination == "" {
		return DestinationPortal
	}
	return o.Destination
}

// Run is one execution of one approved script version: a queue row while it is
// pending or running, and the durable history of that execution afterwards.
//
// The two roles are deliberately one table. A run's history IS its queue
// record — what was executed, with which parameters, by whose request, how
// long it took, what it wrote — and splitting them would mean copying every
// field to keep the history readable.
type Run struct {
	ID string `json:"id" example:"run_a1b2c3d4"`

	ScriptID  string `json:"script_id"`
	VersionID string `json:"version_id"`
	// Version is the version NUMBER executed, carried alongside the id so a
	// run reads as "daily-sales v3" without a second lookup.
	Version int `json:"version" example:"3"`

	Trigger string `json:"trigger" example:"tool"`
	Status  string `json:"status" example:"succeeded"`

	// ScheduleID names the schedule that materialized this run, and is empty
	// for every other trigger. It is what the single-fire guarantee is written
	// against: the run's (schedule, fire time) pair is unique, so however many
	// replicas notice the same fire, exactly one run exists for it.
	ScheduleID string `json:"schedule_id,omitempty"`

	// Params are the bound, type-checked parameter values the run executes
	// with. They are bound once, when the run is created, so a re-read of the
	// row explains the run exactly.
	Params map[string]any `json:"params,omitempty"`

	// RequestedBy is the email of whoever asked for this run.
	RequestedBy string `json:"requested_by,omitempty" example:"jane@example.com"`

	// FireTime is the instant the run computes against, handed to the script as
	// run.fire_time. It is pinned when the run is created and never moves,
	// which is what ScheduledFor cannot promise: an infrastructure retry pushes
	// the due time out, and a run delayed that way must still produce the report
	// it was asked for rather than one shifted by the delay.
	FireTime     time.Time  `json:"fire_time"`
	ScheduledFor time.Time  `json:"scheduled_for"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`

	// Attempt counts claims of this run, and LockedUntil / LockedBy carry the
	// current worker's lease. Together they are the fencing token: a write
	// from a worker whose lease expired and was reclaimed matches no row.
	Attempt     int        `json:"attempt"`
	LockedUntil *time.Time `json:"locked_until,omitempty"`
	LockedBy    string     `json:"locked_by,omitempty"`

	Error        string      `json:"error,omitempty"`
	Log          string      `json:"log,omitempty"`
	LogTruncated bool        `json:"log_truncated,omitempty"`
	Metrics      RunMetrics  `json:"metrics"`
	Outputs      []RunOutput `json:"outputs,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Terminal reports whether the run has finished, however it ended. A skipped
// overlap is terminal on arrival: it names a fire that will not be executed,
// so nothing is ever going to move it.
func (r *Run) Terminal() bool {
	return r.Status == RunStatusSucceeded || r.Status == RunStatusFailed ||
		r.Status == RunStatusSkippedOverlap
}

// Output returns the recorded output this run wrote under one name to one
// destination, or nil. A worker consults it before writing: an output this run
// already persisted must not be written twice when the run is reclaimed after a
// crash.
//
// The lookup is by the pair because the name alone is not the identity of a
// write. One result may be both versioned as a portal asset and delivered to a
// bucket, and matching on the name would report the second write as already
// done and silently skip it.
func (r *Run) Output(name, destination string) *RunOutput {
	for i := range r.Outputs {
		if r.Outputs[i].Name == name && destinationOf(r.Outputs[i]) == destination {
			return &r.Outputs[i]
		}
	}
	return nil
}

// Lease is one worker's claim on one run, and the fencing token every write
// against that run carries.
func (r *Run) Lease() RunLease {
	return RunLease{RunID: r.ID, Worker: r.LockedBy, Attempt: r.Attempt}
}

// RunLease identifies the holder of a claim. Every mutating store call takes
// one, and a call whose lease no longer matches the row is refused with
// ErrLeaseLost rather than overwriting the work of the worker that took over.
type RunLease struct {
	RunID   string
	Worker  string
	Attempt int
}

// RunResult is the terminal outcome of one attempt, as the worker reports it.
type RunResult struct {
	// Status is RunStatusSucceeded or RunStatusFailed.
	Status string
	// Error is the failure message, carrying the Starlark backtrace when the
	// script itself failed.
	Error        string
	Log          string
	LogTruncated bool
	Metrics      RunMetrics
}

// RefuseRun reports why the execution gate must not execute this run, or nil
// when it admits it.
//
// This is the gate itself, and it lives in the domain because it is the rule
// the whole feature is built around: nothing the platform runs unattended runs
// except an approved version of an in-service script. Every path into execution
// answers to this one function, so a second producer of runs cannot arrive with
// a second, slightly different idea of what is executable.
//
// It is checked when a run is executed, not only when it is queued, because the
// two happen at different times: between them a script can be disabled,
// retired, or approved onto a different version, and running the queued row
// anyway would execute code whose approval has since moved.
func RefuseRun(sc *Script, v *Version, run *Run) error {
	switch {
	case !sc.Enabled:
		return errors.New("the script is disabled")
	case sc.Status == StatusSuperseded:
		return fmt.Errorf("the script was superseded by %q", sc.SupersededBy)
	case sc.Status == StatusDeprecated:
		return errors.New("the script is deprecated and must not be executed")
	case !sc.Executable():
		return errors.New("the script has no approved version, so nothing may execute it")
	case sc.ApprovedVersionID != run.VersionID:
		return fmt.Errorf("version %d was the approved version when this run was queued and is not any more; request the run again to execute what is approved now", run.Version)
	case !v.Approved() || v.Grants.IsZero():
		return errors.New("the version this run names carries no approval grant")
	}
	return nil
}

// RunFilter selects runs for a history listing.
type RunFilter struct {
	// ScriptID scopes the listing to one script; empty lists across scripts.
	ScriptID string
	// Status scopes the listing to one lifecycle status.
	Status string
	// Limit caps the rows returned; zero means the store default.
	Limit int
}

// RunStore is the queue and the history of script runs.
//
// The queue half follows the shape the platform's other durable queues use: a
// claim that folds crashed-worker recovery into its own predicate (a lease
// that expired makes the row claimable again), so there is no reaper process
// and no leader election, and any number of replicas can run a worker.
type RunStore interface {
	// Enqueue inserts a pending run, assigning ID when empty.
	Enqueue(ctx context.Context, r *Run) error

	// GetRun returns one run by id, or ErrRunNotFound.
	GetRun(ctx context.Context, id string) (*Run, error)

	// ListRuns returns runs matching the filter, newest first.
	ListRuns(ctx context.Context, filter RunFilter) ([]Run, error)

	// Claim takes the next due run for worker, holding it for lease. It
	// returns ErrNoWork when nothing is due.
	Claim(ctx context.Context, worker string, lease time.Duration) (*Run, error)

	// RecordOutput appends one persisted output to the run. It is written as
	// soon as the output exists, not at the end of the run, so a reclaimed run
	// can tell what it already wrote.
	RecordOutput(ctx context.Context, lease RunLease, out RunOutput) error

	// Finish records a terminal result for the claimed run.
	Finish(ctx context.Context, lease RunLease, res RunResult) error

	// Retry returns the claimed run to pending, due after backoff, recording
	// the cause. Reserved for infrastructure failures: a script error is
	// deterministic and retrying it changes nothing.
	Retry(ctx context.Context, lease RunLease, cause string, backoff time.Duration) error

	// PurgeRuns deletes terminal runs older than retention, returning the
	// number removed.
	PurgeRuns(ctx context.Context, retention time.Duration) (int64, error)
}
