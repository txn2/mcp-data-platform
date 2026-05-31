// Package indexjobs is the Postgres-backed, source-kind-agnostic
// embedding-index job queue the platform's semantic-search
// consumers share. It is the generalization of the api-catalog
// embedding queue (formerly pkg/toolkits/apigateway/embedjobs):
// the queue mechanics (lease-based claim with FOR UPDATE SKIP
// LOCKED, exponential-backoff retry, reaper sweep, LISTEN/NOTIFY
// wake-ups, periodic gap reconciliation) carry over unchanged in
// shape, but the unit of work is keyed on an opaque
// (source_kind, source_id) pair instead of a catalog-specific
// (catalog_id, spec_name) pair.
//
// Each consumer plugs in two small contracts:
//
//   - Source declares "what text to embed for a given source_id"
//     (LoadItems) and an optional post-embed hook (OnSucceeded).
//   - Sink declares "where vectors for this kind live": the dedup
//     read (ListExisting), the durable writes (Upsert / UpsertBatch),
//     the expected-count breadcrumb (StampExpected), and the
//     per-kind gap query the reconciler diffs against (FindGaps).
//
// The framework owns everything between those contracts: the
// SHA-256 text-hash dedup, batched calls to the embedding
// provider, chunk-boundary progress publishing, and the
// claim/lease/retry/reaper/reconcile state machine. One worker
// pool, one reaper, and one reconciler serve every registered
// kind, routing by the source_kind stamped on each job row.
//
// The package depends on Postgres-specific features (SKIP LOCKED,
// LISTEN/NOTIFY, partial unique indexes). There is no in-memory
// implementation of the queue because the contract those features
// encode does not collapse to a single-process map; tests that
// need a job store use sqlmock or a real Postgres instance, and
// the Source/Sink contracts are mockable independently.
//
//nolint:revive // max-public-structs: this package's exported surface is one cohesive queue (Job + filter + Source/Sink/Registry contracts + status rollups + worker/reaper/reconciler/listener types), not a heap of unrelated types.
package indexjobs

import (
	"context"
	"errors"
	"time"
)

// Status enumerates the state machine an index job moves through.
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

// Job state values. Stored as TEXT in index_jobs for readability;
// the column has no CHECK constraint so the migration stays
// portable to future states without an ALTER.
const (
	// StatusPending is the worker-claimable state. The job is
	// visible to SELECT ... FOR UPDATE SKIP LOCKED and will be
	// taken by the next idle worker whose next_run_at <= NOW().
	StatusPending Status = "pending"

	// StatusRunning means a worker holds the lease.
	// lease_expires_at is set; if NOW() passes it, the reaper
	// flips the row back to pending so another worker can take
	// over.
	StatusRunning Status = "running"

	// StatusSucceeded is a terminal state: vectors for the unit
	// match its expected item count.
	StatusSucceeded Status = "succeeded"

	// StatusFailed is terminal for this job row: attempts are
	// exhausted (last_error explains) and the worker will not retry
	// it. It does NOT pin the unit permanently, though: the reconciler
	// enqueues a fresh job for the same unit on its next sweep if a
	// vector gap remains, because the partial unique index only
	// suppresses pending/running rows, not failed ones. So a transient
	// failure self-heals within one reconcile interval, while a
	// permanent failure re-fails each cycle until the operator fixes
	// the cause.
	StatusFailed Status = "failed"
)

// Trigger identifies what enqueued a job. Kept on the row for
// audit (the admin job-history view filters by it) and for the
// worker's manual-retry handling (which skips the dedup pass).
//
// Trigger is distinct from a job's source_kind: source_kind names
// the corpus (api_catalog, tools, prompts, ...); Trigger names the
// event that produced the job within that corpus.
type Trigger string

