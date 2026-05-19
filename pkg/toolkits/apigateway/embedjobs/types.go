// Package embedjobs is the Postgres-backed embedding job queue
// for api-catalog operation vectors.
//
// The queue replaces the inline embedding pass that earlier
// revisions of the api-catalog admin path ran synchronously
// inside the HTTP request handler. Inline embedding had three
// failure modes the queue removes:
//
//  1. A slow embedding provider (Ollama at ~200ms/op, a 300-op
//     spec is 60 seconds) hit ingress / gateway / browser
//     timeouts well before completion. The operator saw a failed
//     HTTP request and had no way to tell whether the server-side
//     work had committed.
//  2. A pod restart mid-embed lost all progress (the DB
//     transaction either fully committed or fully rolled back,
//     but there was no checkpoint and no resumption).
//  3. Multiple operators or multiple replica pods racing to
//     embed the same spec each held a connection to the embedding
//     provider open, blowing past whatever per-host concurrency
//     cap the provider enforced.
//
// The queue model:
//
//   - Producers (spec write paths) atomically insert a job row
//     alongside the spec row and NOTIFY the queue channel. ON
//     CONFLICT against the partial unique index makes "spec edited
//     while a job is already pending" a no-op rather than a
//     duplicate enqueue.
//   - Workers across every pod race for the next pending job via
//     SELECT ... FOR UPDATE SKIP LOCKED. SKIP LOCKED is the multi-
//     pod safety net: two pods see the same pending row, one
//     locks it, the other moves on to the next row without
//     blocking.
//   - A claimed job takes a time-bounded lease (default 10 min).
//     If the holding pod dies, the reaper expires the lease and
//     flips the row back to pending. Another worker picks it up.
//   - The reconciler runs on every pod boot AND on a periodic
//     tick. It compares api_catalog_specs.operation_count against
//     COUNT(*) in api_catalog_operation_embeddings for each
//     (catalog, spec) and enqueues jobs for any gaps. This is
//     the "embeddings converge" guarantee: a spec written before
//     the provider was configured, vectors lost to an outage, or
//     any other gap is filled in without operator action.
//
// The package depends on Postgres-specific features (SKIP LOCKED,
// LISTEN/NOTIFY, partial unique indexes). There is no in-memory
// implementation because the contract those features encode does
// not collapse to a single-process map. Tests that need a job
// store use a Postgres instance via sqlmock or dockertest.
//
//nolint:revive // max-public-structs: this package's exported surface is one cohesive queue (Job + filter + status/health rollups + worker/reaper/reconciler/listener types), not a heap of unrelated types.
package embedjobs

import (
	"context"
	"errors"
	"time"
)

// Status enumerates the state machine an embedding job moves
// through.
//
//	pending  -> running         (worker claim)
//	running  -> succeeded       (worker complete)
//	running  -> pending         (retryable failure with backoff)
//	running  -> failed          (attempts exhausted)
//	running  -> pending         (lease expired, reaper releases)
//
// pending is the only state visible to claim queries; running is
// the only state visible to the reaper.
type Status string

// Job state values. Stored as TEXT in api_catalog_embedding_jobs
// for readability; the column has no CHECK constraint so the
// migration stays portable to future states without an ALTER.
const (
	// StatusPending is the worker-claimable state. The job is
	// visible to SELECT ... FOR UPDATE SKIP LOCKED and will be
	// taken by the next idle worker whose next_run_at <= NOW().
	StatusPending Status = "pending"

	// StatusRunning means a worker holds the lease. lease_expires_at
	// is set; if NOW() passes it, the reaper flips the row back
	// to pending so another worker can take over.
	StatusRunning Status = "running"

	// StatusSucceeded is a terminal state: vectors for the spec
	// match its operation_count.
	StatusSucceeded Status = "succeeded"

	// StatusFailed is a terminal state: attempts exhausted.
	// last_error explains. The reconciler will not re-enqueue
	// a failed job; the operator must use the manual-retry admin
	// endpoint to force another attempt.
	StatusFailed Status = "failed"
)

// Kind identifies what triggered a job. Kept on the row for
// audit (the admin job-history view filters by it) and for the
// reconciler's "did I already enqueue this gap" check.
type Kind string

