// Package runstate is the vocabulary of a managed-script run's queue history
// (#1859, #1860): how each attempt ended, why a failed run failed and whether
// running it again is expected to help, and what a running run's worker is
// doing. It is its own package so the run record in pkg/script can carry it
// without growing that package's public surface, and so the store, the worker,
// the tool layer and the portal all spell it one way.
package runstate

import "time"

// How an attempt of a run ended (#1860), as its history records it.
const (
	// AttemptFinished is an attempt that recorded the run's verdict:
	// succeeded, failed or canceled.
	AttemptFinished = "finished"
	// AttemptRetried is an attempt that failed on a platform fault and put the
	// run back on the queue under the attempt budget.
	AttemptRetried = "retried"
	// AttemptReleased is an attempt its worker handed back at shutdown.
	AttemptReleased = "released"
	// AttemptShed is an attempt its worker stopped and requeued to relieve
	// memory while other runs executed beside it.
	AttemptShed = "shed"
	// AttemptLeaseExpired is an attempt whose worker stopped without reporting
	// a result: its lease ran out with the run still marked running, which is
	// what an OOM kill, a crash or a lost node leaves behind.
	AttemptLeaseExpired = "lease_expired"
	// AttemptUnresponsive is an attempt a cancel ended because its worker had
	// stopped reporting while its lease still ran.
	AttemptUnresponsive = "unresponsive"
)

// Why a failed run failed (#1859), which decides whether running it again is
// expected to succeed.
const (
	// CauseScript is a failure the script produced: an evaluation error, a
	// fail(), an argument a binding refused, a limit it exceeded. The same
	// version on the same inputs fails the same way.
	CauseScript = "script"
	// CauseUpstream is a failure whose cause is outside the script and
	// usually temporary: an upstream that timed out, dropped the connection,
	// or kept refusing with 429 or 503 past the host's retries.
	CauseUpstream = "upstream"
	// CauseMemory is a run stopped for holding more memory than its budget,
	// or more than its replica could give it (#1861).
	CauseMemory = "memory"
	// CauseWorkerLost is a run whose workers kept stopping without reporting
	// a result until its reclaims were spent (#1860).
	CauseWorkerLost = "worker_lost"
	// CausePlatform is a platform fault that outlasted the attempt budget:
	// the run's session or its script could not be opened or read.
	CausePlatform = "platform"
	// CauseStateConflict is a run whose platform.save_state was refused
	// because another run of the script saved first (#1537). Its outputs
	// stand; run again, it reads the state the other run saved.
	CauseStateConflict = "state_conflict"
)

// CauseRetryable reports whether a run that failed for cause is expected to
// succeed when it runs again unchanged: an upstream that was unavailable, or a
// state another run moved first. Every other cause is the script's or
// reproduces on the next attempt.
func CauseRetryable(cause string) bool { return cause == CauseUpstream || cause == CauseStateConflict }

// DefaultMaxReclaims is how many times a run is taken over from a worker whose
// lease expired before it is failed instead (scripts.worker.max_reclaims). A
// run that kills its worker kills the next one too, so the bound is small.
const DefaultMaxReclaims = 2

// HeartbeatStaleAfter is how long a running run's worker may go without
// reporting before the run reads as unresponsive and a cancel ends it directly
// (#1860). A worker reports every two seconds whatever the script is doing, so
// this is fifteen missed reports.
const HeartbeatStaleAfter = 30 * time.Second

// What a running run's holder is doing, as Liveness reports it.
const (
	// LivenessExecuting is a run whose worker holds the lease and reports.
	LivenessExecuting = "executing"
	// LivenessUnresponsive is a run whose worker holds the lease but has not
	// reported for HeartbeatStaleAfter: most often a replica that was killed,
	// whose lease has not yet run out.
	LivenessUnresponsive = "unresponsive"
	// LivenessLeaseExpired is a run whose lease ran out with no worker
	// holding it; the next claim takes it over, or fails it once its
	// reclaims are spent.
	LivenessLeaseExpired = "lease_expired"
)

// Attempt is one ended attempt of a run.
type Attempt struct {
	Attempt   int        `json:"attempt"`
	Worker    string     `json:"worker"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// Outcome is one of the Attempt* values.
	Outcome string `json:"outcome"`
	// Error is the failure or reason the attempt ended on, when it had one.
	Error string `json:"error,omitempty"`
}
