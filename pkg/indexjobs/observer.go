package indexjobs

import (
	"context"
	"errors"
	"time"
)

// Observer receives what the queue does, as it happens, so a metrics backend
// can answer the questions the job table answers only one page at a time: how
// fast units complete, how long a pass takes, how many are running on each
// replica, how often the embedder times out (#1837).
//
// Every argument is a plain value so an implementation needs nothing from this
// package; *observability.Metrics is the one the platform wires. Labels are
// bounded: kind is a registered source kind, trigger one of the Trigger
// values, and outcome and status the closed sets declared below.
type Observer interface {
	// IndexJobEnqueued reports one Enqueue. created is false when the
	// partial unique index folded it into a pending or running job for the
	// same unit.
	IndexJobEnqueued(ctx context.Context, kind, trigger string, created bool)
	// IndexJobStarted and IndexJobFinished bracket one claimed job on this
	// replica. Finished carries how the pass ended and how long it ran,
	// from claim to the store write that settled it.
	IndexJobStarted(ctx context.Context, kind string)
	IndexJobFinished(ctx context.Context, kind, trigger, outcome string, d time.Duration)
	// IndexJobItems reports one pass's plan: the items sent to the embedder
	// and the ones whose persisted vector was reused.
	IndexJobItems(ctx context.Context, kind string, embedded, reused int)
	// IndexEmbedCall reports one EmbedBatch call to the provider.
	IndexEmbedCall(ctx context.Context, kind string, texts int, status string, d time.Duration)
	// IndexLeasesReleased reports the leases one reaper sweep took back from
	// workers that stopped renewing them.
	IndexLeasesReleased(ctx context.Context, released int)
	// IndexUnitsDeferred reports the gaps one reconciler sweep left unqueued
	// because the unit is parked (see ParkCandidate.ParkedUntil).
	IndexUnitsDeferred(ctx context.Context, kind string, deferred int)
}

// Outcomes a finished job reports. They are finer than the row's status
// because the row cannot say why a job is pending again, or that a success
// never landed because another worker owned the lease by then.
const (
	OutcomeSucceeded  = "succeeded"   // vectors persisted, job completed
	OutcomeRetried    = "retried"     // retryable error, rescheduled with backoff
	OutcomeFailed     = "failed"      // attempts exhausted or not retryable
	OutcomeSourceGone = "source_gone" // the unit was deleted; resolved, not failed
	OutcomeLeaseLost  = "lease_lost"  // the lease rotated before the result was written
	OutcomeStoreError = "store_error" // the write that settles the job failed
)

// Embed-call statuses.
const (
	EmbedStatusOK      = "ok"
	EmbedStatusTimeout = "timeout"
	EmbedStatusError   = "error"
)

// observe holds an optional Observer and nil-checks every report, so the
// queue's call sites record unconditionally.
type observe struct{ o Observer }

func (x observe) enqueued(ctx context.Context, key Key, trigger Trigger, created bool) {
	if x.o != nil {
		x.o.IndexJobEnqueued(ctx, key.SourceKind, string(trigger), created)
	}
}

func (x observe) started(ctx context.Context, job *Job) {
	if x.o != nil {
		x.o.IndexJobStarted(ctx, job.SourceKind)
	}
}

func (x observe) finished(ctx context.Context, job *Job, outcome string, d time.Duration) {
	if x.o != nil {
		x.o.IndexJobFinished(ctx, job.SourceKind, string(job.Trigger), outcome, d)
	}
}

func (x observe) items(ctx context.Context, kind string, embedded, reused int) {
	if x.o != nil {
		x.o.IndexJobItems(ctx, kind, embedded, reused)
	}
}

func (x observe) embedCall(ctx context.Context, kind string, texts int, err error, d time.Duration) {
	if x.o != nil {
		x.o.IndexEmbedCall(ctx, kind, texts, embedStatus(err), d)
	}
}

func (x observe) leasesReleased(ctx context.Context, n int) {
	if x.o != nil && n > 0 {
		x.o.IndexLeasesReleased(ctx, n)
	}
}

func (x observe) deferred(ctx context.Context, byKind map[string]int) {
	if x.o == nil {
		return
	}
	for kind, n := range byKind {
		x.o.IndexUnitsDeferred(ctx, kind, n)
	}
}

// embedStatus classifies an EmbedBatch result with the same test the batch
// loop shrinks on, so a timeout the metric counts is a timeout the pass
// reacted to.
func embedStatus(err error) string {
	switch {
	case err == nil:
		return EmbedStatusOK
	case isEmbedTimeout(err):
		return EmbedStatusTimeout
	default:
		return EmbedStatusError
	}
}

// settleOutcome is the outcome of a job whose settling write (Complete,
// Retry or Fail) returned err: the intended outcome when it landed, lease_lost
// when the lease had rotated to another worker, store_error otherwise.
func settleOutcome(err error, intended string) string {
	switch {
	case err == nil:
		return intended
	case errors.Is(err, ErrNotFound):
		return OutcomeLeaseLost
	default:
		return OutcomeStoreError
	}
}