// Job kinds. The set is closed; the producer always uses one of
// these literals.
const (
	// KindSpecWrite jobs are enqueued by the admin spec write
	// paths (upsert / upload / refresh / clone). Every spec
	// mutation produces one.
	KindSpecWrite Kind = "spec_write"

	// KindReconciler jobs are enqueued by the periodic gap
	// detector for specs whose operation_count and embedding
	// row count disagree.
	KindReconciler Kind = "reconciler"

	// KindManualRetry jobs are enqueued by the force-retry admin
	// endpoint, the escape hatch for when the model was swapped
	// externally and vectors are stale even though the text hash
	// is unchanged. The worker treats this kind specially: it
	// skips the dedup pass so every operation is re-embedded.
	KindManualRetry Kind = "manual_retry"
)

// MaxAttempts caps retries before a job moves to StatusFailed.
// Set at the package level (not configurable via Config yet) so
// the worker and the reconciler share one source of truth. A
// real-world value lives in tension between "give a flaky
// provider time to recover" and "stop hammering a permanently-
// broken upstream." Five attempts with exponential backoff covers
// ~30 minutes of transient outage; longer outages convert to
// failed and surface in the admin UI for operator attention.
const MaxAttempts = 5

// LeaseDuration is how long a claiming worker holds its lease
// before the reaper considers the job abandoned. Long enough for
// a 1000-operation spec against a slow embedding provider; short
// enough that a genuinely crashed pod's work resumes within
// minutes, not hours.
const LeaseDuration = 10 * time.Minute

// ReaperInterval is how often the reaper sweeps for expired
// leases. Mid-point between fast resumption after a crash and
// not hammering the DB on every tick.
const ReaperInterval = 30 * time.Second

// ReconcilerInterval is the gap-detector tick. The reconciler
// also runs once on pod boot, so the periodic tick is a backstop
// for "vectors disappeared between boots" (operator manually
// DELETEd rows, a partial restore from backup, etc.). Five
// minutes is generous; the data path tolerates lexical fallback
// while a gap waits.
const ReconcilerInterval = 5 * time.Minute

// Job is one row in api_catalog_embedding_jobs.
//
// The struct mirrors the SQL columns one-for-one so admin
// handlers and tests can construct it directly without an
// intermediate DTO. LeaseExpiresAt / StartedAt / CompletedAt are
// pointers because they are nullable in the schema (NULL when
// the corresponding state has not been reached).
type Job struct {
	ID             int64
	CatalogID      string
	SpecName       string
	Kind           Kind
	Status         Status
	Attempts       int
	LastError      string
	NextRunAt      time.Time
	WorkerID       string
	LeaseExpiresAt *time.Time
	CreatedAt      time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
	// EmbeddedSoFar is the worker's in-flight progress counter,
	// bumped at every chunk boundary inside a long embed pass so
	// the catalog status endpoint can render "running, N/M" while
	// the final UPSERT transaction is still pending. Reset to 0
	// only by Claim on the next pickup, which means a pending row
	// recovered by Retry / ReleaseExpiredLeases, a terminal
	// succeeded / failed row, or any other non-running state may
	// still carry a non-zero value from a prior attempt. Callers
	// gate the display on Status == running rather than on this
	// counter; the value is meaningful only while a worker holds
	// the lease. See #430.
	EmbeddedSoFar int
}

// SpecKey is the composite (catalog_id, spec_name) reference
// used by claim, enqueue, and reconciler queries.
type SpecKey struct {
	CatalogID string
	SpecName  string
}

// ListFilter narrows the result set for admin queries against
// the job table. Zero-value fields are ignored.
type ListFilter struct {
	CatalogID string
	SpecName  string
	Status    Status
	Kind      Kind
	Limit     int
}

// SpecStatusRow is the embedding status of one (catalog, spec)
// from the operator's point of view: how many operations the
// spec parses to, how many vectors are persisted, and what the
// most recent job's state was.
//
// The admin UI renders one of these per spec in the catalog
// panel. "N/M indexed" is OperationCount and EmbeddingCount.
// "queued" / "running (attempt N)" / "failed: ..." derives from
// JobStatus and the related fields.
type SpecStatusRow struct {
	CatalogID      string
	SpecName       string
	OperationCount int
	EmbeddingCount int
	JobStatus      Status // empty when no job row exists yet
	JobAttempts    int
	JobLastError   string
	JobUpdatedAt   *time.Time
	// EmbeddedSoFar mirrors Job.EmbeddedSoFar on the most recent
	// job row. Reset to 0 only by Claim; a terminal succeeded /
	// failed row or a pending row recovered from a lease expiry
	// may still carry a prior attempt's value. The portal gates
	// its "running, N/M" rendering on JobStatus == running so the
	// stale value never reaches the UI. See #430.
	EmbeddedSoFar int
}