// Job triggers. The set is closed; producers always use one of
// these literals.
const (
	// TriggerWrite jobs are enqueued by a consumer's write path
	// (a spec upsert, a prompt save, an insight approval, ...).
	// Every source-row mutation produces one.
	TriggerWrite Trigger = "write"

	// TriggerReconciler jobs are enqueued by the periodic gap
	// detector for units whose indexed-vector count and expected
	// count disagree.
	TriggerReconciler Trigger = "reconciler"

	// TriggerManualRetry jobs are enqueued by an operator-driven
	// force-retry path, the escape hatch for when the embedding
	// model was swapped externally and vectors are stale even
	// though the text hash is unchanged. The worker treats this
	// trigger specially: it skips the dedup pass so every item is
	// re-embedded.
	TriggerManualRetry Trigger = "manual_retry"
)

// MaxAttempts caps retries before a job moves to StatusFailed.
// Set at the package level (not configurable) so the worker and
// the reconciler share one source of truth. Five attempts with
// exponential backoff covers ~30 minutes of transient outage;
// longer outages convert to failed and surface in the admin UI.
const MaxAttempts = 5

// DefaultLeaseDuration is the fallback lease window when the
// worker / store has not been configured with an explicit
// duration. 10 minutes is long enough for a large unit against a
// typical provider and short enough that a genuinely crashed
// pod's work resumes within minutes. The worker heartbeat extends
// the active lease while a job is making progress, so this value
// gates "pod went silent", not "embed batch is slow".
const DefaultLeaseDuration = 10 * time.Minute

// DefaultEmbedBatchSize is the fallback chunk size when the worker
// has not been configured with an explicit batch size. 32 keeps a
// single timed-out batch's lost progress small while amortizing
// per-call overhead.
const DefaultEmbedBatchSize = 32

// ReaperInterval is how often the reaper sweeps for expired
// leases. Mid-point between fast resumption after a crash and not
// hammering the DB on every tick.
const ReaperInterval = 30 * time.Second

// ReconcilerInterval is the gap-detector tick. The reconciler also
// runs once on pod boot, so the periodic tick is a backstop for
// "vectors disappeared between boots". Five minutes is generous;
// the data path tolerates lexical fallback while a gap waits.
const ReconcilerInterval = 5 * time.Minute

// NotifyChannel is the Postgres LISTEN/NOTIFY channel producers
// and workers use to coordinate low-latency wake-ups. Workers
// LISTEN on this channel; producers issue pg_notify after a
// successful enqueue. The payload is intentionally empty because
// the worker re-queries the table on every wake.
const NotifyChannel = "index_jobs"

// Key identifies one unit of indexing work: the corpus
// (SourceKind) and an opaque, consumer-defined identifier within
// that corpus (SourceID). For api_catalog the SourceID encodes
// (catalog_id, spec_name); for a 1:1 corpus like prompts it is
// the prompt id. The framework never parses SourceID; only the
// consumer's Source and Sink interpret it.
type Key struct {
	SourceKind string
	SourceID   string
}

// Item is one embeddable unit within a Source unit. A unit may
// yield many items (an api spec yields one per operation) or
// exactly one (a prompt, a tool, an insight). ItemID is unique
// within the (source_kind, source_id) pair and is the dedup key
// the Sink stores vectors against.
type Item struct {
	ItemID string
	Text   string
}

// Vector is one item's embedding row. TextHash is the SHA-256 of
// the text fed to the provider, used to skip recomputation when an
// item's text is unchanged across reindexes. Model and Dim record
// the provider identity and dimensionality at write time so a
// model swap has a row-level breadcrumb that cached vectors no
// longer match the current provider's output.
//
// The same type is used for both the dedup read (Sink.ListExisting)
// and the write (Sink.Upsert): the fields are identical, so unlike
// the api-catalog precursor there is no separate "existing" vs
// "computed" type. Embedding is empty on a freshly-planned row that
// still needs a provider call and is filled in by the embed loop.
type Vector struct {
	ItemID    string
	TextHash  []byte
	Embedding []float32
	Model     string
	Dim       int
}

