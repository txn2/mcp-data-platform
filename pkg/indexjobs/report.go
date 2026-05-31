package indexjobs

import (
	"context"
	"errors"
	"fmt"
)

// Coverage reports a kind's indexed-vs-expected vector totals for the
// admin Indexing dashboard. Indexed is the number of vectors the
// kind's Sink currently persists. Expected is the number it should
// hold once fully indexed, and is meaningful only when ExpectedKnown
// is true: api_catalog stamps an operation_count per spec, so it
// reports a real ratio; the tools kind re-syncs continuously and
// stamps no expected count, so it reports ExpectedKnown=false and the
// dashboard shows a sync indicator instead of an indexed/expected
// ratio.
type Coverage struct {
	Indexed       int
	Expected      int
	ExpectedKnown bool
}

// CoverageReporter is an optional Sink capability. A Sink whose vector
// table can report indexed (and optionally expected) item totals
// implements it so the admin Indexing surface can render coverage for
// the kind. Sinks that cannot derive coverage simply do not implement
// it; the surface falls back to the job-state rollup from Counts.
type CoverageReporter interface {
	Coverage(ctx context.Context) (Coverage, error)
}

// ErrUnknownKind is returned by Reporter.Coverage and Reporter.Reindex
// when the requested source kind is not registered. Admin handlers
// translate it to a 404.
var ErrUnknownKind = errors.New("indexjobs: unknown source kind")

// Verdict is the plain-language health state the admin Indexing
// dashboard leads with for each kind. It collapses the three
// structurally different metric families the surface used to render
// side by side (vector coverage, per-unit job state, and open
// failures) into one of four words an operator reads at a glance, so a
// healthy fully-indexed-and-idle kind never looks broken.
type Verdict string

const (
	// VerdictIndexing means work is in flight: at least one unit is
	// running or pending. It takes priority over every other state so
	// an active pass never reads as degraded or idle.
	VerdictIndexing Verdict = "indexing"

	// VerdictDegraded means the kind needs attention with no active
	// pass to fix it: an open (unresolved) failure, or a known coverage
	// shortfall (indexed < expected) that nothing is currently closing.
	VerdictDegraded Verdict = "degraded"

	// VerdictIdleComplete means the kind is fully indexed (or in sync)
	// and there is nothing to do, AND it carries no job history. This is
	// the seeded-before-the-queue case from issue #509: vectors present,
	// zero job rows, last activity nil. It must read as "fully indexed,
	// idle", never as "last activity never".
	VerdictIdleComplete Verdict = "idle_complete"

	// VerdictHealthy means the kind is fully indexed (or in sync) and
	// actively kept that way: no shortfall, no open failure, no work in
	// flight, and job history showing it has run.
	VerdictHealthy Verdict = "healthy"
)

// DeriveVerdict reduces a kind's job-state counts and optional coverage
// to a single Verdict. The branch order encodes the priority an
// operator cares about: active work first, then anything needing
// attention, then the two quiescent-and-healthy states. It reads only
// fields already computed elsewhere, so it is a pure function the admin
// handler and tests can exercise without a database.
//
// A nil counts (no queue wired) is treated as fully quiescent. The
// "needs attention" guards use UnresolvedFailures (open failures), not
// Failed (the per-unit latest-status rollup), so a kind whose newest
// row is a dismissed or superseded failure is not painted degraded. A
// coverage shortfall counts only when ExpectedKnown: a continuously
// re-syncing kind (ExpectedKnown=false) has no expected target and so
// can never be "short".
func DeriveVerdict(c *KindCounts, cov *Coverage) Verdict {
	if hasActiveWork(c) {
		return VerdictIndexing
	}
	if (c != nil && c.UnresolvedFailures > 0) || coverageShort(cov) {
		return VerdictDegraded
	}
	// Quiescent and not short: fully indexed or in sync. Distinguish the
	// seeded-before-the-queue case (no job history) from an actively
	// maintained index so the former never renders "never". A zero-value
	// timestamp is treated as "no history" too, matching the handler's
	// IsZero check that omits last_activity, so the verdict and the
	// emitted timestamp never disagree.
	if c == nil || c.LastActivity == nil || c.LastActivity.IsZero() {
		return VerdictIdleComplete
	}
	return VerdictHealthy
}

// hasActiveWork reports whether a pass is in flight for the kind
// (running or pending units), which the verdict treats as "indexing".
func hasActiveWork(c *KindCounts) bool {
	return c != nil && (c.Running > 0 || c.Pending > 0)
}

// coverageShort reports a known coverage shortfall: ExpectedKnown kinds
// whose indexed count trails the expected count. A continuously-syncing
// kind (ExpectedKnown=false) has no expected target and can never be
// short.
func coverageShort(cov *Coverage) bool {
	return cov != nil && cov.ExpectedKnown && cov.Indexed < cov.Expected
}

// Reporter aggregates the cross-kind health the admin Indexing
// dashboard renders: the registered kinds, per-kind job-state counts,
// optional coverage, the job list / drill-down, and the operator-driven
// re-index command. It composes the queue Store with the Registry so it
// can dispatch coverage and gap enumeration to each kind's Sink. One
// Reporter serves every registered kind uniformly, so a new index_jobs
// consumer gets dashboard visibility for free the moment it registers.
type Reporter struct {
	store Store
	reg   *Registry
}