// CatalogHealth is the per-catalog roll-up the catalog header
// renders ("All indexed" vs "3 pending, 1 failed").
type CatalogHealth struct {
	CatalogID    string
	SpecsTotal   int
	SpecsIndexed int
	SpecsPending int
	SpecsRunning int
	SpecsFailed  int
}

// ErrNoJob is returned by Claim when no pending job is available.
// Workers use it as the wait signal: receive this, block on
// LISTEN/NOTIFY or the poll tick, then call Claim again.
var ErrNoJob = errors.New("embedjobs: no pending job available")

// ErrNotFound is returned by Get when no job with the supplied
// id exists. Distinct from ErrNoJob (which is a normal idle
// state) so admin handlers can translate it to a 404.
var ErrNotFound = errors.New("embedjobs: job not found")

// Store is the persistence interface. The concrete implementation
// is Postgres-backed (see store_postgres.go); the interface is
// declared here so worker / reaper / reconciler unit tests can
// substitute mocks for the row-level operations.
type Store interface {
	// Enqueue inserts a pending job for the supplied spec key.
	// Returns true when a new row was created, false when the
	// partial unique index suppressed the insert (a pending or
	// running job for the same spec already exists). Producers
	// fire-and-forget; the bool is for tests and metrics.
	Enqueue(ctx context.Context, key SpecKey, kind Kind) (created bool, err error)

	// Claim acquires the next runnable job. Implementations use
	// SELECT ... FOR UPDATE SKIP LOCKED so concurrent callers
	// across pods do not block on each other. The returned job's
	// status is updated to running and lease_expires_at is set
	// to NOW() + LeaseDuration before Claim returns.
	//
	// Returns ErrNoJob when no pending job is available.
	Claim(ctx context.Context, workerID string) (*Job, error)

	// Complete marks the job succeeded. Idempotent against the
	// same id but only when the caller is the lease holder (the
	// worker_id column is checked); a foreign worker calling
	// Complete on a job whose lease has rotated is a no-op
	// (returns ErrNotFound).
	Complete(ctx context.Context, id int64, workerID string) error

	// UpdateProgress sets embedded_so_far on a running job's row.
	// Called by the worker at chunk boundaries inside a long embed
	// pass so the status endpoint can render incremental progress
	// before the final UPSERT transaction commits. Best-effort:
	// a write that misses (because the lease rotated and a foreign
	// worker now holds the job) is silently dropped; the new
	// holder will re-publish its own count. Returns nil on success
	// even when zero rows match the (id, worker_id) filter.
	UpdateProgress(ctx context.Context, id int64, workerID string, embeddedSoFar int) error

	// Retry releases the lease and reschedules the job with an
	// exponential backoff (5 * 2^attempts seconds). Used for
	// retryable provider failures. The next claim sees the new
	// next_run_at and waits accordingly.
	Retry(ctx context.Context, id int64, workerID, errMsg string) error

	// Fail moves the job to the failed terminal state with the
	// supplied error message. Used after attempts == MaxAttempts.
	Fail(ctx context.Context, id int64, workerID, errMsg string) error

	// ReleaseExpiredLeases is the reaper's sweep. Flips every
	// row whose status=running and lease_expires_at<=NOW() back
	// to pending so another worker can claim it. Returns the
	// number of rows released for log/metric output.
	ReleaseExpiredLeases(ctx context.Context) (released int, err error)

	// ReconcileGaps enqueues pending jobs for every spec whose
	// embedding-row count does not equal its operation_count and
	// which does not already have a pending or running job.
	// Returns the number of jobs enqueued.
	ReconcileGaps(ctx context.Context) (enqueued int, err error)

	// Get returns a single job row by id. Used by the admin
	// detail endpoint. Returns ErrNotFound when no such id.
	Get(ctx context.Context, id int64) (*Job, error)

	// List returns jobs matching the filter, newest first.
	List(ctx context.Context, filter ListFilter) ([]Job, error)

	// SpecStatuses returns one SpecStatusRow per spec in the
	// catalog, joining operation_count, embedding row count, and
	// the most recent job's state. Used by the admin spec list
	// to render embedding badges.
	SpecStatuses(ctx context.Context, catalogID string) ([]SpecStatusRow, error)

	// Health returns the per-catalog roll-up the catalog header
	// renders.
	Health(ctx context.Context, catalogID string) (*CatalogHealth, error)
}