// ListFilter narrows the result set for admin queries against the
// job table. Zero-value fields are ignored.
type ListFilter struct {
	SourceKind string
	SourceID   string
	// SourceIDPrefix matches every job whose source_id begins with
	// the given prefix. Consumers that pack structure into the
	// source_id (api_catalog encodes catalog_id then spec_name)
	// use this to list every unit under a parent without the
	// framework having to understand the encoding.
	SourceIDPrefix string
	Trigger        Trigger
	Status         Status
	Limit          int
}

// Job is one row in index_jobs. The struct mirrors the SQL columns
// one-for-one so admin handlers and tests can construct it
// directly. LeaseExpiresAt / StartedAt / CompletedAt are pointers
// because they are nullable in the schema.
type Job struct {
	ID             int64
	SourceKind     string
	SourceID       string
	Trigger        Trigger
	Status         Status
	Attempts       int
	LastError      string
	NextRunAt      time.Time
	WorkerID       string
	LeaseExpiresAt *time.Time
	CreatedAt      time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
	// ItemsDone is the worker's in-flight progress counter, bumped
	// at every chunk boundary inside a long embed pass so a status
	// endpoint can render "running, N/M" while the final upsert
	// transaction is still pending. Reset to 0 only by Claim; a
	// terminal or reaper-recovered row may still carry a prior
	// attempt's value, so callers gate display on
	// Status == StatusRunning.
	ItemsDone int
}

// KindCounts is the per-source-kind job-state roll-up the generic
// admin index-jobs surface renders. It is computed from index_jobs
// alone (no per-kind vector table needed), so the framework can
// produce it for every registered kind uniformly.
type KindCounts struct {
	SourceKind string
	Pending    int
	Running    int
	Succeeded  int
	Failed     int
	// LastActivity is the most recent moment any job for this kind
	// transitioned: MAX over the kind's rows of the greatest of
	// completed_at, started_at, and created_at. Nil when the kind has
	// no jobs. Computed as a true aggregate (not the newest-by-id row)
	// so an out-of-order completion of an older job is not missed.
	LastActivity *time.Time
	// UnresolvedFailures is the number of distinct units for this kind
	// with at least one open failed job (status='failed' AND
	// resolved_at IS NULL). It is the "is this kind degraded?" signal
	// the dashboard verdict keys on, and is deliberately distinct from
	// Failed: Failed is the per-unit latest-status rollup and still
	// counts a unit whose newest row is failed even after that failure
	// was dismissed (resolved) or superseded, whereas UnresolvedFailures
	// drops to zero the moment every failure is resolved.
	UnresolvedFailures int
}

// FailedUnit is one unit (source_kind, source_id) whose index attempts
// left an unresolved failure, aggregated for the admin Indexing
// dashboard's failure-triage surface. Collapsing a unit's repeated
// failed rows into one entry keeps a unit that failed many times from
// flooding the panel, while still exposing what an operator needs to
// triage it: how long it has been failing, whether it ever succeeded,
// and which job to drill into for the un-redacted error.
type FailedUnit struct {
	SourceKind string
	SourceID   string
	// LatestJobID is the id of the unit's most recent unresolved failed
	// job: the row the dashboard's drill-in links to for the full,
	// un-redacted error and the job timeline.
	LatestJobID int64
	// LastError is that latest failed job's error, un-redacted (the
	// triage cards group on a redacted signature but drill in to this).
	LastError string
	// Attempts is the latest failed job's worker-attempt count.
	Attempts int
	// Occurrences is how many open failed rows the unit has. A value >1
	// means the unit failed, was retried, and failed again without an
	// intervening success.
	Occurrences int
	// FirstFailedAt is the earliest open failure for the unit ("first
	// seen"); LastFailedAt is the most recent ("last seen").
	FirstFailedAt time.Time
	LastFailedAt  time.Time
	// LastSucceededAt is the unit's most recent successful completion,
	// or nil if it has never succeeded. When set, the dashboard shows
	// "last succeeded Xm ago" to distinguish a unit that used to work
	// from one that never has.
	LastSucceededAt *time.Time
}