// NewReporter returns a Reporter over the shared queue store and the
// kind registry. Both must be non-nil; the platform only constructs a
// Reporter once the queue is wired (database + embedding provider both
// present), so a nil-guarded accessor returns nil rather than a
// half-built Reporter.
func NewReporter(store Store, reg *Registry) *Reporter {
	return &Reporter{store: store, reg: reg}
}

// Kinds returns every registered source kind, sorted.
func (r *Reporter) Kinds() []string { return r.reg.Kinds() }

// Counts returns the per-state job rollup for one source kind. This is
// the wiring of indexjobs.Store.Counts that #438 added but never
// connected to a surface.
func (r *Reporter) Counts(ctx context.Context, kind string) (*KindCounts, error) {
	counts, err := r.store.Counts(ctx, kind)
	if err != nil {
		return nil, fmt.Errorf("indexjobs: reporter counts for kind %q: %w", kind, err)
	}
	return counts, nil
}

// Coverage returns the indexed-vs-expected rollup for the kind, or nil
// when the kind's Sink does not implement CoverageReporter (the
// dashboard then renders job-state only for that kind). Returns
// ErrUnknownKind when the kind is not registered.
func (r *Reporter) Coverage(ctx context.Context, kind string) (*Coverage, error) {
	_, sink, ok := r.reg.Lookup(kind)
	if !ok {
		return nil, ErrUnknownKind
	}
	cr, ok := sink.(CoverageReporter)
	if !ok {
		// A registered kind whose Sink does not implement CoverageReporter
		// simply has no coverage to show; the admin surface renders its
		// job-state rollup only. nil value + nil error is the documented
		// "no coverage" signal, distinct from ErrUnknownKind.
		return nil, nil //nolint:nilnil // nil,nil is the intended "kind reports no coverage" result
	}
	cov, err := cr.Coverage(ctx)
	if err != nil {
		return nil, fmt.Errorf("indexjobs: coverage for kind %q: %w", kind, err)
	}
	return &cov, nil
}

// List returns jobs matching the filter, newest first. A zero-value
// SourceKind lists across every kind, which the dashboard's cross-kind
// failure triage relies on.
func (r *Reporter) List(ctx context.Context, filter ListFilter) ([]Job, error) {
	jobs, err := r.store.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("indexjobs: reporter list: %w", err)
	}
	return jobs, nil
}

// ActiveFailures returns the units whose index attempts left an open
// failure, one entry per unit, most-recently-failed first, for the
// dashboard's failure-triage surface. An empty kind lists across every
// kind. limit bounds the result.
func (r *Reporter) ActiveFailures(ctx context.Context, kind string, limit int) ([]FailedUnit, error) {
	units, err := r.store.ActiveFailures(ctx, kind, limit)
	if err != nil {
		return nil, fmt.Errorf("indexjobs: reporter active failures: %w", err)
	}
	return units, nil
}

// Resolve dismisses every open failure for the unit, backing the
// dashboard's explicit "this failure no longer reflects reality"
// action. It returns the number of failed rows resolved; zero is not
// an error (the unit may already be clean, e.g. a concurrent success
// already resolved it).
//
// Resolve deliberately does NOT require the kind to be registered.
// The most important failures to dismiss are leftover rows for a kind
// no consumer registers any more (the "no consumer registered for
// source_kind" tombstones); requiring registration would make exactly
// those undismissable. The (kind, sourceID) pair only ever matches the
// operator's own failing units, so dismissing an unregistered kind is
// safe.
func (r *Reporter) Resolve(ctx context.Context, kind, sourceID string) (int, error) {
	n, err := r.store.ResolveFailures(ctx, Key{SourceKind: kind, SourceID: sourceID})
	if err != nil {
		return 0, fmt.Errorf("indexjobs: reporter resolve %q/%q: %w", kind, sourceID, err)
	}
	return n, nil
}

// Reindex enqueues manual-retry jobs for the kind. With a non-empty
// sourceID it targets exactly that unit (the failure-triage retry
// button and the per-spec re-embed path). With an empty sourceID it
// re-enqueues every unit the kind's Sink currently reports as out of
// sync via FindGaps, which is the one generic per-kind enumeration the
// framework exposes: for api_catalog that is every spec whose vector
// count disagrees with its operation_count; for tools it is the single
// tool corpus (its FindGaps always returns the source). The
// manual-retry trigger makes the worker skip its text-hash dedup so
// every item is re-embedded. Returns the source ids enqueued (which may
// be empty when nothing is out of sync), or ErrUnknownKind when the
// kind is not registered.
//
// Enqueue is idempotent: a unit that already has an open
// (pending/running) job is collapsed by the partial unique index, so a
// re-index issued while a pass is in flight does not double-queue it.
func (r *Reporter) Reindex(ctx context.Context, kind, sourceID string) ([]string, error) {
	_, sink, ok := r.reg.Lookup(kind)
	if !ok {
		return nil, ErrUnknownKind
	}
	ids := []string{sourceID}
	if sourceID == "" {
		gaps, err := sink.FindGaps(ctx)
		if err != nil {
			return nil, fmt.Errorf("indexjobs: reindex find gaps for kind %q: %w", kind, err)
		}
		ids = gaps
	}
	enqueued := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := r.store.Enqueue(ctx, Key{SourceKind: kind, SourceID: id}, TriggerManualRetry); err != nil {
			return enqueued, fmt.Errorf("indexjobs: reindex enqueue %q/%q: %w", kind, id, err)
		}
		enqueued = append(enqueued, id)
	}
	return enqueued, nil
}
