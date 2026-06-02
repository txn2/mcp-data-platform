package apigateway

import "sync/atomic"

// MemBudget is a process-wide admission controller for the bytes the
// api gateway commits to response-body buffering. It is the structural
// fix for issue #535: per-request size caps bound a single call, but
// nothing bounded the SUM of concurrent calls, so a burst of large
// responses (each individually under its cap) could collectively
// exhaust the heap and get the container OOMKilled (exit 137).
//
// A single MemBudget is shared across every api connection and both
// buffering tools (api_invoke_endpoint and api_export). Before a tool
// allocates a body buffer it Reserves the worst-case byte count; the
// reservation is refused (Acquire returns false) when granting it
// would push committed bytes past the configured maximum. The caller
// then rejects the request before allocating, rather than allocating
// and risking OOM. Reservations are released when the buffer is no
// longer held.
//
// The raw streaming passthrough (REST shim) deliberately does NOT
// reserve against this budget: it io.Copy's the upstream body through
// a small fixed buffer and never holds the whole body, so it is the
// memory-bounded escape hatch for legitimately large bodies.
//
// A nil *MemBudget means "unlimited" — Acquire always succeeds and
// Release is a no-op. This keeps the budget optional: tests and
// deployments that do not configure a limit pass nil and pay nothing.
type MemBudget struct {
	// max is the ceiling on concurrently-committed bytes. Zero means
	// unlimited (the budget is disabled but still safe to call).
	max int64
	// inUse is the bytes currently reserved. Mutated only through
	// atomic CAS in Acquire and atomic add in Release so concurrent
	// callers never over-commit.
	inUse atomic.Int64
}

// NewMemBudget returns a budget capping concurrently-committed body
// bytes at maxBytes. maxBytes <= 0 yields an unlimited (disabled)
// budget that is still safe to call — Acquire always succeeds.
func NewMemBudget(maxBytes int64) *MemBudget {
	if maxBytes < 0 {
		maxBytes = 0
	}
	return &MemBudget{max: maxBytes}
}

// Acquire attempts to reserve n bytes. It returns true and commits the
// reservation when the budget can accommodate it, or false (committing
// nothing) when granting n would exceed the maximum. A nil budget, a
// disabled budget (max == 0), or a non-positive n always succeeds and
// commits nothing measurable — callers still pair such a call with
// Release(n), which is a no-op in those cases.
//
// The check-and-commit is a lock-free compare-and-swap loop so that
// under a concurrent burst at most one caller can win the last slice
// of the budget; the rest observe the updated total and are refused.
func (b *MemBudget) Acquire(n int64) bool {
	if b == nil || b.max == 0 || n <= 0 {
		return true
	}
	for {
		cur := b.inUse.Load()
		next := cur + n
		// Guard against overflow as well as the ordinary ceiling: a
		// caller passing a pathologically large n must be refused, not
		// wrapped into a negative total.
		if next < cur || next > b.max {
			return false
		}
		if b.inUse.CompareAndSwap(cur, next) {
			return true
		}
	}
}

// Release returns n bytes to the budget. It is safe to call on a nil
// or disabled budget, and with n <= 0, in all of which cases it does
// nothing. Release must be paired with a prior Acquire(n) that
// returned true; releasing more than was acquired is clamped at zero
// so an accounting bug cannot drive inUse negative and silently widen
// the effective budget.
func (b *MemBudget) Release(n int64) {
	if b == nil || b.max == 0 || n <= 0 {
		return
	}
	for {
		cur := b.inUse.Load()
		next := max(cur-n, 0)
		if b.inUse.CompareAndSwap(cur, next) {
			return
		}
	}
}

// InUse reports the bytes currently reserved. Zero on a nil or
// disabled budget. Intended for observability and tests.
func (b *MemBudget) InUse() int64 {
	if b == nil {
		return 0
	}
	return b.inUse.Load()
}

// Max reports the configured ceiling, or 0 when the budget is nil or
// disabled (unlimited).
func (b *MemBudget) Max() int64 {
	if b == nil {
		return 0
	}
	return b.max
}

// Enabled reports whether the budget enforces a ceiling. A nil or
// zero-max budget is disabled and admits every reservation.
func (b *MemBudget) Enabled() bool {
	return b != nil && b.max > 0
}