// ErrNoJob is returned by Claim when no pending job is available.
// Workers use it as the wait signal: receive this, block on
// LISTEN/NOTIFY or the poll tick, then call Claim again.
var ErrNoJob = errors.New("indexjobs: no pending job available")

// ErrNotFound is returned by Get when no job with the supplied id
// exists, and by Complete / Retry / Fail / RenewLease when the
// (id, worker_id, status) predicate matches no row (the lease has
// rotated). Distinct from ErrNoJob (a normal idle state) so admin
// handlers can translate it to a 404 and workers can abandon a
// rotated lease gracefully.
var ErrNotFound = errors.New("indexjobs: job not found")

// Store is the persistence interface for the job queue. The
// concrete implementation is Postgres-backed (see
// store_postgres.go); the interface is declared here so
// worker / reaper / reconciler unit tests can substitute mocks.
//
// The Store deliberately knows nothing about vectors or expected
// counts: gap detection lives in Sink.FindGaps (per kind, because
// the indexed-count comes from each kind's own vector table) and
// the reconciler drives it via Enqueue.
type Store interface {
	// Enqueue inserts a pending job for the supplied key. Returns
	// created=true when a new row was written, created=false when
	// the partial unique index suppressed the insert (a pending or
	// running job for the same key already exists). Producers
	// fire-and-forget; the bool is for tests and metrics.
	Enqueue(ctx context.Context, key Key, trigger Trigger) (created bool, err error)

	// Claim acquires the next runnable job across all kinds via
	// SELECT ... FOR UPDATE SKIP LOCKED. The returned job's status
	// is set to running and lease_expires_at to NOW() + the store's
	// configured lease duration before Claim returns. Returns
	// ErrNoJob when no pending job is available.
	Claim(ctx context.Context, workerID string) (*Job, error)

	// Complete marks the job succeeded. The worker_id predicate
	// enforces lease ownership; a foreign worker whose lease has
	// rotated gets ErrNotFound.
	Complete(ctx context.Context, id int64, workerID string) error

	// UpdateProgress sets items_done on a running job's row.
	// Best-effort: a write that misses because the lease rotated is
	// silently dropped (returns nil). The final Complete is the
	// authoritative success signal.
	UpdateProgress(ctx context.Context, id int64, workerID string, itemsDone int) error

	// Retry releases the lease and reschedules the job with
	// exponential backoff. Used for retryable failures.
	Retry(ctx context.Context, id int64, workerID, errMsg string) error

	// Fail moves the job to the failed terminal state. Used after
	// attempts == MaxAttempts.
	Fail(ctx context.Context, id int64, workerID, errMsg string) error

	// ReleaseExpiredLeases is the reaper's sweep. Flips every
	// status=running row whose lease_expires_at <= NOW() back to
	// pending. Returns the number of rows released.
	ReleaseExpiredLeases(ctx context.Context) (released int, err error)

	// RenewLease extends a running job's lease window by duration.
	// The worker calls this on a timer during a long embed pass.
	// The (id, worker_id, status='running') predicate enforces
	// ownership; a renew from a rotated worker returns ErrNotFound.
	RenewLease(ctx context.Context, id int64, workerID string, duration time.Duration) error

	// Get returns a single job row by id. Returns ErrNotFound when
	// no such id.
	Get(ctx context.Context, id int64) (*Job, error)

	// List returns jobs matching the filter, newest first.
	List(ctx context.Context, filter ListFilter) ([]Job, error)

	// Counts returns the per-state job roll-up for one source kind.
	// Used by the generic admin index-jobs surface.
	Counts(ctx context.Context, sourceKind string) (*KindCounts, error)

	// ActiveFailures returns the units whose index attempts left an
	// open failure (status='failed' AND resolved_at IS NULL), one entry
	// per unit, most-recently-failed first. An empty sourceKind lists
	// across every kind, which the cross-kind triage panel relies on.
	// limit bounds the result; a non-positive or oversized limit falls
	// back to the store default.
	ActiveFailures(ctx context.Context, sourceKind string, limit int) ([]FailedUnit, error)

	// ResolveFailures stamps resolved_at on every open failed row for
	// the unit (status='failed' AND resolved_at IS NULL), clearing it
	// from the triage surface. Returns the number of rows resolved.
	// Backs the dashboard's explicit "dismiss"; Complete performs the
	// same resolution internally when a later job for the unit
	// succeeds, so a superseded failure self-clears.
	ResolveFailures(ctx context.Context, key Key) (resolved int, err error)
}

// Source is a consumer's "what to index" contract. One Source per
// source kind is registered with the framework; the worker calls
// LoadItems on every claimed job to fetch the current set of
// embeddable items for the unit (which may have changed since the
// job was enqueued).
type Source interface {
	// Kind returns the source_kind this Source handles. It must
	// match the source_kind producers stamp on jobs and the kind
	// the paired Sink serves.
	Kind() string

	// LoadItems returns every embeddable item for the unit. An
	// empty slice (with a nil error) means "nothing to index"
	// (e.g. the source row was deleted between enqueue and claim);
	// the worker treats that as a clean completion that writes zero
	// vectors. An error is treated as a unit-resolve failure and
	// terminates the job (the source is gone or unreadable; retry
	// will not help).
	LoadItems(ctx context.Context, sourceID string) ([]Item, error)

	// OnSucceeded is an optional post-embed hook called after a
	// successful job completes (e.g. reload live connections so
	// they pick up the new vectors). Implementations that need no
	// hook leave it a no-op.
	OnSucceeded(sourceID string)
}

// Sink is a consumer's "where vectors live" contract. One Sink per
// source kind is registered with the framework. Sinks own their
// own physical storage (api_catalog keeps its existing
// api_catalog_operation_embeddings table; new kinds use the
// framework's generic per-kind tables), which is why gap detection
// (FindGaps) and the expected-count breadcrumb (StampExpected) are
// per-Sink rather than framework-global.
type Sink interface {
	// Kind returns the source_kind this Sink serves. Must match the
	// paired Source.
	Kind() string

	// ListExisting returns the persisted vectors for the unit keyed
	// by item id, for the worker's text-hash + model dedup pass.
	ListExisting(ctx context.Context, key Key) (map[string]Vector, error)

	// Upsert atomically replaces every vector for the unit with the
	// supplied set (delete-absent + insert/update), so a reindex
	// that drops items removes their stale vectors.
	Upsert(ctx context.Context, key Key, rows []Vector) error

	// UpsertBatch writes a single chunk's vectors in place without
	// deleting rows outside the batch. The worker calls this once
	// per chunk so a job that fails mid-pass leaves its prior
	// chunks visible to the next attempt's ListExisting dedup.
	UpsertBatch(ctx context.Context, key Key, rows []Vector) error

	// StampExpected records the unit's expected item count so the
	// reconciler's gap predicate has a target. Called after a
	// successful embed pass with len(items).
	StampExpected(ctx context.Context, key Key, count int) error

	// FindGaps returns the source ids for this kind whose indexed
	// vector count does not match the expected count. The reconciler
	// enqueues a job per returned id; the framework's Enqueue
	// idempotently suppresses any id that already has an open
	// (pending/running) job, so implementations need only diff the
	// kind's expected count against COUNT(*) in the kind's vector
	// table and must NOT consult index_jobs themselves (doing so would
	// re-couple the Sink to the queue's internals).
	FindGaps(ctx context.Context) ([]string, error)
}
